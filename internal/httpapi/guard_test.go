package httpapi_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

// TestWorkerGuard covers contract §1.3: a worker reaches only its allow-list; a worker of unknown task only
// /health (old B12, C-B12: the daemon itself checks).
func TestWorkerGuard(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "one"))
	own := e.delegate("Own task.")
	e.queue.Push(t, ok("c2", "two"))
	other := e.delegate("Other task.")
	e.wait(own.ID, other.ID)
	elsewhere := filepath.Join(filepath.Dir(e.cwd), "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}

	// Each case opens a new connection: the caller is cached per connection.
	e.client.Transport = &http.Transport{DisableKeepAlives: true}
	e.who.set(&httpapi.Caller{Worker: true, TaskID: own.ID})
	allowed := []string{
		"/api/v1/health",
		"/api/v1/tasks/" + own.ID,
		"/api/v1/tasks/" + own.ID + "/events",
		"/api/v1/memory?cwd=" + url.QueryEscape(e.cwd),
	}
	for _, p := range allowed {
		e.must(e.get(p), http.StatusOK)
	}
	denied := []string{
		"/api/v1/tasks/" + other.ID,
		"/api/v1/tasks/" + own.ID + "/report",
		"/api/v1/tasks/" + own.ID + "/git",
		"/api/v1/tasks",
		"/api/v1/accounts",
		"/api/v1/settings",
		"/api/v1/logs",
		"/api/v1/state",
		"/api/v1/events",
		"/api/v1/memory?cwd=" + url.QueryEscape(elsewhere),
		"/api/v1/memory",
		"/api/v1/spec?cwd=" + url.QueryEscape(elsewhere) + "&dir=x",
		"/",
		// Milestone 14 lists none of its endpoints for workers.
		"/api/v1/members",
		"/api/v1/members/track",
		"/api/v1/hooks",
		"/api/v1/meetings",
		"/api/v1/contests",
		"/api/v1/tasks/" + own.ID + "/notes",
	}
	for _, p := range denied {
		got := e.wantError(e.get(p), http.StatusForbidden, api.CodeForbidden, "")
		if got.Details["caller"] != "worker" {
			t.Errorf("%s: details = %v", p, got.Details)
		}
	}
	for _, p := range []string{"/api/v1/memory", "/api/v1/tasks/" + own.ID + "/review", "/api/v1/tasks"} {
		e.wantError(e.post(p, map[string]any{"cwd": e.cwd, "text": "x", "verdict": "accepted"}),
			http.StatusForbidden, api.CodeForbidden, "")
	}
	e.wantError(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{}, nil), http.StatusForbidden,
		api.CodeForbidden, "")

	// A worker whose task is unknown (fail closed) gets only /health.
	e.who.set(&httpapi.Caller{Worker: true})
	e.must(e.get("/api/v1/health"), http.StatusOK)
	e.wantError(e.get("/api/v1/tasks/"+own.ID), http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.get("/api/v1/memory?cwd="+url.QueryEscape(e.cwd)), http.StatusForbidden, api.CodeForbidden, "")

	// The lead again.
	e.who.set(nil)
	e.must(e.get("/api/v1/accounts"), http.StatusOK)
}
