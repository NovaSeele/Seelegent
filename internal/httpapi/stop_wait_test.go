package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// An open meeting wait ends at once when the daemon stops, with daemon_stopping: Close does not sit out the HTTP
// grace and the client gets an answer, not a broken connection (M18 hunt H8).
func TestMeetingWaitEndsWhenTheDaemonStops(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	e.must(e.post("/api/v1/members", map[string]any{"id": "ana", "name": "Ana", "backend": "agy", "rank": "senior"}),
		http.StatusCreated)
	// The members' workers run until release exists; the daemon's stop leaves them running, so the test lets them
	// end (and their launchers let go of the data folder) before it is removed.
	release := filepath.Join(t.TempDir(), "release")
	t.Cleanup(func() { letWorkersEnd(t, e, release) })
	for range 4 {
		e.queue.Push(t, line(map[string]any{"event": "init", "conversation_id": "m1",
			"init": map[string]any{"model": "m"}})+"\n#!wait-file "+release+"\n")
	}
	var m api.Meeting
	e.must(e.post("/api/v1/meetings", map[string]any{"question": "Which?", "cwd": e.cwd,
		"attendees": []string{"ana"}}), http.StatusCreated).json(t, &m)
	type answer struct {
		status int
		body   []byte
		err    error
	}
	got := make(chan answer, 1)
	started := make(chan struct{})
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet,
			e.base+"/api/v1/meetings/"+m.ID+"/wait?seconds=120", http.NoBody)
		hc := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		close(started)
		resp, err := hc.Do(req)
		if err != nil {
			got <- answer{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		got <- answer{status: resp.StatusCode, body: b, err: err}
	}()
	<-started
	time.Sleep(time.Second) // the wait is open
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	if err := e.a.Close(ctx); err != nil {
		t.Errorf("Close: %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("Close took %s with a meeting wait open", d)
	}
	select {
	case a := <-got:
		if a.err != nil {
			t.Fatalf("the wait got no answer: %v", a.err)
		}
		var body api.ErrorBody
		if err := json.Unmarshal(a.body, &body); err != nil || a.status != http.StatusServiceUnavailable ||
			body.Error.Code != api.CodeDaemonStopping {
			t.Errorf("answer %d %s", a.status, a.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the meeting wait is still open 10 s after Close")
	}
}

// letWorkersEnd creates release (the workers' wait-file) and waits until no launcher holds its lock any more.
func letWorkersEnd(t *testing.T, e *env, release string) {
	t.Helper()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Error(err)
		return
	}
	end := time.Now().Add(limit)
	for time.Now().Before(end) {
		locks, _ := filepath.Glob(filepath.Join(e.paths.Tasks, "*", "*", "launch", "launcher.lock"))
		held := 0
		for _, l := range locks {
			if err := os.Remove(l); err != nil && !errors.Is(err, fs.ErrNotExist) {
				held++
			}
		}
		if held == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Error("a worker launcher still runs")
}
