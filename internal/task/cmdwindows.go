package task

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// A worker's script writes files no word of its command names (python fix.py, npm run format). The lane counts
// such a file as the worker's when it changed while one of the worker's commands ran: its modification time in the
// end snapshot lies in the command's run window, from its start event to its end event plus runSlack. A change
// outside every window (the lead editing while the worker thinks) stays unseen. A code runner whose start is not
// known writes where Seelegent cannot tell: unknown, so the lane fails closed (field report 4, review).

// runSlack is added to a command's end: a file's time may trail its last event a little.
const runSlack = 2 * time.Second

// maxRunEvents bounds the worker events read for the run windows of one lane check.
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
	evs, err := s.repo.TurnEvents(ctx, t.ID, api.EventWorker, from, to, maxRunEvents)
	if err != nil {
		s.log.Warn("task: read the command events for the lane", "task", t.ID, "err", err)
		return nil, timed
	}
	type open struct {
		at   time.Time
		turn int
	}
	started := map[string]open{}
	for _, e := range evs {
		raw, ok := e.Data.(json.RawMessage)
		if !ok {
			continue
		}
		var ev event.Event
		if json.Unmarshal(raw, &ev) != nil || ev.Tool == nil || ev.Tool.Command == "" || ev.Tool.Blocked {
			continue
		}
		at, err := api.ParseTime(e.TS)
		if err != nil {
			continue
		}
		id := ev.Tool.ID
		if id == "" {
			id = "step " + strconv.Itoa(ev.Step)
		}
		key := strconv.Itoa(e.Turn) + "/" + strconv.Itoa(e.Attempt) + "/" + id
		switch ev.Kind {
		case event.KindToolCall:
			if _, ok := started[key]; !ok {
				started[key] = open{at: at, turn: e.Turn}
			}
		case event.KindToolResult:
			run := commandRun{command: ev.Tool.Command, end: at}
			if o, ok := started[key]; ok {
				run.start = o.at
				delete(started, key)
			} else if ev.Tool.DurationMs > 0 {
				run.start = at.Add(-time.Duration(ev.Tool.DurationMs) * time.Millisecond)
			} else {
				continue // no start: untimed
			}
			runs = append(runs, run)
			timed[run.command] = true
		}
	}
	for _, o := range started {
		end := s.clk.Now()
		if tu := t.TurnN(o.turn); tu != nil && !tu.EndedAt.IsZero() {
			end = tu.EndedAt
		}
		runs = append(runs, commandRun{start: o.at, end: end})
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
		w.runners = true
		if !timed[c] && !slices.Contains(w.unknown, c) {
			w.unknown = append(w.unknown, c)
		}
	}
}

// ranDuring reports whether a file changed at the given time while one of runs ran.
func ranDuring(runs []commandRun, at time.Time) bool {
	for _, r := range runs {
		if !at.Before(r.start) && !at.After(r.end.Add(runSlack)) {
			return true
		}
	}
	return false
}
