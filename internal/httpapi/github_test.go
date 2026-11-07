package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

const ghTestToken = "ghp_HttpApiTestToken0123456789abcdefABCD"

// dashboard are the headers of a fetch from the dashboard page.
func (e *env) dashboard() map[string]string {
	return map[string]string{"Origin": "http://127.0.0.1:" + strconv.Itoa(e.port), "Sec-Fetch-Site": "same-origin",
		"Sec-Fetch-Mode": "cors", "X-Seelegent-UI": "1"}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitHubRoutes(t *testing.T) {
	e := newEnv(t)
	var auths []string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v3/repos/octo/demo/issues" {
			_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 7, "title": "Flaky test", "body": "b",
				"state": "open", "html_url": "http://x/7", "updated_at": "2026-11-08T00:00:00Z"}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	t.Cleanup(gh.Close)
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"githubEnterpriseHosts": []string{gh.URL}},
		nil), http.StatusOK)

	var acc api.GitHubAccess
	e.must(e.get("/api/v1/github/access?refresh=1"), http.StatusOK).json(t, &acc)
	if acc.Tier != api.TierManual {
		t.Fatalf("access = %+v", acc)
	}
	// The token routes answer the dashboard only.
	e.wantError(e.req(http.MethodPut, "/api/v1/github/token", map[string]string{"token": ghTestToken}, nil),
		http.StatusForbidden, api.CodeForbidden, "")
	noFetch := e.dashboard()
	noFetch["Sec-Fetch-Site"] = "cross-site"
	e.wantError(e.req(http.MethodPut, "/api/v1/github/token", map[string]string{"token": ghTestToken}, noFetch),
		http.StatusForbidden, api.CodeForbidden, "")
	r := e.must(e.req(http.MethodPut, "/api/v1/github/token", map[string]string{"token": ghTestToken},
		e.dashboard()), http.StatusOK)
	if strings.Contains(string(r.body), ghTestToken) || !strings.Contains(string(r.body), "ABCD") {
		t.Fatalf("token answer: %s", r.body)
	}
	r = e.must(e.get("/api/v1/github/access?refresh=1"), http.StatusOK)
	r.json(t, &acc)
	if acc.Tier != api.TierToken || acc.TokenHint != "…ABCD" || strings.Contains(string(r.body), ghTestToken) {
		t.Fatalf("access = %s", r.body)
	}
	info, err := os.Stat(filepath.Join(e.paths.Home, "github", "token"))
	if err != nil || info.Size() == 0 {
		t.Fatalf("token file: %v", err)
	}

	repo := filepath.Join(e.cwd, "demo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "-c", "user.name=T", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "x")
	gitRun(t, repo, "remote", "add", "origin", gh.URL+"/octo/demo.git")
	e.wantError(e.post("/api/v1/github/repos", map[string]any{}), http.StatusBadRequest, api.CodeInvalidArgument, "path")
	var rp api.Repo
	e.must(e.post("/api/v1/github/repos", map[string]any{"path": repo}), http.StatusCreated).json(t, &rp)
	if !rp.GitHub || rp.Owner != "octo" {
		t.Fatalf("repo = %+v", rp)
	}
	var sync api.SyncResult
	e.must(e.post("/api/v1/github/repos/"+rp.ID+"/sync", nil), http.StatusOK).json(t, &sync)
	if sync.Added != 1 {
		t.Fatalf("sync = %+v", sync)
	}
	for _, a := range auths {
		if a != "" && !strings.HasSuffix(a, ghTestToken) {
			t.Fatalf("authorization %q", a)
		}
	}
	var issues []api.Issue
	e.must(e.get("/api/v1/github/repos/"+rp.ID+"/issues"), http.StatusOK).json(t, &issues)
	if len(issues) != 1 || issues[0].Key != "gh#7" {
		t.Fatalf("issues = %+v", issues)
	}
	e.wantError(e.get("/api/v1/github/repos/"+rp.ID+"/issues?state=weird"), http.StatusBadRequest,
		api.CodeInvalidArgument, "state")
	e.must(e.post("/api/v1/github/repos/"+rp.ID+"/issues", map[string]any{"title": "Local one"}), http.StatusCreated)
	var tr api.TriageResult
	e.must(e.post("/api/v1/github/repos/"+rp.ID+"/issues/gh%237/triage",
		map[string]any{"action": "skip", "reason": "duplicate"}), http.StatusOK).json(t, &tr)
	if tr.Issue.Decision == nil || tr.Issue.Decision.Action != "skip" {
		t.Fatalf("triage = %+v", tr)
	}
	e.wantError(e.post("/api/v1/github/repos/"+rp.ID+"/issues/gh%237/triage", map[string]any{"action": "nope"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.get("/api/v1/github/repos/rnope"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/github/tickets/tk-missing"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.post("/api/v1/github/tickets/tk-missing/link-pr", map[string]any{"number": 0}),
		http.StatusBadRequest, api.CodeInvalidArgument, "number")
	var tickets []api.TicketSummary
	e.must(e.get("/api/v1/github/tickets"), http.StatusOK).json(t, &tickets)
	if tickets == nil {
		t.Fatal("tickets is null")
	}

	// ticketId is set by the daemon only (C-B21).
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "ticketId": "tk1"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "ticketId")
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "automationId": "a1"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "automationId")

	// Workers may not use any of it.
	e.client.Transport = &http.Transport{DisableKeepAlives: true}
	e.who.set(&httpapi.Caller{Worker: true, TaskID: "t-1"})
	e.must(e.get("/api/v1/github/repos"), http.StatusForbidden)
	e.must(e.post("/api/v1/github/repos/"+rp.ID+"/sync", nil), http.StatusForbidden)
	e.who.set(nil)

	e.must(e.req(http.MethodDelete, "/api/v1/github/token", nil, e.dashboard()), http.StatusNoContent)
	acc = api.GitHubAccess{}
	e.must(e.get("/api/v1/github/access?refresh=1"), http.StatusOK).json(t, &acc)
	if acc.Tier != api.TierManual || acc.TokenHint != "" {
		t.Fatalf("access after delete = %+v", acc)
	}
	e.must(e.req(http.MethodDelete, "/api/v1/github/repos/"+rp.ID, nil, nil), http.StatusNoContent)
}

func TestAutomationRoutes(t *testing.T) {
	e := newEnv(t)
	in := map[string]any{"prompt": "Tidy the changelog", "cwd": e.cwd, "schedule": map[string]any{"every": "daily",
		"at": "07:30"}, "timezone": "UTC", "enabled": true, "precheck": "exit 3"}
	var res api.AutomationEnableResult
	e.must(e.post("/api/v1/automations", in), http.StatusCreated).json(t, &res)
	a := res.Automation
	if a.Enabled || !a.EnableRequested || !res.Confirm || !strings.Contains(res.ConfirmURL, a.ID) {
		t.Fatalf("create = %+v", res)
	}
	// What the lead (MCP, CLI) can do: ask.
	e.must(e.post("/api/v1/automations/"+a.ID+"/enable", nil), http.StatusOK).json(t, &res)
	if res.Automation.Enabled || !res.Confirm {
		t.Fatalf("enable = %+v", res)
	}
	e.must(e.req(http.MethodPatch, "/api/v1/automations/"+a.ID, map[string]any{"enabled": true}, nil),
		http.StatusOK).json(t, &res)
	if res.Automation.Enabled {
		t.Fatal("PATCH enabled turned it on")
	}
	// Only the dashboard gets a token and confirms.
	e.wantError(e.post("/api/v1/automations/"+a.ID+"/confirm-token", nil), http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm", map[string]string{"token": "x"}, nil),
		http.StatusForbidden, api.CodeForbidden, "")
	shown := map[string]string{"revision": a.Revision}
	e.wantError(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm-token", map[string]string{},
		e.dashboard()), http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm-token",
		map[string]string{"revision": "0000000000000000"}, e.dashboard()), http.StatusConflict, api.CodeBadState, "")
	var tok api.AutomationConfirmToken
	e.must(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm-token", shown, e.dashboard()),
		http.StatusOK).json(t, &tok)
	e.wantError(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm", map[string]string{"token": "bad"},
		e.dashboard()), http.StatusForbidden, api.CodeForbidden, "")
	e.must(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm-token", shown, e.dashboard()),
		http.StatusOK).json(t, &tok)
	var on api.Automation
	e.must(e.req(http.MethodPost, "/api/v1/automations/"+a.ID+"/confirm", map[string]string{"token": tok.Token},
		e.dashboard()), http.StatusOK).json(t, &on)
	if !on.Enabled || on.NextRun == "" {
		t.Fatalf("confirmed = %+v", on)
	}

	var p api.SchedulePreview
	e.must(e.post("/api/v1/automations/preview", map[string]any{"schedule": map[string]any{"cron": "0 9 * * 1"},
		"timezone": "Europe/Berlin", "count": 2}), http.StatusOK).json(t, &p)
	if len(p.Runs) != 2 || !strings.Contains(p.Local[0], "09:00 Mon") {
		t.Fatalf("preview = %+v", p)
	}
	e.wantError(e.post("/api/v1/automations/preview", map[string]any{"schedule": map[string]any{"cron": "x"}}),
		http.StatusBadRequest, api.CodeInvalidArgument, "")

	var run api.AutomationRun
	e.must(e.post("/api/v1/automations/"+a.ID+"/run", nil), http.StatusOK).json(t, &run)
	if run.Outcome != api.AutoSkippedPrecheck || !strings.Contains(run.Detail, "exit code 3") {
		t.Fatalf("run = %+v", run)
	}
	var runs []api.AutomationRun
	e.must(e.get("/api/v1/automations/"+a.ID+"/runs?limit=5"), http.StatusOK).json(t, &runs)
	if len(runs) != 1 {
		t.Fatalf("runs = %+v", runs)
	}
	var list []api.Automation
	e.must(e.get("/api/v1/automations"), http.StatusOK).json(t, &list)
	if len(list) != 1 || list[0].LastRun == nil {
		t.Fatalf("list = %+v", list)
	}
	e.client.Transport = &http.Transport{DisableKeepAlives: true}
	e.who.set(&httpapi.Caller{Worker: true, TaskID: "t-1"})
	e.must(e.get("/api/v1/automations"), http.StatusForbidden)
	e.who.set(nil)
	e.must(e.req(http.MethodDelete, "/api/v1/automations/"+a.ID, nil, nil), http.StatusNoContent)
	e.wantError(e.get("/api/v1/automations/"+a.ID), http.StatusNotFound, api.CodeNotFound, "")
}
