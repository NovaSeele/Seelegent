package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

// hookDecision reads a hook input and decides.
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
	c := guard.Checker{Alias: gitAlias(ctx, os.Environ(), h.Cwd)}
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

// gitShimArgs splits the arguments of `seelegent __git [--shim-dir=DIR] <git args>`, which the POSIX git shim runs.
func gitShimArgs(args []string) (dir string, rest []string) {
	if len(args) > 0 && strings.HasPrefix(args[0], "--shim-dir=") {
		return strings.TrimPrefix(args[0], "--shim-dir="), args[1:]
	}
	return "", args
}

// runGitShim checks a git command line and runs the real git (found on PATH without shimDir). It returns the
// exit code: git's own, or 1 for a refusal (printed on stderr, starting with the guard's marker).
func runGitShim(ctx context.Context, shimDir string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	env := os.Environ()
	search := env
	if shimDir != "" {
		search = guard.WithoutDir(env, shimDir)
	}
	if os.Getenv(account.EnvGitGuard) != "off" {
		if r := (guard.Checker{Alias: gitAlias(ctx, search, "")}).Git(args); r.Deny {
			_, _ = fmt.Fprintln(stderr, r.Reason)
			return 1
		}
	}
	git, err := gitx.NewBinary(func() []string { return search }, nil).Path()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "seelegent git shim:", err)
		return 1
	}
	//nolint:gosec // the shim runs the real git with the worker's own arguments, after the guard allowed them
	c := exec.CommandContext(ctx, git, args...)
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
