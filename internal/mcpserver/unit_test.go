package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/mcpserver"
)

// wantTools is the old tool list (inventory 02-interfaces §1.7–§1.23): names in order, required fields and
// property names. Written from the inventory, not from tools.json, so the two check each other.
var wantTools = []struct {
	name     string
	required []string
	props    string
}{
	{"delegate", []string{"prompt", "cwd"}, "prompt cwd title member backend provider model effort mode profile spec " +
		"task_ids gate lane force_lane allow_git verify after after_mode variants variant_specs judge deadline_minutes " +
		"wait_seconds wake_on_blocked"},
	// followup requires only task_id: force makes message optional (old C-B8).
	{"followup", []string{"task_id"}, "task_id message profile model effort task_ids gate lane force_lane allow_git " +
		"verify wait_seconds wake_on_blocked provider force"},
	{"wait", []string{"task_ids"}, "task_ids wait_seconds any wake_on_blocked"},
	{"review", []string{"task_id", "verdict"}, "task_id verdict notes answer_objection"},
	{"cancel", []string{"task_id"}, "task_id wait_seconds"},
	{"task", []string{"task_id"}, "task_id steps full"},
	{"status", nil, "full"},
	{"memory", []string{"cwd", "action"}, "cwd action text"},
	{"profiles", []string{"action"}, "action cwd scope id role"},
	{"proof", []string{"action"}, "action cwd feature force run_id wait_seconds since"},
	{"members", nil, ""},
	{"meeting", []string{"action"}, "action meeting_id question title options context cwd attendees sealed max_rounds " +
		"estimate_only wait_seconds answers note decision rationale option items dissent_id outcome full"},
	{"consult", []string{"task_id", "member"}, "task_id member focus wait_seconds"},
	// issues declares wake_on_blocked, which its handler reads (old C-B20).
	{"issues", []string{"action"}, "action repo path name first_commit private key state title body decision reason " +
		"member backend model prompt gate lane question attendees options context ticket_id status draft " +
		"commit_message number shared_dirs extra wait_seconds wake_on_blocked full"},
	{"notes", []string{"action", "task_id"}, "action task_id note_id file line end_line side body extra wait_seconds " +
		"wake_on_blocked"},
	{"automations", []string{"action"}, "action id name cwd prompt every at weekday cron timezone member backend model " +
		"gate lane precheck min_remaining use_before_reset grace_minutes limit"},
	{"contest", []string{"action"}, "action contest_id winner wait_seconds full"},
}

// stub is a fake daemon API: handlers by "METHOD /path" (under /api/v1), and the bodies it received.
type stub struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	bodies map[string][]byte
	uris   map[string]string
}

func newStub(t *testing.T, routes map[string]http.HandlerFunc) (*stub, *client.Client) {
	t.Helper()
	s := &stub{routes: routes, bodies: map[string][]byte{}, uris: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		s.mu.Lock()
		s.bodies[key], s.uris[key] = body, r.URL.RequestURI()
		h := s.routes[key]
		s.mu.Unlock()
		if h == nil {
			reply(w, http.StatusNotFound, api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeNotFound, Message: "no route " + key}})
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return s, client.New(srv.URL, srv.Client())
}

func (s *stub) body(t *testing.T, key string, out any) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := json.Unmarshal(s.bodies[key], out); err != nil {
		t.Fatalf("%s body %q: %v", key, s.bodies[key], err)
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func answer(status int, v any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { reply(w, status, v) }
}

// waitAnswer answers POST /wait with one report per requested id.
func waitAnswer(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req api.WaitRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		res := api.WaitResult{Done: true, Woke: api.WokeDone, Tasks: []api.TaskSummary{}}
		for _, id := range req.IDs {
			res.Reports = append(res.Reports, api.TaskReport{TaskID: id, Status: status, FilesChanged: []string{}})
		}
		reply(w, http.StatusOK, res)
	}
}

