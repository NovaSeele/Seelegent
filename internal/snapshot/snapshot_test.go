package snapshot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gitx"
)

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	p, err := gitx.NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), p, args...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// newRepo makes a temp repository with one commit; the test owns it, so destructive git commands are fine here.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.name", "Test")
	gitRun(t, dir, "config", "user.email", "test@example.invalid")
	gitRun(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	write(t, filepath.Join(dir, "b.txt"), "b1\nb2\nb3\nb4\nb5\nb6\nb7\n")
	write(t, filepath.Join(dir, "c.txt"), "c\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	root, err := gitx.New(nil, nil).Root(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func analyze(t *testing.T, s *Store, start, end Manifest, edited ...string) []Risk {
	t.Helper()
	ed := map[string]bool{}
	for _, e := range edited {
		ed[Key(e)] = true
	}
	g := s.Git()
	return Analyze(Input{Start: start, End: end, Edited: ed, Others: map[string]bool{},
		Head: func(rel string) ([]byte, bool) {
			data, ok, _ := g.Blob(context.Background(), start.Root, start.Head, rel, MaxFileBytes, false)
			return data, ok
		},
		Content: s.Content})
}

func TestWorkAtRiskAndRestore(t *testing.T) {
	root := newRepo(t)
	ctx := t.Context()
	s := New(t.TempDir(), gitx.New(nil, nil), func() time.Time { return time.Unix(1700000000, 0) })
	// Someone else's uncommitted work: two lines in a.txt, an untracked file, and seven lines rewritten in b.txt.
	write(t, filepath.Join(root, "a.txt"), "one\ntwo\nthree\nmine 1\nmine 2\n")
	write(t, filepath.Join(root, "notes.md"), "draft\n")
	write(t, filepath.Join(root, "b.txt"), "x1\nx2\nx3\nx4\nx5\nx6\nx7\n")
	start, err := s.Take(ctx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(start.Files) != 3 || start.Head == "" {
		t.Fatalf("start: %+v", start)
	}
	if err := s.Save("t1", 1, KindStart, start); err != nil {
		t.Fatal(err)
	}
	// The turn: a shell command discards a.txt and deletes notes.md; the edit tool rewrites b.txt; c.txt gets a
	// harmless edit by the tool.
	gitRun(t, root, "checkout", "--", "a.txt")
	if err := os.Remove(filepath.Join(root, "notes.md")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "b.txt"), "b1\n")
	write(t, filepath.Join(root, "c.txt"), "c\nmore\n")
	extra := []string{"a.txt", "notes.md", "b.txt", "c.txt"}
	end, err := s.Take(ctx, root, extra)
	if err != nil {
		t.Fatal(err)
	}
	risks := analyze(t, s, start, end, "b.txt", "c.txt")
	got := map[string]Risk{}
	for _, r := range risks {
		got[r.Path] = r
	}
	if len(got) != 3 || got["a.txt"].Why != WhyOutsideTools || got["a.txt"].Lost != 2 ||
		!got["notes.md"].Deleted || got["b.txt"].Why != WhyLargeRemoval || got["b.txt"].Lost != 7 {
		t.Fatalf("risks: %+v", risks)
	}
	// The same edit by the tool, but losing fewer than LargeRemoval lines, is the worker's own business.
	if r := analyze(t, s, start, end, "a.txt", "b.txt", "c.txt"); len(r) != 2 {
		t.Fatalf("edited a.txt: %+v", r)
	}

	res, err := s.Restore("t1", 1, []string{"a.txt", "notes.md", "c.txt", "../x"})
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "a.txt")) != "one\ntwo\nthree\nmine 1\nmine 2\n" ||
		read(t, filepath.Join(root, "notes.md")) != "draft\n" {
		t.Fatal("restore did not bring the files back")
	}
	errs := 0
	for _, r := range res {
		if r.Error != "" {
			errs++
		}
	}
	if errs != 2 { // c.txt was clean at the start; ../x is not in the snapshot
		t.Fatalf("restore results: %+v", res)
	}
	// The repository has no Seelegent refs and the index is as git left it.
	p, _ := gitx.NewBinary(nil, nil).Path()
	if out, _ := exec.CommandContext(t.Context(), p, "-C", root, "for-each-ref").Output(); strings.Contains(
		string(out), "seelegent") {
		t.Fatalf("refs: %s", out)
	}
}

func TestSweep(t *testing.T) {
	root := newRepo(t)
	s := New(t.TempDir(), gitx.New(nil, nil), nil)
	write(t, filepath.Join(root, "a.txt"), "changed\n")
	m, err := s.Take(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"keep", "drop"} {
		if err := s.Save(id, 1, KindStart, m); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "a.txt"), "only in drop\n")
	m2, err := s.Take(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save("drop", 1, KindEnd, m2); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sweep(t.Context(), func(id string) bool { return id == "keep" })
	if err != nil || n != 1 {
		t.Fatalf("sweep: %d %v", n, err)
	}
	if _, err := s.Content(m.Files[0]); err != nil {
		t.Fatalf("kept blob gone: %v", err)
	}
	if _, err := s.Content(m2.Files[0]); err == nil {
		t.Fatal("unused blob kept")
	}
	if _, err := s.Load("drop", 1, KindStart); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dropped manifest: %v", err)
	}
}

func TestJunkName(t *testing.T) {
	for name, want := range map[string]bool{"a.orig": true, "x/nul": true, "NUL.txt": true, "com1": true,
		"b.go~": true, "notes.tmp": true, "main.go": false, "console.go": false, "company.md": false} {
		if JunkName(name) != want {
			t.Errorf("JunkName(%q) = %v", name, !want)
		}
	}
}
