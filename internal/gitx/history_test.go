package gitx

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func head(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
}

func fileOf(files []api.GitFile, path string) (api.GitFile, bool) {
	for _, f := range files {
		if f.Path == path {
			return f, true
		}
	}
	return api.GitFile{}, false
}

// TestTaskGitCommitsSinceStart: a worker commits during the task. The view lists the commits since the start
// (newest first, with the turn that made each), the files they changed counted from the start to the working
// tree, and keeps the uncommitted files apart; the diff against the start shows committed and uncommitted work;
// the commit endpoint shows one commit and refuses the start commit and older ones.
func TestTaskGitCommitsSinceStart(t *testing.T) {
	dir := newRepo(t)
	ctx := t.Context()
	r := New(nil, nil)
	start := head(t, dir)
	t1 := time.Now().Add(-time.Hour)
	t2 := time.Now().Add(-time.Minute)
	tf := TaskFiles{Cwd: dir, Files: map[string]int{"b.txt": 2}, Start: &TaskStart{Head: start, Branch: "main"},
		Turns: []TurnSpan{{N: 1, Start: t1, End: t2.Add(-time.Second)}, {N: 2, Start: t2}}}

	before, err := r.TaskGit(ctx, tf)
	if err != nil || before.StartState != api.GitStartRecorded || len(before.Commits) != 0 ||
		len(before.CommittedFiles) != 0 || before.StartHead != start || before.BranchChanged {
		t.Fatalf("before any commit: %+v, %v", before, err)
	}

	// Turn 1 commits a.txt and new.txt, turn 2 commits b.txt; then a.txt is edited again and c.txt is left
	// uncommitted.
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	write(t, filepath.Join(dir, "new.txt"), "n1\nn2\n")
	git(t, dir, "add", "a.txt", "new.txt")
	t.Setenv("GIT_COMMITTER_DATE", fmt.Sprintf("%d +0000", time.Now().Add(-30*time.Minute).Unix()))
	git(t, dir, "commit", "-q", "-m", "first change")
	t.Setenv("GIT_COMMITTER_DATE", "")
	write(t, filepath.Join(dir, "b.txt"), "bee\nmore\n")
	git(t, dir, "commit", "-q", "-am", "second change")
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\nfour\n")
	write(t, filepath.Join(dir, "c.txt"), "sea\n")

	g, err := r.TaskGit(ctx, tf)
	if err != nil {
		t.Fatal(err)
	}
	if g.StartState != api.GitStartRecorded || g.Head != head(t, dir) || g.CommitsMore != 0 {
		t.Fatalf("view: %+v", g)
	}
	if len(g.Commits) != 2 || g.Commits[0].Subject != "second change" || g.Commits[1].Subject != "first change" ||
		g.Commits[0].Turn != 2 || g.Commits[1].Turn != 1 || g.Commits[1].FileCount != 2 || g.Commits[0].Author != "Test" ||
		g.Commits[0].AuthorTime == "" {
		t.Fatalf("commits: %+v", g.Commits)
	}
	// Committed files: counted from the start to the working tree, each with the turn of the commit that changed it
	// (or the later turn the stream says touched it).
	a, okA := fileOf(g.CommittedFiles, "a.txt")
	n, okN := fileOf(g.CommittedFiles, "new.txt")
	b, okB := fileOf(g.CommittedFiles, "b.txt")
	if len(g.CommittedFiles) != 3 || !okA || !okN || !okB || a.Additions != 2 || a.Deletions != 1 ||
		n.Status != api.GitAdded || n.Additions != 2 || b.Turn != 2 || a.Turn != 1 || n.Turn != 1 {
		t.Fatalf("committed files: %+v", g.CommittedFiles)
	}
	// Uncommitted files, counted against HEAD: only the second edit of a.txt and the new c.txt.
	ua, okUA := fileOf(g.Files, "a.txt")
	if _, okB := fileOf(g.Files, "b.txt"); okB || !okUA || ua.Additions != 1 || ua.Deletions != 0 {
		t.Fatalf("uncommitted files: %+v", g.Files)
	}
	if _, ok := fileOf(g.Files, "c.txt"); !ok {
		t.Fatalf("c.txt missing from the uncommitted files: %+v", g.Files)
	}

	// The diff against the start holds both the committed and the uncommitted lines of a.txt.
	d, err := r.Diff(ctx, dir, "a.txt", start, false)
	if err != nil {
		t.Fatal(err)
	}
	var adds []string
	for _, h := range d.Hunks {
		for _, l := range h.Lines {
			if l.Kind == api.DiffAdd {
				adds = append(adds, l.Text)
			}
		}
	}
	if strings.Join(adds, ",") != "TWO,four" {
		t.Fatalf("diff against the start adds %q", adds)
	}

	// One commit with its files and diff; an abbreviation works.
	c, err := r.TaskCommit(ctx, tf, g.Commits[1].SHA[:8])
	if err != nil {
		t.Fatal(err)
	}
	if c.SHA != g.Commits[1].SHA || c.Parent != start || len(c.Files) != 2 || len(c.Diffs) != 2 ||
		c.Diffs[0].Path != "a.txt" || c.Diffs[1].Path != "new.txt" || c.FileCount != 2 || c.Turn != 1 {
		t.Fatalf("commit detail: %+v", c)
	}
	// The start commit and anything before it are not the task's; nor is a name git does not know.
	for _, sha := range []string{start, start[:10], "deadbeefdeadbeef"} {
		if _, err := r.TaskCommit(ctx, tf, sha); !errors.Is(err, ErrNotInTask) {
			t.Fatalf("TaskCommit(%s) = %v, want ErrNotInTask", sha, err)
		}
	}
	for _, sha := range []string{"--output=x", "HEAD", "abc", "", strings.Repeat("a", 65), "HEAD~1"} {
		if _, err := r.TaskCommit(ctx, tf, sha); !errors.Is(err, ErrInvalid) {
			t.Fatalf("TaskCommit(%q) = %v, want ErrInvalid", sha, err)
		}
	}
	// A commit on another branch, not reachable from HEAD, is refused too.
	side := strings.TrimSpace(git(t, dir, "commit-tree", "HEAD^{tree}", "-p", start, "-m", "side"))
	if _, err := r.TaskCommit(ctx, tf, side); !errors.Is(err, ErrNotInTask) {
		t.Fatalf("a commit of another branch: %v", err)
	}
}

