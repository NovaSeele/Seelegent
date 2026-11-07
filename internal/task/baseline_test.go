package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// TestBaselineFindsMovedGit: a git that was removed after the daemon found it does not cost the baseline: the
// capture finds git again.
func TestBaselineFindsMovedGit(t *testing.T) {
	realGit, err := gitx.NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	stale := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	fake := filepath.Join(stale, name)
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := "PATH=" + stale + string(filepath.ListSeparator) + filepath.Dir(realGit)
	bin := gitx.NewBinary(func() []string { return []string{path} }, nil)
	if got, err := bin.Path(); err != nil || got != fake {
		t.Fatalf("Path = %q, %v; want %q", got, err, fake)
	}
	if err := os.Remove(fake); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	cmd := exec.CommandContext(t.Context(), realGit, "init", "-q")
	cmd.Dir = repo
	cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	file := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, files, start := captureBaseline(t.Context(), bin, repo, time.Now())
	if root == "" || len(files) != 1 || filepath.Base(files[0]) != "a.txt" || start == nil || start.Head != "" ||
		start.Branch == "" {
		t.Fatalf("baseline = %q, %q, start %+v (want no head: no commit yet)", root, files, start)
	}
}
