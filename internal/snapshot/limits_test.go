package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// A turn that creates more new files than MaxFiles must not push the files it destroyed out of the end snapshot
// (M18 hunt H5): the start snapshot's files are read first and outside the limits, the cut is recorded.
func TestEndSnapshotKeepsStartFilesPastLimits(t *testing.T) {
	root := newRepo(t)
	ctx := t.Context()
	s := New(t.TempDir(), gitx.New(nil, nil), func() time.Time { return time.Unix(1700000000, 0) })
	write(t, filepath.Join(root, "src", "work.txt"), "user 1\nuser 2\nuser 3\n") // untracked user work
	write(t, filepath.Join(root, "a.txt"), "one\ntwo\nthree\nmine 1\nmine 2\n")  // dirty tracked
	start, err := s.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if start.Truncated || len(start.Files) != 2 {
		t.Fatalf("start: %+v", start)
	}
	// The turn: a build writes MaxFiles files into dist/ (not ignored), a shell command deletes the user's file.
	for i := range MaxFiles + 1 {
		write(t, filepath.Join(root, "dist", fmt.Sprintf("f%04d.js", i)), "x\n")
	}
	if err := os.Remove(filepath.Join(root, "src", "work.txt")); err != nil {
		t.Fatal(err)
	}
	var extra []string
	for _, f := range start.Files {
		extra = append(extra, f.Path)
	}
	end, err := s.Take(ctx, root, extra)
	if err != nil {
		t.Fatal(err)
	}
	if !end.Truncated {
		t.Error("the end snapshot went past MaxFiles without being marked truncated")
	}
	f, ok := end.Find("src/work.txt")
	if !ok || !f.Missing || f.Status != api.GitUntracked && f.Status != "" {
		t.Fatalf("src/work.txt in the end snapshot: %+v, %v", f, ok)
	}
	if a, ok := end.Find("a.txt"); !ok || a.Status != api.GitModified {
		t.Fatalf("a.txt keeps its git status when read early: %+v, %v", a, ok)
	}
	if n := len(end.Files); n != MaxFiles+2 {
		t.Errorf("end has %d files, want the 2 extra plus MaxFiles dirty ones", n)
	}
	found := false
	for _, r := range analyze(t, s, start, end) {
		if r.Path == "src/work.txt" && r.Deleted {
			found = true
		}
	}
	if !found {
		t.Error("the deleted user file is not reported at risk")
	}
}

// Only untracked files under the heavy folders are left out; a tracked file under venv/ or .cache/ is copied like
// any other (docs/safety.md).
func TestTrackedFileUnderHeavyFolderIsKept(t *testing.T) {
	root := newRepo(t)
	write(t, filepath.Join(root, "venv", "pinned.cfg"), "one\n")
	gitRun(t, root, "add", "venv/pinned.cfg")
	gitRun(t, root, "commit", "-q", "-m", "pinned")
	write(t, filepath.Join(root, "venv", "pinned.cfg"), "one\nmine\n")
	write(t, filepath.Join(root, "venv", "lib", "dep.py"), "x\n") // untracked
	s := New(t.TempDir(), gitx.New(nil, nil), nil)
	m, err := s.Take(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Find("venv/pinned.cfg"); !ok {
		t.Errorf("the tracked file under venv/ is missing: %+v", m.Files)
	}
	if _, ok := m.Find("venv/lib/dep.py"); ok {
		t.Errorf("the untracked file under venv/ was copied: %+v", m.Files)
	}
}
