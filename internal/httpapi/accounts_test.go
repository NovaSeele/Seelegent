package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/signin"
)

// TestAccountsProvidersKeys covers accounts, providers and API keys (contract §4).
func TestAccountsProvidersKeys(t *testing.T) {
	e := newEnv(t)
	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)
	stream := e.stream(boot.LastEventID)

	e.wantError(e.post("/api/v1/accounts", map[string]any{"backend": "gpt", "kind": "isolated", "label": "x"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "backend")
	e.wantError(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "oauth", "label": "x"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "kind")
	var off api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "isolated", "label": "Off",
		"enabled": false, "priority": 5}), http.StatusCreated).json(t, &off)
	if off.Enabled || off.Status != api.AccountDisabled || off.Priority != 5 || off.Quota.Groups == nil ||
		off.Models == nil || off.MaxConcurrent == 0 || off.Email != "" {
		t.Fatalf("added = %+v", off)
	}
	var on api.Account
	e.must(e.req(http.MethodPatch, "/api/v1/accounts/"+off.ID, map[string]any{"enabled": true, "label": "On"}, nil),
		http.StatusOK).json(t, &on)
	if !on.Enabled || on.Label != "On" || on.Status == api.AccountDisabled {
		t.Fatalf("patched = %+v", on)
	}
	stream.until("account.updated", func(f frame) bool { return strings.Contains(f.data, `"label":"On"`) })

	// Providers and API-key accounts.
	var pl api.ProviderList
	e.must(e.get("/api/v1/providers"), http.StatusOK).json(t, &pl)
	if len(pl.Presets) < 5 || pl.Items == nil {
		t.Fatalf("providers = %+v", pl)
	}
	tiers := map[string]string{"fast": "deepseek-chat", "standard": "deepseek-chat", "strong": "deepseek-reasoner",
		"max": "deepseek-reasoner"}
	var prov api.Provider
	e.must(e.post("/api/v1/providers", map[string]any{"id": "ds", "preset": "deepseek", "tiers": tiers,
		"models": []string{"deepseek-chat", "deepseek-reasoner"}}), http.StatusCreated).json(t, &prov)
	if prov.Backend != "claude" || prov.BaseURL == "" || prov.AllowedModels == "" || len(prov.Models) != 2 {
		t.Fatalf("provider = %+v", prov)
	}
	stream.until("providers.changed", nil)
	e.wantError(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"backend": "codex"}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "backend")
	e.must(e.req(http.MethodPatch, "/api/v1/providers/ds", map[string]any{"name": "Deep"}, nil), http.StatusOK).
		json(t, &prov)
	if prov.Name != "Deep" {
		t.Fatalf("renamed provider = %+v", prov)
	}
	var keyed api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "claude", "kind": "api-key", "label": "Key",
		"provider": "ds"}), http.StatusCreated).json(t, &keyed)
	if keyed.Status != api.AccountNoKey || keyed.Quota.Source != "passive" {
		t.Fatalf("api-key account = %+v", keyed)
	}
	secret := "sk-test-0123456789abcdef"
	// The key always comes in a second call: POST /accounts has no key field.
	e.wantError(e.post("/api/v1/accounts", map[string]any{"backend": "claude", "kind": "api-key", "label": "One step",
		"provider": "ds", "key": secret}), http.StatusBadRequest, api.CodeInvalidArgument, "key")
	r := e.req(http.MethodPut, "/api/v1/accounts/"+keyed.ID+"/key", map[string]any{"key": secret}, nil)
	e.must(r, http.StatusOK).json(t, &keyed)
	if strings.Contains(string(r.body), secret) || keyed.KeyHint == "" || keyed.Status == api.AccountNoKey {
		t.Fatalf("key set = %s", r.body)
	}
	e.wantError(e.req(http.MethodPut, "/api/v1/accounts/"+off.ID+"/key", map[string]any{"key": secret}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "key")
	if r := e.req(http.MethodPut, "/api/v1/accounts/"+off.ID+"/key", map[string]any{"key": secret}, nil); strings.Contains(string(r.body), secret) {
		t.Fatalf("the key is in an error: %s", r.body)
	}
	e.wantError(e.req(http.MethodDelete, "/api/v1/providers/ds", nil, nil), http.StatusConflict, api.CodeConflict, "")
	var cleared api.Account
	e.must(e.req(http.MethodDelete, "/api/v1/accounts/"+keyed.ID+"/key", nil, nil), http.StatusOK).json(t, &cleared)
	if cleared.KeyHint != "" || cleared.Status != api.AccountNoKey {
		t.Fatalf("key cleared = %+v", cleared)
	}
	e.must(e.req(http.MethodDelete, "/api/v1/accounts/"+keyed.ID, nil, nil), http.StatusNoContent)
	stream.until("account.deleted", func(f frame) bool { return strings.Contains(f.data, keyed.ID) })
	e.must(e.req(http.MethodDelete, "/api/v1/providers/ds", nil, nil), http.StatusNoContent)

	// Quota: reads (none here: no readers) and the account list.
	var list []api.Account
	e.must(e.post("/api/v1/quota/refresh", nil), http.StatusOK).json(t, &list)
	if len(list) != 1 || list[0].ID != off.ID {
		t.Fatalf("after refresh = %+v", list)
	}
	e.must(e.post("/api/v1/accounts/"+off.ID+"/refresh", nil), http.StatusOK)
	e.wantError(e.post("/api/v1/accounts/a-nope/refresh", nil), http.StatusNotFound, api.CodeNotFound, "")

	// Usage.
	var sum api.Summary
	e.must(e.get("/api/v1/usage/summary?days=3"), http.StatusOK).json(t, &sum)
	if sum.Days != 3 || sum.Daily == nil || sum.Projects == nil {
		t.Fatalf("usage summary = %+v", sum)
	}
	e.wantError(e.get("/api/v1/usage/summary?days=31"), http.StatusBadRequest, api.CodeInvalidArgument, "days")
	var hist api.History
	e.must(e.get("/api/v1/usage/history?bucket=day&since="+url.QueryEscape(time.Now().Add(-time.Hour).UTC().
		Format(time.RFC3339))), http.StatusOK).json(t, &hist)
	if hist.Bucket != "day" || hist.Since == "" {
		t.Fatalf("usage history = %+v", hist)
	}
	e.must(e.get("/api/v1/usage/history?bucket=hour&accountId="+off.ID+"&since="+url.QueryEscape(time.Now().
		Add(-time.Hour).UTC().Format(time.RFC3339))), http.StatusOK).json(t, &hist)
	if hist.Bucket != "hour" {
		t.Fatalf("usage history of one account = %+v", hist)
	}
	e.wantError(e.get("/api/v1/usage/history?since=yesterday"), http.StatusBadRequest, api.CodeInvalidArgument, "since")
}

