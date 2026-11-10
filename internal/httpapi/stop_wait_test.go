package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/platform"
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

// letWorkersEnd creates release (the workers' wait-file) and waits until every worker launch of the test has
// ended and its launcher is gone, so that no launcher or worker outlives the test and holds on to the data folder.
// A launch still running after workerEndWait is canceled (its launcher stops the worker tree) and waited for.
// Removing the launch lock cannot tell whether a launcher runs: Linux and macOS let a locked file be removed.
func letWorkersEnd(t *testing.T, e *env, release string) {
	t.Helper()
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Error(err)
	}
	dirs, err := filepath.Glob(filepath.Join(e.paths.Tasks, "*", "*", "launch"))
	if err != nil {
		t.Error(err)
	}
	for _, dir := range dirs {
		h, err := launch.Attach(dir)
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), workerEndWait)
		_, err = h.Wait(ctx)
		cancel()
		if err != nil {
			_ = h.Cancel()
			ctx, cancel := context.WithTimeout(context.Background(), limit)
			_, err = h.Wait(ctx)
			cancel()
		}
		if err != nil {
			t.Errorf("launch %s: %v", dir, err)
			continue
		}
		r, ok := h.Ready()
		if !ok {
			continue
		}
		for end := time.Now().Add(limit); platform.ProcessRunning(r.LauncherPID, r.LauncherStart); {
			if time.Now().After(end) {
				t.Errorf("launcher %d of %s still runs", r.LauncherPID, dir)
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// workerEndWait is how long letWorkersEnd lets a released worker end by itself before it cancels the launch.
const workerEndWait = 20 * time.Second
