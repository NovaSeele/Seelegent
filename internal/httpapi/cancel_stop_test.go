package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// waitingCancel is a task engine whose Cancel records the cancel (or not) and then waits for the worker until the
// request's context ends, as task.Service does.
type waitingCancel struct {
	Tasks
	record  bool
	waiting chan struct{}
}

func (f *waitingCancel) Cancel(ctx context.Context, id string, _ api.CancelRequest) (api.TaskSummary, bool, error) {
	if !f.record {
		close(f.waiting)
		<-ctx.Done()
		return api.TaskSummary{}, false, ctx.Err() // cut before the cancel was written
	}
	close(f.waiting)
	<-ctx.Done()
	return api.TaskSummary{ID: id, Status: api.StatusRunning}, false, nil
}

// A cancel whose wait the shutdown cuts short says whether the cancel was recorded, so the lead neither cancels
// again nor takes it for a failure (M18 review G5).
func TestCancelCutByTheStopSaysItWasRecorded(t *testing.T) {
	for _, record := range []bool{true, false} {
		f := &waitingCancel{record: record, waiting: make(chan struct{})}
		ctx, stop := context.WithCancel(context.Background())
		s := &Server{o: Options{Tasks: f}, log: slog.New(slog.DiscardHandler), mux: http.NewServeMux(), ctx: ctx}
		s.taskRoutes()
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, apiPrefix+"/tasks/t1/cancel",
			strings.NewReader(`{"waitSeconds":30}`))
		req.Header.Set("Content-Type", "application/json")
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.mux.ServeHTTP(rec, req)
		}()
		<-f.waiting
		stop() // the daemon stops
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the cancel wait did not end with the stop")
		}
		var body api.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusServiceUnavailable ||
			body.Error.Code != api.CodeDaemonStopping {
			t.Fatalf("recorded %v: answer %d %s", record, rec.Code, rec.Body)
		}
		if got := body.Error.Details["cancelRecorded"] == true; got != record {
			t.Errorf("recorded %v: details %v", record, body.Error.Details)
		}
		if record && !strings.Contains(body.Error.Message, "cancel was recorded") {
			t.Errorf("message = %q", body.Error.Message)
		}
	}
}