// stubSession connects a server on c with a fixed folder and session environment.
func stubSession(t *testing.T, c *client.Client, edit func(*mcpserver.Options)) *mcp.ClientSession {
	t.Helper()
	o := mcpserver.Options{Daemon: fixedDaemon{c: c}, Version: "test", Port: 47831, Tiers: config.Defaults().Backends.Agy.Tiers,
		Getwd: func() (string, error) { return filepath.Join(t.TempDir(), "wd"), nil },
		Getenv: func(k string) string {
			return map[string]string{"SEELEGENT_SESSION": "lead-1", "CLAUDE_CODE_SESSION_ID": "ignored",
				"CLAUDE_CODE_HOST_SESSION_ID": "host-1"}[k]
		}}
	if edit != nil {
		edit(&o)
	}
	return connect(t, o)
}

func TestToolsList(t *testing.T) {
	_, c := newStub(t, nil)
	cs := stubSession(t, c, nil)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != len(wantTools) {
		t.Fatalf("%d tools, want %d", len(res.Tools), len(wantTools))
	}
	tiers := config.Defaults().Backends.Agy.Tiers
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	for _, want := range wantTools {
		tool := byName[want.name]
		if tool == nil {
			t.Fatalf("no tool %s", want.name)
		}
		var schema struct {
			Type       string                     `json:"type"`
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		data, _ := json.Marshal(tool.InputSchema)
		if err := json.Unmarshal(data, &schema); err != nil || schema.Type != "object" {
			t.Fatalf("%s schema %s: %v", tool.Name, data, err)
		}
		if !slices.Equal(schema.Required, want.required) {
			t.Errorf("%s required = %v, want %v", tool.Name, schema.Required, want.required)
		}
		var props []string
		for p := range schema.Properties {
			props = append(props, p)
		}
		wantProps := strings.Fields(want.props)
		slices.Sort(props)
		slices.Sort(wantProps)
		if !slices.Equal(props, wantProps) {
			t.Errorf("%s properties = %v, want %v", tool.Name, props, wantProps)
		}
		if mcpserver.Unavailable()[tool.Name] != strings.HasPrefix(tool.Description, "Not available in this version") {
			t.Errorf("%s description = %.60s", tool.Name, tool.Description)
		}
		if strings.Contains(string(data), "{{") {
			t.Errorf("%s has an unfilled placeholder", tool.Name)
		}
	}
	// Shapes the old schema had: enums and oneOfs (spot checks), and the tier models in the model description.
	delegate := byName["delegate"]
	schema, _ := json.Marshal(delegate.InputSchema)
	for _, frag := range []string{`"enum":["agy","codex","claude"]`, `"enum":["full","plan"]`,
		`"enum":["low","medium","high","xhigh","max"]`, `"enum":[false]`, `"minimum":2`, `"maximum":5`,
		`"fast\" (` + tiers.Fast + `: lookups`} {
		if !strings.Contains(string(schema), frag) {
			t.Errorf("delegate schema lacks %s", frag)
		}
	}
	if !strings.HasPrefix(delegate.Description, "Hand an implementation task to a worker") ||
		!strings.HasSuffix(delegate.Description, "read gate.steps.") {
		t.Errorf("delegate description changed: %.80s", delegate.Description)
	}
	init := cs.InitializeResult()
	if init.ServerInfo.Name != "seelegent" || !strings.Contains(init.Instructions, "Live view: http://127.0.0.1:47831/") {
		t.Errorf("initialize = %+v", init)
	}
}

// TestDelegateRequest checks how delegate arguments map onto the API request.
func TestDelegateRequest(t *testing.T) {
	st, c := newStub(t, map[string]http.HandlerFunc{
		"POST /tasks": answer(http.StatusCreated, api.TaskSummary{ID: "t1", Status: api.StatusWaitingDeps}),
		"POST /wait":  waitAnswer("waiting_deps"),
	})
	var wd string
	cs := stubSession(t, c, func(o *mcpserver.Options) {
		wd = filepath.Join(t.TempDir(), "wd")
		o.Getwd = func() (string, error) { return wd, nil }
	})
	var rep api.TaskReport
	ok(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": "sub", "mode": "full", "model": "fast",
		"gate": false, "lane": false, "allow_git": "true", "spec": "specs/001", "task_ids": []string{"T1"},
		"after": []string{"t0"}, "after_mode": "done", "force_lane": true, "unknown_field": 1}, &rep)
	var req map[string]any
	st.body(t, "POST /tasks", &req)
	want := map[string]any{"prompt": "p", "cwd": filepath.Join(wd, "sub"), "mode": "edit", "model": "fast",
		"gate": false, "allowGit": true, "spec": map[string]any{"dir": "specs/001", "taskIds": []any{"T1"}},
		"after": []any{"t0"}, "afterMode": "done", "session": map[string]any{"id": "lead-1", "hostId": "host-1"}}
	a, _ := json.Marshal(req)
	b, _ := json.Marshal(want)
	if !bytes.Equal(a, b) {
		t.Fatalf("delegate body\n %s\nwant\n %s", a, b)
	}
	var wreq api.WaitRequest
	st.body(t, "POST /wait", &wreq)
	if wreq.TimeoutSeconds != 0 || !wreq.Reports || wreq.WakeOnBlocked == nil || !*wreq.WakeOnBlocked ||
		!strings.Contains(st.uris["POST /wait"], "cwd=") {
		t.Fatalf("wait = %+v %s", wreq, st.uris["POST /wait"])
	}

	// Without after the default wait is 600 s; values are clamped to 0..3000.
	ok(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": wd, "wait_seconds": 99999}, &rep)
	st.body(t, "POST /wait", &wreq)
	if wreq.TimeoutSeconds != 3000 {
		t.Fatalf("clamped wait = %d", wreq.TimeoutSeconds)
	}
	ok(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": wd}, &rep)
	st.body(t, "POST /wait", &wreq)
	if wreq.TimeoutSeconds != 600 {
		t.Fatalf("default wait = %d", wreq.TimeoutSeconds)
	}
}

func TestArgumentErrors(t *testing.T) {
	st, c := newStub(t, map[string]http.HandlerFunc{
		"GET /tasks/t1":           answer(http.StatusOK, api.TaskDetail{TaskSummary: api.TaskSummary{ID: "t1"}}),
		"POST /tasks/t1/followup": answer(http.StatusOK, api.TaskSummary{ID: "t1"}),
		"POST /wait":              waitAnswer("done"),
	})
	cs := stubSession(t, c, nil)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"delegate", map[string]any{"cwd": "x"}, "prompt is required"},
		{"delegate", map[string]any{"prompt": "p"}, "cwd is required"},
		{"delegate", map[string]any{"prompt": "p", "cwd": "x", "mode": "yolo"}, `mode "yolo" must be full or plan`},
		{"delegate", map[string]any{"prompt": "p", "cwd": "x", "gate": "go test ./..."}, "running gate commands is not available"},
		{"delegate", map[string]any{"prompt": "p", "cwd": "x", "task_ids": []string{"T1"}}, "task_ids needs spec"},
		{"delegate", map[string]any{"prompt": "p", "cwd": "x", "wait_seconds": "soon"}, "invalid argument"},
		{"followup", map[string]any{"task_id": "t1"}, "message is required"},
		{"followup", map[string]any{"task_id": "t1", "message": "m", "provider": "deepseek"},
			`cannot change provider in followup (task has provider "none", requested "deepseek")`},
		{"wait", map[string]any{}, "task_ids is required"},
		{"memory", map[string]any{"cwd": "x", "action": "remove"}, "must be read, append or replace"},
		{"profiles", map[string]any{"action": "upsert", "role": "x"}, "role object is required"},
	}
	for _, tc := range cases {
		msg := fails(t, cs, tc.tool, tc.args)
		if !strings.HasPrefix(msg, "Seelegent error: ") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s %v = %q, want %q", tc.tool, tc.args, msg, tc.want)
		}
	}
	// force makes the message optional.
	var rep api.TaskReport
	ok(t, cs, "followup", map[string]any{"task_id": "t1", "force": true, "wait_seconds": 0}, &rep)
	var req api.FollowupRequest
	st.body(t, "POST /tasks/t1/followup", &req)
	if !req.Force || req.Message != "" {
		t.Fatalf("forced followup = %+v", req)
	}
}

