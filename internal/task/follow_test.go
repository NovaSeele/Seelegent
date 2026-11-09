package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// followLine is one line of a worker's output as the tail delivers it.
type followLine struct {
	stderr bool
	text   string
	next   int64 // offset after the line in its file
}

// followLines gives stream (and "!"-prefixed stderr) lines their file offsets.
func followLines(texts ...string) []followLine {
	var out []followLine
	var stream, stderr int64
	for _, t := range texts {
		l := followLine{text: t}
		if rest, ok := strings.CutPrefix(t, "!"); ok {
			l.stderr, l.text = true, rest
			stderr += int64(len(rest)) + 1
			l.next = stderr
		} else {
			stream += int64(len(t)) + 1
			l.next = stream
		}
		out = append(out, l)
	}
	return out
}

// followTask creates a running task with one turn and one attempt for a follower to write to.
func followTask(h *harness, id string) {
	h.t.Helper()
	ctx := context.Background()
	now := h.clk.Now()
	tk := &Task{ID: id, Title: id, Status: StatusRunning, Kind: KindNormal, Mode: ModeEdit, Cwd: h.cwd,
		ProjectKey: "p", Backend: config.BackendAgy, CreatedAt: now, UpdatedAt: now,
		Turns: []*Turn{{N: 1, By: ByLead, Message: "go", Status: TurnRunning, StartedAt: now,
			Attempts: []*Attempt{{N: 1, AccountID: "a1", Backend: config.BackendAgy, Status: AttemptRunning,
				StartedAt: now}}}}}
	if _, err := h.s.repo.Create(ctx, tk, nil); err != nil {
		h.t.Fatal(err)
	}
}

// expectedEvents is what a follower must log for lines: every event the parser gives, with consecutive message
// deltas of one step merged (the order and content one write per line gave).
func expectedEvents(t *testing.T, h *harness, lines []followLine) []event.Event {
	t.Helper()
	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}
	p := b.NewParser(nil)
	var out []event.Event
	var pending *event.Event
	for _, l := range lines {
		var evs []event.Event
		if l.stderr {
			evs = p.Stderr(l.text)
		} else {
			evs = p.Line([]byte(l.text))
		}
		for _, ev := range evs {
			if ev.Kind == event.KindMessage && pending != nil && ev.Step == pending.Step {
				pending.Text += ev.Text
				continue
			}
			if pending != nil {
				out = append(out, *pending)
				pending = nil
			}
			if ev.Kind == event.KindMessage {
				e := ev
				pending = &e
				continue
			}
			out = append(out, ev)
		}
	}
	if pending != nil {
		out = append(out, *pending)
	}
	return out
}

// gatedWrites wraps a follower's write: the first write waits until release is closed, a write numbered failAt
// (from 1) fails, and every batch is recorded.
type gatedWrites struct {
	mu      sync.Mutex
	batches [][]NewEvent
	entered chan struct{}
	release chan struct{}
	failAt  int
}

func gateWrites(f *follower, failAt int) *gatedWrites {
	g := &gatedWrites{entered: make(chan struct{}), release: make(chan struct{}), failAt: failAt}
	inner := f.write
	f.write = func(evs []NewEvent, off Offsets) error {
		g.mu.Lock()
		g.batches = append(g.batches, evs)
		n := len(g.batches)
		g.mu.Unlock()
		if n == 1 {
			close(g.entered)
			<-g.release
		}
		if n == g.failAt {
			return errors.New("disk full")
		}
		return inner(evs, off)
	}
	return g
}

func (g *gatedWrites) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.batches)
}

// feed delivers lines to f as a tail does.
func feed(t *testing.T, f *follower, lines []followLine) error {
	t.Helper()
	for _, l := range lines {
		if err := f.line(l.stderr, []byte(l.text), l.next); err != nil {
			return err
		}
	}
	return nil
}

// startPump runs f's pump until the returned stop is called.
func startPump(f *follower) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.pump(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

// checkLogged compares the worker events of task id with want: same order and content, seqs 1.. without gaps.
func checkLogged(t *testing.T, h *harness, id string, want []event.Event) []api.TaskEvent {
	t.Helper()
	got := h.events(id)
	if len(got) != len(want) {
		t.Fatalf("logged %d events, want %d", len(got), len(want))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) || e.Type != api.EventWorker || e.Turn != 1 || e.Attempt != 1 {
			t.Fatalf("event %d = seq %d type %s turn %d attempt %d", i, e.Seq, e.Type, e.Turn, e.Attempt)
		}
		wantData, err := json.Marshal(want[i])
		if err != nil {
			t.Fatal(err)
		}
		gotData, err := json.Marshal(e.Data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotData, wantData) {
			t.Errorf("event %d data = %s, want %s", i, e.Data, wantData)
		}
	}
	return got
}

