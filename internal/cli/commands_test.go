package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/app"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/doctor"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/ops"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// envExecute makes the test binary run the real command line (Execute, with its signal handling) instead of the
// tests or the fake CLI.
const envExecute = "SEELEGENT_TEST_EXECUTE"

func TestMain(m *testing.M) {
	if os.Getenv(envExecute) != "" {
		Execute()
	}
	fakecli.Main(m)
}

const limit = 60 * time.Second

// cliEnv clears the variables that change how the command line behaves, so the developer's own do not leak in.
func cliEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{account.EnvWorker, ops.EnvLead, ops.EnvSession, envNoAutoStart, config.EnvPort,
		"CLAUDE_CODE_SESSION_ID", "CODEX_SESSION_ID", "CODEX_THREAD_ID", "GEMINI_SESSION_ID", "GEMINI_CLI_SESSION"} {
		t.Setenv(k, "")
	}
	t.Setenv(envNoAutoStart, "1")
	t.Setenv(backend.EnvAgyBin, fakecli.Exe(t))
}

// testDeps never spawn a process and record the URLs the browser was asked to open.
type opened struct{ urls []string }

func testDeps(o *opened) deps {
	return deps{
		spawn: func(config.Paths, int) (client.StartFunc, error) {
			return func(context.Context) (<-chan error, error) { panic("the test started a daemon process") }, nil
		},
		openURL: func(_ context.Context, url string) error {
			if o != nil {
				o.urls = append(o.urls, url)
			}
			return nil
		},
		stopTimeout: 30 * time.Second,
	}
}

// result is one command line run.
type result struct {
	code           int
	stdout, stderr string
}

func (r result) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(r.stdout), v); err != nil {
		t.Fatalf("stdout is not the JSON wanted (%v):\n%s\nstderr: %s", err, r.stdout, r.stderr)
	}
}

func runCLI(t *testing.T, d deps, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*limit)
	defer cancel()
	var out, errOut bytes.Buffer
	code := run(ctx, d, args, &out, &errOut)
	return result{code: code, stdout: out.String(), stderr: errOut.String()}
}