// TestTaskGitCommitCap: more commits than MaxTaskCommits are counted in CommitsMore.
func TestTaskGitCommitCap(t *testing.T) {
	dir := newRepo(t)
	start := head(t, dir)
	for i := range api.MaxTaskCommits + 3 {
		git(t, dir, "commit", "-q", "--allow-empty", "-m", "empty "+string(rune('a'+i%26)))
	}
	g, err := New(nil, nil).TaskGit(t.Context(), TaskFiles{Cwd: dir, Start: &TaskStart{Head: start}})
	if err != nil || len(g.Commits) != api.MaxTaskCommits || g.CommitsMore != 3 {
		t.Fatalf("commits %d, more %d, %v", len(g.Commits), g.CommitsMore, err)
	}
}

// TestTaskGitBranchChangedAndUnreachable: a switch to a branch that still holds the start is reported as a branch
// change with the commits listed; a rebase that rewrote the start away is "unreachable" and falls back to the
// uncommitted files, and the commit endpoint answers with a start error.
func TestTaskGitBranchChangedAndUnreachable(t *testing.T) {
	dir := newRepo(t)
	ctx := t.Context()
	r := New(nil, nil)
	start := head(t, dir)
	tf := TaskFiles{Cwd: dir, Start: &TaskStart{Head: start, Branch: "main"}}

	git(t, dir, "switch", "-q", "-c", "feature")
	write(t, filepath.Join(dir, "a.txt"), "changed\n")
	git(t, dir, "commit", "-q", "-am", "on feature")
	g, err := r.TaskGit(ctx, tf)
	if err != nil || !g.BranchChanged || g.Branch != "feature" || g.StartBranch != "main" ||
		g.StartState != api.GitStartRecorded || len(g.Commits) != 1 {
		t.Fatalf("branch change: %+v, %v", g, err)
	}

	// Detached HEAD counts as a change of branch too.
	git(t, dir, "switch", "-q", "--detach")
	if g, err := r.TaskGit(ctx, tf); err != nil || !g.BranchChanged || g.Branch != "" {
		t.Fatalf("detached: %+v, %v", g, err)
	}
	// Back on main, rewrite the start commit itself (an amend, as a rebase would): the start is no longer an
	// ancestor of HEAD although the branch is the same.
	git(t, dir, "switch", "-q", "main")
	git(t, dir, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
	write(t, filepath.Join(dir, "b.txt"), "dirty\n")
	g, err = r.TaskGit(ctx, tf)
	if err != nil || g.StartState != api.GitStartUnreachable || g.BranchChanged || len(g.Commits) != 0 ||
		len(g.CommittedFiles) != 0 || len(g.Files) != 1 || g.Files[0].Path != "b.txt" {
		t.Fatalf("unreachable: %+v, %v", g, err)
	}
	var se *StartError
	if _, err := r.TaskCommit(ctx, tf, head(t, dir)); !errors.As(err, &se) || se.State != api.GitStartUnreachable ||
		!errors.Is(err, ErrNoStart) {
		t.Fatalf("TaskCommit with an unreachable start: %v", err)
	}

	// A start that names no commit at all (gone after a gc, or a damaged record) is unreachable as well.
	tf.Start.Head = strings.Repeat("0", 40)
	if g, err := r.TaskGit(ctx, tf); err != nil || g.StartState != api.GitStartUnreachable {
		t.Fatalf("missing start: %+v, %v", g, err)
	}
}

// TestTaskGitLegacyAndUnborn: a task without a start shows its uncommitted files as before with startState
// "unknown" (never an error) and the commit endpoint refuses with a start error; a task that started in a
// repository without commits counts every commit as its own, diffed against the empty tree.
func TestTaskGitLegacyAndUnborn(t *testing.T) {
	dir := newRepo(t)
	ctx := t.Context()
	r := New(nil, nil)
	write(t, filepath.Join(dir, "a.txt"), "x\n")
	git(t, dir, "commit", "-q", "-am", "unseen")
	write(t, filepath.Join(dir, "b.txt"), "y\n")
	g, err := r.TaskGit(ctx, TaskFiles{Cwd: dir})
	if err != nil || g.StartState != api.GitStartUnknown || len(g.Commits) != 0 || len(g.Files) != 1 {
		t.Fatalf("legacy: %+v, %v", g, err)
	}
	var se *StartError
	if _, err := r.TaskCommit(ctx, TaskFiles{Cwd: dir}, head(t, dir)); !errors.As(err, &se) ||
		se.State != api.GitStartUnknown {
		t.Fatalf("legacy commit: %v", err)
	}

	fresh := t.TempDir()
	git(t, fresh, "init", "-q", "-b", "main")
	git(t, fresh, "config", "user.name", "Test")
	git(t, fresh, "config", "user.email", "test@example.invalid")
	tf := TaskFiles{Cwd: fresh, Start: &TaskStart{Branch: "main"}}
	if g, err := r.TaskGit(ctx, tf); err != nil || g.StartState != api.GitStartRecorded || g.Head != "" {
		t.Fatalf("unborn, no commit yet: %+v, %v", g, err)
	}
	write(t, filepath.Join(fresh, "first.txt"), "1\n2\n")
	git(t, fresh, "add", "first.txt")
	git(t, fresh, "commit", "-q", "-m", "root")
	g, err = r.TaskGit(ctx, tf)
	if err != nil || len(g.Commits) != 1 || len(g.CommittedFiles) != 1 || g.CommittedFiles[0].Additions != 2 {
		t.Fatalf("unborn start: %+v, %v", g, err)
	}
	c, err := r.TaskCommit(ctx, tf, g.Commits[0].SHA)
	if err != nil || c.Parent != "" || len(c.Diffs) != 1 || c.Files[0].Status != api.GitAdded {
		t.Fatalf("root commit: %+v, %v", c, err)
	}
	d, err := r.Diff(ctx, fresh, "first.txt", EmptyTree(g.Head), false)
	if err != nil || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 2 {
		t.Fatalf("diff against the empty tree: %+v, %v", d, err)
	}
}

// TestStatusUpstream: the repository status names the upstream and counts the commits ahead and behind; a gone
// upstream says so.
func TestStatusUpstream(t *testing.T) {
	remote := newRepo(t)
	dir := t.TempDir()
	git(t, dir, "clone", "-q", remote, ".")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "user.email", "test@example.invalid")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "local 1")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "local 2")
	git(t, remote, "commit", "-q", "--allow-empty", "-m", "remote 1")
	git(t, dir, "fetch", "-q")
	r := New(nil, nil)
	st, err := r.Status(t.Context(), dir)
	if err != nil || st.Upstream != "origin/main" || st.Ahead != 2 || st.Behind != 1 || st.UpstreamGone {
		t.Fatalf("status: %+v, %v", st, err)
	}
	git(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
	st, err = r.Status(t.Context(), dir)
	if err != nil || st.Upstream != "origin/main" || !st.UpstreamGone || st.Ahead != 0 {
		t.Fatalf("gone upstream: %+v, %v", st, err)
	}
	if st, err := r.Status(t.Context(), remote); err != nil || st.Upstream != "" || st.UpstreamGone {
		t.Fatalf("no upstream: %+v, %v", st, err)
	}
}

func TestTurnAt(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 600_000_000, time.UTC)
	turns := []TurnSpan{{N: 1, Start: base, End: base.Add(time.Minute)}, {N: 2, Start: base.Add(2 * time.Minute)}}
	for _, c := range []struct {
		at   time.Time
		want int
	}{
		{base.Truncate(time.Second), 1}, // commit times have whole seconds
		{base.Add(-2 * time.Second), 0},
		{base.Add(90 * time.Second), 0}, // between the turns
		{base.Add(time.Hour), 2},        // the open turn
	} {
		if got := turnAt(turns, c.at); got != c.want {
			t.Errorf("turnAt(%v) = %d, want %d", c.at, got, c.want)
		}
	}
}
