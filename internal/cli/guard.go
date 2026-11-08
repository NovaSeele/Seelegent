package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// aliasTimeout bounds the git call that reads an alias for the guard.
const aliasTimeout = 5 * time.Second

// newGuardCommand is the hidden `seelegent __guard claude|agy` that the pre-tool hooks of worker profiles run
// (installed by the backends' Prepare). It reads the hook input on standard input and prints the CLI's refusal
// for a risky git command. It never fails: unreadable input is refused, anything else is allowed or refused.
func newGuardCommand() *cobra.Command {
	return &cobra.Command{
		Use:    guard.HookSubcommand + " <claude|agy>",
		Short:  "Pre-tool hook of worker profiles: refuses risky git commands (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			if kind != guard.HookClaude && kind != guard.HookAgy {
				return usagef("unknown hook kind %q (want claude or agy)", kind)
			}
			r := hookDecision(cmd.Context(), kind, cmd.InOrStdin())
			if out := guard.HookOutput(kind, r); out != nil {
				_, _ = cmd.OutOrStdout().Write(out)
			}
			return nil
		},
	}
}

// hookDecision reads a hook input and decides. SEELEGENT_GIT_GUARD=off (--allow-git) turns it off: the hook
// inherits the environment of the worker CLI, which the daemon set and the worker's shell commands cannot change
// (they run as children of the CLI). The git shim, which runs inside the worker's shell, ignores the variable.
func hookDecision(ctx context.Context, kind string, stdin io.Reader) guard.Result {
	if os.Getenv(account.EnvGitGuard) == "off" {
		return guard.Result{}
	}
	in, err := io.ReadAll(io.LimitReader(stdin, guard.HookInputLimit))
	if err != nil {
		return guard.Unreadable()
	}
	h, err := guard.ParseHook(kind, in)
	if err != nil {
		return guard.Unreadable()
	}
	if strings.TrimSpace(h.Command) == "" {
		return guard.Result{}
	}
	c := h.Checker(gitAlias(ctx, os.Environ(), h.Cwd))
	c.Config = fetchConfig(ctx, os.Environ(), h.Cwd)
	c.Branches = gitBranches(ctx, os.Environ(), h.Cwd, runtime.GOOS)
	return c.Command(h.Command, h.Shells...)
}

// gitAlias returns a resolver that reads git aliases with `git config --get alias.<name>` in dir.
func gitAlias(ctx context.Context, env []string, dir string) func(string) (string, bool) {
	bin := gitx.NewBinary(func() []string { return env }, nil)
	return func(name string) (string, bool) {
		var out []byte
		err := bin.Run(func(git string) error {
			cctx, cancel := context.WithTimeout(ctx, aliasTimeout)
			defer cancel()
			//nolint:gosec // git comes from the resolver; the alias name is one argument, never a shell word
			c := exec.CommandContext(cctx, git, "-c", "core.fsmonitor=false", "config", "--get", "alias."+name)
			if dir != "" {
				if st, err := os.Stat(dir); err == nil && st.IsDir() {
					c.Dir = dir
				}
			}
			c.Env = gitx.Env(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
			platform.HideWindow(c)
			var err error
			out, err = c.Output()
			return err
		})
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(out)), true
	}
}

// fetchConfig returns a reader of the fetch settings (guard.FetchConfigPattern) of the repository a git command
// works in: `git <globals> config --null --get-regexp` run in dir. No match (exit code 1) is no settings.
func fetchConfig(ctx context.Context, env []string, dir string) func(globals []string) ([]guard.ConfigEntry, error) {
	bin := gitx.NewBinary(func() []string { return env }, nil)
	return func(globals []string) ([]guard.ConfigEntry, error) {
		out, err := guardGit(ctx, bin, env, dir, globals, "config", "--null", "--get-regexp", guard.FetchConfigPattern)
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return configEntries(out), nil
	}
}

// configEntries reads the output of git config --null --get-regexp: "key\nvalue\0" records, and "key\0" for a key
// written with no value ([fetch] prune), which git reads as true.
func configEntries(out []byte) []guard.ConfigEntry {
	var entries []guard.ConfigEntry
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		key, value, ok := strings.Cut(rec, "\n")
		if !ok {
			value = "true"
		}
		entries = append(entries, guard.ConfigEntry{Key: key, Value: value})
	}
	return entries
}

// foldsCase reports an OS whose disks ignore case by default (NTFS, APFS): there the branches are compared whatever
// core.ignorecase says, since `git -c core.ignorecase=false` does not change the disk.
func foldsCase(goos string) bool { return goos == "windows" || goos == "darwin" }

