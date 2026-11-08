package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// A cancel whose wait the daemon's stop cut short after the cancel was recorded is no failure: the tool says the
// cancel stands and is carried out when the daemon starts again (M18 review G5). Without the flag it stays an error.
func TestCancelRecordedWhileTheDaemonStops(t *testing.T) {
	recorded := api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeDaemonStopping, Message: "stopping",
		Details: map[string]any{"cancelRecorded": true}}}
	plain := api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeDaemonStopping, Message: "stopping"}}
	_, c := newStub(t, map[string]http.HandlerFunc{
		"POST /tasks/t1/cancel": answer(http.StatusServiceUnavailable, recorded),
		"POST /tasks/t2/cancel": answer(http.StatusServiceUnavailable, plain),
	})
	cs := stubSession(t, c, nil)
	res := callTool(t, cs, "cancel", map[string]any{"task_id": "t1", "wait_seconds": 10})
	data, _ := json.Marshal(res.StructuredContent)
	if res.IsError || !strings.Contains(string(data), `"cancelling"`) ||
		!strings.Contains(string(data), "the cancel was recorded") {
		t.Fatalf("recorded cancel = %v %s %q", res.IsError, data, text(res, 0))
	}
	if res := callTool(t, cs, "cancel", map[string]any{"task_id": "t2", "wait_seconds": 10}); !res.IsError {
		t.Fatalf("a cancel cut before it was recorded is no error: %s", text(res, 0))
	}
}
