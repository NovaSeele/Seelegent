package task

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/logx"
)

// coalesceWindow is how long consecutive message deltas of one step are buffered before they are written as one
// event (contract §3.2).
const coalesceWindow = 250 * time.Millisecond

// stderrTailBytes is how much of stderr the parser gets to classify a failed turn.
const stderrTailBytes = 8 << 10

// maxBatch caps the events of one write: a line that fills the batch writes it itself, so a worker that streams
// faster than the log stores waits instead of growing the batch without bound.
const maxBatch = 256

// follower turns one attempt's output into logged events. The stream and stderr are tailed from their start, so
// the parser always sees the whole turn; lines at or before the committed offsets are already in the log (a
// resumed attempt after a daemon restart) and only rebuild the parser's state. Consecutive message deltas of one
// step are coalesced before a seq is assigned.
//
// The log is written by group commit: a line's events join the batch and wake the pump, which writes the whole
// batch with the offsets it reaches in one transaction and publishes it as one bus batch. The first line of a read
// chunk is written at once (no timer holds it); the lines parsed while that write runs, the rest of the chunk, go
// together into the next one. A write always covers whole lines, so after a crash every event of the bytes before
// the committed offsets is in the log and none after them, and the events, their seqs and their frames are the
// ones a write per line gave.
type follower struct {
	s       *Service
	r       *run
	id      string
	turn, n int
	// write stores evs and commits off in one transaction, then publishes the events (Service.appendEvents; tests
	// replace it).
	write func(evs []NewEvent, off Offsets) error

	mu        sync.Mutex
	parser    backend.Parser
	coll      backend.Collector
	committed Offsets // in the log
	pos       Offsets // read so far
	// ready is how far the batch reaches: every event of the bytes before it is in the batch or in the log. It
	// stays behind pos while a message is being coalesced.
	ready   Offsets
	pending *event.Event // the message being coalesced (not in the batch yet)
	batch   []NewEvent
	err     error         // the first failed write: the follow fails with it
	kick    chan struct{} // a message started coalescing
	wake    chan struct{} // the batch has events

	wmu sync.Mutex // one write at a time, so batches reach the log in the order they were cut
}

// newFollower returns the follower of attempt n of turn of task id, resuming after committed.
func (s *Service) newFollower(r *run, parser backend.Parser, id string, turn, n int, committed Offsets) *follower {
	committed.Turn, committed.Attempt = turn, n
	start := Offsets{Turn: turn, Attempt: n}
	f := &follower{s: s, r: r, id: id, turn: turn, n: n, parser: parser, committed: committed, pos: start,
		ready: start, kick: make(chan struct{}, 1), wake: make(chan struct{}, 1)}
	f.write = func(evs []NewEvent, off Offsets) error { return s.appendEvents(s.background(), id, evs, &off) }
	return f
}