// TestSettingsMemoryRoles covers contract §5.
func TestSettingsMemoryRoles(t *testing.T) {
	e := newEnv(t)
	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)
	stream := e.stream(boot.LastEventID)

	var set config.Settings
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"maxConcurrentPerAccount": 3,
		"backends": map[string]any{"codex": map[string]any{"sandbox": "read-only"}}}, nil), http.StatusOK).json(t, &set)
	if set.MaxConcurrentPerAccount != 3 || set.Backends.Codex.Sandbox != "read-only" || !set.Backends.Agy.Enabled {
		t.Fatalf("patched = %+v", set)
	}
	stream.until("settings.updated", nil)
	data, err := os.ReadFile(e.paths.Config)
	if err != nil || !strings.Contains(string(data), `"maxConcurrentPerAccount": 3`) {
		t.Fatalf("config.json = %s, %v", data, err)
	}
	got := e.wantError(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"port": 1234, "nope": 1,
		"minRemaining": "x"}, nil), http.StatusBadRequest, api.CodeInvalidArgument, "")
	fields, _ := got.Details["fields"].([]any)
	if len(fields) != 3 {
		t.Fatalf("fields = %v", got.Details)
	}
	e.must(e.get("/api/v1/settings"), http.StatusOK).json(t, &set)
	if set.MaxConcurrentPerAccount != 3 {
		t.Fatalf("a refused patch changed the settings: %+v", set)
	}
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"maxConcurrentPerAccount": nil}, nil),
		http.StatusOK).json(t, &set)
	if set.MaxConcurrentPerAccount != config.Defaults().MaxConcurrentPerAccount {
		t.Fatalf("reset = %d", set.MaxConcurrentPerAccount)
	}
	e.wantError(e.req(http.MethodPatch, "/api/v1/settings", "[1]", nil), http.StatusBadRequest,
		api.CodeInvalidArgument, "")

	// Memory.
	var m api.Memory
	e.must(e.req(http.MethodPut, "/api/v1/memory", map[string]any{"cwd": e.cwd, "text": "Use pnpm."}, nil),
		http.StatusOK).json(t, &m)
	e.must(e.post("/api/v1/memory", map[string]any{"cwd": e.cwd, "text": "Run make lint."}), http.StatusOK).json(t, &m)
	if !strings.Contains(m.Text, "Use pnpm.") || !strings.Contains(m.Text, "Run make lint.") || m.UpdatedAt == "" {
		t.Fatalf("memory = %+v", m)
	}
	stream.until("memory.changed", nil)
	e.must(e.get("/api/v1/memory?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &m)
	if !strings.Contains(m.Text, "Run make lint.") {
		t.Fatalf("memory read = %+v", m)
	}

	// Roles.
	var tpl []api.Role
	e.must(e.get("/api/v1/roles/templates"), http.StatusOK).json(t, &tpl)
	if len(tpl) == 0 {
		t.Fatal("no role templates")
	}
	var created api.Role
	e.must(e.post("/api/v1/roles", map[string]any{"role": map[string]any{"id": "reviewer", "name": "Reviewer",
		"description": "Reviews", "rules": "Be strict.", "outputStyle": "", "skills": []string{},
		"preferredAccounts": []string{}, "scope": "", "overridesGlobal": false}, "scope": "global"}),
		http.StatusCreated).json(t, &created)
	if created.ID != "reviewer" || created.Scope != "global" {
		t.Fatalf("role = %+v", created)
	}
	stream.until("roles.changed", nil)
	var rl api.RoleList
	e.must(e.get("/api/v1/roles?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &rl)
	if len(rl.Items) != 1 || rl.Projects == nil {
		t.Fatalf("roles = %+v", rl)
	}
	e.wantError(e.post("/api/v1/roles", map[string]any{"role": map[string]any{"id": "x"}, "scope": "team"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "scope")
	e.wantError(e.post("/api/v1/roles", map[string]any{"role": map[string]any{"id": "x", "name": "X"},
		"scope": "project"}), http.StatusBadRequest, api.CodeInvalidArgument, "cwd")
	// The same id at the same place replaces the role.
	var replaced api.Role
	e.must(e.post("/api/v1/roles", map[string]any{"role": map[string]any{"id": "reviewer", "name": "Strict reviewer",
		"skills": []string{}, "preferredAccounts": []string{}}, "scope": "global"}),
		http.StatusCreated).json(t, &replaced)
	stream.until("roles.changed", nil)
	e.must(e.get("/api/v1/roles?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &rl)
	if len(rl.Items) != 1 || rl.Items[0].Name != "Strict reviewer" || replaced.CreatedAt != created.CreatedAt {
		t.Fatalf("after replace: %+v (created %s)", rl.Items, created.CreatedAt)
	}
	e.must(e.req(http.MethodDelete, "/api/v1/roles/reviewer?scope=global", nil, nil), http.StatusNoContent)
	stream.until("roles.changed", nil)
	e.wantError(e.req(http.MethodDelete, "/api/v1/roles/reviewer?scope=global", nil, nil), http.StatusNotFound,
		api.CodeNotFound, "")

	// A project role is deleted by the project's key or by its folder.
	for _, by := range []string{"key", "path"} {
		e.must(e.post("/api/v1/roles", map[string]any{"role": map[string]any{"id": "local", "name": "Local",
			"skills": []string{}, "preferredAccounts": []string{}}, "scope": "project", "cwd": e.cwd}),
			http.StatusCreated)
		e.must(e.get("/api/v1/roles?all=true"), http.StatusOK).json(t, &rl)
		if len(rl.Projects) != 1 {
			t.Fatalf("projects = %+v", rl.Projects)
		}
		where := rl.Projects[0].Key
		if by == "path" {
			where = e.cwd
		}
		e.must(e.req(http.MethodDelete, "/api/v1/roles/local?scope=project&cwd="+url.QueryEscape(where), nil, nil),
			http.StatusNoContent)
		e.must(e.get("/api/v1/roles?all=true"), http.StatusOK).json(t, &rl)
		if len(rl.Items) != 0 || len(rl.Projects) != 0 {
			t.Fatalf("delete by %s left %+v", by, rl)
		}
	}
	var skills []api.Skill
	e.must(e.get("/api/v1/roles/skills?cwd="+url.QueryEscape(e.cwd)), http.StatusOK).json(t, &skills)
	if skills == nil {
		t.Fatal("skills is null")
	}
}

// procOpener runs the sign-in command as a plain child process instead of a window.
type procOpener struct{ env func([]string) []string }

type procTerminal struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
}

func (o procOpener) Open(_ context.Context, spec platform.TerminalSpec) (signin.Terminal, error) {
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) //nolint:noctx // the test binary; a window outlives the request
	cmd.Dir = spec.Dir
	cmd.Env = o.env(spec.Env)
	if spec.StdoutPath != "" {
		f, err := os.Create(spec.StdoutPath)
		if err != nil {
			return nil, err
		}
		cmd.Stdout = f
		defer func() { _ = f.Close() }()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &procTerminal{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		t.code = cmd.ProcessState.ExitCode()
		close(t.done)
	}()
	return t, nil
}

func (t *procTerminal) Wait(ctx context.Context) (int, error) {
	select {
	case <-t.done:
		return t.code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (t *procTerminal) Identity() (pid int, start uint64, ok bool) { return t.cmd.Process.Pid, 0, true }

// TestSignIn covers the sign-in endpoints with the fake CLI as agy: start, progress over SSE, success enables the
// account, the code endpoint, logout, refusals.
func TestSignIn(t *testing.T) {
	e := newEnvWith(t, func(o *envOptions) {
		o.opener = procOpener{env: func(env []string) []string { return o.queue.Env(env) }}
	})
	var acc api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "isolated", "label": "New",
		"enabled": false}), http.StatusCreated).json(t, &acc)
	e.wantError(e.get("/api/v1/accounts/"+acc.ID+"/login"), http.StatusNotFound, api.CodeNotFound, "")
	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)
	stream := e.stream(boot.LastEventID)

	e.queue.Push(t, `{"status":"SUCCESS"}`+"\n#!exit 0\n")
	var l api.LoginSession
	e.must(e.post("/api/v1/accounts/"+acc.ID+"/login", nil), http.StatusOK).json(t, &l)
	if l.AccountID != acc.ID || l.State != api.LoginStarting {
		t.Fatalf("login = %+v", l)
	}
	// GET returns the session (a finished one stays readable for a few seconds).
	var got api.LoginSession
	e.must(e.get("/api/v1/accounts/"+acc.ID+"/login"), http.StatusOK).json(t, &got)
	if got.AccountID != acc.ID {
		t.Fatalf("GET login = %+v", got)
	}
	stream.until("account.login", func(f frame) bool {
		var s api.LoginSession
		return json.Unmarshal([]byte(f.data), &s) == nil && s.State == api.LoginDone
	})
	deadline := time.Now().Add(limit)
	for {
		var list []api.Account
		e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &list)
		a := list[0]
		if a.Enabled && !a.SigningIn {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("account after sign-in = %+v", a)
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.wantError(e.post("/api/v1/accounts/"+acc.ID+"/login/code", map[string]any{"code": "123"}),
		http.StatusConflict, api.CodeBadState, "")
	var out api.Account
	e.must(e.post("/api/v1/accounts/"+acc.ID+"/logout", nil), http.StatusOK).json(t, &out)
	if out.ID != acc.ID {
		t.Fatalf("logout = %+v", out)
	}
	e.wantError(e.post("/api/v1/accounts/a-nope/login", nil), http.StatusNotFound, api.CodeNotFound, "")
	var sys api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "system", "label": "Sys"}),
		http.StatusCreated).json(t, &sys)
	e.wantError(e.post("/api/v1/accounts/"+sys.ID+"/login", nil), http.StatusBadRequest, api.CodeInvalidArgument, "")
	if _, err := os.Stat(filepath.Join(e.paths.Profiles, acc.ID)); err != nil {
		t.Fatalf("logout did not recreate the profile: %v", err)
	}
}
