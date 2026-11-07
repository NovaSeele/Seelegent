package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The safe git runner of the worktree features (contests and tickets). It runs only an allow-list of subcommands
// and refuses everything that could lose or move the user's work: no checkout, reset, stash, clean, restore,
// switch, merge, rebase or pull, no --force anywhere, `git apply` on the working tree only, and staging or
// committing only in a worktree Seelegent owns or into a temporary index. A push names exactly one branch (the
// ticket's, ExecOptions.PushBranch) on origin, never with force, delete or mirror, and runs no hook; a fetch only
// brings objects (no local ref changes). The `-c` options before the
// subcommand are checked against their own allow-list (old bug D17: `-c core.hooksPath=...` or
// `-c core.sshCommand=...` used to pass unchecked).

// ErrRefused is matched by a git command the safe runner does not run.
var ErrRefused = errors.New("git command refused")

// ExecOptions are the options of Exec.
type ExecOptions struct {
	// Env holds extra variables, such as GIT_INDEX_FILE for a temporary index.
	Env []string
	// Timeout bounds the command (CommitTimeout when 0).
	Timeout time.Duration
	// Limit caps the standard output kept, in bytes (0: no cap); Exec fails when it is exceeded.
	Limit int
	// Owned says dir is a worktree Seelegent created and owns: only there may git stage into the real index or
	// commit.
	Owned bool
	// PushBranch is the only branch a push may name (`push [-u] origin <branch>`); a push without it is refused.
	PushBranch string
	// Setup allows the commands that set up a repository the user asked Seelegent to create or publish: `init`,
	// `remote add origin -- <url>` and an empty first commit (`commit --allow-empty`).
	Setup bool
	// Stdin is the command's standard input (`cat-file --batch`); none when nil.
	Stdin []byte
}

// BranchPrefix starts every branch Seelegent creates (ticket branches); `worktree add -b` takes no other name.
const BranchPrefix = "seelegent/"

// configAllowed are the `-c` keys Exec passes on: a fixed identity and no signing prompt for internal commits.
var configAllowed = []string{"user.name", "user.email", "commit.gpgsign"}

// subAllowed are the subcommands Exec runs.
var subAllowed = []string{"add", "apply", "cat-file", "check-ignore", "commit", "commit-tree", "diff", "diff-tree",
	"fetch", "init", "log", "ls-files", "ls-remote", "merge-base", "push", "read-tree", "remote", "rev-list", "rev-parse",
	"show", "status", "symbolic-ref", "worktree", "write-tree"}

// CheckSafe reports whether Exec would run args with opt, and why not.
func CheckSafe(opt ExecOptions, args ...string) error {
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] != "-c" || i+1 >= len(args) {
			return fmt.Errorf("%w: option %q before the subcommand", ErrRefused, args[i])
		}
		key, _, ok := strings.Cut(args[i+1], "=")
		if !ok || !slices.Contains(configAllowed, strings.ToLower(key)) {
			return fmt.Errorf("%w: -c %s", ErrRefused, key)
		}
		i += 2
	}
	if i >= len(args) {
		return fmt.Errorf("%w: no subcommand", ErrRefused)
	}
	sub, rest := args[i], args[i+1:]
	if !slices.Contains(subAllowed, sub) {
		return fmt.Errorf("%w: git %s", ErrRefused, sub)
	}
	temp := hasEnv(opt.Env, "GIT_INDEX_FILE")
	for _, a := range rest {
		if a == "--" {
			break
		}
		if a == "--force" || a == "-f" || strings.HasPrefix(a, "--force=") || a == "--ext-diff" ||
			strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--exec") || strings.HasPrefix(a, "--upload-pack") ||
			strings.HasPrefix(a, "--receive-pack") {
			return fmt.Errorf("%w: git %s %s", ErrRefused, sub, a)
		}
	}
	return checkSub(opt, temp, sub, rest)
}

