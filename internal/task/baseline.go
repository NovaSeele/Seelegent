package task

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// baselineTimeout bounds each git call of the baseline capture.
const baselineTimeout = 20 * time.Second

// baselineMax caps how many paths a baseline keeps (the brief shows brief.BaselineShown of them).
const baselineMax = 1000

// captureBaseline lists the files under cwd that already have uncommitted changes, as absolute paths, and the
// repository root. A folder outside git, or a git that is missing, fails or times out, gives no baseline and no
// error: the baseline only warns the worker, it never blocks a task.
func captureBaseline(ctx context.Context, bin *gitx.Binary, cwd string) (root string, files []string) {
	git, err := bin.Path()
	if err != nil {
		return "", nil
	}
	out, err := runGit(ctx, git, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	root = filepath.Clean(strings.TrimSpace(string(out)))
	out, err = runGit(ctx, git, cwd, "--literal-pathspecs", "-c", "core.quotepath=off",
		"status", "--porcelain", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return root, nil
	}
	return root, parsePorcelainZ(root, out)
}

// parsePorcelainZ reads `git status --porcelain -z`: "XY path" entries, where a rename or copy, in the index (X)
// or in the worktree (Y, an intent-to-add file renamed with status.renames), is followed by its source path, which
// is skipped. Paths are relative to the repository root.
func parsePorcelainZ(root string, out []byte) []string {
	var files []string
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := string(fields[i])
		if len(f) < 4 || f[2] != ' ' {
			continue
		}
		if strings.ContainsAny(f[:2], "RC") {
			i++ // the source path
		}
		if len(files) < baselineMax {
			files = append(files, filepath.Join(root, filepath.FromSlash(f[3:])))
		}
	}
	return files
}

// runGit runs the git executable git (from gitx.Binary: never a Windows batch shim) without a shell in dir, with
// the baseline timeout and no console window. core.fsmonitor is turned off: a worker could have planted a command
// there in .git/config, and git status would run it.
func runGit(ctx context.Context, git, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, baselineTimeout)
	defer cancel()
	args = append([]string{"-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	platform.HideWindow(cmd)
	return cmd.Output()
}
