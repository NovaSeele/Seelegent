package task

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/logx"
)

// coalesceWindow is how long consecutive message deltas of one step are buffered before they are written as one
// event (contract §3.2).
const coalesceWindow = 250 * time.Millisecond

// stderrTailBytes is how much of stderr the parser gets to classify a failed turn.
const stderrTailBytes = 8 << 10

// follower turns one attempt's output into logged events. The stream and stderr are tailed from their start, so
// the parser always sees the whole turn; lines at or before the committed offsets are already in the log (a
// resumed attempt after a daemon restart) and only rebuild the parser's state. Consecutive message deltas of one
// step are coalesced before a seq is assigned and written with the offsets they cover in one transaction.
type follower struct {
	s       *Service
	r       *run
	id      string
	turn, n int

	mu        sync.Mutex
	parser    backend.Parser
	coll      backend.Collector
	committed Offsets // already in the log
	pos       Offsets // read so far
	pending   *event.Event
	kick      chan struct{}
}

// follow follows attempt n of turn until the worker exited and every line was read, then returns the outcome. It
// fails only when the daemon shuts down (the worker keeps running) or the log cannot be written.
func (s *Service) follow(r *run, h *launch.Handle, b backend.Backend, m attemptMask, id string, turn, n int,
	committed Offsets) (attemptResult, error) {
	committed.Turn, committed.Attempt = turn, n
	parser := b.NewParser(m.secrets)
	if m.patterns {
		parser.MaskPatterns(logx.NewRedactor().String)
	}
	f := &follower{s: s, r: r, id: id, turn: turn, n: n, parser: parser, committed: committed,
		pos: Offsets{Turn: turn, Attempt: n}, kick: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(s.root)
	defer cancel()

	flusherDone := make(chan struct{})
	go func() {
		defer close(flusherDone)
		f.flusher(ctx)
	}()
	stderrErr := goSafe(func() error {
		return h.TailStderr(ctx, 0, func(line []byte, next int64) error { return f.line(true, line, next) })
	})
	err := safely(func() error {
		return h.Tail(ctx, 0, func(line []byte, next int64) error { return f.line(false, line, next) })
	})
	if err != nil {
		cancel()
	}
	if serr := <-stderrErr; err == nil && serr != nil {
		err = serr
	}
	cancel()
	<-flusherDone
	if err != nil {
		if s.root.Err() != nil {
			return attemptResult{}, fmt.Errorf("follow %s: %w", id, s.root.Err())
		}
		return attemptResult{}, fmt.Errorf("follow the worker: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.flushLocked(nil); err != nil {
		return attemptResult{}, err
	}
	exit, _, err := h.Exited()
	if err != nil {
		return attemptResult{}, fmt.Errorf("read the exit record: %w", err)
	}
	tail, err := h.StderrTail(stderrTailBytes)
	if err != nil {
		s.log.Warn("task: read stderr", "task", id, "err", err)
	}
	finish := f.parser.Finish(exit, tail)
	for _, ev := range finish {
		f.coll.Add(ev)
	}
	return attemptResult{outcome: f.coll.Outcome(exit), exit: exit, finish: finish}, nil
}

// line takes one line of the stream (or stderr).
func (f *follower) line(stderr bool, line []byte, next int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var evs []event.Event
	replay := false
	if stderr {
		evs = f.parser.Stderr(string(line))
		f.pos.Stderr = next
		replay = next <= f.committed.Stderr
	} else {
		evs = f.parser.Line(line)
		f.pos.Stream = next
		replay = next <= f.committed.Stream
	}
	now := f.s.clk.Now()
	for _, ev := range evs {
		f.coll.Add(ev)
		f.r.note(ev, now, replay)
	}
	if replay || len(evs) == 0 {
		return nil
	}
	if f.mergeLocked(evs) {
		return nil
	}
	return f.flushLocked(evs)
}

// mergeLocked buffers evs when they are all message deltas of the step being buffered.
func (f *follower) mergeLocked(evs []event.Event) bool {
	step := evs[0].Step
	if f.pending != nil {
		step = f.pending.Step
	}
	for _, ev := range evs {
		if ev.Kind != event.KindMessage || ev.Step != step {
			return false
		}
	}
	if f.pending == nil {
		p := evs[0]
		f.pending = &p
		evs = evs[1:]
		select {
		case f.kick <- struct{}{}:
		default:
		}
	}
	for _, ev := range evs {
		f.pending.Text += ev.Text
	}
	return true
}

// flushLocked writes the buffered message and evs with the offsets read so far.
func (f *follower) flushLocked(evs []event.Event) error {
	var out []NewEvent
	now := f.s.clk.Now()
	if f.pending != nil {
		out = append(out, NewEvent{Type: api.EventWorker, Turn: f.turn, Attempt: f.n, Data: *f.pending, TS: now})
		f.pending = nil
	}
	for _, ev := range evs {
		out = append(out, NewEvent{Type: api.EventWorker, Turn: f.turn, Attempt: f.n, Data: ev, TS: now})
	}
	off := Offsets{Turn: f.turn, Attempt: f.n, Stream: max(f.pos.Stream, f.committed.Stream),
		Stderr: max(f.pos.Stderr, f.committed.Stderr)}
	if len(out) == 0 && off == f.committed {
		return nil
	}
	if err := f.s.appendEvents(f.s.background(), f.id, out, &off); err != nil {
		return err
	}
	f.committed = off
	return nil
}

// flusher writes a buffered message coalesceWindow after it started.
func (f *follower) flusher(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
		}
		t := f.s.clk.NewTimer(coalesceWindow)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
		f.mu.Lock()
		if f.pending != nil {
			if err := f.flushLocked(nil); err != nil && f.s.root.Err() == nil {
				f.s.log.Warn("task: write events", "task", f.id, "err", err)
			}
		}
		f.mu.Unlock()
	}
}

// safely runs fn and turns a panic into an error, so a parser bug fails the task instead of the daemon (old B2).
func safely(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("internal error: %v\n%s", v, debug.Stack())
		}
	}()
	return fn()
}

// goSafe runs fn in a goroutine (through safely) and delivers its error.
func goSafe(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- safely(fn) }()
	return ch
}