// checkSub holds the rules of each subcommand.
func checkSub(opt ExecOptions, temp bool, sub string, rest []string) error {
	switch sub {
	case "worktree":
		if len(rest) == 0 || !slices.Contains([]string{"add", "remove", "prune", "list"}, rest[0]) {
			return fmt.Errorf("%w: git worktree %s", ErrRefused, strings.Join(rest, " "))
		}
		if rest[0] == "add" && !slices.Contains(rest, "--detach") && !newSeelegentBranch(rest[1:]) {
			return fmt.Errorf("%w: git worktree add needs --detach or -b %s<name>", ErrRefused, BranchPrefix)
		}
	case "push":
		return checkPush(opt, rest)
	case "fetch":
		return checkFetch(rest)
	case "ls-remote":
		for _, a := range rest {
			if strings.Contains(a, "::") || strings.HasPrefix(a, "-") && a != "--refs" && a != "--heads" {
				return fmt.Errorf("%w: git ls-remote %s", ErrRefused, a)
			}
		}
	case "remote":
		switch {
		case len(rest) == 2 && rest[0] == "get-url" && rest[1] == "origin",
			len(rest) == 3 && rest[0] == "get-url" && rest[1] == "--push" && rest[2] == "origin":
		case opt.Setup && len(rest) == 4 && rest[0] == "add" && rest[1] == "origin" && rest[2] == "--" &&
			!strings.HasPrefix(rest[3], "-") && !strings.Contains(rest[3], "::"):
		default:
			return fmt.Errorf("%w: git remote %s", ErrRefused, strings.Join(rest, " "))
		}
	case "init":
		if !opt.Setup {
			return fmt.Errorf("%w: git init outside a repository setup", ErrRefused)
		}
		for _, a := range rest {
			if strings.HasPrefix(a, "--template") || strings.HasPrefix(a, "--separate-git-dir") {
				return fmt.Errorf("%w: git init %s", ErrRefused, a)
			}
		}
	case "symbolic-ref":
		// Reads only: one ref name and the read options (two names would set the ref).
		names := 0
		for _, a := range rest {
			switch {
			case a == "-q" || a == "--quiet" || a == "--short":
			case strings.HasPrefix(a, "-"):
				return fmt.Errorf("%w: git symbolic-ref %s", ErrRefused, a)
			default:
				names++
			}
		}
		if names != 1 {
			return fmt.Errorf("%w: git symbolic-ref may only read one ref", ErrRefused)
		}
	case "apply":
		for _, a := range rest {
			if a == "--index" || a == "--cached" || a == "--3way" || a == "-3" || a == "--reject" ||
				a == "--unsafe-paths" || strings.HasPrefix(a, "--directory") {
				return fmt.Errorf("%w: git apply %s (the working tree only)", ErrRefused, a)
			}
		}
	case "read-tree", "write-tree":
		if !temp {
			return fmt.Errorf("%w: git %s outside a temporary index", ErrRefused, sub)
		}
		for _, a := range rest {
			if a == "-u" || a == "--reset" || a == "-m" || strings.HasPrefix(a, "--prefix") {
				return fmt.Errorf("%w: git read-tree %s", ErrRefused, a)
			}
		}
	case "add":
		if !temp && !opt.Owned {
			return fmt.Errorf("%w: git add outside an owned worktree or a temporary index", ErrRefused)
		}
	case "commit":
		firstCommit := opt.Setup && !temp && slices.Contains(rest, "--allow-empty")
		if (!opt.Owned || temp) && !firstCommit {
			return fmt.Errorf("%w: git commit outside an owned worktree", ErrRefused)
		}
		for _, a := range rest {
			if a == "-a" || a == "--all" || a == "--amend" || strings.HasPrefix(a, "--fixup") {
				return fmt.Errorf("%w: git commit %s", ErrRefused, a)
			}
		}
	}
	return nil
}

// newSeelegentBranch reports whether `worktree add` args make a new branch named with BranchPrefix (-b, never -B,
// which resets an existing branch).
func newSeelegentBranch(args []string) bool {
	for i, a := range args {
		if a == "-B" || strings.HasPrefix(a, "--orphan") {
			return false
		}
		if a == "-b" && i+1 < len(args) {
			name := args[i+1]
			return strings.HasPrefix(name, BranchPrefix) && len(name) > len(BranchPrefix) && ValidBranch(name)
		}
	}
	return false
}

// ValidBranch reports whether name is a plain branch name: no option, no refspec or revision characters, no "..".
func ValidBranch(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.Contains(name, "..") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".lock") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	return !strings.ContainsAny(name, " ~^:?*[\\+\x00\t\n\r")
}

