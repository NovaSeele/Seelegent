package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestMembersOverHTTP covers members CRUD, PATCH as a merge, members.changed and delegate by a turned-off member
// (409 member_disabled).
func TestMembersOverHTTP(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	st := e.stream("")
	st.until("hello", nil)

	var m api.Member
	e.must(e.post("/api/v1/members", map[string]any{"id": "ana", "name": "Ana", "backend": "agy", "rank": "senior",
		"tier": "strong"}), http.StatusCreated).json(t, &m)
	if m.ID != "ana" || !m.Enabled || m.Rank != api.RankSenior {
		t.Fatalf("created = %+v", m)
	}
	st.until("members.changed", nil)
	e.wantError(e.post("/api/v1/members", map[string]any{"id": "ana", "name": "Ana", "backend": "agy", "rank": "senior"}),
		http.StatusConflict, api.CodeConflict, "")
	e.wantError(e.post("/api/v1/members", map[string]any{"id": "Bad Id", "name": "x", "rank": "senior"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	e.must(e.req(http.MethodPatch, "/api/v1/members/ana", map[string]any{"enabled": false}, nil), http.StatusOK).
		json(t, &m)
	if m.Enabled || m.Name != "Ana" || m.Tier != "strong" {
		t.Fatalf("patched = %+v", m)
	}
	e.wantError(e.req(http.MethodPatch, "/api/v1/members/ana", map[string]any{"id": "bob"}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "id")
	e.wantError(e.req(http.MethodPatch, "/api/v1/members/nope", map[string]any{"name": "x"}, nil),
		http.StatusNotFound, api.CodeNotFound, "")

	got := e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "member": "ana"}),
		http.StatusConflict, api.CodeConflict, "")
	if got.Details["reason"] != "member_disabled" || got.Details["member"] != "ana" {
		t.Errorf("details = %v", got.Details)
	}
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "member": "ghost"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	e.must(e.req(http.MethodPatch, "/api/v1/members/ana", map[string]any{"enabled": true}, nil), http.StatusOK)
	e.queue.Push(t, ok("c1", "done"))
	var sum api.TaskSummary
	e.must(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "member": "ana"}),
		http.StatusCreated).json(t, &sum)
	if sum.Member != "ana" {
		t.Errorf("summary member = %q", sum.Member)
	}
	e.wait(sum.ID)

	var track []api.MemberTrack
	e.must(e.get("/api/v1/members/track"), http.StatusOK).json(t, &track)
	var list []api.Member
	e.must(e.get("/api/v1/members"), http.StatusOK).json(t, &list)
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	e.must(e.req(http.MethodDelete, "/api/v1/members/ana", nil, nil), http.StatusNoContent)
	e.wantError(e.req(http.MethodDelete, "/api/v1/members/ana", nil, nil), http.StatusNotFound, api.CodeNotFound, "")
	e.must(e.get("/api/v1/members"), http.StatusOK).json(t, &list)
	if len(list) != 0 {
		t.Fatalf("after delete = %+v", list)
	}
}

