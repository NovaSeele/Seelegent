package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// An untracked folder with many files is one row of the Git tab view; everything else (Dirty for the snapshots,
// TaskGit for a commit) still sees every file.
func TestUntrackedFoldersCollapseInTheViewOnly(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	for i := range 400 { // a browser profile a worker left in the repository
		write(t, filepath.Join(dir, ".scratch", "profile", "Default", fmt.Sprintf("m%03d.json", i)), "{}\n")
	}
	for i := range 25 {
		write(t, filepath.Join(dir, "pages", "gen", fmt.Sprintf("g%02d.vue", i)), "x\n") // a new folder in a tracked one
		write(t, filepath.Join(dir, "pages", fmt.Sprintf("p%02d.vue", i)), "y\n")        // next to a tracked file
	}
	for i := range 5 {
		write(t, filepath.Join(dir, "few", fmt.Sprintf("f%d.txt", i)), "z\n")
	}

	_, dirty, err := r.Dirty(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	scratch := 0
	for _, f := range dirty {
		if strings.HasPrefix(f.Path, ".scratch/") {
			scratch++
		}
	}
	if scratch != 400 {
		t.Fatalf("Dirty lists %d files of .scratch, want 400", scratch)
	}

	tf := TaskFiles{Cwd: dir}
	full, err := r.TaskGit(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Files) != 455 || len(full.UntrackedDirs) != 0 {
		t.Fatalf("TaskGit: %d files, %d folders", len(full.Files), len(full.UntrackedDirs))
	}

	view, err := r.TaskGitView(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.UntrackedDirs) != 2 {
		t.Fatalf("folders: %+v", view.UntrackedDirs)
	}
	d := view.UntrackedDirs[0]
	if d.Path != ".scratch/" || d.Count != 400 || d.Bytes != 1200 || len(d.Files) != api.GitDirFiles ||
		d.Files[0].Path != ".scratch/profile/Default/m000.json" || d.Files[0].Additions != 1 {
		t.Fatalf(".scratch row: path %q count %d bytes %d listed %d first %+v", d.Path, d.Count, d.Bytes,
			len(d.Files), d.Files[0])
	}
	if g := view.UntrackedDirs[1]; g.Path != "pages/gen/" || g.Count != 25 || len(g.Files) != 25 {
		t.Fatalf("pages/gen row: %+v", g)
	}
	var paths []string
	for _, f := range view.Files {
		paths = append(paths, f.Path)
		if strings.HasPrefix(f.Path, ".scratch/") || strings.HasPrefix(f.Path, "pages/gen/") {
			t.Fatalf("a collapsed file is in Files: %s", f.Path)
		}
	}
	if len(paths) != 30 || !slices.Contains(paths, "pages/p00.vue") || !slices.Contains(paths, "few/f0.txt") {
		t.Fatalf("view files: %v", paths)
	}

	// A commit may still name a file of a collapsed folder, beyond the listed ones too.
	if _, err := r.Commit(ctx, tf, "keep one", []string{".scratch/profile/Default/m399.json"}); err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(out) !=
		".scratch/profile/Default/m399.json" {
		t.Fatalf("committed: %q", out)
	}
	// .scratch now holds a tracked file: git no longer shows it as one entry, so neither does the view.
	view, err = r.TaskGitView(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.UntrackedDirs) != 1 || view.UntrackedDirs[0].Path != "pages/gen/" || len(view.Files) != 429 {
		t.Fatalf("after the commit: %d files, folders %+v", len(view.Files), view.UntrackedDirs)
	}
}

// A repository with no commit yet: status, the view with its folders, the diffs and the commit work.
func TestUnbornHead(t *testing.T) {
	gitExe(t)
	dir := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "config", "commit.gpgsign", "false")
	write(t, filepath.Join(dir, "index.html"), "<html>\n</html>\n")
	write(t, filepath.Join(dir, "staged.txt"), "s1\ns2\n")
	git(t, dir, "add", "staged.txt")
	for i := range 30 {
		write(t, filepath.Join(dir, "node_modules", "pkg", fmt.Sprintf("f%02d.js", i)), "x\n")
	}
	ctx := context.Background()
	r := New(nil, nil)

	st, err := r.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Repo || st.Branch != "main" || len(st.Files) != 32 {
		t.Fatalf("status: repo %v branch %q files %d", st.Repo, st.Branch, len(st.Files))
	}
	head, dirty, err := r.Dirty(ctx, dir)
	if err != nil || head != "" || len(dirty) != 32 {
		t.Fatalf("dirty: head %q, %d files, %v", head, len(dirty), err)
	}

	tf := TaskFiles{Cwd: dir, Start: &TaskStart{Branch: "main"}}
	view, err := r.TaskGitView(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	if view.Head != "" || len(view.UntrackedDirs) != 1 || view.UntrackedDirs[0].Path != "node_modules/" ||
		view.UntrackedDirs[0].Count != 30 || len(view.Files) != 2 {
		t.Fatalf("view: head %q files %+v folders %+v", view.Head, view.Files, view.UntrackedDirs)
	}
	for _, f := range view.Files {
		if f.Additions != 2 {
			t.Errorf("%s: %d lines added, want 2", f.Path, f.Additions)
		}
	}

	// The diff of a staged file is against the empty tree; an untracked file is added in full.
	for _, c := range []struct {
		rel       string
		untracked bool
	}{{"staged.txt", false}, {"index.html", true}, {"index.html", false}} {
		d, err := r.Diff(ctx, dir, c.rel, "", c.untracked)
		if err != nil {
			t.Fatalf("diff %s: %v", c.rel, err)
		}
		if len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 || d.Hunks[0].Lines[0].Kind != api.DiffAdd {
			t.Fatalf("diff %s: %+v", c.rel, d)
		}
	}
	if _, err := r.Diff(ctx, dir, "staged.txt", emptyTree, false); err != nil {
		t.Fatalf("diff against the empty tree: %v", err)
	}
	if data, ok, err := r.Blob(ctx, dir, "", "staged.txt", 100, false); err != nil || ok || data != nil {
		t.Fatalf("blob at an unborn HEAD: %q %v %v", data, ok, err)
	}
	if _, err := r.Commit(ctx, tf, "first", []string{"index.html", "staged.txt"}); err != nil {
		t.Fatal(err)
	}
}

// An image's stamp changes when the file is rewritten, also when its status and line counts do not (a binary is
// always 0/0), so the view's image URLs change and the preview is read again. Other files have none.
func TestImageStamps(t *testing.T) {
	dir := newRepo(t)
	ctx := context.Background()
	r := New(nil, nil)
	logo := filepath.Join(dir, "img", "logo.png")
	write(t, logo, "PNG\x00one")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "logo")
	blob := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD:img/logo.png"))
	write(t, logo, "PNG\x00two")
	write(t, filepath.Join(dir, "new.svg"), "<svg/>")
	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	tf := TaskFiles{Cwd: dir}
	stamps := func() map[string]api.GitFile {
		t.Helper()
		view, err := r.TaskGitView(ctx, tf)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]api.GitFile{}
		for _, f := range view.Files {
			out[f.Path] = f
		}
		return out
	}
	first := stamps()
	before := first["img/logo.png"]
	if !strings.HasSuffix(before.Stamp, "."+blob) || strings.HasPrefix(before.Stamp, ".") || !before.Binary {
		t.Fatalf("logo: %+v (HEAD blob %s)", before, blob)
	}
	if s := first["new.svg"].Stamp; s == "" || !strings.HasSuffix(s, ".") {
		t.Fatalf("a new image has no HEAD half: %q", s)
	}
	if s := first["a.txt"].Stamp; s != "" {
		t.Fatalf("a text file has a stamp: %q", s)
	}
	// The same size, the same status and counts: only the content and the time differ.
	write(t, logo, "PNG\x00six")
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(logo, later, later); err != nil {
		t.Fatal(err)
	}
	after := stamps()["img/logo.png"]
	if after.Status != before.Status || after.Additions != before.Additions || after.Deletions != before.Deletions {
		t.Fatalf("the rewrite changed more than the content: %+v, %+v", before, after)
	}
	if after.Stamp == before.Stamp {
		t.Fatalf("the stamp stayed %q after the image was rewritten", after.Stamp)
	}
	// TaskGit (what a commit reads) does not stamp.
	full, err := r.TaskGit(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range full.Files {
		if f.Stamp != "" {
			t.Fatalf("TaskGit stamped %s", f.Path)
		}
	}
}