func TestDaemonErrors(t *testing.T) {
	_, c := newStub(t, map[string]http.HandlerFunc{
		"POST /tasks": answer(http.StatusNotImplemented, api.ErrorBody{Error: api.ErrorInfo{
			Code: api.CodeNotImplemented, Message: "this endpoint comes with a later milestone"}}),
		"POST /tasks/t1/review": answer(http.StatusConflict, api.ErrorBody{Error: api.ErrorInfo{
			Code: api.CodeBadState, Message: "task t1 is running", Details: map[string]any{"status": "running"}}}),
	})
	cs := stubSession(t, c, nil)
	if msg := fails(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": "x"}); !strings.Contains(msg,
		"is not available in this version of Seelegent yet") {
		t.Fatalf("501 = %s", msg)
	}
	res := callTool(t, cs, "review", map[string]any{"task_id": "t1", "verdict": "accepted"})
	data, _ := json.Marshal(res.StructuredContent)
	if !res.IsError || text(res, 0) != "Seelegent error: task t1 is running" ||
		!strings.Contains(string(data), `"code":"bad_state"`) || !strings.Contains(string(data), `"status":"running"`) {
		t.Fatalf("409 = %s %s", text(res, 0), data)
	}

	bad := stubSession(t, c, func(o *mcpserver.Options) { o.ConfigErr = errors.New("config.json: bad JSON") })
	if msg := fails(t, bad, "status", nil); !strings.Contains(msg, "the settings cannot be read: config.json: bad JSON") {
		t.Fatalf("config error = %s", msg)
	}
	down := stubSession(t, c, func(o *mcpserver.Options) {
		o.Daemon = failingDaemon{err: client.ErrNotRunning}
	})
	res = callTool(t, down, "status", nil)
	data, _ = json.Marshal(res.StructuredContent)
	if !res.IsError || !strings.Contains(string(data), "daemon_unavailable") {
		t.Fatalf("daemon down = %s %s", text(res, 0), data)
	}
}