// committedOffsets reads the attempt's committed offsets from the log.
func committedOffsets(h *harness, id string) Offsets {
	a := h.task(id).Turns[0].Attempts[0]
	return Offsets{Turn: 1, Attempt: 1, Stream: a.StreamOffset, Stderr: a.StderrOffset}
}

// The lines read while a write runs go into one transaction and one bus batch; the events, their order, seqs and
// frames are the ones a write per line gave, and the offsets of the last line are committed.
func TestFollowerGroupCommit(t *testing.T) {
	h := newHarness(t)
	followTask(h, "tf1")
	lines := followLines(agyInit("c1"), agyDelta(1, "Reading "), agyDelta(1, "the file. "), "!a warning",
		agyTool(2, "CALLING", "go test ./..."), agyTool(2, "DONE", "go test ./..."), agyDelta(3, "Editing."),
		agyEdit(4, "main.go"), "!another warning", agyDelta(5, "Done "), agyDelta(5, "now."),
		agyResult("c1", "All done."))
	want := expectedEvents(t, h, lines)

	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}
	sub := h.s.Bus().Subscribe(1024)
	defer sub.Close()
	f := h.s.newFollower(&run{id: "tf1"}, b.NewParser(nil), "tf1", 1, 1, Offsets{})
	g := gateWrites(f, 0)
	stop := startPump(f)
	if err := feed(t, f, lines[:1]); err != nil {
		t.Fatal(err)
	}
	<-g.entered // the first line's write runs: the rest of the chunk arrives meanwhile
	if err := feed(t, f, lines[1:]); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	h.waitFor("the second write", func() bool { return g.count() >= 2 })
	stop()
	if err := f.drain(); err != nil {
		t.Fatal(err)
	}
	if n := g.count(); n > 3 {
		t.Errorf("%d writes for %d lines, want at most 3 (first line, rest of the chunk, last message)", n, len(lines))
	}
	got := checkLogged(t, h, "tf1", want)

	// The bus carried the same events, in seq order, one batch per write.
	var frames []api.TaskEvent
	for len(frames) < len(got) {
		select {
		case n := <-sub.C():
			if n.Kind == NoticeEvent && n.Event != nil {
				frames = append(frames, *n.Event)
			}
		case <-time.After(waitLimit):
			t.Fatalf("got %d frames, want %d", len(frames), len(got))
		}
	}
	for i := range got {
		a, _ := json.Marshal(got[i])
		b, _ := json.Marshal(frames[i])
		if !bytes.Equal(a, b) {
			t.Errorf("frame %d = %s, stored %s", i, b, a)
		}
	}
	last := lines[len(lines)-1].next
	if off := committedOffsets(h, "tf1"); off.Stream != last || off.Stderr != lines[8].next {
		t.Errorf("committed %+v, want stream %d stderr %d", off, last, lines[8].next)
	}
}

// A write that fails mid-chunk stores nothing of its batch and commits no offset past the last stored line; the
// follow fails, and a resumed follower (a daemon restart) logs every event exactly once.
func TestFollowerCrashMidChunk(t *testing.T) {
	h := newHarness(t)
	followTask(h, "tf2")
	lines := followLines(agyInit("c2"), agyTool(1, "CALLING", "ls"), agyTool(1, "DONE", "ls"), "!warn",
		agyDelta(2, "Thinking "), agyDelta(2, "more."), agyEdit(3, "a.go"), agyResult("c2", "ok"))
	want := expectedEvents(t, h, lines)
	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}

	f := h.s.newFollower(&run{id: "tf2"}, b.NewParser(nil), "tf2", 1, 1, Offsets{})
	g := gateWrites(f, 2)
	stop := startPump(f)
	if err := feed(t, f, lines[:1]); err != nil {
		t.Fatal(err)
	}
	<-g.entered
	if err := feed(t, f, lines[1:5]); err != nil {
		t.Fatal(err)
	}
	close(g.release)
	h.waitFor("the failed write", func() bool { return g.count() >= 2 })
	stop()
	if err := feed(t, f, lines[5:]); err == nil {
		t.Error("a line after the failed write was taken")
	}
	if err := f.drain(); err == nil {
		t.Error("drain after a failed write succeeded")
	}
	checkLogged(t, h, "tf2", want[:1]) // only the first line's write is stored
	off := committedOffsets(h, "tf2")
	if off.Stream != lines[0].next || off.Stderr != 0 {
		t.Fatalf("committed %+v after the failed write, want stream %d stderr 0", off, lines[0].next)
	}

	// The restarted daemon follows the attempt again from the start, with the committed offsets.
	f2 := h.s.newFollower(&run{id: "tf2"}, b.NewParser(nil), "tf2", 1, 1, off)
	stop2 := startPump(f2)
	if err := feed(t, f2, lines); err != nil {
		t.Fatal(err)
	}
	stop2()
	if err := f2.drain(); err != nil {
		t.Fatal(err)
	}
	checkLogged(t, h, "tf2", want)
	if off := committedOffsets(h, "tf2"); off.Stream != lines[len(lines)-1].next || off.Stderr != lines[3].next {
		t.Errorf("committed %+v at the end", off)
	}
}

