package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// serve starts a test server for handler and returns a client of it.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, srv.Client())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestDoSendsHeadersAndDecodes(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Seelegent") != "1" || r.URL.Path != "/api/v1/tasks" || r.Method != http.MethodPost ||
			r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s headers %v", r.Method, r.URL.Path, r.Header)
		}
		var req api.DelegateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Prompt != "p" {
			t.Errorf("body %+v %v", req, err)
		}
		writeJSON(w, http.StatusCreated, api.TaskSummary{ID: "t1", Status: api.StatusQueued})
	})
	sum, err := c.Delegate(t.Context(), api.DelegateRequest{Prompt: "p", Cwd: "/x"})
	if err != nil || sum.ID != "t1" {
		t.Fatalf("delegate = %+v, %v", sum, err)
	}
}

func TestErrors(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/tasks/worker":
			writeJSON(w, http.StatusForbidden, api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeForbidden,
				Message: "workers may not call this endpoint", Details: map[string]any{"caller": "worker"}}})
		case "/api/v1/tasks/busy":
			writeJSON(w, http.StatusConflict, api.ErrorBody{Error: api.ErrorInfo{Code: api.CodeBadState,
				Message: "task busy is running"}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<html>not here</html>\nsecond line"))
		}
	})
	_, err := c.Task(t.Context(), "worker")
	var e *Error
	if !errors.As(err, &e) || !e.Worker() || e.Code != api.CodeForbidden {
		t.Fatalf("worker error = %#v", err)
	}
	_, err = c.Task(t.Context(), "busy")
	if !IsCode(err, api.CodeBadState) || err.Error() != "task busy is running" {
		t.Fatalf("bad state = %v", err)
	}
	_, err = c.Task(t.Context(), "other")
	if !errors.As(err, &e) || e.Code != "" || e.Status != 404 || e.Error() != "HTTP 404: <html>not here</html>" {
		t.Fatalf("foreign error = %#v", err)
	}
}

func TestUnreachableAndCancel(t *testing.T) {
	c := ForPort(freePort(t), nil)
	if _, err := c.Health(t.Context()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("closed port = %v", err)
	}
	block := make(chan struct{})
	slow := serve(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	})
	defer close(block)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := slow.Wait(ctx, api.WaitRequest{IDs: []string{"t"}, TimeoutSeconds: 3000}, "")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("cancelled wait = %v after %v", err, time.Since(start))
	}
}

func TestQueryEscapes(t *testing.T) {
	got := ""
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RequestURI()
		writeJSON(w, http.StatusOK, api.TaskPage{Items: []api.TaskSummary{}})
	})
	if _, err := c.Tasks(t.Context(), TaskQuery{Statuses: []api.TaskStatus{api.StatusQueued, api.StatusRunning},
		Limit: 5, Text: "a b"}); err != nil {
		t.Fatal(err)
	}
	if got != "/api/v1/tasks?limit=5&q=a+b&status=queued%2Crunning" {
		t.Fatalf("uri = %s", got)
	}
	if err := c.DeleteRole(t.Context(), "r/1", "project", `C:\p`); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "/api/v1/roles/r%2F1?") {
		t.Fatalf("delete uri = %s", got)
	}
}

// freePort returns a port nothing listens on (it was free a moment ago).
func freePort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