// follow follows attempt n of turn until the worker exited and every line was read, then returns the outcome. It
// fails only when the daemon shuts down (the worker keeps running) or the log cannot be written.
func (s *Service) follow(r *run, h *launch.Handle, b backend.Backend, m attemptMask, id string, turn, n int,
	committed Offsets) (attemptResult, error) {
	parser := b.NewParser(m.secrets)
	if m.patterns {
		parser.MaskPatterns(logx.NewRedactor().String)
	}
	tp, _ := parser.(threadUsageParser)
	if tp != nil {
		tp.SetUsageBaseline(s.usageBaseline(id, turn, n))
	}
	f := s.newFollower(r, parser, id, turn, n, committed)
	ctx, cancel := context.WithCancel(s.root)
	defer cancel()

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		f.pump(ctx)
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
	<-pumpDone
	if err != nil {
		if s.root.Err() != nil {
			return attemptResult{}, fmt.Errorf("follow %s: %w", id, s.root.Err())
		}
		return attemptResult{}, fmt.Errorf("follow the worker: %w", err)
	}
	if err := f.drain(); err != nil {
		return attemptResult{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
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
	out := f.coll.Outcome(exit)
	if tp != nil {
		if u, ok := tp.ThreadUsage(); ok {
			out.ThreadUsage = &u
		}
	}
	return attemptResult{outcome: out, exit: exit, finish: finish}, nil
}

// threadUsageParser is a parser whose CLI reports usage as the conversation's running total (codex: the
// turn.completed of a resumed thread counts every earlier run of it again). It gets the total before the attempt,
// reports the attempt's part, and returns the new total, which the attempt's outcome keeps for the next one.
type threadUsageParser interface {
	SetUsageBaseline(event.Usage)
	ThreadUsage() (event.Usage, bool)
}

// usageBaseline is the conversation's usage total before attempt n of turn (usageBaselineOf). A task that cannot be
// read gives zero, so the attempt counts the whole total: too much rather than nothing.
func (s *Service) usageBaseline(id string, turn, n int) event.Usage {
	t, err := s.repo.Get(s.background(), id)
	if err != nil {
		s.log.Warn("task: read the usage before the attempt", "task", id, "err", err)
		return event.Usage{}
	}
	return usageBaselineOf(t, turn, n)
}

// usageBaselineOf is the usage total of the conversation attempt n of turn continues, as the latest earlier attempt
// in that conversation reported it; zero for an attempt that starts a new conversation. An attempt with usage but
// no total ended before the outcome kept the total: its usage was the CLI's total then.
func usageBaselineOf(t *Task, turn, n int) event.Usage {
	tu := t.TurnN(turn)
	if tu == nil || n < 1 || n > len(tu.Attempts) {
		return event.Usage{}
	}
	cur := tu.Attempts[n-1]
	if !cur.Continuing || cur.ConversationID == "" {
		return event.Usage{}
	}
	var base event.Usage
	for _, x := range t.Turns {
		for _, a := range x.Attempts {
			if x.N > turn || (x.N == turn && a.N >= n) || a.ConversationID != cur.ConversationID || a.Outcome == nil {
				continue
			}
			switch {
			case a.Outcome.ThreadUsage != nil:
				base = *a.Outcome.ThreadUsage
			case a.Usage != nil:
				base = *a.Usage
			}
		}
	}
	return base
}

// activity is what the worker does now, as the task's active step shows it: its step label and open tool.
func (r *run) activity() [2]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return [2]string{r.live.step, r.live.tool}
}

// line takes one line of the stream (or stderr).
func (f *follower) line(stderr bool, line []byte, next int64) error {
	f.mu.Lock()
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return err
	}
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
	before := f.r.activity()
	for _, ev := range evs {
		f.coll.Add(ev)
		f.r.note(ev, now, replay)
		if !replay && ev.Tool != nil && ev.Tool.Blocked {
			// The whole refused command goes to the (redacted) daemon log: the task's events cut it short, which kept
			// a false refusal from being understood (field report 4, F11).
			f.s.log.Info("task: the git guard refused a command", "task", f.id, "turn", f.turn,
				"command", ev.Tool.Command, "reason", ev.Tool.Output)
		}
	}
	if !replay && f.r.activity() != before {
		// The task's active step changed: the summary views show it (field report 4, F9: it stayed "starting" for
		// 45 minutes, as worker events never republished the summary).
		f.s.bus.Publish(Notice{Kind: NoticeLive, TaskID: f.id})
	}
	if replay || len(evs) == 0 || f.mergeLocked(evs) {
		if f.pending == nil {
			f.ready = f.pos
		}
		f.mu.Unlock()
		return nil
	}
	f.releaseLocked(now)
	for _, ev := range evs {
		f.batch = append(f.batch, NewEvent{Type: api.EventWorker, Turn: f.turn, Attempt: f.n, Data: ev, TS: now})
	}
	f.ready = f.pos
	full := len(f.batch) >= maxBatch
	f.mu.Unlock()
	if full {
		return f.flush()
	}
	signal(f.wake)
	return nil
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
		signal(f.kick)
	}
	for _, ev := range evs {
		f.pending.Text += ev.Text
	}
	return true
}

// releaseLocked moves the message being coalesced into the batch, stamped now.
func (f *follower) releaseLocked(now time.Time) {
	if f.pending == nil {
		return
	}
	f.batch = append(f.batch, NewEvent{Type: api.EventWorker, Turn: f.turn, Attempt: f.n, Data: *f.pending, TS: now})
	f.pending = nil
	f.ready = f.pos
}

// flush writes the batch with the offsets it reaches, in one transaction. A failed write is kept: every later line
// and flush returns it.
func (f *follower) flush() error {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	f.mu.Lock()
	if f.err != nil {
		err := f.err
		f.mu.Unlock()
		return err
	}
	evs := f.batch
	f.batch = nil
	off := Offsets{Turn: f.turn, Attempt: f.n, Stream: max(f.ready.Stream, f.committed.Stream),
		Stderr: max(f.ready.Stderr, f.committed.Stderr)}
	same := off == f.committed
	f.mu.Unlock()
	if len(evs) == 0 && same {
		return nil
	}
	err := f.write(evs, off)
	f.mu.Lock()
	if err != nil {
		f.err = err
	} else {
		f.committed = off
	}
	f.mu.Unlock()
	return err
}

// drain writes what is left once both files were read to their end: the message being coalesced, the batch and
// the final offsets.
func (f *follower) drain() error {
	f.mu.Lock()
	f.releaseLocked(f.s.clk.Now())
	f.ready = f.pos
	f.mu.Unlock()
	return f.flush()
}

// pump writes the batch whenever it has events, and releases a coalesced message coalesceWindow after it started,
// until ctx ends (drain writes the rest).
func (f *follower) pump(ctx context.Context) {
	var timer clock.Timer
	var fire <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.kick:
			if fire == nil {
				timer = f.s.clk.NewTimer(coalesceWindow)
				fire = timer.C()
			}
			continue
		case <-fire:
			fire = nil
			f.mu.Lock()
			f.releaseLocked(f.s.clk.Now())
			f.mu.Unlock()
		case <-f.wake:
		}
		// flush keeps an error: the tail stops at its next line and follow reports it.
		_ = f.flush()
	}
}

// signal wakes the receiver of ch without blocking (ch has room for one).
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
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
