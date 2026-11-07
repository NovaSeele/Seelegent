package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/contest"
)

// TestCanceledIsNotInternal: a request whose client went away gets nothing written and no Error log; a canceled
// operation of a live request is 503, never 500.
func TestCanceledIsNotInternal(t *testing.T) {
	var logs bytes.Buffer
	s := &Server{log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gone := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/tasks/wait", http.NoBody)
	rec := httptest.NewRecorder()
	s.writeError(rec, gone, fmt.Errorf("reading the task: %w", context.Canceled))
	if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
		t.Fatalf("written to a gone client: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	if strings.Contains(logs.String(), "level=ERROR") || !strings.Contains(logs.String(), "client went away") {
		t.Fatalf("logs = %s", logs.String())
	}

	logs.Reset()
	live := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/state", http.NoBody)
	rec = httptest.NewRecorder()
	s.writeError(rec, live, fmt.Errorf("stopping: %w", context.Canceled))
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(logs.String(), "level=ERROR") {
		t.Fatalf("canceled operation = %d %q, logs %s", rec.Code, rec.Body, logs.String())
	}
}

// TestContestDataErrors: a removed variant without a stored copy is not_found, a damaged copy bad_state, and both
// name the reason and the variant for the dashboard.
func TestContestDataErrors(t *testing.T) {
	for _, tc := range []struct {
		reason string
		code   api.ErrorCode
	}{{contest.DataGone, api.CodeNotFound}, {contest.DataDamaged, api.CodeBadState}} {
		ae := toAPIError(fmt.Errorf("diff: %w", &contest.DataError{Contest: "c1", Label: "V2", Reason: tc.reason}))
		if ae.code != tc.code || ae.details["reason"] != tc.reason || ae.details["label"] != "V2" {
			t.Errorf("%s = %+v", tc.reason, ae)
		}
	}
}
