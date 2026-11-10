package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	gitPath, err := gitx.NewBinary(nil, nil).Path() // as the daemon finds it: never a Windows batch shim
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), gitPath, args...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "test@example.invalid")
	gitIn(t, dir, "config", "user.name", "Test")
	gitIn(t, dir, "config", "core.hooksPath", filepath.Join(dir, ".no-hooks"))
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-q", "-m", "init")
}

// TestTaskLifecycle drives a task through the API with the fake CLI: delegate, SSE, wait, detail, events (tail,
// old B6), report, prompt, list paging, git (diff, suggestion, commit rules), followup/commit refusals while
// running, cancel, review (verdict enum, old B15) and the system endpoints.
func TestTaskLifecycle(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	acc := e.addAccount("first")

	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)
	if len(boot.Accounts) != 1 || boot.Accounts[0].ID != acc.ID || len(boot.Backends) != 3 || boot.LastEventID == "" ||
		boot.Settings.Port != 47831 || boot.Tasks == nil || boot.Projects == nil {
		t.Fatalf("state = %+v", boot)
	}
	stream := e.stream(boot.LastEventID)
	hello := stream.next()
	if hello.event != "hello" || hello.retry != "2000" || hello.id != boot.LastEventID {
		t.Fatalf("first frame = %+v", hello)
	}

	e.queue.Push(t, ok("conv-1", "Added new.txt.", "#!touch "+filepath.Join(e.cwd, "new.txt")))
	sum := e.delegate("Add a file.")
	if sum.Status == "" || sum.Cwd == "" {
		t.Fatalf("delegate = %+v", sum)
	}
	created := stream.until("task.created", nil)
	var cs api.TaskSummary
	if err := json.Unmarshal([]byte(created.data), &cs); err != nil || cs.ID != sum.ID {
		t.Fatalf("task.created = %s, %v", created.data, err)
	}
	ev := stream.until("task.event", nil)
	var te api.TaskEvent
	if err := json.Unmarshal([]byte(ev.data), &te); err != nil || te.TaskID != sum.ID || te.Seq < 1 {
		t.Fatalf("task.event = %s", ev.data)
	}
	res := e.wait(sum.ID)
	if res.Woke != api.WokeDone || res.Tasks[0].Status != api.StatusDone {
		t.Fatalf("wait = %+v", res)
	}
	// since (a mark taken before the caller created the tasks, N2) is an RFC 3339 time.
	e.wantError(e.post("/api/v1/wait?since=yesterday", map[string]any{"ids": []string{sum.ID}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "since")
	e.must(e.post("/api/v1/wait?since=2026-11-08T00:00:00.000Z", map[string]any{"ids": []string{sum.ID}}),
		http.StatusOK).json(t, &res)
	if res.Woke != api.WokeDone {
		t.Fatalf("wait since = %+v", res)
	}
	stream.until("task.updated", func(f frame) bool { return strings.Contains(f.data, `"status":"done"`) })

	var detail api.TaskDetail
	e.must(e.get("/api/v1/tasks/"+sum.ID), http.StatusOK).json(t, &detail)
	if detail.Prompt != "Add a file." || len(detail.Turns) != 1 || detail.Turns[0].Response != "Added new.txt." {
		t.Fatalf("detail = %+v", detail)
	}

	var all, tail, first api.EventPage
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/events"), http.StatusOK).json(t, &all)
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/events?tail=2"), http.StatusOK).json(t, &tail)
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/events?after=0&limit=1"), http.StatusOK).json(t, &first)
	n := len(all.Items)
	if n < 3 || len(tail.Items) != 2 || tail.Items[1].Seq != all.Items[n-1].Seq || tail.LastSeq != all.LastSeq ||
		len(first.Items) != 1 || first.Items[0].Seq != 1 {
		t.Fatalf("events: all %d, tail %+v, first %+v", n, tail.Items, first.Items)
	}
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/events?tail=2&after=1"), http.StatusBadRequest,
		api.CodeInvalidArgument, "tail")
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/events?after=-1"), http.StatusBadRequest,
		api.CodeInvalidArgument, "after")

	var rep api.TaskReport
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/report"), http.StatusOK).json(t, &rep)
	if rep.TaskID != sum.ID || rep.Response != "Added new.txt." {
		t.Fatalf("report = %+v", rep)
	}
	var prompt api.PromptText
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/prompt?turn=1"), http.StatusOK).json(t, &prompt)
	if !strings.Contains(prompt.Prompt, "Add a file.") {
		t.Fatalf("prompt = %q", prompt.Prompt)
	}
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/prompt"), http.StatusBadRequest, api.CodeInvalidArgument, "turn")
	if info := e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/prompt?turn=9"), http.StatusNotFound, api.CodeNotFound,
		""); !strings.Contains(info.Message, "has no turn 9") {
		t.Fatalf("prompt of a missing turn: %+v", info)
	}

	// git: the touched file is the task's change.
	var g api.TaskGit
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/git"), http.StatusOK).json(t, &g)
	if !g.Repo || g.Branch != "main" || len(g.Files) != 1 || g.Files[0].Path != "new.txt" ||
		g.Files[0].Status != api.GitUntracked || g.Suggestion != nil {
		t.Fatalf("task git = %+v", g)
	}
	var d api.FileDiff
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/git/diff?path=new.txt"), http.StatusOK).json(t, &d)
	if d.Path != "new.txt" {
		t.Fatalf("diff = %+v", d)
	}
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/git/diff?path=../x"), http.StatusBadRequest,
		api.CodeInvalidArgument, "path")
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/git/suggest-message", nil), http.StatusAccepted).json(t, &g)
	if g.Suggestion == nil || g.Suggestion.State != api.SuggestionReady || g.Suggestion.Message == "" {
		t.Fatalf("suggestion = %+v", g.Suggestion)
	}
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/git/suggest-message/dismiss", nil), http.StatusOK).json(t, &g)
	if g.Suggestion != nil {
		t.Fatalf("dismissed suggestion = %+v", g.Suggestion)
	}
	e.wantError(e.post("/api/v1/tasks/"+sum.ID+"/git/commit", map[string]any{"message": "", "files": []string{"new.txt"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.post("/api/v1/tasks/"+sum.ID+"/git/commit", map[string]any{"message": "m", "files": []string{"README.md"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
	var cr api.CommitResult
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/git/commit", map[string]any{"message": "Add new.txt", "files": []string{"new.txt"}}),
		http.StatusOK).json(t, &cr)
	if len(cr.SHA) != 40 || !strings.Contains(gitIn(t, e.cwd, "log", "--oneline", "-1"), "Add new.txt") {
		t.Fatalf("commit = %+v", cr)
	}
	// The worker created new.txt with a shell command, not an edit tool: it is an outside edit.
	var outside []api.FileDiff
	e.must(e.get("/api/v1/tasks/"+sum.ID+"/turns/1/outside-diff"), http.StatusOK).json(t, &outside)
	if len(outside) != 1 || outside[0].Path != "new.txt" {
		t.Fatalf("outside diff = %+v", outside)
	}
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/turns/x/outside-diff"), http.StatusBadRequest,
		api.CodeInvalidArgument, "n")
	e.wantError(e.get("/api/v1/tasks/"+sum.ID+"/turns/9/outside-diff"), http.StatusNotFound, api.CodeNotFound, "")
	// Nothing was at risk: a restore must name its files, and a file the turn started without is skipped.
	e.wantError(e.post("/api/v1/tasks/"+sum.ID+"/turns/1/restore", map[string]any{}), http.StatusBadRequest,
		api.CodeInvalidArgument, "")
	var rr api.RestoreResult
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/turns/1/restore", map[string]any{"files": []string{"new.txt"}}),
		http.StatusOK).json(t, &rr)
	if len(rr.Restored) != 0 || len(rr.Skipped) != 1 {
		t.Fatalf("restore = %+v", rr)
	}
	e.wantError(e.post("/api/v1/tasks/"+sum.ID+"/turns/1/restore", map[string]any{"files": []string{"../x"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	var rs api.RepoStatus
	e.must(e.get("/api/v1/git/status?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &rs)
	if !rs.Repo || rs.Branch != "main" || rs.Dirty {
		t.Fatalf("repo status = %+v", rs)
	}
	gone := filepath.Join(e.cwd, "gone")
	var br map[string]*api.BranchInfo
	e.must(e.get("/api/v1/git/branches?cwd="+url.QueryEscape(e.cwd)+"&cwd="+url.QueryEscape(gone)), http.StatusOK).
		json(t, &br)
	if br[e.cwd] == nil || br[e.cwd].Branch != "main" || br[gone] != nil || len(br) != 2 {
		t.Fatalf("branches = %+v", br)
	}

	// A running task: followup and commit are refused, cancel stops it.
	e.queue.Push(t, hang("conv-2"))
	run := e.delegate("Hang.")
	e.wantError(e.post("/api/v1/tasks/"+run.ID+"/followup", map[string]any{"message": "more"}),
		http.StatusConflict, api.CodeBadState, "")
	e.wantError(e.post("/api/v1/tasks/"+run.ID+"/git/commit", map[string]any{"message": "m", "files": []string{"x"}}),
		http.StatusConflict, api.CodeBadState, "")
	e.wantError(e.post("/api/v1/tasks/"+run.ID+"/followup", map[string]any{"message": "more", "by": "boss"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "by")
	e.wantError(e.post("/api/v1/tasks/"+run.ID+"/cancel", map[string]any{"waitSeconds": 61}),
		http.StatusBadRequest, api.CodeInvalidArgument, "waitSeconds")
	var cancelled api.TaskSummary
	r := e.post("/api/v1/tasks/"+run.ID+"/cancel", map[string]any{"waitSeconds": 30})
	if r.status != http.StatusOK && r.status != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", r.status, r.body)
	}
	r.json(t, &cancelled)

	// Review: the verdict enum is strict.
	e.wantError(e.post("/api/v1/tasks/"+sum.ID+"/review", map[string]any{"verdict": "accept"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "verdict")
	var reviewed api.TaskSummary
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/review", map[string]any{"verdict": "accepted", "notes": "fine"}),
		http.StatusOK).json(t, &reviewed)
	if reviewed.Review == nil || reviewed.Review.Verdict != api.VerdictAccepted {
		t.Fatalf("review = %+v", reviewed.Review)
	}
	// A rule goes into the project's notes; one that cannot be saved is a warning of a review that succeeds.
	var lesson, tooLong api.TaskSummary
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/review", map[string]any{"verdict": "accepted", "rule": "Run go vet."}),
		http.StatusOK).json(t, &lesson)
	if !lesson.LessonSaved || len(lesson.Warnings) != 0 {
		t.Fatalf("review with a rule = %+v", lesson)
	}
	var notes api.Memory
	e.must(e.get("/api/v1/memory?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &notes)
	if notes.Text != "## Lessons from reviews\n- Run go vet.\n" {
		t.Fatalf("notes = %q", notes.Text)
	}
	e.must(e.post("/api/v1/tasks/"+sum.ID+"/review", map[string]any{"verdict": "rejected",
		"rule": strings.Repeat("x", 301)}), http.StatusOK).json(t, &tooLong)
	if tooLong.LessonSaved || len(tooLong.Warnings) != 1 ||
		!strings.HasPrefix(tooLong.Warnings[0], "rule not saved: invalid: the rule is 301 bytes") ||
		tooLong.Review == nil || tooLong.Review.Verdict != api.VerdictRejected {
		t.Fatalf("review with a long rule = %+v", tooLong)
	}

	// Lists: keyset paging, filters.
	var p1, p2, done api.TaskPage
	e.must(e.get("/api/v1/tasks?limit=1"), http.StatusOK).json(t, &p1)
	if len(p1.Items) != 1 || p1.Items[0].ID != run.ID || p1.NextCursor == "" {
		t.Fatalf("page 1 = %+v", p1)
	}
	e.must(e.get("/api/v1/tasks?limit=1&cursor="+url.QueryEscape(p1.NextCursor)), http.StatusOK).json(t, &p2)
	if len(p2.Items) != 1 || p2.Items[0].ID != sum.ID || p2.NextCursor != "" {
		t.Fatalf("page 2 = %+v", p2)
	}
	e.must(e.get("/api/v1/tasks?status=done,failed&q=add"), http.StatusOK).json(t, &done)
	if len(done.Items) != 1 || done.Items[0].ID != sum.ID {
		t.Fatalf("filtered = %+v", done)
	}

	var projects []api.Project
	e.must(e.get("/api/v1/projects"), http.StatusOK).json(t, &projects)
	if len(projects) != 1 || projects[0].TaskCount != 2 {
		t.Fatalf("projects = %+v", projects)
	}
	var backends []api.Backend
	e.must(e.get("/api/v1/backends"), http.StatusOK).json(t, &backends)
	if backends[0].ID != "agy" || !backends[0].Installed || backends[0].Accounts != 1 || backends[1].Installed {
		t.Fatalf("backends = %+v", backends)
	}
	var models []api.Model
	e.must(e.get("/api/v1/models?backend=agy"), http.StatusOK).json(t, &models)
	if len(models) == 0 || models[0].Backend != "agy" || len(models[0].Tiers) == 0 {
		t.Fatalf("models = %+v", models)
	}
	e.wantError(e.get("/api/v1/models?backend=gpt"), http.StatusBadRequest, api.CodeInvalidArgument, "backend")
}

// TestSpecAndLogs covers GET /spec (tasks.md parsing, dir inside cwd) and GET /logs.
func TestSpecAndLogs(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.cwd, "specs", "001-x")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	md := "# Tasks: X\n\n## Phase 1: Setup\n- [x] T001 Create the module\n- [ ] T002 [P] [US1] Add the parser\n" +
		"\n## Phase 2: Core\n- [X] T003 [P] Wire it\nnot a task\n"
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	var v api.SpecView
	e.must(e.get("/api/v1/spec?cwd="+url.QueryEscape(e.cwd)+"&dir=specs/001-x"), http.StatusOK).json(t, &v)
	want := []api.SpecTask{
		{ID: "T001", Title: "Create the module", Done: true, Phase: "Phase 1: Setup"},
		{ID: "T002", Title: "Add the parser", Phase: "Phase 1: Setup", Parallel: true},
		{ID: "T003", Title: "Wire it", Done: true, Phase: "Phase 2: Core", Parallel: true},
	}
	if v.Dir != "specs/001-x" || v.Feature != "001-x" || len(v.Tasks) != 3 {
		t.Fatalf("spec = %+v", v)
	}
	for i := range want {
		if v.Tasks[i] != want[i] {
			t.Errorf("task %d = %+v, want %+v", i, v.Tasks[i], want[i])
		}
	}
	e.wantError(e.get("/api/v1/spec?cwd="+url.QueryEscape(dir)+"&dir=../.."), http.StatusBadRequest,
		api.CodeInvalidArgument, "dir")
	e.wantError(e.get("/api/v1/spec?cwd="+url.QueryEscape(e.cwd)+"&dir=."), http.StatusNotFound, api.CodeNotFound, "")
	// A tasks.md that is not a regular file, or is over 4 MiB, is refused.
	if err := os.MkdirAll(filepath.Join(e.cwd, "specs", "002-folder", "tasks.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.wantError(e.get("/api/v1/spec?cwd="+url.QueryEscape(e.cwd)+"&dir=specs/002-folder"), http.StatusBadRequest,
		api.CodeInvalidArgument, "dir")
	big := filepath.Join(e.cwd, "specs", "003-big")
	if err := os.MkdirAll(big, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(big, "tasks.md"), []byte(strings.Repeat("- [ ] T001 x\n", 4<<20/13+1)),
		0o600); err != nil {
		t.Fatal(err)
	}
	e.wantError(e.get("/api/v1/spec?cwd="+url.QueryEscape(e.cwd)+"&dir=specs/003-big"), http.StatusBadRequest,
		api.CodeInvalidArgument, "dir")

	if err := os.WriteFile(filepath.Join(e.paths.Logs, "hooks.log"), []byte("one\r\ntwo\nkey sk-ant-api03-"+
		strings.Repeat("a", 40)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs api.LogLines
	e.must(e.get("/api/v1/logs?name=hooks&tail=2"), http.StatusOK).json(t, &logs)
	if logs.Name != "hooks" || len(logs.Lines) != 2 || logs.Lines[0] != "two" || strings.Contains(logs.Lines[1], "aaaa") {
		t.Fatalf("logs = %+v", logs)
	}
	e.wantError(e.get("/api/v1/logs?tail=0"), http.StatusBadRequest, api.CodeInvalidArgument, "tail")
}