// gitBranches returns a reader of the branches (guard.Checker.Branches) of the repository a git command works in,
// when its disk ignores case: on Windows and macOS always, elsewhere when core.ignorecase is true. Outside a
// repository there are none.
func gitBranches(ctx context.Context, env []string, dir, goos string) func(globals []string) ([]string, error) {
	bin := gitx.NewBinary(func() []string { return env }, nil)
	return func(globals []string) ([]string, error) {
		var ee *exec.ExitError
		if !foldsCase(goos) {
			out, err := guardGit(ctx, bin, env, dir, globals, "config", "--bool", "core.ignorecase")
			if errors.As(err, &ee) && ee.ExitCode() == 1 {
				return nil, nil // unset
			}
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(string(out)) != "true" {
				return nil, nil
			}
		}
		out, err := guardGit(ctx, bin, env, dir, globals, "for-each-ref", "--format=%(refname)", "refs/heads/")
		if errors.As(err, &ee) && strings.Contains(string(ee.Stderr), "not a git repository") {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var refs []string
		for _, line := range strings.Split(string(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				refs = append(refs, line)
			}
		}
		if refs == nil {
			refs = []string{}
		}
		return refs, nil
	}
}

// guardGit runs `git -c core.fsmonitor=false <globals> <args>` in dir (when it is a folder) for the guard, with
// English messages, no optional locks and no prompts, and returns its output (stderr is in an *exec.ExitError).
func guardGit(ctx context.Context, bin *gitx.Binary, env []string, dir string, globals []string,
	args ...string) ([]byte, error) {
	var out []byte
	err := bin.Run(func(git string) error {
		cctx, cancel := context.WithTimeout(ctx, aliasTimeout)
		defer cancel()
		full := append(append([]string{"-c", "core.fsmonitor=false"}, globals...), args...)
		c := exec.CommandContext(cctx, git, full...)
		if dir != "" {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				c.Dir = dir
			}
		}
		c.Env = gitx.Env(env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANGUAGE=C")
		platform.HideWindow(c)
		c.WaitDelay = 2 * time.Second // a child git leaves can hold the pipes after the timeout kills git
		var err error
		out, err = c.Output()
		return err
	})
	return out, err
}

// gitShimArgs splits the arguments of `seelegent __git [--shim-dir=DIR] <git args>`, which the POSIX git shim runs.
func gitShimArgs(args []string) (dir string, rest []string) {
	if len(args) > 0 && strings.HasPrefix(args[0], "--shim-dir=") {
		return strings.TrimPrefix(args[0], "--shim-dir="), args[1:]
	}
	return "", args
}

// runGitShim checks a git command line and runs the real git (found on PATH without shimDir). It returns the
// exit code: git's own, or 1 for a refusal (printed on stderr, starting with the guard's marker).
//
// The shim never reads SEELEGENT_GIT_GUARD: a worker's own shell can set it (env SEELEGENT_GIT_GUARD=off git
// stash). Whether a worker is guarded is decided by the daemon, which puts the shim on PATH only when the guard is
// on for the turn (gitGuard set, no --allow-git).
func runGitShim(ctx context.Context, shimDir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	env := os.Environ()
	search := env
	if shimDir != "" {
		search = guard.WithoutDir(env, shimDir)
	}
	check := guard.Checker{Alias: gitAlias(ctx, search, ""), Config: fetchConfig(ctx, search, ""),
		Branches: gitBranches(ctx, search, "", runtime.GOOS)}
	if r := check.Git(args); r.Deny {
		_, _ = fmt.Fprintln(stderr, r.Reason)
		return 1
	}
	git, err := gitx.NewBinary(func() []string { return search }, nil).Path()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "seelegent git shim:", err)
		return 1
	}
	// With help.autocorrect set, git would run the subcommand it guesses from a typo (git stsh: stash), which the
	// guard read as an unknown command and allowed.
	full := append([]string{"-c", "help.autocorrect=0"}, args...)
	//nolint:gosec // the shim runs the real git with the worker's own arguments, after the guard allowed them
	c := exec.CommandContext(ctx, git, full...)
	c.Stdin, c.Stdout, c.Stderr = stdin, stdout, stderr
	c.Env = env // nested git calls (aliases, hooks) go through the shim again
	if err := c.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintln(stderr, "seelegent git shim:", err)
		return 1
	}
	return 0
}

// shimMain runs the Windows git shim (seelegent copied as git.exe) when this process is one, and reports whether
// it was.
func shimMain(ctx context.Context) (code int, ok bool) {
	exe, _ := os.Executable()
	if !guard.IsShim(os.Args[0], exe) {
		return 0, false
	}
	return runGitShim(ctx, filepath.Dir(exe), os.Args[1:], os.Stdin, os.Stdout, os.Stderr), true
}
