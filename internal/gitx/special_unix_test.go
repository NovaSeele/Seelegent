//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// within fails the test when fn does not return within a second (a FIFO open without a writer never returns).
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("%s blocked on a FIFO", what)
	}
}

// TestSpecialFiles: a worker leaves a FIFO and symbolic links in the repository. Status, TaskGit, Diff and Commit
// return at once; a FIFO counts 0 lines and has an empty diff; a link shows its target, never the linked file.
func TestSpecialFiles(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret.txt")
	write(t, secret, "do not show\n")
	if err := os.Symlink(secret, filepath.Join(dir, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pipe", filepath.Join(dir, "to-pipe")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")

	var st api.RepoStatus
	var err error
	within(t, "Status", func() { st, err = r.Status(ctx, dir) })
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]api.GitFile{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["pipe"]; ok && (f.Additions != 0 || f.Binary) {
		t.Errorf("pipe = %+v", f)
	}
	if f := byPath["outside"]; f.Additions != 1 {
		t.Errorf("outside = %+v (a link is one line: its target)", f)
	}

	tf := TaskFiles{Cwd: dir, Files: map[string]int{"a.txt": 1}}
	within(t, "TaskGit", func() { _, err = r.TaskGit(ctx, tf) })
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"pipe", "to-pipe", "outside"} {
		for _, untracked := range []bool{true, false} {
			var d api.FileDiff
			within(t, "Diff "+p, func() { d, err = r.Diff(ctx, dir, p, "", untracked) })
			if err != nil {
				t.Fatalf("diff %s: %v", p, err)
			}
			text := ""
			for _, h := range d.Hunks {
				for _, l := range h.Lines {
					text += l.Text + "\n"
				}
			}
			switch {
			case p == "pipe" && len(d.Hunks) != 0:
				t.Errorf("pipe diff = %+v", d)
			case p == "outside" && (strings.Contains(text, "do not show") || !strings.Contains(text, secret)):
				t.Errorf("outside diff = %q, want the link target only", text)
			case p == "to-pipe" && strings.TrimSpace(text) != "pipe":
				t.Errorf("to-pipe diff = %q", text)
			}
		}
	}
	within(t, "Commit", func() { _, err = r.Commit(ctx, tf, "Task work", []string{"a.txt"}) })
	if err != nil {
		t.Fatal(err)
	}
}
