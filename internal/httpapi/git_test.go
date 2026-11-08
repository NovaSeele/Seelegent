package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// waitFile waits (real time) until path exists.
func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

// TestTaskGitCommitsDuringTask (bugs.md F70): the worker commits during its turn. The Git view lists the commit
// with its turn and the file it changed, keeps the uncommitted file apart, diffs against the start, and the commit
// endpoint shows that commit only; a commit outside the task's range, a bad id and a commit of a committed file
// are refused. A branch switch is reported, and history that lost the start answers with coded states.
func TestTaskGitCommitsDuringTask(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	e.addAccount("first")
	start := strings.TrimSpace(gitIn(t, e.cwd, "rev-parse", "HEAD"))
	sync := t.TempDir()
	ready, goOn := filepath.Join(sync, "ready"), filepath.Join(sync, "go")
	e.queue.Push(t, ok("conv-1", "Committed work.",
		"#!write "+filepath.Join(e.cwd, "work.go")+"\t"+`"package work\n\nfunc A() {}\n"`,
		"#!touch "+ready, "#!wait-file "+goOn))
	sum := e.delegate("Write work.go and commit it.")

	// The worker commits work.go during its turn, and leaves notes.txt uncommitted.
	waitFile(t, ready)
	gitIn(t, e.cwd, "add", "work.go")
	gitIn(t, e.cwd, "commit", "-q", "-m", "Add work.go")
	if err := os.WriteFile(filepath.Join(e.cwd, "notes.txt"), []byte("todo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goOn, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	e.wait(sum.ID)
	commit := strings.TrimSpace(gitIn(t, e.cwd, "rev-parse", "HEAD"))

	base := "/api/v1/tasks/" + sum.ID + "/git"
	var g api.TaskGit
	e.must(e.get(base), http.StatusOK).json(t, &g)
	if g.StartState != api.GitStartRecorded || g.StartHead != start || g.StartBranch != "main" || g.BranchChanged ||
		g.Head != commit || len(g.Commits) != 1 || g.Commits[0].SHA != commit || g.Commits[0].Subject != "Add work.go" ||
		g.Commits[0].Turn != 1 || g.Commits[0].FileCount != 1 || g.CommitsMore != 0 {
		t.Fatalf("task git = %+v", g)
	}
	if len(g.CommittedFiles) != 1 || g.CommittedFiles[0].Path != "work.go" || g.CommittedFiles[0].Turn != 1 ||
		g.CommittedFiles[0].Additions != 3 || len(g.Files) != 1 || g.Files[0].Path != "notes.txt" {
		t.Fatalf("committed %+v, uncommitted %+v", g.CommittedFiles, g.Files)
	}

	// The task diff compares against the start (the committed lines show); base=head shows what is left to commit.
	var d api.FileDiff
	e.must(e.get(base+"/diff?path=work.go"), http.StatusOK).json(t, &d)
	if len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 3 {
		t.Fatalf("diff against the start = %+v", d)
	}
	e.must(e.get(base+"/diff?path=work.go&base=head"), http.StatusOK).json(t, &d)
	if len(d.Hunks) != 0 {
		t.Fatalf("diff against HEAD = %+v", d)
	}
	e.wantError(e.get(base+"/diff?path=work.go&base=main"), http.StatusBadRequest, api.CodeInvalidArgument, "base")

	// One commit, by full or short id.
	var c api.GitCommitDetail
	e.must(e.get(base+"/commits/"+commit[:7]), http.StatusOK).json(t, &c)
	if c.SHA != commit || c.Parent != start || c.Turn != 1 || len(c.Files) != 1 || c.Files[0].Path != "work.go" ||
		len(c.Diffs) != 1 || c.Diffs[0].Path != "work.go" || c.Truncated {
		t.Fatalf("commit = %+v", c)
	}
	// The start commit is not the task's; neither is a commit git does not know. Ids that are not hex never
	// reach git.
	for _, sha := range []string{start, "0123456789abcdef"} {
		if info := e.wantError(e.get(base+"/commits/"+sha), http.StatusNotFound, api.CodeNotFound, ""); info.
			Details["reason"] != "not_in_task" {
			t.Fatalf("commit %s: %+v", sha, info)
		}
	}
	for _, sha := range []string{"HEAD", "abc", "--output=x", "HEAD~1"} {
		e.wantError(e.get(base+"/commits/"+sha), http.StatusBadRequest, api.CodeInvalidArgument, "sha")
	}
	// A committed file is no longer the task's to commit; the uncommitted one is.
	e.wantError(e.post(base+"/commit", map[string]any{"message": "m", "files": []string{"work.go"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	// A switch to another branch that still holds the start: reported, the commits still listed.
	gitIn(t, e.cwd, "switch", "-q", "-c", "other")
	e.must(e.get(base), http.StatusOK).json(t, &g)
	if !g.BranchChanged || g.Branch != "other" || g.StartBranch != "main" || len(g.Commits) != 1 {
		t.Fatalf("after a branch switch: %+v", g)
	}

	// A branch whose history does not hold the start: unreachable, the uncommitted files only, a coded refusal.
	gitIn(t, e.cwd, "switch", "-q", "--orphan", "fresh")
	gitIn(t, e.cwd, "commit", "-q", "--allow-empty", "-m", "unrelated")
	e.must(e.get(base), http.StatusOK).json(t, &g)
	if g.StartState != api.GitStartUnreachable || len(g.Commits) != 0 || len(g.CommittedFiles) != 0 {
		t.Fatalf("unreachable start: %+v", g)
	}
	head := strings.TrimSpace(gitIn(t, e.cwd, "rev-parse", "HEAD"))
	if info := e.wantError(e.get(base+"/commits/"+head), http.StatusConflict, api.CodeBadState, ""); info.
		Details["reason"] != "start_unreachable" {
		t.Fatalf("commit with an unreachable start: %+v", info)
	}
}

// TestTaskGitWithoutStart: a task that recorded no start (here: its folder was not in git when it started, like a
// task from before starts were recorded) shows its uncommitted files as before with startState "unknown", and its
// commit endpoint answers 409 start_unknown; never a 500. The repository view carries the upstream fields.
func TestTaskGitWithoutStart(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	e.queue.Push(t, ok("conv-1", "Done."))
	sum := e.delegate("Do it.")
	e.wait(sum.ID)
	initRepo(t, e.cwd)
	if err := os.WriteFile(filepath.Join(e.cwd, "later.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/tasks/" + sum.ID + "/git"
	var g api.TaskGit
	e.must(e.get(base), http.StatusOK).json(t, &g)
	if !g.Repo || g.StartState != api.GitStartUnknown || g.StartHead != "" || len(g.Commits) != 0 ||
		len(g.Files) != 1 || g.Files[0].Path != "later.txt" {
		t.Fatalf("task git = %+v", g)
	}
	var d api.FileDiff
	e.must(e.get(base+"/diff?path=later.txt"), http.StatusOK).json(t, &d)
	if len(d.Hunks) != 1 {
		t.Fatalf("diff = %+v", d)
	}
	head := strings.TrimSpace(gitIn(t, e.cwd, "rev-parse", "HEAD"))
	if info := e.wantError(e.get(base+"/commits/"+head), http.StatusConflict, api.CodeBadState, ""); info.
		Details["reason"] != "start_unknown" {
		t.Fatalf("commit without a start: %+v", info)
	}
	var st api.RepoStatus
	e.must(e.get("/api/v1/git/status?cwd="+e.cwd), http.StatusOK).json(t, &st)
	if !st.Repo || st.Upstream != "" || st.UpstreamGone || len(st.Files) != 1 {
		t.Fatalf("status = %+v", st)
	}
}