// must runs a command that must succeed.
func must(t *testing.T, d deps, args ...string) result {
	t.Helper()
	r := runCLI(t, d, args...)
	if r.code != ExitOK {
		t.Fatalf("seelegent %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

// wantErr runs a command that must fail with exit code and, with --json, the error code.
func wantErr(t *testing.T, d deps, exit int, code string, args ...string) result {
	t.Helper()
	r := runCLI(t, d, args...)
	if r.code != exit {
		t.Fatalf("seelegent %s: exit %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, exit,
			r.stdout, r.stderr)
	}
	if code != "" {
		var e errorOut
		r.json(t, &e)
		if e.Code != code || e.Error == "" {
			t.Fatalf("seelegent %s: error %+v, want code %s", strings.Join(args, " "), e, code)
		}
	}
	return r
}

// testDaemon is a real App with the fake CLI as agy on a random port; SEELEGENT_HOME and SEELEGENT_PORT point the
// command line at it.
type testDaemon struct {
	a     *app.App
	queue *fakecli.Queue
	paths config.Paths
	cwd   string
}

func newTestDaemon(t *testing.T) *testDaemon {
	t.Helper()
	cliEnv(t)
	home := t.TempDir()
	paths, err := config.NewPaths(filepath.Join(home, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	cwd, err := fsutil.Canonical(filepath.Join(home, "project"))
	if err != nil {
		t.Fatal(err)
	}
	d := &testDaemon{queue: fakecli.NewQueue(t), paths: paths, cwd: cwd}
	set := config.Defaults()
	a, err := app.New(t.Context(), app.Options{Paths: paths, Settings: &set, Env: d.queue.Env(os.Environ()),
		RealHome: filepath.Join(home, "realhome"), Launcher: fakecli.Launcher(t), NoQuotaReaders: true, NoGh: true,
		Bins: map[string]string{config.BackendAgy: fakecli.Exe(t), config.BackendClaude: "", config.BackendCodex: ""}})
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
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvHome, paths.Home)
	t.Setenv(config.EnvPort, strconv.Itoa(int(ap.Port())))
	acc, _, err := a.Accounts.Add(t.Context(), account.New{Backend: config.BackendAgy, Kind: account.KindIsolated,
		Label: "first", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = acc
	a.Pool.Notify()
	return d
}

func line(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func script(conv, response string) string {
	return line(map[string]any{"event": "init", "conversation_id": conv, "init": map[string]any{"model": "m"}}) + "\n" +
		line(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS", "response": response,
			"conversation_id": conv, "usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}}) +
		"\n"
}

// TestTaskCommands drives the task commands against a real daemon and the fake worker.
func TestTaskCommands(t *testing.T) {
	d := newTestDaemon(t)
	td := testDeps(nil)
	if err := os.WriteFile(filepath.Join(d.cwd, "prompt.txt"), []byte("Do the thing from a file."), 0o600); err != nil {
		t.Fatal(err)
	}

	// delegate with @file relative to --cwd; the lead is cli.
	d.queue.Push(t, script("conv-1", "Done one."))
	var rep api.TaskReport
	must(t, td, "delegate", "--cwd", d.cwd, "--title", "First", "--json", "@prompt.txt").json(t, &rep)
	if rep.Status != string(api.StatusDone) || rep.Response != "Done one." || rep.TaskID == "" || rep.Dashboard == "" {
		t.Fatalf("delegate report = %+v", rep)
	}
	id := rep.TaskID
	var detail api.TaskDetail
	must(t, td, "task", id, "--full", "--json").json(t, &detail)
	if detail.Prompt != "Do the thing from a file." || detail.Session == nil || detail.Session.Lead != api.LeadCLI ||
		detail.Title != "First" {
		t.Fatalf("task --full = prompt %q, session %+v, title %q", detail.Prompt, detail.Session, detail.Title)
	}

	// followup (text), then wait, task and ls.
	d.queue.Push(t, script("conv-1", "Done two."))
	out := must(t, td, "followup", id, "Second", "turn.").stdout
	if !strings.Contains(out, "Followup sent to task "+id) || !strings.Contains(out, "Done two.") ||
		!strings.Contains(out, "["+string(api.StatusDone)+"], turn 2") {
		t.Fatalf("followup text:\n%s", out)
	}
	var wr api.WaitResult
	must(t, td, "wait", id, "--seconds", "0", "--json").json(t, &wr)
	if len(wr.Reports) != 1 || wr.Reports[0].TaskID != id || !wr.Done {
		t.Fatalf("wait = %+v", wr)
	}
	if out := must(t, td, "wait", id, "--seconds", "0").stdout; !strings.Contains(out, "Wait finished (woke: done") ||
		!strings.Contains(out, "First") {
		t.Fatalf("wait text:\n%s", out)
	}
	if out := must(t, td, "task", id).stdout; !strings.Contains(out, "Task "+id+" [done]") || !strings.Contains(out, "Turns: 2") {
		t.Fatalf("task text:\n%s", out)
	}
	var page api.TaskPage
	must(t, td, "ls", "--json").json(t, &page)
	if len(page.Items) != 1 || page.Items[0].ID != id {
		t.Fatalf("ls = %+v", page)
	}
	if out := must(t, td, "ls").stdout; !strings.Contains(out, "STATUS") || !strings.Contains(out, id) {
		t.Fatalf("ls text:\n%s", out)
	}
	must(t, td, "ls", "--blocked", "--json").json(t, &page)
	if len(page.Items) != 0 {
		t.Fatalf("ls --blocked = %+v", page)
	}

	// review
	var sum api.TaskSummary
	must(t, td, "review", id, "accepted", "--notes", "Good.", "--json").json(t, &sum)
	if sum.Review == nil || sum.Review.Verdict != "accepted" {
		t.Fatalf("review = %+v", sum)
	}
	wantErr(t, td, ExitUsage, codeUsage, "review", id, "maybe", "--json")

	// a lead named by SEELEGENT_LEAD; --wait 0 returns at once.
	t.Setenv(ops.EnvLead, "Codex")
	d.queue.Push(t, script("conv-2", "Done three."))
	out = must(t, td, "delegate", "--cwd", d.cwd, "--wait", "0", "Third.").stdout
	if !strings.Contains(out, "delegated [") {
		t.Fatalf("delegate --wait 0:\n%s", out)
	}
	id2 := strings.Fields(out)[1]
	must(t, td, "wait", id2, "--json").json(t, &wr)
	must(t, td, "task", id2, "--full", "--json").json(t, &detail)
	if detail.Session == nil || detail.Session.Lead != api.LeadCodex {
		t.Fatalf("lead = %+v", detail.Session)
	}
	t.Setenv(ops.EnvLead, "")

	// run: the report JSON without --json.
	d.queue.Push(t, script("conv-3", "Ran."))
	must(t, td, "run", "--cwd", d.cwd, "Run", "it.").json(t, &rep)
	if rep.Response != "Ran." {
		t.Fatalf("run = %+v", rep)
	}

	// cancel a hanging task once it runs.
	started := filepath.Join(t.TempDir(), "started")
	d.queue.Push(t, line(map[string]any{"event": "init", "conversation_id": "conv-h", "init": map[string]any{"model": "m"}})+
		"\n#!touch "+started+"\n#!hang\n")
	must(t, td, "delegate", "--cwd", d.cwd, "--wait", "0", "--json", "Hang.").json(t, &rep)
	hung := rep.TaskID
	for deadline := time.Now().Add(limit); ; time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the hanging worker did not start")
		}
	}
	r := must(t, td, "cancel", hung, "--wait", "60", "--json")
	r.json(t, &rep)
	if rep.Status != string(api.StatusCancelled) {
		t.Fatalf("cancel = %+v", rep)
	}

	// memory
	var mem api.Memory
	must(t, td, "memory", "append", "--cwd", d.cwd, "Use", "tabs.", "--json").json(t, &mem)
	if !strings.Contains(mem.Text, "Use tabs.") {
		t.Fatalf("memory append = %+v", mem)
	}
	if out := must(t, td, "memory", "read", "--cwd", d.cwd).stdout; !strings.Contains(out, "Use tabs.") {
		t.Fatalf("memory read:\n%s", out)
	}
	if err := os.WriteFile(filepath.Join(d.cwd, "notes.md"), []byte("From file."), 0o600); err != nil {
		t.Fatal(err)
	}
	must(t, td, "memory", "replace", "--cwd", d.cwd, "@notes.md")
	must(t, td, "memory", "read", "--cwd", d.cwd, "--json").json(t, &mem)
	if strings.TrimSpace(mem.Text) != "From file." {
		t.Fatalf("memory replace from @file = %q", mem.Text)
	}
	wantErr(t, td, ExitUsage, codeUsage, "memory", "append", "--cwd", d.cwd, "--json")
	wantErr(t, td, ExitUsage, codeUsage, "memory", "read", "--json")
	wantErr(t, td, ExitUsage, codeUsage, "memory", "erase", "--cwd", d.cwd, "--json")

	// errors from the API keep their code.
	wantErr(t, td, ExitError, string(api.CodeNotFound), "task", "no-such-task", "--json")
	if r := wantErr(t, td, ExitError, "", "task", "no-such-task"); !strings.HasPrefix(r.stderr, "Error: ") || r.stdout != "" {
		t.Fatalf("text error: %+v", r)
	}
	if left := d.queue.Left(t); left != 0 {
		t.Fatalf("%d scripts left", left)
	}
}

// TestContestFlags: contest options that do not go together are usage errors before anything is sent.
func TestContestFlags(t *testing.T) {
	cliEnv(t)
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvPort, strconv.Itoa(freeTestPort(t)))
	td := testDeps(nil)
	for _, args := range [][]string{
		{"delegate", "--variants", "2", "--after", "t1", "x"},
		{"delegate", "--variant", "color=red", "x"},
		{"delegate", "--judge", "ana", "--no-judge", "x"},
		{"members", "set", "ana"},
		{"meeting", "post", "m1", "--answer", "no-equals"},
		{"notes", "add", "t1", "body"},
		{"consult", "t1"},
	} {
		wantErr(t, td, ExitUsage, codeUsage, append(args, "--json")...)
	}
	// Nothing runs: the daemon is not started and not needed for the refusal; without one the command fails.
	wantErr(t, td, ExitError, codeDaemon, "ls", "--json")
	if r := wantErr(t, td, ExitError, "", "ls"); !strings.Contains(r.stderr, envNoAutoStart) {
		t.Fatalf("no autostart: %s", r.stderr)
	}
}

// TestWorkerGuard (old B12): inside a worker the commands that change something are refused before anything else;
// the read-only ones stay allowed.
func TestWorkerGuard(t *testing.T) {
	d := newTestDaemon(t)
	td := testDeps(nil)
	t.Setenv(account.EnvWorker, "1")
	for _, args := range [][]string{
		{"delegate", "x"}, {"delegate", "--bogus-flag", "x"}, {"run", "x"}, {"followup", "t-1", "x"},
		{"review", "t-1", "accepted"}, {"cancel", "t-1"}, {"memory", "append", "--cwd", d.cwd, "x"},
		{"memory", "replace", "--cwd", d.cwd, "x"}, {"memory", "--cwd", d.cwd}, {"stop"}, {"restart"}, {"ui"},
		{"daemon"}, {"serve", "--port", "1"}, {"proof", "run", "--cwd", d.cwd}, {"proof", "cancel-run", "r1"},
		{"members", "add", "--name", "x"}, {"members", "remove", "x"}, {"consult", "t-1", "--member", "x"},
		{"notes", "send", "t-1"}, {"meeting", "open", "q"}, {"meeting", "decide", "m1", "x"},
		{"contest", "pick", "k1"}, {"hooks", "test", "h1"}, {"hooks", "set", "x.json"},
		{"issues", "triage", "r1", "gh#1", "skip"}, {"issues", "pr", "k1"}, {"issues", "sync", "r1"},
		{"automations", "enable", "a1"}, {"automations", "create", "x"}, {"automations", "run", "a1"},
	} {
		r := runCLI(t, td, args...)
		if r.code != ExitError || r.stderr != ops.WorkerMessage+"\n" || r.stdout != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, r.code, r.stdout, r.stderr)
		}
		var e errorOut
		jr := runCLI(t, td, append(args, "--json")...)
		jr.json(t, &e)
		if jr.code != ExitError || e.Code != string(api.CodeForbidden) || e.Details["caller"] != "worker" ||
			e.Error != ops.WorkerMessage {
			t.Errorf("%v --json: exit %d, %+v", args, jr.code, e)
		}
	}
	must(t, td, "status")
	must(t, td, "ls")
	must(t, td, "memory", "read", "--cwd", d.cwd)
	must(t, td, "proof", "lanes", "--cwd", d.cwd)
	must(t, td, "version")
	// The team reads are allowed here; the daemon itself still refuses a real worker (contract §1.3).
	must(t, td, "members", "list")
	must(t, td, "meeting", "list")
	must(t, td, "contest", "list")
	must(t, td, "hooks", "list")
	must(t, td, "issues", "repos")
	must(t, td, "issues", "tickets")
	must(t, td, "automations", "list")
}

// gitIdentity gives git an author where none is configured (a fresh HOME): the first commit of a new repository is
// the user's, not Seelegent's.
func gitIdentity(t *testing.T) {
	t.Helper()
	for k, v := range map[string]string{"GIT_AUTHOR_NAME": "Test", "GIT_COMMITTER_NAME": "Test",
		"GIT_AUTHOR_EMAIL": "test@example.invalid", "GIT_COMMITTER_EMAIL": "test@example.invalid"} {
		t.Setenv(k, v)
	}
}

// TestGitHubCommands: the issues and automations commands (milestone 15). Nothing reaches GitHub: the repo has no
// remote, and the daemon never runs gh (NoGh).
func TestGitHubCommands(t *testing.T) {
	gitIdentity(t)
	d := newTestDaemon(t)
	td := testDeps(nil)
	repo := filepath.Join(filepath.Dir(d.cwd), "repo")
	var r api.Repo
	must(t, td, "issues", "init", repo, "--json").json(t, &r)
	if r.ID == "" || r.GitHub {
		t.Fatalf("init = %+v", r)
	}
	if out := must(t, td, "issues", "repos").stdout; !strings.Contains(out, "GitHub access: manual") ||
		!strings.Contains(out, r.ID) {
		t.Fatalf("repos:\n%s", out)
	}
	var i api.Issue
	must(t, td, "issues", "add", repo, "--title", "Fix it", "body text", "--json").json(t, &i)
	if i.Key != "local#1" || i.Body != "body text" {
		t.Fatalf("add = %+v", i)
	}
	must(t, td, "issues", "suggest", r.ID, "local#1", "skip", "--reason", "dup")
	var tri api.TriageResult
	must(t, td, "issues", "triage", r.ID, "local#1", "skip", "--reason", "duplicate", "--json").json(t, &tri)
	if tri.Issue.Decision == nil || tri.Issue.Decision.By != "lead" || tri.Ticket != nil {
		t.Fatalf("triage = %+v", tri)
	}
	if out := must(t, td, "issues", "list", r.ID, "--state", "all").stdout; !strings.Contains(out, "local#1") ||
		!strings.Contains(out, "skip") {
		t.Fatalf("list:\n%s", out)
	}
	if out := must(t, td, "issues", "shared-dirs", r.ID, "node_modules").stdout; out != "node_modules\n" {
		t.Fatalf("shared-dirs: %q", out)
	}
	if out := must(t, td, "issues", "shared-dirs", r.ID, "--clear").stdout; out != "" {
		t.Fatalf("shared-dirs --clear: %q", out)
	}
	wantErr(t, td, ExitError, "", "issues", "list", filepath.Join(repo, "nope"))
	wantErr(t, td, ExitError, string(api.CodeNotFound), "issues", "ticket", "k-nope", "--json")
	wantErr(t, td, ExitUsage, "usage", "issues", "link-pr", "k1", "zero", "--json")
	wantErr(t, td, ExitUsage, "usage", "issues", "token", "--json")

	var x api.Automation
	must(t, td, "automations", "create", "--cwd", d.cwd, "--every", "daily", "--at", "09:00", "--precheck",
		"exit 3", "--name", "nightly", "check the logs", "--json").json(t, &x)
	if x.ID == "" || x.Enabled || x.Prompt != "check the logs" || x.Quota.MinRemaining != 20 {
		t.Fatalf("create = %+v", x)
	}
	var en api.AutomationEnableResult
	must(t, td, "automations", "enable", x.ID, "--json").json(t, &en)
	if !en.Confirm || en.Automation.Enabled || !strings.Contains(en.ConfirmURL, "confirm="+x.ID) {
		t.Fatalf("enable = %+v", en)
	}
	// Run now runs only what the user confirmed in the dashboard.
	if r := wantErr(t, td, ExitError, "bad_state", "automations", "run", x.ID, "--json"); !strings.Contains(r.stdout,
		"not_confirmed") {
		t.Fatalf("run unconfirmed:\n%s", r.stdout)
	}
	if out := must(t, td, "automations", "runs", x.ID).stdout; out != "" {
		t.Fatalf("runs:\n%s", out)
	}
	must(t, td, "automations", "update", x.ID, "--min-remaining", "50", "--json").json(t, &x)
	if x.Quota.MinRemaining != 50 || x.Schedule.At != "09:00" {
		t.Fatalf("update = %+v", x)
	}
	must(t, td, "automations", "disable", x.ID, "--json").json(t, &x)
	if x.Enabled || x.EnableRequested {
		t.Fatalf("disable = %+v", x)
	}
	var pv api.SchedulePreview
	must(t, td, "automations", "preview", "--cron", "0 9 * * 1", "--timezone", "UTC", "--count", "2", "--json").
		json(t, &pv)
	if len(pv.Runs) != 2 {
		t.Fatalf("preview = %+v", pv)
	}
	wantErr(t, td, ExitUsage, "usage", "automations", "preview", "--json")
	must(t, td, "automations", "delete", x.ID)
	if out := must(t, td, "automations", "list").stdout; out != "" {
		t.Fatalf("list after delete: %q", out)
	}
	wantErr(t, td, ExitUsage, "usage", "automations", "confirm", x.ID, "--json")
}

// TestTeamCommands: members, hooks, meetings, contests and notes through the command line.
func TestTeamCommands(t *testing.T) {
	d := newTestDaemon(t)
	td := testDeps(nil)
	var m api.Member
	must(t, td, "members", "add", "--id", "ana", "--name", "Ana", "--backend", "agy", "--rank", "peer", "--json").
		json(t, &m)
	if m.ID != "ana" || m.Rank != api.RankPeer || !m.Enabled {
		t.Fatalf("added %+v", m)
	}
	must(t, td, "members", "set", "ana", "--enabled=false", "--json").json(t, &m)
	if m.Enabled || m.Name != "Ana" {
		t.Fatalf("set %+v", m)
	}
	if out := must(t, td, "members", "list").stdout; !strings.Contains(out, "ana") || !strings.Contains(out, "off") {
		t.Fatalf("list:\n%s", out)
	}
	r := wantErr(t, td, ExitError, string(api.CodeConflict), "delegate", "--cwd", d.cwd, "--member", "ana", "x",
		"--json")
	if !strings.Contains(r.stdout, "member_disabled") {
		t.Errorf("disabled member: %s", r.stdout)
	}
	must(t, td, "members", "remove", "ana")
	wantErr(t, td, ExitError, string(api.CodeNotFound), "members", "remove", "ana", "--json")

	hooks := filepath.Join(t.TempDir(), "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":[{"on":["task.done"],"run":"echo hi","enabled":true}]}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	var hs []api.Hook
	must(t, td, "hooks", "set", hooks, "--json").json(t, &hs)
	if len(hs) != 1 || hs[0].ID == "" {
		t.Fatalf("hooks = %+v", hs)
	}
	var run api.HookRun
	must(t, td, "hooks", "test", hs[0].ID, "--json").json(t, &run)
	if run.ExitCode == nil || *run.ExitCode != 0 || !strings.Contains(run.Output, "hi") {
		t.Errorf("run = %+v", run)
	}
	if out := must(t, td, "meeting", "list").stdout; out != "No meetings.\n" {
		t.Errorf("meetings: %q", out)
	}
	if out := must(t, td, "contest", "list").stdout; out != "No contests.\n" {
		t.Errorf("contests: %q", out)
	}
	wantErr(t, td, ExitError, string(api.CodeNotFound), "notes", "list", "t-nope", "--json")
	wantErr(t, td, ExitError, string(api.CodeInvalidArgument), "delegate", "--cwd", d.cwd, "--variants", "2", "x",
		"--json")
}

func TestStatusAndUI(t *testing.T) {
	newTestDaemon(t)
	var o opened
	td := testDeps(&o)
	var st statusOut
	must(t, td, "status", "--json").json(t, &st)
	if !st.Running || st.Health == nil || len(st.Accounts) != 1 || st.Accounts[0].Label != "first" ||
		len(st.Backends) == 0 || st.ActiveTasks == nil || st.Dashboard == "" {
		t.Fatalf("status = %+v", st)
	}
	out := must(t, td, "status").stdout
	if !strings.Contains(out, "running at "+st.Dashboard) || !strings.Contains(out, "first") {
		t.Fatalf("status text:\n%s", out)
	}
	if out := must(t, td, "ui").stdout; out != "Dashboard: "+st.Dashboard+"\n" || len(o.urls) != 1 ||
		o.urls[0] != st.Dashboard {
		t.Fatalf("ui = %q, opened %v", out, o.urls)
	}
	var ui struct{ Dashboard string }
	must(t, td, "ui", "--no-open", "--json").json(t, &ui)
	if ui.Dashboard != st.Dashboard || len(o.urls) != 1 {
		t.Fatalf("ui --no-open = %+v, opened %v", ui, o.urls)
	}

	// Another home where nothing runs: status says so (exit 0) and never starts a daemon.
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(config.EnvPort, strconv.Itoa(freeTestPort(t)))
	t.Setenv(envNoAutoStart, "")
	if out := must(t, td, "status").stdout; out != "not running\n" {
		t.Fatalf("status, nothing running = %q", out)
	}
	must(t, td, "status", "--json").json(t, &st)
	if st.Running || st.Accounts == nil {
		t.Fatalf("status --json, nothing running = %+v", st)
	}
}

// inProcessDeps start the daemon as `seelegent daemon` would, but in this process (runDaemon).
func inProcessDeps(t *testing.T) deps {
	d := testDeps(nil)
	d.spawn = func(_ config.Paths, port int) (client.StartFunc, error) {
		return func(context.Context) (<-chan error, error) {
			exited := make(chan error, 1)
			go func() {
				exited <- runDaemon(context.WithoutCancel(t.Context()), testInfo, daemonFlags{port: port}, io.Discard)
			}()
			return exited, nil
		}, nil
	}
	return d
}

// TestStopRestart covers stop and restart with a real `seelegent daemon` (run in this process).
func TestStopRestart(t *testing.T) {
	cliEnv(t)
	paths := testHome(t)
	port := freeTestPort(t)
	t.Setenv(config.EnvPort, strconv.Itoa(port))
	td := inProcessDeps(t)
	t.Cleanup(func() { runCLI(t, td, "stop") })

	if out := must(t, td, "stop").stdout; out != "not running\n" {
		t.Fatalf("stop, nothing running = %q", out)
	}
	// restart starts it even with SEELEGENT_NO_AUTO_START=1.
	if out := must(t, td, "restart").stdout; out != "Daemon restarted.\n" {
		t.Fatalf("restart = %q", out)
	}
	if pid, ok := client.ReadPortFilePID(paths); !ok || pid != os.Getpid() {
		t.Fatalf("port file pid = %d, %v", pid, ok)
	}
	var st statusOut
	must(t, td, "status", "--json").json(t, &st)
	if !st.Running || st.Health.Port != port {
		t.Fatalf("status after restart = %+v", st)
	}
	var rs struct{ OK, Restarted bool }
	must(t, td, "restart", "--json").json(t, &rs)
	if !rs.OK || !rs.Restarted {
		t.Fatalf("restart --json = %+v", rs)
	}
	var so stopOut
	must(t, td, "stop", "--json").json(t, &so)
	if !so.OK || !so.Stopped {
		t.Fatalf("stop --json = %+v", so)
	}
	if out := must(t, td, "status").stdout; out != "not running\n" {
		t.Fatalf("status after stop = %q", out)
	}
	if _, err := os.Stat(paths.PortFile); !os.IsNotExist(err) {
		t.Fatalf("port file after stop: %v", err)
	}

	// A port file left behind by a daemon that crashed is cleaned up and is no failure.
	if err := os.WriteFile(paths.PortFile, []byte(strconv.Itoa(port)+"\n999999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := must(t, td, "stop").stdout; out != "not running\n" {
		t.Fatalf("stop, stale port file = %q", out)
	}
	if _, err := os.Stat(paths.PortFile); !os.IsNotExist(err) {
		t.Fatalf("stale port file not removed: %v", err)
	}

	// A daemon that holds the lock but does not answer: say where it is, exit 1, touch nothing.
	held, err := fsutil.TryLock(paths.LockFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PortFile, []byte(strconv.Itoa(port)+"\n4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"stop", "status"} {
		r := wantErr(t, td, ExitError, "", cmd)
		if !strings.Contains(r.stderr, "does not answer") || !strings.Contains(r.stderr, "process 4242") {
			t.Fatalf("%s, lock held: %s", cmd, r.stderr)
		}
		wantErr(t, td, ExitError, codeDaemon, cmd, "--json")
	}
	_ = held.Unlock()
	_ = os.Remove(paths.PortFile)
}

// TestStopDuringWait: a daemon stopped while a wait is open answers the wait with daemon_stopping at once (so the
// stop is quick), and the wait never starts the daemon again: after a plain stop it ends with daemon_stopped; after
// a restart it goes on against the new daemon and returns the result.
func TestStopDuringWait(t *testing.T) {
	cliEnv(t)
	testHome(t)
	t.Setenv(config.EnvPort, strconv.Itoa(freeTestPort(t)))
	t.Setenv(envNoAutoStart, "") // a wait could start the daemon: it must not
	td := inProcessDeps(t)
	td.stopGrace = 2 * time.Second
	var spawns atomic.Int32
	spawn := td.spawn
	td.spawn = func(p config.Paths, port int) (client.StartFunc, error) {
		start, err := spawn(p, port)
		return func(ctx context.Context) (<-chan error, error) { spawns.Add(1); return start(ctx) }, err
	}
	t.Cleanup(func() { // t.Context() has ended by now
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		_ = run(ctx, td, []string{"stop"}, io.Discard, io.Discard)
	})

	must(t, td, "ui", "--no-open")
	// No account: the task stays pending (waiting for one) until it is cancelled.
	var rep api.TaskReport
	must(t, td, "delegate", "--wait", "0", "--cwd", t.TempDir(), "--json", "Wait for me.").json(t, &rep)
	id := rep.TaskID
	waitInBackground := func() <-chan result {
		ch := make(chan result, 1)
		go func() { ch <- runCLI(t, td, "wait", id, "--seconds", "60", "--no-wake", "--json") }()
		time.Sleep(500 * time.Millisecond) // the wait is open on the daemon
		return ch
	}

	// stop: quick, and the wait ends with daemon_stopped without starting a daemon.
	waited := waitInBackground()
	begun := time.Now()
	must(t, td, "stop")
	if took := time.Since(begun); took > 2500*time.Millisecond {
		t.Fatalf("stop with an open wait took %s", took)
	}
	r := <-waited
	var e errorOut
	r.json(t, &e)
	if r.code != ExitError || e.Code != ops.CodeDaemonStopped || !strings.Contains(e.Error, "keep running") {
		t.Fatalf("wait after stop: exit %d %+v", r.code, e)
	}
	if n := spawns.Load(); n != 1 {
		t.Fatalf("%d daemon starts, want only the first", n)
	}
	if out := must(t, td, "status").stdout; out != "not running\n" {
		t.Fatalf("status after stop = %q", out)
	}

	// restart: the wait carries on against the new daemon and returns once the task ends.
	must(t, td, "ui", "--no-open")
	waited = waitInBackground()
	must(t, td, "restart")
	time.Sleep(300 * time.Millisecond) // the wait finds the new daemon
	must(t, td, "cancel", id)
	r = <-waited
	var res api.WaitResult
	r.json(t, &res)
	if r.code != ExitOK || len(res.Reports) != 1 || res.Reports[0].Status != string(api.StatusCancelled) {
		t.Fatalf("wait across a restart: exit %d %+v\nstderr: %s", r.code, res, r.stderr)
	}
	if n := spawns.Load(); n != 3 { // ui, ui, restart: none by the waits
		t.Fatalf("%d daemon starts, want 3", n)
	}
}

// TestOtherHomesDaemon: home B pointed at the port of home A's daemon never uses, adopts or stops it: status, stop
// and doctor report the conflict, a task command does not start a daemon, and A's daemon keeps running.
func TestOtherHomesDaemon(t *testing.T) {
	newTestDaemon(t) // home A, its port in SEELEGENT_PORT
	homeA := os.Getenv(config.EnvHome)
	td := testDeps(nil)
	var spawns atomic.Int32
	td.spawn = func(config.Paths, int) (client.StartFunc, error) {
		return func(context.Context) (<-chan error, error) { spawns.Add(1); return nil, errors.New("no start") }, nil
	}
	t.Setenv(config.EnvHome, t.TempDir()) // home B, same port
	t.Setenv(envNoAutoStart, "")
	const conflict = "another data directory"
	for _, args := range [][]string{{"status"}, {"stop"}, {"ls"}, {"memory", "read", "--cwd", t.TempDir()}} {
		r := wantErr(t, td, ExitError, "", args...)
		if !strings.Contains(r.stderr, conflict) || !strings.Contains(r.stderr, fmt.Sprintf("(pid %d)", os.Getpid())) {
			t.Fatalf("%v from home B: %s", args, r.stderr)
		}
		wantErr(t, td, ExitError, codeDaemon, append(args, "--json")...)
	}
	var checks []doctor.Check
	r := runCLI(t, td, "doctor", "--json")
	r.json(t, &checks)
	if r.code != ExitError || !slices.ContainsFunc(checks, func(c doctor.Check) bool {
		return c.Name == "daemon" && c.Status == doctor.Fail && strings.Contains(c.Message, conflict)
	}) {
		t.Fatalf("doctor from home B: exit %d %+v", r.code, checks)
	}
	if n := spawns.Load(); n != 0 {
		t.Fatalf("home B started %d daemons", n)
	}
	t.Setenv(config.EnvHome, homeA)
	var st statusOut
	must(t, td, "status", "--json").json(t, &st)
	if !st.Running {
		t.Fatal("home A's daemon was stopped by home B")
	}
}

// listenOn holds 127.0.0.1:port until the test ends, as another program would.
func listenOn(t *testing.T, port int) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

// TestDaemonPortTaken: `seelegent daemon` on a port another program holds exits 3.
func TestDaemonPortTaken(t *testing.T) {
	cliEnv(t)
	testHome(t)
	port := freeTestPort(t)
	listenOn(t, port)
	r := runCLI(t, testDeps(nil), "serve", "--port", strconv.Itoa(port), "--json")
	var e errorOut
	r.json(t, &e)
	if r.code != ExitDaemonLocked || e.Code != codeLocked || !strings.Contains(e.Error, "is taken") {
		t.Fatalf("port taken: exit %d, %+v", r.code, e)
	}
}

func TestDoctor(t *testing.T) {
	newTestDaemon(t)
	td := testDeps(nil)
	// The process list is injected: an old Node MCP server and an unrelated node program.
	td.processes = func(context.Context) ([]platform.Process, error) {
		return []platform.Process{
			{PID: 4120, Name: "node.exe", Args: []string{"node", `C:\Programs\seelegent\versions\0.3.0\src\cli.js`,
				"mcp"}},
			{PID: 4121, Name: "node.exe", Args: []string{"node", "server.js"}},
		}, nil
	}
	var checks []doctor.Check
	r := runCLI(t, td, "doctor", "--json")
	r.json(t, &checks)
	byName := map[string]doctor.Check{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	for _, name := range []string{"data_dir", "settings", "database", "daemon", "daemon_version", "old_node",
		"port_file", "dashboard", "git", "backend_agy", "backend_claude", "backend_codex", "accounts", "path"} {
		if _, ok := byName[name]; !ok {
			t.Errorf("no %s check in %+v", name, checks)
		}
	}
	for _, name := range []string{"data_dir", "settings", "database", "daemon", "daemon_version", "port_file",
		"backend_agy", "accounts"} {
		if byName[name].Status != doctor.OK {
			t.Errorf("%s = %+v", name, byName[name])
		}
	}
	if c := byName["old_node"]; c.Status != doctor.Warn || !strings.Contains(c.Message, "1 process of the old Node "+
		"version runs (pid 4120)") {
		t.Errorf("old_node = %+v", c)
	}
	if !strings.Contains(byName["accounts"].Message, "1 account, 1 enabled") || strings.Contains(r.stdout, "@") {
		t.Errorf("accounts = %+v", byName["accounts"])
	}
	if want := 0; doctor.Failed(checks) {
		want = ExitError
		if r.code != want {
			t.Fatalf("exit %d with a failed check", r.code)
		}
	} else if r.code != ExitOK {
		t.Fatalf("exit %d without a failed check: %s", r.code, r.stderr)
	}

	// Nothing running and a stale port file: warnings, never a failure. Text output is a table.
	paths := testHome(t)
	t.Setenv(config.EnvPort, strconv.Itoa(freeTestPort(t)))
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PortFile, []byte("1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runCLI(t, td, "doctor")
	if !strings.Contains(r.stdout, "[WARN] daemon") || !strings.Contains(r.stdout, "[WARN] port_file") ||
		!strings.Contains(r.stdout, "[OK]   database") {
		t.Fatalf("doctor text:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "[FAIL]") != (r.code == ExitError) {
		t.Fatalf("exit %d for:\n%s", r.code, r.stdout)
	}
	if _, err := os.Stat(paths.DB); !os.IsNotExist(err) {
		t.Fatalf("doctor created the database: %v", err)
	}

	// Broken settings fail.
	if err := os.WriteFile(paths.Config, []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	r = runCLI(t, td, "doctor")
	if r.code != ExitError || !strings.Contains(r.stdout, "[FAIL] settings") {
		t.Fatalf("broken settings: exit %d\n%s", r.code, r.stdout)
	}
}