type failingDaemon struct{ err error }

func (f failingDaemon) Ensure(context.Context) (*client.Client, error) { return nil, f.err }

// flakyDaemon hands out a client of a dead daemon first (it went away), then the working one.
type flakyDaemon struct {
	dead, live *client.Client
	calls      atomic.Int32
}

func (f *flakyDaemon) Ensure(context.Context) (*client.Client, error) {
	if f.calls.Add(1) == 1 {
		return f.dead, nil
	}
	return f.live, nil
}

// TestWaitSurvivesRestart: a wait whose daemon goes away finds it again and waits on (as the old client.js did).
func TestWaitSurvivesRestart(t *testing.T) {
	_, live := newStub(t, map[string]http.HandlerFunc{"POST /wait": waitAnswer("done")})
	deadSrv := httptest.NewServer(http.NotFoundHandler())
	dead := client.New(deadSrv.URL, nil)
	deadSrv.Close()
	fd := &flakyDaemon{dead: dead, live: live}
	cs := stubSession(t, live, func(o *mcpserver.Options) {
		o.Daemon = fd
		o.RetryPause = 10 * time.Millisecond
	})
	var rep api.TaskReport
	ok(t, cs, "wait", map[string]any{"task_ids": []string{"t1"}, "wait_seconds": 30}, &rep)
	if rep.Status != "done" || fd.calls.Load() < 2 {
		t.Fatalf("wait = %+v after %d ensures", rep, fd.calls.Load())
	}
}