// checkFetch allows `fetch [-q|--quiet] [--no-tags] <remote> refs/heads/<branch>...`: objects only, never a local
// ref (no `:` in a refspec, no `+`), from origin or a URL that cannot be read as an option or a remote helper.
func checkFetch(rest []string) error {
	var pos []string
	for _, a := range rest {
		switch {
		case a == "-q" || a == "--quiet" || a == "--no-tags":
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("%w: git fetch %s", ErrRefused, a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) < 2 || strings.Contains(pos[0], "::") {
		return fmt.Errorf("%w: git fetch must name a remote and branches", ErrRefused)
	}
	for _, ref := range pos[1:] {
		b, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || !ValidBranch(b) {
			return fmt.Errorf("%w: git fetch %s (only refs/heads/<branch>, into no local ref)", ErrRefused, ref)
		}
	}
	return nil
}

// isSha reports whether s is a full hexadecimal object name (SHA-1 or SHA-256).
func isSha(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// checkPush allows exactly `push [-u|--set-upstream] [--porcelain] [-q] origin <branch>` (or the refspec
// refs/heads/<branch>:refs/heads/<branch>, or <commit sha>:refs/heads/<branch>) for opt.PushBranch: never force,
// delete, mirror, tags or other refs.
func checkPush(opt ExecOptions, rest []string) error {
	b := opt.PushBranch
	if !ValidBranch(b) {
		return fmt.Errorf("%w: git push without a named branch", ErrRefused)
	}
	var pos []string
	for _, a := range rest {
		switch {
		case a == "-u" || a == "--set-upstream" || a == "--porcelain" || a == "-q" || a == "--quiet":
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("%w: git push %s", ErrRefused, a)
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 || pos[0] != "origin" {
		return fmt.Errorf("%w: git push must name origin and one branch", ErrRefused)
	}
	ref := pos[1]
	if src, dst, ok := strings.Cut(ref, ":"); ok && isSha(src) && dst == "refs/heads/"+b {
		return nil
	}
	if ref != b && ref != "refs/heads/"+b+":refs/heads/"+b {
		return fmt.Errorf("%w: git push %s (only %s)", ErrRefused, ref, b)
	}
	return nil
}

func hasEnv(env []string, name string) bool {
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, name) {
			return true
		}
	}
	return false
}

// Exec runs a git command of the worktree features after CheckSafe and returns its standard output. A non-zero
// exit is an *Error (ExitCode reads its code).
func (r *Repos) Exec(ctx context.Context, dir string, opt ExecOptions, args ...string) ([]byte, error) {
	if err := CheckSafe(opt, args...); err != nil {
		return nil, err
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = CommitTimeout
	}
	if runsHooks(args) {
		// worktree add runs post-checkout, commit runs post-commit even with --no-verify and push runs pre-push: the
		// repository's hooks never run for an internal command. The null device holds no hook on any system.
		args = append([]string{"-c", "core.hooksPath=" + os.DevNull}, args...)
	}
	out, over, err := r.runIn(ctx, dir, opt.Env, opt.Stdin, timeout, opt.Limit, args...)
	if err == nil && over {
		return out, &Error{Args: args, Err: fmt.Errorf("output larger than %d bytes", opt.Limit)}
	}
	return out, err
}

// runsHooks reports whether args (already checked) name a subcommand that can run repository hooks.
func runsHooks(args []string) bool {
	i := 0
	for i+1 < len(args) && args[i] == "-c" {
		i += 2
	}
	return i < len(args) && (args[i] == "commit" || args[i] == "worktree" || args[i] == "push")
}

// ExitCode is the exit code of a git command that ran and failed, or -1.
func ExitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// Identity is the fixed author of Seelegent's internal commits (in worktrees it owns; never on a branch, never
// pushed), with signing off.
func Identity() []string {
	return []string{"-c", "user.name=Seelegent", "-c", "user.email=seelegent@localhost", "-c", "commit.gpgsign=false"}
}

// SnapshotTree writes the tree of the working tree at dir: HEAD plus every uncommitted and untracked file that is
// not ignored, staged into a temporary index made in tmpDir. Neither the repository's index nor its files change;
// the file contents become objects of the repository. The paths in exclude (relative, slash-separated; such as
// shared-folder links) are left as HEAD has them.
func (r *Repos) SnapshotTree(ctx context.Context, dir, tmpDir string, exclude ...string) (string, error) {
	f, err := os.CreateTemp(tmpDir, "index-*")
	if err != nil {
		return "", fmt.Errorf("snapshot index: %w", err)
	}
	index := f.Name()
	_ = f.Close()
	_ = os.Remove(index) // git creates the index; an empty file is not one
	defer func() {
		_ = os.Remove(index)
		_ = os.Remove(index + ".lock")
	}()
	opt := ExecOptions{Env: []string{"GIT_INDEX_FILE=" + filepath.Clean(index)}}
	if _, err := r.Exec(ctx, dir, ExecOptions{Timeout: ReadTimeout}, "rev-parse", "--verify", "--quiet",
		"HEAD^{commit}"); err == nil {
		if _, err := r.Exec(ctx, dir, opt, "read-tree", "HEAD"); err != nil {
			return "", err
		}
	}
	args := []string{"add", "-A", "--", "."}
	for _, x := range exclude {
		if x = strings.Trim(filepath.ToSlash(x), "/"); x == "" {
			continue
		}
		// Only what add would stage is excluded: git add fails on an excluded path it ignores anyway (a junction
		// under "node_modules/") or does not see. A symlink is no folder to "node_modules/", so add would stage it.
		out, err := r.Exec(ctx, dir, ExecOptions{Timeout: ReadTimeout}, "ls-files", "--others", "--exclude-standard",
			"-z", "--", ":(literal)"+x)
		if err != nil || len(bytes.Trim(out, "\x00")) == 0 {
			continue
		}
		args = append(args, ":(exclude,literal)"+x)
	}
	if _, err := r.Exec(ctx, dir, opt, args...); err != nil {
		return "", err
	}
	out, err := r.Exec(ctx, dir, opt, "write-tree")
	if err != nil {
		return "", err
	}
	tree := strings.TrimSpace(string(out))
	if tree == "" {
		return "", &Error{Args: []string{"write-tree"}, Err: errors.New("no tree")}
	}
	return tree, nil
}

// Head is the commit HEAD names in dir, or "" in a repository without commits.
func (r *Repos) Head(ctx context.Context, dir string) string {
	out, err := r.Exec(ctx, dir, ExecOptions{Timeout: ReadTimeout}, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