// A message being coalesced is not covered by the offsets of an earlier write: a crash before it is stored makes
// the restarted follower log it.
func TestFollowerOffsetsStopBeforeCoalescedMessage(t *testing.T) {
	h := newHarness(t)
	followTask(h, "tf3")
	lines := followLines(agyInit("c3"), agyDelta(1, "half "), "!noise without events?", agyDelta(1, "done"))
	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}
	f := h.s.newFollower(&run{id: "tf3"}, b.NewParser(nil), "tf3", 1, 1, Offsets{})
	if err := feed(t, f, lines); err != nil {
		t.Fatal(err)
	}
	if err := f.flush(); err != nil { // what the pump writes before the coalesce window ends
		t.Fatal(err)
	}
	if off := committedOffsets(h, "tf3"); off.Stream != lines[0].next {
		t.Errorf("committed stream %d while the message from %d on is buffered, want %d", off.Stream,
			lines[0].next, lines[0].next)
	}
	if err := f.drain(); err != nil {
		t.Fatal(err)
	}
	checkLogged(t, h, "tf3", expectedEvents(t, h, lines))
	if off := committedOffsets(h, "tf3"); off.Stream != lines[3].next {
		t.Errorf("committed stream %d at the end, want %d", off.Stream, lines[3].next)
	}
}

// A batch that reaches maxBatch is written by the line that filled it.
func TestFollowerBatchCap(t *testing.T) {
	h := newHarness(t)
	followTask(h, "tf4")
	texts := []string{agyInit("c4")}
	for i := range maxBatch + 10 {
		texts = append(texts, agyTool(i+1, "CALLING", "ls"))
	}
	lines := followLines(texts...)
	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}
	f := h.s.newFollower(&run{id: "tf4"}, b.NewParser(nil), "tf4", 1, 1, Offsets{})
	writes := 0
	inner := f.write
	f.write = func(evs []NewEvent, off Offsets) error {
		writes++
		if len(evs) > maxBatch {
			t.Errorf("a write of %d events", len(evs))
		}
		return inner(evs, off)
	}
	if err := feed(t, f, lines); err != nil { // no pump: only the cap writes
		t.Fatal(err)
	}
	if writes != 1 {
		t.Errorf("%d writes before drain, want 1", writes)
	}
	if err := f.drain(); err != nil {
		t.Fatal(err)
	}
	if n := len(h.events("tf4")); n != len(expectedEvents(t, h, lines)) {
		t.Errorf("logged %d events", n)
	}
}

// TestFollowerActiveStep: the active step follows the worker's agy events (a tool, then responding, then a tool agy
// reports only when it is done) and each change is published for the summary views (field report 4, F9: the task
// showed "starting" for 45 minutes).
func TestFollowerActiveStep(t *testing.T) {
	h := newHarness(t)
	followTask(h, "tf9")
	b, err := h.s.backend(config.BackendAgy)
	if err != nil {
		t.Fatal(err)
	}
	sub := h.s.Bus().Subscribe(1024)
	defer sub.Close()
	r := &run{id: "tf9"}
	f := h.s.newFollower(r, b.NewParser(nil), "tf9", 1, 1, Offsets{})
	lines := followLines(agyInit("c9"), agyTool(1, "CALLING", "go test ./..."), agyTool(1, "DONE", "go test ./..."),
		agyDelta(2, "Done "), agyDelta(2, "now."), agyEdit(3, "a.go"))
	var steps []string
	for _, l := range lines {
		if err := f.line(l.stderr, []byte(l.text), l.next); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, r.activity()[0])
	}
	want := []string{"", "run_command", "run_command", "responding", "responding", "replace_file_content"}
	if strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Errorf("steps = %q, want %q", steps, want)
	}
	live := 0
	for done := false; !done; {
		select {
		case n := <-sub.C():
			if n.Kind == NoticeLive && n.TaskID == "tf9" {
				live++
			}
		default:
			done = true
		}
	}
	// run_command opens, closes, responding, the edit: four changes; the second delta and the init change nothing.
	if live != 4 {
		t.Errorf("live notices = %d, want 4", live)
	}
}