// TestTaskSteps: steps=N shows the newest N tool steps (old B6 showed the oldest).
func TestTaskSteps(t *testing.T) {
	ev := func(seq int64, name, out string) client.RawEvent {
		data, _ := json.Marshal(map[string]any{"kind": "tool_result", "tool": map[string]any{"name": name,
			"state": "done", "input": map[string]any{"path": name}, "output": out}})
		return client.RawEvent{Seq: seq, Type: api.EventWorker, Data: data}
	}
	msg, _ := json.Marshal(map[string]any{"kind": "message", "text": "hi"})
	st, c := newStub(t, map[string]http.HandlerFunc{
		"GET /tasks/t1/report": answer(http.StatusOK, api.TaskReport{TaskID: "t1", Status: "done", FilesChanged: []string{}}),
		"GET /tasks/t1/events": answer(http.StatusOK, client.EventPage{Items: []client.RawEvent{ev(1, "a", "x"),
			{Seq: 2, Type: api.EventWorker, Data: msg}, ev(3, "b", strings.Repeat("y", 900)), ev(4, "c", "z")}}),
	})
	cs := stubSession(t, c, nil)
	var got struct {
		TaskID string `json:"task_id"`
		Steps  []struct {
			Tool   string          `json:"tool"`
			Params json.RawMessage `json:"params"`
			Output string          `json:"output"`
		} `json:"recent_steps"`
	}
	ok(t, cs, "task", map[string]any{"task_id": "t1", "steps": 2}, &got)
	if len(got.Steps) != 2 || got.Steps[0].Tool != "b" || got.Steps[1].Tool != "c" || len(got.Steps[0].Output) != 800 ||
		compact(got.Steps[1].Params) != `{"path":"c"}` {
		t.Fatalf("steps = %+v", got)
	}
	if !strings.Contains(st.uris["GET /tasks/t1/events"], "tail=2000") {
		t.Fatalf("events uri = %s", st.uris["GET /tasks/t1/events"])
	}
}

func TestStatusQuota(t *testing.T) {
	acc := api.Account{ID: "a1", Backend: "codex", Kind: "isolated", Label: "codex 1", Status: api.AccountOK,
		Busy: 1, Quota: api.QuotaSnapshot{Groups: []api.Group{
			{Name: "Codex", Windows: []api.QuotaWindow{{Window: "5h", Remaining: 0.8}, {Window: "weekly", Remaining: 0.35}}},
			{Name: "Codex gpt-5", Windows: []api.QuotaWindow{{Window: "5h", Remaining: 0.5}}}}}}
	agy := api.Account{ID: "a2", Backend: "agy", Kind: "api-key", Label: "agy 1", Status: api.AccountOK, KeyHint: "…abcd",
		Resting: true, RestingUntil: "2026-11-07T10:00:00.000Z", Quota: api.QuotaSnapshot{Groups: []api.Group{
			{Name: "Gemini Models", Windows: []api.QuotaWindow{{Window: "5h", Remaining: 0.25}, {Window: "weekly", Remaining: 1}}}}}}
	blocked := api.TaskSummary{ID: "t2", Title: "B", Status: api.StatusDone,
		Attention: &api.Attention{State: api.AttentionBlocked, Reason: "question"}}
	_, c := newStub(t, map[string]http.HandlerFunc{
		"GET /accounts": answer(http.StatusOK, []api.Account{acc, agy}),
		"GET /tasks": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("status") != "" {
				reply(w, http.StatusOK, api.TaskPage{Items: []api.TaskSummary{{ID: "t1", Title: "A", Status: api.StatusRunning}}})
				return
			}
			reply(w, http.StatusOK, api.TaskPage{Items: []api.TaskSummary{blocked}})
		},
		"GET /backends": answer(http.StatusInternalServerError, api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeInternal}}),
	})
	cs := stubSession(t, c, nil)
	var out struct {
		Accounts []map[string]any `json:"accounts"`
		Active   []map[string]any `json:"active_tasks"`
		Backends []any            `json:"backends"`
	}
	ok(t, cs, "status", nil, &out)
	a0, a1 := out.Accounts[0], out.Accounts[1]
	if a0["gemini5h"] != nil || line(a0["remaining"]) != `{"5h":50,"weekly":35}` || a0["running"] != float64(1) {
		t.Fatalf("codex account = %v", a0)
	}
	if a1["gemini5h"] != float64(25) || a1["geminiWeekly"] != float64(100) || a1["auth"] != "api-key" ||
		a1["keyHint"] != "…abcd" || a1["resting"] != true {
		t.Fatalf("agy account = %v", a1)
	}
	if len(out.Active) != 2 || out.Active[0]["id"] != "t2" || out.Active[0]["reason"] != "question" ||
		out.Active[1]["reason"] != nil || out.Backends != nil {
		t.Fatalf("active = %v backends %v", out.Active, out.Backends)
	}
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	_ = json.Compact(&b, raw)
	return b.String()
}