// TestNotesConsultHooksOverHTTP covers the review notes of a finished task, the consult checks and the hooks.
func TestNotesConsultHooksOverHTTP(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	done := e.delegate("Write a.txt.")
	e.wait(done.ID)
	st := e.stream("")
	st.until("hello", nil)

	var n api.Note
	e.must(e.post("/api/v1/tasks/"+done.ID+"/notes", map[string]any{"file": "a.txt", "line": 1, "body": "Rename it."}),
		http.StatusCreated).json(t, &n)
	if n.ID == "" || n.Body != "Rename it." {
		t.Fatalf("note = %+v", n)
	}
	st.until("notes.changed", func(f frame) bool { return strings.Contains(f.data, done.ID) })
	e.wantError(e.post("/api/v1/tasks/"+done.ID+"/notes", map[string]any{"file": "../out.txt", "line": 1,
		"body": "x"}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.post("/api/v1/tasks/"+done.ID+"/notes", map[string]any{"file": "a.txt", "line": 1, "body": "x",
		"by": "robot"}), http.StatusBadRequest, api.CodeInvalidArgument, "by")
	e.wantError(e.get("/api/v1/tasks/t-nope/notes"), http.StatusNotFound, api.CodeNotFound, "")
	e.must(e.req(http.MethodPatch, "/api/v1/tasks/"+done.ID+"/notes/"+n.ID, map[string]any{"state": "resolved"},
		nil), http.StatusOK).json(t, &n)
	if n.State != api.NoteResolved {
		t.Errorf("state = %q", n.State)
	}
	var notes []api.Note
	e.must(e.get("/api/v1/tasks/"+done.ID+"/notes"), http.StatusOK).json(t, &notes)
	if len(notes) != 1 {
		t.Fatalf("notes = %+v", notes)
	}
	e.must(e.req(http.MethodDelete, "/api/v1/tasks/"+done.ID+"/notes/"+n.ID, nil, nil), http.StatusNoContent)

	e.wantError(e.post("/api/v1/tasks/"+done.ID+"/consult", map[string]any{}), http.StatusBadRequest,
		api.CodeInvalidArgument, "member")
	e.wantError(e.post("/api/v1/tasks/"+done.ID+"/consult", map[string]any{"member": "ghost"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	const run = "echo hook-ok"
	var hooks []api.Hook
	e.must(e.req(http.MethodPut, "/api/v1/hooks", map[string]any{"hooks": []map[string]any{
		{"name": "done", "on": []string{"task.done"}, "run": run, "enabled": true}}}, nil), http.StatusOK).
		json(t, &hooks)
	if len(hooks) != 1 || hooks[0].ID == "" {
		t.Fatalf("hooks = %+v", hooks)
	}
	e.wantError(e.req(http.MethodPut, "/api/v1/hooks", map[string]any{"hooks": []map[string]any{
		{"on": []string{"task.exploded"}, "run": run}}}, nil), http.StatusBadRequest, api.CodeInvalidArgument, "")
	var hr api.HookRun
	e.must(e.post("/api/v1/hooks/"+hooks[0].ID+"/test", nil), http.StatusOK).json(t, &hr)
	if hr.ExitCode == nil || *hr.ExitCode != 0 || !strings.Contains(hr.Output, "hook-ok") || !hr.Test {
		t.Errorf("run = %+v", hr)
	}
	st.until("hook.ran", nil)
	e.wantError(e.post("/api/v1/hooks/h-nope/test", nil), http.StatusNotFound, api.CodeNotFound, "")
}

// A PUT /hooks without the list is refused and keeps every hook; only an explicit [] removes them all (M18 hunt
// H14).
func TestPutHooksWithoutTheListKeepsThem(t *testing.T) {
	e := newEnv(t)
	var hooks []api.Hook
	e.must(e.req(http.MethodPut, "/api/v1/hooks", map[string]any{"hooks": []map[string]any{
		{"name": "done", "on": []string{"task.done"}, "run": "echo ok", "enabled": true}}}, nil), http.StatusOK).
		json(t, &hooks)
	if len(hooks) != 1 {
		t.Fatalf("hooks = %+v", hooks)
	}
	for _, body := range []string{`{}`, `null`, `{"hooks": null}`} {
		e.wantError(e.req(http.MethodPut, "/api/v1/hooks", body, nil), http.StatusBadRequest,
			api.CodeInvalidArgument, "hooks")
		e.must(e.get("/api/v1/hooks"), http.StatusOK).json(t, &hooks)
		if len(hooks) != 1 {
			t.Fatalf("after PUT %s: hooks = %+v", body, hooks)
		}
	}
	e.must(e.req(http.MethodPut, "/api/v1/hooks", `{"hooks": []}`, nil), http.StatusOK).json(t, &hooks)
	if len(hooks) != 0 {
		t.Fatalf("PUT [] left %+v", hooks)
	}
	e.must(e.get("/api/v1/hooks"), http.StatusOK).json(t, &hooks)
	if len(hooks) != 0 {
		t.Fatalf("after PUT []: hooks = %+v", hooks)
	}
}

// TestMeetingsContestsValidation covers the lists and the refusals of meetings and contests.
func TestMeetingsContestsValidation(t *testing.T) {
	e := newEnv(t)
	var ms []api.MeetingSummary
	e.must(e.get("/api/v1/meetings"), http.StatusOK).json(t, &ms)
	if len(ms) != 0 {
		t.Fatalf("meetings = %+v", ms)
	}
	e.wantError(e.post("/api/v1/meetings", map[string]any{"question": "Which?", "cwd": e.cwd,
		"attendees": []string{"ghost"}}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.get("/api/v1/meetings/m-nope"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/meetings/m-nope/wait?seconds=1501"), http.StatusBadRequest, api.CodeInvalidArgument,
		"seconds")
	e.wantError(e.post("/api/v1/meetings/m-nope/dissents/d1/outcome", map[string]any{"outcome": "maybe"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "outcome")

	var cs []api.ContestSummary
	e.must(e.get("/api/v1/contests"), http.StatusOK).json(t, &cs)
	if len(cs) != 0 {
		t.Fatalf("contests = %+v", cs)
	}
	// e.cwd is not a git repository.
	e.wantError(e.post("/api/v1/contests", map[string]any{"prompt": "x", "cwd": e.cwd, "variants": 2}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.post("/api/v1/contests", map[string]any{"prompt": "x", "cwd": "rel"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "cwd")
	e.wantError(e.get("/api/v1/contests/k-nope"), http.StatusNotFound, api.CodeNotFound, "")
	// meetingId and contestId are never request fields of POST /tasks (C-B21).
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "meetingId": "m1"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "meetingId")
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "contestId": "k1"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "contestId")
}
