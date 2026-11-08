package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

// TestDaemonStop: POST /daemon/stop needs the lead (worker guard) and the CSRF checks of every mutating request,
// then answers 202 and signals the app's shutdown.
func TestDaemonStop(t *testing.T) {
	e := newEnv(t)
	stopped := func() bool {
		select {
		case <-e.a.StopRequested():
			return true
		default:
			return false
		}
	}
	e.client.Transport = &http.Transport{DisableKeepAlives: true}

	e.wantError(e.req(http.MethodPost, "/api/v1/daemon/stop", nil, map[string]string{"X-Seelegent": ""}),
		http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/daemon/stop", nil, map[string]string{"Origin": "http://evil.example"}),
		http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.get("/api/v1/daemon/stop"), http.StatusMethodNotAllowed, api.CodeInvalidArgument, "")

	e.who.set(&httpapi.Caller{Worker: true, TaskID: "t-1"})
	if got := e.wantError(e.post("/api/v1/daemon/stop", nil), http.StatusForbidden, api.CodeForbidden, ""); got.Details["caller"] != "worker" {
		t.Errorf("details = %v", got.Details)
	}
	e.who.set(nil)
	if stopped() {
		t.Fatal("a refused request stopped the daemon")
	}

	var ans api.DaemonStop
	e.must(e.post("/api/v1/daemon/stop", nil), http.StatusAccepted).json(t, &ans)
	if !ans.Stopping || ans.PID != os.Getpid() {
		t.Fatalf("answer = %+v", ans)
	}
	select {
	case <-e.a.StopRequested():
	case <-time.After(5 * time.Second):
		t.Fatal("stop not signaled")
	}
	// A second stop while shutting down is harmless.
	e.must(e.post("/api/v1/daemon/stop", nil), http.StatusAccepted)
}

// TestWaitEndsOnClose: closing the daemon (stop or Ctrl+C) answers an open wait with 503 daemon_stopping at once,
// so the shutdown does not wait out the wait, and the health answer names the data directory.
func TestWaitEndsOnClose(t *testing.T) {
	e := newEnv(t)
	var h api.Health
	e.must(e.get("/api/v1/health"), http.StatusOK).json(t, &h)
	if h.HomeID != config.HomeID(e.paths.Home) || h.HomeID == "" {
		t.Fatalf("health homeId = %q", h.HomeID)
	}
	sum := e.delegate("No account runs this: it stays pending.")
	answered := make(chan response, 1)
	go func() {
		answered <- e.post("/api/v1/wait", map[string]any{"ids": []string{sum.ID}, "timeoutSeconds": 60,
			"wakeOnBlocked": false})
	}()
	time.Sleep(300 * time.Millisecond) // the wait is open
	begun := time.Now()
	ctx, cancel := context.WithTimeout(t.Context(), limit)
	defer cancel()
	if err := e.a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begun); took > 2*time.Second {
		t.Fatalf("closing with an open wait took %s", took)
	}
	select {
	case r := <-answered:
		e.wantError(r, http.StatusServiceUnavailable, api.CodeDaemonStopping, "")
	case <-time.After(5 * time.Second):
		t.Fatal("the wait was not answered")
	}
}
