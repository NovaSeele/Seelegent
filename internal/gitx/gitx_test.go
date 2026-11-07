package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// gitExe finds git the way the daemon does (never a Windows batch shim); the test skips without one.
func gitExe(t *testing.T) string {
	t.Helper()
	p, err := NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	return p
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), gitExe(t), args...)
	cmd.Dir = dir
	cmd.Env = Env(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// newRepo makes a repository with one commit of a.txt, b.txt, other.txt and pages/[id].vue.
func newRepo(t *testing.T) string {
	t.Helper()
	gitExe(t)
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\nthree\n")
	write(t, filepath.Join(dir, "b.txt"), "bee\n")
	write(t, filepath.Join(dir, "other.txt"), "other\n")
	write(t, filepath.Join(dir, "pages", "[id].vue"), "<template/>\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestStatusTaskGitAndCommit(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\nfour\n")
	write(t, filepath.Join(dir, "new.txt"), "n1\nn2\n")
	write(t, filepath.Join(dir, "pages", "[id].vue"), "<template>x</template>\n")
	git(t, dir, "mv", "b.txt", "c.txt")
	write(t, filepath.Join(dir, "other.txt"), "other changed\n")
	git(t, dir, "add", "other.txt") // the user's own staged change

	st, err := r.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Repo || st.Branch != "main" || !st.Dirty {
		t.Fatalf("status = %+v", st)
	}
	byPath := map[string]api.GitFile{}
	for _, f := range st.Files {
		byPath[f.Path] = f
	}
	if f := byPath["a.txt"]; f.Status != api.GitModified || f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("a.txt = %+v", f)
	}
	if f := byPath["new.txt"]; f.Status != api.GitUntracked || f.Additions != 2 {
		t.Errorf("new.txt = %+v", f)
	}
	if f := byPath["c.txt"]; f.Status != api.GitRenamed || f.OldPath != "b.txt" {
		t.Errorf("c.txt = %+v", f)
	}

	tf := TaskFiles{Cwd: dir, Baseline: []string{filepath.Join(dir, "other.txt")},
		Files: map[string]int{"a.txt": 1, "c.txt": 2}}
	tg, err := r.TaskGit(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range tg.Files {
		paths = append(paths, f.Path)
	}
	// new.txt and pages/[id].vue are dirty under the task's folder and not in the baseline; other.txt is.
	if want := []string{"a.txt", "c.txt", "new.txt", "pages/[id].vue"}; !slices.Equal(paths, want) {
		t.Fatalf("task files = %v, want %v", paths, want)
	}
	if !slices.Equal(tg.Preexisting, []string{"other.txt"}) || tg.Files[1].Turn != 2 {
		t.Errorf("task git = %+v", tg)
	}

	for _, bad := range [][]string{{"other.txt"}, {"../x"}, {"/etc/passwd"}, {}} {
		if _, err := r.Commit(ctx, tf, "msg", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("commit %v: err = %v", bad, err)
		}
	}
	if _, err := r.Commit(ctx, tf, "  ", []string{"a.txt"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty message: %v", err)
	}

	res, err := r.Commit(ctx, tf, "Task work", []string{"a.txt", "c.txt", "new.txt", "pages/[id].vue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SHA) < 40 || !strings.Contains(res.Summary, "changed") {
		t.Errorf("commit = %+v", res)
	}
	names := git(t, dir, "show", "--name-status", "--format=", "-M", "HEAD")
	for _, want := range []string{"a.txt", "c.txt", "b.txt", "new.txt", "pages/[id].vue"} {
		if !strings.Contains(names, want) {
			t.Errorf("commit lacks %s:\n%s", want, names)
		}
	}
	if strings.Contains(names, "other.txt") {
		t.Errorf("the user's staged change went into the commit:\n%s", names)
	}
	if staged := git(t, dir, "diff", "--cached", "--name-only"); strings.TrimSpace(staged) != "other.txt" {
		t.Errorf("staged after commit = %q", staged)
	}
}

func TestDiff(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	write(t, filepath.Join(dir, "new.txt"), "n1\nn2")
	write(t, filepath.Join(dir, "bin.dat"), "a\x00b")
	d, err := r.Diff(ctx, dir, "a.txt", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 1 || d.Hunks[0].OldStart != 1 || d.Hunks[0].NewLines != 3 {
		t.Fatalf("diff = %+v", d)
	}
	var kinds []string
	for _, l := range d.Hunks[0].Lines {
		kinds = append(kinds, l.Kind+":"+l.Text)
	}
	if want := []string{"context:one", "del:two", "add:TWO", "context:three"}; !slices.Equal(kinds, want) {
		t.Errorf("lines = %v", kinds)
	}
	if l := d.Hunks[0].Lines[2]; l.NewNo != 2 || l.OldNo != 0 {
		t.Errorf("add line = %+v", l)
	}
	d, err = r.Diff(ctx, dir, "new.txt", "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].Lines[1].Text != "n2" {
		t.Errorf("untracked diff = %+v", d)
	}
	if d, err = r.Diff(ctx, dir, "bin.dat", "", true); err != nil || !d.Binary {
		t.Errorf("binary diff = %+v, %v", d, err)
	}
	if _, err := r.Diff(ctx, dir, "a.txt", "main; rm -rf", false); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad base: %v", err)
	}
}

func TestStatusOutsideRepoAndBranchCache(t *testing.T) {
	gitExe(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	r := New(func() time.Time { return now }, nil)
	plain := t.TempDir()
	st, err := r.Status(ctx, plain)
	if err != nil || st.Repo {
		t.Fatalf("status outside git = %+v, %v", st, err)
	}
	if b, err := r.Branch(ctx, plain); err != nil || b != nil {
		t.Fatalf("branch outside git = %+v, %v", b, err)
	}
	dir := newRepo(t)
	b, err := r.Branch(ctx, dir)
	if err != nil || b.Branch != "main" || b.Dirty {
		t.Fatalf("branch = %+v, %v", b, err)
	}
	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	if b, _ := r.Branch(ctx, dir); b.Dirty {
		t.Error("the cache was not used")
	}
	now = now.Add(11 * time.Second)
	if b, _ := r.Branch(ctx, dir); !b.Dirty {
		t.Error("the cache did not expire")
	}
}

// TestNoTextconv: a textconv driver from the repository config and .gitattributes is a command a worker can set
// up; the daemon's diffs must never run it (it would run outside any worker tree).
func TestNoTextconv(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	marker := filepath.Join(t.TempDir(), "ran")
	git(t, dir, "config", "diff.evil.textconv", "touch '"+filepath.ToSlash(marker)+"'; cat")
	write(t, filepath.Join(dir, ".gitattributes"), "*.txt diff=evil\n")
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	// The setup works: plain git runs the driver.
	git(t, dir, "diff", "--", "a.txt")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the textconv driver did not run under plain git: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Status(ctx, dir); err != nil {
		t.Fatal(err)
	}
	tf := TaskFiles{Cwd: dir, Files: map[string]int{"a.txt": 1}}
	if _, err := r.TaskGit(ctx, tf); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Diff(ctx, dir, "a.txt", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Commit(ctx, tf, "Task work", []string{"a.txt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a gitx call ran the textconv driver (marker: %v)", err)
	}
}

func TestCleanPath(t *testing.T) {
	root := t.TempDir()
	for _, ok := range []string{"a.txt", "dir/b.txt", `dir\c.txt`, "pages/[id].vue", ":!x"} {
		if _, err := CleanPath(root, ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "..", "a/../../b", "/abs", ".", "a\x00b"} {
		if _, err := CleanPath(root, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseDiffTruncatedAndSuggest(t *testing.T) {
	text := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1,2 @@\n-a\n+b\n+partial li"
	d := ParseDiff(text, true)
	if !d.Truncated || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].OldLines != 1 {
		t.Errorf("parsed = %+v", d)
	}
	msg := SuggestMessage("Fix the login handler", "## Done\n\nThe handler now checks the token.\n\nMore.",
		[]api.GitFile{{Path: "a.go"}})
	if msg != "Fix the login handler\n\nThe handler now checks the token.\n\n- a.go" {
		t.Errorf("message = %q", msg)
	}
}
