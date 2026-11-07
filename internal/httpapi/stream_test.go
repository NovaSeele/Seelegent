package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestStreamResume covers contract §7: hello first, resume with Last-Event-ID replays exactly the missed frames,
// an id of another daemon start gets reset restart.
func TestStreamResume(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	var boot api.BootState
	e.must(e.get("/api/v1/state"), http.StatusOK).json(t, &boot)

	live := e.stream(boot.LastEventID)
	hello := live.next()
	var h api.Hello
	if err := json.Unmarshal([]byte(hello.data), &h); err != nil || h.Instance == "" || h.PID == 0 || len(h.Build) != 12 {
		t.Fatalf("hello = %+v", hello)
	}
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Stream me.")
	e.wait(sum.ID)
	last := live.until("task.updated", func(f frame) bool {
		var s api.TaskSummary
		return json.Unmarshal([]byte(f.data), &s) == nil && s.ID == sum.ID && s.Status == api.StatusDone
	})

	// Everything after the snapshot again, in order, ending with the same frame.
	replay := e.stream(boot.LastEventID)
	if f := replay.next(); f.event != "hello" || f.id != boot.LastEventID {
		t.Fatalf("resume hello = %+v", f)
	}
	prev := seqOf(t, boot.LastEventID)
	var seenEvent, seenCreated bool
	for {
		f := replay.next()
		if f.event == ":" {
			continue
		}
		if s := seqOf(t, f.id); s != prev+1 {
			t.Fatalf("replay seq %d after %d (%+v)", s, prev, f)
		}
		prev++
		seenEvent = seenEvent || f.event == "task.event"
		seenCreated = seenCreated || f.event == "task.created"
		if f.id == last.id {
			break
		}
	}
	if !seenEvent || !seenCreated {
		t.Fatalf("replay without task frames: event %v created %v", seenEvent, seenCreated)
	}

	// An id of another daemon start, or garbage: hello, then reset restart.
	for _, id := range []string{"0badc0de.5", "garbage"} {
		s := e.stream(id)
		if f := s.next(); f.event != "hello" {
			t.Fatalf("%s: first = %+v", id, f)
		}
		f := s.next()
		var r api.Reset
		if err := json.Unmarshal([]byte(f.data), &r); err != nil || f.event != "reset" || r.Reason != api.ResetRestart {
			t.Fatalf("%s: reset = %+v", id, f)
		}
	}

	// The query parameter works like the header.
	q := e.streamPath("/api/v1/events?lastEventId=garbage", "")
	if f := q.next(); f.event != "hello" {
		t.Fatalf("query hello = %+v", f)
	}
	if f := q.next(); f.event != "reset" {
		t.Fatalf("query reset = %+v", f)
	}
}
