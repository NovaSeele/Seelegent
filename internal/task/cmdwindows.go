package task

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// A worker's script writes files no word of its command names (python fix.py, npm run format). The lane counts
// such a file as the worker's when it changed while one of the worker's commands ran: its modification time in the
// end snapshot lies in the command's run window, from its start event to its end event, widened by runSlack on both
// sides. A change outside every window (the lead editing while the worker thinks) stays unseen. A code runner whose
// start is not known writes where Seelegent cannot tell: unknown, so the lane fails closed (field report 4, review).

// runSlack widens a command's run window on both sides. The daemon stamps an event when it reads the worker's line,
// up to launch.ReadDelay after the worker wrote it (the tail backs off while the worker is quiet), and later still on
// a busy machine: a command's first write may come before its call event's time. A file's time may also trail the
// result event a little.
const runSlack = launch.ReadDelay + 2*time.Second

// maxRunEvents bounds the shell tool events read for the run windows of one lane check. Each is a few fields
// (CommandEvents), so the bound holds the memory to a few megabytes; a runner past it has no known start and fails
// closed.
const maxRunEvents = 50000

// commandRun is when one of a worker's shell commands ran.
type commandRun struct {
	command    string
	start, end time.Time
}

// commandRuns reads the shell commands of turns from..to of t from their tool events: a call event and its result
// give the window; a result alone gives it with its duration. timed holds the commands whose start is known. A call
// without a result (the command was still running when the turn ended) runs until the turn's end.
func (s *Service) commandRuns(ctx context.Context, t *Task, from, to int) (runs []commandRun, timed map[string]bool) {
	timed = map[string]bool{}
	evs, err := s.repo.CommandEvents(ctx, t.ID, from, to, maxRunEvents)
	if err != nil {
		s.log.Warn("task: read the command events for the lane", "task", t.ID, "err", err)
		return nil, timed
	}
	if len(evs) == maxRunEvents {
		s.log.Warn("task: the lane reads only the first command events", "task", t.ID, "events", maxRunEvents)
	}
	type open struct {
		at      time.Time
		turn    int
		command string
	}
	started := map[string]open{}
	var order []string
	for _, e := range evs {
		id := e.ToolID
		if id == "" {
			id = "step " + strconv.Itoa(e.Step)
		}
		key := strconv.Itoa(e.Turn) + "/" + strconv.Itoa(e.Attempt) + "/" + id
		switch e.Kind {
		case event.KindToolCall:
			if _, ok := started[key]; !ok {
				started[key] = open{at: e.TS, turn: e.Turn, command: e.Command}
				order = append(order, key)
			}
		case event.KindToolResult:
			run := commandRun{command: e.Command, end: e.TS}
			if o, ok := started[key]; ok {
				run.start = o.at
				delete(started, key)
			} else if e.DurationMs > 0 {
				run.start = e.TS.Add(-time.Duration(e.DurationMs) * time.Millisecond)
			} else {
				continue // no start: untimed
			}
			runs = append(runs, run)
			timed[run.command] = true
		}
	}
	for _, key := range order {
		o, ok := started[key]
		if !ok {
			continue
		}
		end := s.clk.Now()
		if tu := t.TurnN(o.turn); tu != nil && !tu.EndedAt.IsZero() {
			end = tu.EndedAt
		}
		runs = append(runs, commandRun{command: o.command, start: o.at, end: end})
		timed[o.command] = true
	}
	return runs, timed
}

// addRuns gives w the run windows of the commands of t's turns since the instruction up to turn n, with the files'
// modification times from end (the end snapshot of turn n; nil: none, no window applies), and lists as unknown the
// code runners among commands (all the instruction's commands so far) whose start is not known.
func (s *Service) addRuns(ctx context.Context, w *workerWrites, t *Task, n int, commands []string,
	end *snapshot.Manifest) {
	runs, timed := s.commandRuns(ctx, t, t.Turns[instructionStart(t, n)].N, n)
	w.runs = runs
	if end != nil {
		w.mtime = func(rel string) (time.Time, bool) {
			f, ok := end.Find(rel)
			if !ok || f.Missing || f.ModTime == 0 {
				return time.Time{}, false
			}
			return time.Unix(0, f.ModTime), true
		}
	}
	for _, c := range commands {
		if !guard.Runs(c) {
			continue
		}
		if w.runner == "" {
			w.runner = c
		}
		if !timed[c] && !slices.Contains(w.unknown, c) {
			w.unknown = append(w.unknown, c)
		}
	}
}

// runAt returns the run during which a file changed at the given time: the first whose window holds it.
func runAt(runs []commandRun, at time.Time) (commandRun, bool) {
	for _, r := range runs {
		if !at.Before(r.start.Add(-runSlack)) && !at.After(r.end.Add(runSlack)) {
			return r, true
		}
	}
	return commandRun{}, false
}
