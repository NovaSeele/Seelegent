package task

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// baselineTimeout bounds each git call of the baseline capture.
const baselineTimeout = 20 * time.Second

// baselineMax caps how many paths a baseline keeps (the brief shows brief.BaselineShown of them).
const baselineMax = 1000

// captureBaseline lists the files under cwd that already have uncommitted changes, as absolute paths, and the
// repository root. A folder outside git, or a git that fails or times out, gives no baseline and no error: the
// baseline only warns the worker, it never blocks a task.
func captureBaseline(ctx context.Context, cwd string) (root string, files []string) {
	out, err := runGit(ctx, cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil
	}
	root = filepath.Clean(strings.TrimSpace(string(out)))
	out, err = runGit(ctx, cwd, "--literal-pathspecs", "-c", "core.quotepath=off",
		"status", "--porcelain", "-z", "--untracked-files=all", "--", ".")
	if err != nil {
		return root, nil
	}
	return root, parsePorcelainZ(root, out)
}

// parsePorcelainZ reads `git status --porcelain -z`: "XY path" entries, where a rename or copy is followed by its
// source path, which is skipped. Paths are relative to the repository root.
func parsePorcelainZ(root string, out []byte) []string {
	var files []string
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		f := string(fields[i])
		if len(f) < 4 || f[2] != ' ' {
			continue
		}
		if f[0] == 'R' || f[0] == 'C' {
			i++ // the source path
		}
		if len(files) < baselineMax {
			files = append(files, filepath.Join(root, filepath.FromSlash(f[3:])))
		}
	}
	return files
}

// runGit runs git without a shell in dir, with the baseline timeout and no console window.
func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, baselineTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	platform.HideWindow(cmd)
	return cmd.Output()
}
