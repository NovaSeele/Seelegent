package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/mcpserver"
)

// Meeting and contest waits handle a stopping daemon as task waits do (M18 hunt H8): the daemon is only looked for
// again, never started, and the call ends with daemon_stopped; a daemon that comes back carries the wait on.
func TestTeamWaitsDaemonStopping(t *testing.T) {
	stoppingBody := api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeDaemonStopping,
		Message: "the daemon is stopping"}}
	_, stopping := newStub(t, map[string]http.HandlerFunc{
		"GET /meetings/m1/wait": answer(http.StatusServiceUnavailable, stoppingBody),
		"GET /contests/c1/wait": answer(http.StatusServiceUnavailable, stoppingBody),
	})
	_, back := newStub(t, map[string]http.HandlerFunc{
		"GET /meetings/m1/wait": answer(http.StatusOK, api.Meeting{ID: "m1", Status: "decided"}),
		"GET /contests/c1/wait": answer(http.StatusOK, api.Contest{ID: "c1", Status: api.ContestStatusReady}),
	})
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"meeting", map[string]any{"action": "wait", "meeting_id": "m1", "wait_seconds": 30}},
		{"contest", map[string]any{"action": "wait", "contest_id": "c1", "wait_seconds": 30}},
	}
	for _, c := range calls {
		t.Run(c.tool, func(t *testing.T) {
			session := func(fd *stoppingDaemon) func() (bool, string, string) {
				cs := stubSession(t, stopping, func(o *mcpserver.Options) {
					o.Daemon = fd
					o.RetryPause = 20 * time.Millisecond
					o.StopGrace = 400 * time.Millisecond
				})
				return func() (bool, string, string) {
					res := callTool(t, cs, c.tool, c.args)
					data, _ := json.Marshal(res.StructuredContent)
					return res.IsError, string(data), text(res, 0)
				}
			}
			fd := &stoppingDaemon{stopping: stopping, back: back, gone: -1}
			start := time.Now()
			isErr, data, txt := session(fd)()
			if d := time.Since(start); !isErr || !strings.Contains(data, `"daemon_stopped"`) || d > 5*time.Second {
				t.Fatalf("stopped = %s %s after %v", txt, data, d)
			}
			if fd.finds.Load() == 0 || fd.ensuresAfter.Load() != 0 {
				t.Fatalf("finds %d, ensures after the stop %d", fd.finds.Load(), fd.ensuresAfter.Load())
			}

			fd = &stoppingDaemon{stopping: stopping, back: back, gone: 3}
			isErr, data, txt = session(fd)()
			if isErr || fd.ensuresAfter.Load() != 0 {
				t.Fatalf("restart = %s %s, %d ensures", txt, data, fd.ensuresAfter.Load())
			}
		})
	}
}
