package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/app"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/mcpserver"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

func TestMain(m *testing.M) { fakecli.Main(m) }

const limit = 60 * time.Second

// switchClassifier makes every caller a worker while worker is set (each request opens its own connection).
type switchClassifier struct{ worker atomic.Bool }

func (c *switchClassifier) Classify(netip.AddrPort, netip.AddrPort, time.Time) httpapi.Caller {
	return httpapi.Caller{Worker: c.worker.Load()}
}

// daemon is a real App with the fake CLI as agy, serving the HTTP API on a random port, plus an MCP session on it.
type daemon struct {
	t     *testing.T
	a     *app.App
	queue *fakecli.Queue
	home  string
	cwd   string
	cls   *switchClassifier
	cs    *mcp.ClientSession
}

func newDaemon(t *testing.T) *daemon {
	t.Helper()
	home := t.TempDir()
	paths, err := config.NewPaths(filepath.Join(home, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The daemon stores canonical paths (a Windows short temp path becomes the long one).
	cwd, err := fsutil.Canonical(filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}
	d := &daemon{t: t, queue: fakecli.NewQueue(t), home: home, cwd: cwd, cls: &switchClassifier{}}
	set := config.Defaults()
	set.Port = 47831
	a, err := app.New(t.Context(), app.Options{Paths: paths, Settings: &set, Env: d.queue.Env(os.Environ()),
		RealHome: filepath.Join(home, "realhome"), Launcher: fakecli.Launcher(t), NoQuotaReaders: true, NoGh: true,
		Bins:       map[string]string{config.BackendAgy: fakecli.Exe(t), config.BackendClaude: "", config.BackendCodex: ""},
		Classifier: d.cls})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	d.a = a
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		if err := a.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	addr, err := a.ListenHTTP(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	c := client.New("http://"+addr, hc)
	d.cs = connect(t, mcpserver.Options{Daemon: fixedDaemon{c: c}, Version: "test", Port: 47831,
		Tiers: set.Backends.Agy.Tiers, Getwd: func() (string, error) { return home, nil },
		Getenv: func(k string) string {
			if k == "CLAUDE_CODE_SESSION_ID" {
				return "sess-1"
			}
			return ""
		}})
	return d
}

// fixedDaemon always returns the same client.
type fixedDaemon struct{ c *client.Client }

func (f fixedDaemon) Ensure(context.Context) (*client.Client, error) { return f.c, nil }
func (f fixedDaemon) Find(context.Context) (*client.Client, error)   { return f.c, nil }

// connect runs a server with o over an in-memory transport and returns the client session.
func connect(t *testing.T, o mcpserver.Options) *mcp.ClientSession {
	t.Helper()
	srv, err := mcpserver.New(o)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	return cs
}

func (d *daemon) account(label string) account.Account {
	d.t.Helper()
	acc, _, err := d.a.Accounts.Add(d.t.Context(), account.New{Backend: config.BackendAgy, Kind: account.KindIsolated,
		Label: label, Enabled: true}, nil)
	if err != nil {
		d.t.Fatal(err)
	}
	d.a.Pool.Notify()
	return acc
}

// callTool calls a tool and returns the result.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// ok calls a tool that must succeed and decodes its JSON (first text block) into out. It checks the envelope:
// the structured content is the same JSON (a list wrapped as items) and a summary follows.
func ok(t *testing.T, cs *mcp.ClientSession, name string, args, out any) string {
	t.Helper()
	res := callTool(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s failed: %s", name, text(res, 0))
	}
	raw := text(res, 0)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		t.Fatalf("%s: %v in %s", name, err, raw)
	}
	var first, structured any
	_ = json.Unmarshal([]byte(raw), &first)
	if _, isList := first.([]any); isList {
		first = map[string]any{"items": first}
	}
	sdata, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(sdata, &structured)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(structured)
	if !bytes.Equal(a, b) {
		t.Fatalf("%s: structured content %s differs from text %s", name, b, a)
	}
	if len(res.Content) != 2 || text(res, 1) == "" {
		t.Fatalf("%s: no summary in %d blocks", name, len(res.Content))
	}
	return text(res, 1)
}

// fails calls a tool that must fail and returns its text.
func fails(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res := callTool(t, cs, name, args)
	if !res.IsError {
		t.Fatalf("%s succeeded: %s", name, text(res, 0))
	}
	return text(res, 0)
}

func text(res *mcp.CallToolResult, i int) string {
	if i >= len(res.Content) {
		return ""
	}
	tc, isText := res.Content[i].(*mcp.TextContent)
	if !isText {
		return ""
	}
	return tc.Text
}

func line(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func initLine(conv string) string {
	return line(map[string]any{"event": "init", "conversation_id": conv, "init": map[string]any{"model": "m"}})
}

func resultLine(conv, response string) string {
	return line(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "response": response,
		"conversation_id": conv, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
}

func script(conv, response string) string {
	return initLine(conv) + "\n" + resultLine(conv, response) + "\n"
}

// TestTasksEndToEnd drives the task tools against a real daemon and the fake CLI.
func TestTasksEndToEnd(t *testing.T) {
	d := newDaemon(t)
	d.account("first")

	d.queue.Push(t, script("conv-1", "Turn one."))
	var rep api.TaskReport
	sum := ok(t, d.cs, "delegate", map[string]any{"prompt": "Build it.", "cwd": "project", "mode": "full",
		"wait_seconds": 60}, &rep)
	if rep.Status != "done" || rep.Response != "Turn one." || rep.Turn != 1 || !strings.Contains(sum, "done") {
		t.Fatalf("delegate = %+v (%s)", rep, sum)
	}
	detail, err := d.a.Tasks.Get(t.Context(), rep.TaskID)
	if err != nil || detail.Cwd != d.cwd || detail.Session == nil || detail.Session.ID != "sess-1" ||
		detail.Session.Lead != api.LeadClaude {
		t.Fatalf("task cwd %q session %+v, %v", detail.Cwd, detail.Session, err)
	}

	d.queue.Push(t, script("conv-1", "Turn two."))
	ok(t, d.cs, "followup", map[string]any{"task_id": rep.TaskID, "message": "And tests.", "wait_seconds": "60"}, &rep)
	if rep.Status != "done" || rep.Turn != 2 || rep.Response != "Turn two." {
		t.Fatalf("followup = %+v", rep)
	}

	d.queue.Push(t, script("conv-2", "Second task."))
	var second api.TaskReport
	ok(t, d.cs, "delegate", map[string]any{"prompt": "Other.", "cwd": d.cwd, "wait_seconds": 0}, &second)
	var reports []api.TaskReport
	sum = ok(t, d.cs, "wait", map[string]any{"task_ids": []string{rep.TaskID, second.TaskID}, "wait_seconds": 60},
		&reports)
	if len(reports) != 2 || reports[1].Status != "done" || !strings.Contains(sum, "Woke: done") {
		t.Fatalf("wait = %+v (%s)", reports, sum)
	}

	var withSteps struct {
		api.TaskReport
		RecentSteps []any `json:"recent_steps"`
	}
	ok(t, d.cs, "task", map[string]any{"task_id": rep.TaskID, "steps": 3}, &withSteps)
	if withSteps.TaskID != rep.TaskID || withSteps.Turn != 2 {
		t.Fatalf("task = %+v", withSteps)
	}
	var full api.TaskDetail
	ok(t, d.cs, "task", map[string]any{"task_id": rep.TaskID, "full": true}, &full)
	if len(full.Turns) != 2 {
		t.Fatalf("task full = %+v", full)
	}

	var reviewed map[string]string
	ok(t, d.cs, "review", map[string]any{"task_id": rep.TaskID, "verdict": "accepted", "notes": "fine"}, &reviewed)
	if reviewed["task_id"] != rep.TaskID || reviewed["status"] != "done" {
		t.Fatalf("review = %v", reviewed)
	}
	if msg := fails(t, d.cs, "review", map[string]any{"task_id": second.TaskID, "verdict": "acepted"}); !strings.HasPrefix(msg, "Seelegent error: ") {
		t.Fatalf("bad verdict = %s", msg)
	}
	if msg := fails(t, d.cs, "task", map[string]any{"task_id": "t-none"}); !strings.Contains(msg, "Seelegent error") {
		t.Fatalf("unknown task = %s", msg)
	}
}

// TestCancelledWaitKeepsTheWorker: a host cancel ends the wait at once and never cancels the task (old C-B4).
func TestCancelledWaitKeepsTheWorker(t *testing.T) {
	d := newDaemon(t)
	d.account("first")
	d.queue.Push(t, initLine("conv-h")+"\n#!hang\n")
	var rep api.TaskReport
	ok(t, d.cs, "delegate", map[string]any{"prompt": "Hang.", "cwd": d.cwd, "wait_seconds": 0}, &rep)
	waitLive(t, d.a, rep.TaskID)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := d.cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait",
			Arguments: map[string]any{"task_ids": []string{rep.TaskID}, "wait_seconds": 3000}})
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the cancelled wait returned no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled wait did not end")
	}
	time.Sleep(300 * time.Millisecond)
	sum, err := d.a.Tasks.Summary(t.Context(), rep.TaskID)
	if err != nil || sum.Status != api.StatusRunning {
		t.Fatalf("after the cancelled wait the task is %s (%v)", sum.Status, err)
	}

	var st map[string]string
	ok(t, d.cs, "cancel", map[string]any{"task_id": rep.TaskID, "wait_seconds": 30}, &st)
	if st["status"] != "cancelled" {
		t.Fatalf("cancel = %v", st)
	}
}

func waitLive(t *testing.T, a *app.App, id string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		for _, w := range a.Tasks.LiveWorkers() {
			if w.TaskID == id {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no live worker for %s", id)
}

func TestStatusMemoryProfiles(t *testing.T) {
	d := newDaemon(t)
	d.account("first")

	var slim struct {
		Accounts []map[string]any `json:"accounts"`
		Active   []map[string]any `json:"active_tasks"`
		Backends []map[string]any `json:"backends"`
	}
	sum := ok(t, d.cs, "status", map[string]any{}, &slim)
	if len(slim.Accounts) != 1 || slim.Accounts[0]["label"] != "first" || slim.Accounts[0]["backend"] != "agy" ||
		slim.Accounts[0]["auth"] != "login" || len(slim.Active) != 0 || len(slim.Backends) != 3 ||
		!strings.Contains(sum, "1 account") {
		t.Fatalf("status = %+v (%s)", slim, sum)
	}
	for _, k := range []string{"id", "status", "running", "gemini5h", "geminiWeekly", "provider", "keyHint"} {
		if _, has := slim.Accounts[0][k]; !has {
			t.Fatalf("slim account lacks %s: %v", k, slim.Accounts[0])
		}
	}
	var full map[string]any
	ok(t, d.cs, "status", map[string]any{"full": true}, &full)
	if full["dashboard"] != "http://127.0.0.1:47831/" {
		t.Fatalf("full status = %v", full)
	}

	var m api.Memory
	ok(t, d.cs, "memory", map[string]any{"cwd": "project", "action": "append", "text": "Use pnpm."}, &m)
	ok(t, d.cs, "memory", map[string]any{"cwd": d.cwd, "action": "read"}, &m)
	if !strings.Contains(m.Text, "Use pnpm.") || m.Cwd != d.cwd {
		t.Fatalf("memory = %+v", m)
	}
	ok(t, d.cs, "memory", map[string]any{"cwd": d.cwd, "action": "replace", "text": "Only this."}, &m)
	if strings.TrimSpace(m.Text) != "Only this." {
		t.Fatalf("replaced memory = %q", m.Text)
	}
	// The summary counts characters, not bytes.
	sum = ok(t, d.cs, "memory", map[string]any{"cwd": d.cwd, "action": "replace", "text": "Ghi chú tiếng Việt"}, &m)
	if n := utf8.RuneCountInString(m.Text); n == len(m.Text) || !strings.HasSuffix(sum, fmt.Sprintf(": %d characters.", n)) {
		t.Fatalf("summary %q for %q", sum, m.Text)
	}
	if msg := fails(t, d.cs, "memory", map[string]any{"cwd": d.cwd, "action": "delete", "text": "x"}); !strings.Contains(msg, "must be read, append or replace") {
		t.Fatalf("bad action = %s", msg)
	}

	var r api.Role
	ok(t, d.cs, "profiles", map[string]any{"action": "upsert", "role": map[string]any{"id": "reviewer",
		"name": "Reviewer", "rules": "Be strict."}}, &r)
	if r.ID != "reviewer" || r.Scope != "global" {
		t.Fatalf("upsert = %+v", r)
	}
	var roles []map[string]any
	ok(t, d.cs, "profiles", map[string]any{"action": "list"}, &roles)
	found := false
	for _, x := range roles {
		if x["id"] == "reviewer" {
			_, hasCreated := x["createdAt"]
			found = !hasCreated
		}
	}
	if !found {
		t.Fatalf("list = %v", roles)
	}
	ok(t, d.cs, "profiles", map[string]any{"action": "get", "id": "reviewer"}, &r)
	var del map[string]any
	ok(t, d.cs, "profiles", map[string]any{"action": "delete", "id": "reviewer"}, &del)
	if msg := fails(t, d.cs, "profiles", map[string]any{"action": "get", "id": "reviewer"}); !strings.Contains(msg, "not found") {
		t.Fatalf("get deleted = %s", msg)
	}
	var skills []any
	ok(t, d.cs, "profiles", map[string]any{"action": "skills", "cwd": d.cwd}, &skills)
}

// TestWorkerRefusedByDaemon: the daemon's worker guard answers 403 caller worker; the tool shows the old refusal.
func TestWorkerRefusedByDaemon(t *testing.T) {
	d := newDaemon(t)
	d.cls.worker.Store(true)
	for _, name := range []string{"delegate", "status", "memory"} {
		args := map[string]any{"prompt": "x", "cwd": d.cwd, "action": "append", "text": "x"}
		if msg := fails(t, d.cs, name, args); msg != "Seelegent is not available inside a Seelegent worker." {
			t.Fatalf("%s as a worker = %q", name, msg)
		}
	}
}

// TestIssuesAndAutomations: the milestone 15 tools reach the daemon. A repo is made and connected by path, a
// local issue goes through the inbox, and an automation is created disabled, can only be asked to be enabled,
// and runs once on demand (its precheck skips it).
func TestIssuesAndAutomations(t *testing.T) {
	if len(mcpserver.Unavailable()) != 0 {
		t.Fatalf("unavailable tools: %v", mcpserver.Unavailable())
	}
	// The first commit of a new repository is the user's: give git an author where none is configured.
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_COMMITTER_NAME": "Test",
		"GIT_AUTHOR_EMAIL": "test@example.invalid", "GIT_COMMITTER_EMAIL": "test@example.invalid"} {
		t.Setenv(k, v)
	}
	d := newDaemon(t)
	repoDir := filepath.Join(d.home, "repo")
	if err := os.MkdirAll(repoDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var repo api.Repo
	ok(t, d.cs, "issues", map[string]any{"action": "init", "path": repoDir}, &repo)
	var repos []api.Repo
	if sum := ok(t, d.cs, "issues", map[string]any{"action": "repos"}, &repos); len(repos) != 1 ||
		!strings.Contains(sum, "manual") {
		t.Fatalf("repos = %v, %s", repos, sum)
	}
	var issue map[string]any
	ok(t, d.cs, "issues", map[string]any{"action": "add", "repo": repoDir, "title": "Fix the bug", "body": "b"},
		&issue)
	if issue["key"] != "local#1" || issue["body"] != nil {
		t.Fatalf("add = %v", issue)
	}
	ok(t, d.cs, "issues", map[string]any{"action": "suggest", "repo": repo.ID, "key": "local#1",
		"decision": "skip", "reason": "dup"}, &issue)
	if s, _ := issue["suggestion"].(map[string]any); s["action"] != "skip" {
		t.Fatalf("suggest = %v", issue)
	}
	var list []map[string]any
	ok(t, d.cs, "issues", map[string]any{"action": "list", "repo": repo.ID}, &list)
	if len(list) != 1 || list[0]["key"] != "local#1" {
		t.Fatalf("list = %v", list)
	}
	var tri map[string]any
	ok(t, d.cs, "issues", map[string]any{"action": "triage", "repo": repo.ID, "key": "local#1",
		"decision": "skip", "reason": "duplicate"}, &tri)
	if dec, _ := tri["issue"].(map[string]any)["decision"].(map[string]any); dec["by"] != "lead" {
		t.Fatalf("triage = %v", tri)
	}
	var settings map[string]any
	ok(t, d.cs, "issues", map[string]any{"action": "repo_settings", "repo": repo.ID, "shared_dirs": "node_modules"},
		&settings)
	if dirs, _ := settings["sharedDirs"].([]any); len(dirs) != 1 || dirs[0] != "node_modules" {
		t.Fatalf("repo_settings = %v", settings)
	}
	var tickets []any
	ok(t, d.cs, "issues", map[string]any{"action": "tickets"}, &tickets)
	if msg := fails(t, d.cs, "issues", map[string]any{"action": "list", "repo": filepath.Join(d.home, "x")}); !strings.Contains(msg, "not connected") {
		t.Fatalf("unknown repo = %s", msg)
	}
	if msg := fails(t, d.cs, "issues", map[string]any{"action": "pr"}); !strings.Contains(msg, "ticket_id") {
		t.Fatalf("pr = %s", msg)
	}

	var x map[string]any
	sum := ok(t, d.cs, "automations", map[string]any{"action": "create", "name": "nightly", "cwd": d.cwd,
		"prompt": "check", "every": "daily", "at": "09:00", "precheck": "exit 3"}, &x)
	id, _ := x["id"].(string)
	if id == "" || x["enabled"] != false || !strings.Contains(sum, "disabled") {
		t.Fatalf("create = %v, %s", x, sum)
	}
	var en map[string]any
	ok(t, d.cs, "automations", map[string]any{"action": "enable", "id": id}, &en)
	if url, _ := en["confirmUrl"].(string); en["confirm"] != true || !strings.Contains(url, "/automations?confirm="+id) ||
		en["automation"].(map[string]any)["enabled"] != false {
		t.Fatalf("enable = %v", en)
	}
	// Run now runs only what the user confirmed in the dashboard.
	if msg := fails(t, d.cs, "automations", map[string]any{"action": "run", "id": id}); !strings.Contains(msg,
		"confirm="+id) {
		t.Fatalf("run unconfirmed = %s", msg)
	}
	var runs []api.AutomationRun
	ok(t, d.cs, "automations", map[string]any{"action": "runs", "id": id}, &runs)
	if len(runs) != 0 {
		t.Fatalf("runs = %v", runs)
	}
	ok(t, d.cs, "automations", map[string]any{"action": "update", "id": id, "lane": false, "min_remaining": 50}, &x)
	if q, _ := x["quota"].(map[string]any); q["minRemaining"] != float64(50) {
		t.Fatalf("update = %v", x)
	}
	x = nil // a reused map keeps the keys the next answer leaves out
	ok(t, d.cs, "automations", map[string]any{"action": "disable", "id": id}, &x)
	if x["enabled"] != false || x["enableRequested"] != nil {
		t.Fatalf("disable = %v", x)
	}
	ok(t, d.cs, "automations", map[string]any{"action": "delete", "id": id}, &x)
	var all []any
	ok(t, d.cs, "automations", map[string]any{"action": "list"}, &all)
	if len(all) != 0 {
		t.Fatalf("list = %v", all)
	}
	if msg := fails(t, d.cs, "automations", map[string]any{"action": "enable"}); !strings.Contains(msg, "id is required") {
		t.Fatalf("enable without id = %s", msg)
	}
}

// TestTeamTools: the milestone 14 tools reach the daemon (a contest needs a git repository, which d.cwd is not).
func TestTeamTools(t *testing.T) {
	d := newDaemon(t)
	msg := fails(t, d.cs, "delegate", map[string]any{"prompt": "x", "cwd": d.cwd, "variants": 2})
	if !strings.Contains(msg, "git repository") {
		t.Fatalf("variants = %s", msg)
	}
	var members map[string][]any
	ok(t, d.cs, "members", map[string]any{}, &members)
	if len(members["members"]) != 0 {
		t.Fatalf("members = %v", members)
	}
	var list []any
	ok(t, d.cs, "meeting", map[string]any{"action": "list"}, &list)
	ok(t, d.cs, "contest", map[string]any{"action": "status"}, &list)
	if msg := fails(t, d.cs, "meeting", map[string]any{"action": "open", "question": "Which?", "cwd": d.cwd,
		"attendees": []string{"ghost"}}); !strings.Contains(msg, "ghost") {
		t.Fatalf("meeting open = %s", msg)
	}
	if msg := fails(t, d.cs, "consult", map[string]any{"task_id": "t-nope", "member": "ghost"}); !strings.Contains(msg,
		"not found") && !strings.Contains(msg, "ghost") {
		t.Fatalf("consult = %s", msg)
	}
	if msg := fails(t, d.cs, "notes", map[string]any{"action": "list", "task_id": "t-nope"}); !strings.Contains(msg,
		"not found") {
		t.Fatalf("notes = %s", msg)
	}
}
