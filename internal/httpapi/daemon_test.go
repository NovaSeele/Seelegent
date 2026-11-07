package httpapi_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
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
	e.wantError(e.get("/api/v1/daemon/stop"), http.StatusNotFound, api.CodeNotFound, "")

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
