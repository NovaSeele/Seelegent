package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// Takeover timings.
const (
	// takeoverStopWait bounds the wait for a running worker to stop before the console opens.
	takeoverStopWait = 60 * time.Second
	// consolePoll is how often a console's exit file and process are checked.
	consolePoll = time.Second
	// consoleStartLimit is how long a console may take to start its command (the terminal script records its
	// process). A console that neither started nor reported an exit code by then never opened: no terminal could
	// run it (macOS refused the Automation permission, the daemon has no desktop, a broken terminal emulator).
	consoleStartLimit = 90 * time.Second
)

// Takeover hands the task's conversation to the user: a running worker is stopped first and the engine waits for
// it to exit (no worker and console in one conversation, old B4), then a console resumes the conversation with the
// task's own backend and account. When the console ends, a hand-back turn on the same backend and account asks the
// worker to summarize what the user did (old B5).
func (s *Service) Takeover(ctx context.Context, id string) (api.TaskSummary, error) {
	if s.o.Consoles == nil {
		return api.TaskSummary{}, fmt.Errorf("%w: consoles are not available", ErrUnavailable)
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskSummary{}, err
	}
	b, err := s.backend(t.Backend)
	if err != nil {
		return api.TaskSummary{}, err
	}
	owner := t.AccountID
	if live := s.live(id); t.ConversationID == "" || live.conversation != "" {
		if live.conversation == "" {
			return api.TaskSummary{}, badStatef("task %s has no worker conversation to take over yet", id)
		}
		owner = live.account // the running attempt's conversation; it is the task's once the attempt ended
	}
	prev := t.Status
	_, err = s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		switch {
		case t.Manual:
			return nil, badStatef("task %s is already taken over", id)
		case s.consoleOpen(id):
			return nil, badStatef("the takeover console of task %s is still open", id)
		case t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps:
			return nil, badStatef("task %s waits for its prerequisites", id)
		case t.CancelRequested:
			return nil, badStatef("task %s is being cancelled", id)
		case t.GatePhase != nil:
			return nil, badStatef("task %s is running its gate; wait for it or cancel it first", id)
		}
		prev = t.Status
		if t.Status != StatusRunning {
			if err := t.SetStatus(StatusRunning); err != nil {
				return nil, err
			}
		}
		t.Manual = true
		t.Console = &Console{AccountID: owner, Since: s.now(), From: prev}
		t.Waiting, t.WaitDetail, t.WaitingUntil = "", "", time.Time{}
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return []NewEvent{{Type: api.EventTakeover, Turn: turn,
			Data: api.TakeoverData{State: "start", AccountID: owner}}}, nil
	})
	if err != nil {
		return api.TaskSummary{}, err
	}
	if err := s.stopRunFor(ctx, id); err != nil {
		s.revertTakeover(id, prev, time.Time{}, err.Error())
		return api.TaskSummary{}, err
	}
	// The stopped attempt has recorded its conversation and account on the task.
	dir := filepath.Join(s.o.TasksDir, id, fmt.Sprintf("console-%d", s.now().UnixMilli()))
	var spec ConsoleSpec
	if t, err = s.repo.Get(ctx, id); err == nil {
		spec, err = s.consoleFor(ctx, t, b, dir)
	}
	if err == nil {
		_, err = s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
			if !t.Manual || t.Console == nil {
				return nil, badStatef("task %s was cancelled", id)
			}
			t.Console.AccountID = t.AccountID
			t.Console.ExitFile = filepath.Join(dir, spec.Name+".exit")
			t.Console.OpenedAt = s.now()
			var evs []NewEvent
			if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
				evs = append(evs, s.endTurn(t, tu, TurnCancelled, backend.Outcome{}))
			}
			return evs, nil
		})
	}
	var open OpenConsole
	if err == nil {
		open, err = s.o.Consoles.Open(ctx, spec)
		if err != nil {
			err = fmt.Errorf("%w: cannot open a console: %w", ErrUnavailable, err)
		}
	}
	if err != nil {
		s.revertTakeover(id, prev, time.Time{}, err.Error())
		return api.TaskSummary{}, err
	}
	cur, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskSummary{}, err
	}
	if cur.Console != nil {
		s.watchConsole(id, cur.Console, &open)
	}
	return s.summary(cur), nil
}

// stopRunFor stops the task's run, if any, and waits until it has ended (its worker exited).
func (s *Service) stopRunFor(ctx context.Context, id string) error {
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	r.requestStop()
	t := s.clk.NewTimer(takeoverStopWait)
	defer t.Stop()
	select {
	case <-r.done:
		return nil
	case <-t.C():
		return badStatef("the worker of task %s did not stop in time", id)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// consoleFor builds the console that resumes the task's conversation on its account.
func (s *Service) consoleFor(ctx context.Context, t *Task, b backend.Backend, dir string) (ConsoleSpec, error) {
	if t.ConversationID == "" || t.AccountID == "" {
		return ConsoleSpec{}, badStatef("task %s has no worker conversation to take over", t.ID)
	}
	acc, err := s.o.Accounts.Get(ctx, t.AccountID)
	if err != nil {
		return ConsoleSpec{}, badStatef("the account %s of the conversation is not available: %v", t.AccountID, err)
	}
	if b.Bin() == "" {
		return ConsoleSpec{}, fmt.Errorf("%w: %s is not installed", ErrUnavailable, b.Name())
	}
	env, err := s.o.Accounts.Env(ctx, acc, b, account.EnvOptions{Base: s.o.BaseEnv(), Model: t.Model,
		AllowGit: t.AllowGit})
	if err != nil {
		return ConsoleSpec{}, fmt.Errorf("%w: console environment: %w", ErrUnavailable, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ConsoleSpec{}, err
	}
	argv := append([]string{b.Bin()}, strings.Fields(b.Capabilities().ResumeFlag)...)
	argv = append(argv, t.ConversationID)
	return ConsoleSpec{
		Title: "Seelegent " + t.ID,
		Banner: []string{"Seelegent: you are steering task " + t.ID + " (" + t.Title + ").",
			"When you exit, the worker summarizes what you did."},
		Argv: argv, Dir: t.Cwd, Env: env.Vars, ScriptDir: dir, Name: "console",
	}, nil
}

// revertTakeover undoes a takeover that could not start: the task goes back to prev (cancelled when it was
// active, since its turn was stopped for the takeover). A non-zero since reverts only the console opened then.
func (s *Service) revertTakeover(id string, prev Status, since time.Time, why string) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if !t.Manual || (!since.IsZero() && (t.Console == nil || !t.Console.Since.Equal(since))) {
			return nil, errNoChange
		}
		acc := ""
		if t.Console != nil {
			acc = t.Console.AccountID
		}
		t.Manual, t.Console = false, nil
		back := prev
		if back == "" { // a takeover recorded before Console.From existed
			back = StatusCancelled
			if tu := t.LastTurn(); tu != nil && tu.Status == TurnOK {
				back = StatusDone
			}
		}
		if Active(back) {
			back = StatusCancelled // its turn was stopped for the takeover
		}
		var evs []NewEvent
		if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
			evs = append(evs, s.endTurn(t, tu, TurnCancelled, backend.Outcome{}))
		}
		t.Status = back
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return append(evs, NewEvent{Type: api.EventTakeover, Turn: turn,
			Data: api.TakeoverData{State: "end", AccountID: acc}},
			NewEvent{Type: api.EventError, Turn: turn, Data: api.ErrorData{Message: "takeover: " + why}}), nil
	})
	if err != nil {
		s.log.Error("task: revert takeover", "task", id, "err", err)
	}
}

// consoleExitClosed is the exit code recorded for a console whose process ended without writing one (its window
// was closed); the Unix terminal script writes the same code when it is hung up.
const consoleExitClosed = 129

// consoleWatch is a console being watched: the account slot it holds and whether that slot was given back.
type consoleWatch struct {
	lease *pool.Lease
}

// watchConsole counts the console on its account until it ends, then hands the task back. open is nil after a
// daemon restart. The console ends when its command reports an exit code (the wait, or the exit file), or when its
// process is gone without one (a closed window). A cancel gives the slot back at once (releaseConsole); the watch
// itself goes on until the console really ends, so no followup runs beside it.
//
// A console whose command has not started consoleStartLimit after it opened (no process known, no exit code)
// never opened: the takeover is reverted as if the console could not be opened (the task goes back to its status
// before the takeover, with an error event saying why), and the watch ends.
func (s *Service) watchConsole(id string, c *Console, open *OpenConsole) {
	s.mu.Lock()
	if s.watches[id] != nil {
		s.mu.Unlock()
		return
	}
	w := &consoleWatch{lease: s.o.Pool.Reattach(c.AccountID, id)}
	s.watches[id] = w
	s.mu.Unlock()
	exitFile, since, from, proc := c.ExitFile, c.Since, c.From, c.Process
	opened := c.OpenedAt
	if opened.IsZero() {
		opened = since
	}
	identify := func() (ConsoleProcess, bool) { return s.o.Consoles.Find(exitFile) }
	if open != nil && open.Identity != nil {
		identify = open.Identity
	}
	ok := s.spawn("console", id, func() {
		defer func() {
			w.lease.Release()
			s.mu.Lock()
			if s.watches[id] == w {
				delete(s.watches, id)
			}
			s.mu.Unlock()
		}()
		waited := make(chan int, 1)
		if open != nil && open.Wait != nil {
			// The wait ends with the watch: a watch that ends on the process check or the start limit does not leave
			// it polling until shutdown.
			wctx, stop := context.WithCancel(s.root)
			defer stop()
			go func() {
				code, err := open.Wait(wctx)
				if err != nil {
					code = -1
				}
				waited <- code
			}()
		}
		code, never := -1, false
		for {
			if c, ok := readExitFile(exitFile); ok {
				code = c
				break
			}
			if proc == nil {
				if p, ok := identify(); ok {
					proc = &p
					s.noteConsoleProcess(id, since, p)
				} else if s.now().Sub(opened) >= consoleStartLimit {
					never = true
					break
				}
			}
			if proc != nil && !s.o.Consoles.Running(*proc) {
				code = consoleExitClosed
				if c, ok := readExitFile(exitFile); ok {
					code = c
				}
				break
			}
			timer := s.clk.NewTimer(consolePoll)
			select {
			case <-s.root.Done():
				timer.Stop()
				return // the next daemon watches the console again
			case c := <-waited:
				timer.Stop()
				if s.root.Err() != nil {
					return
				}
				code = c
			case <-timer.C():
				continue
			}
			break
		}
		w.lease.Release()
		if never {
			s.revertTakeover(id, from, since, fmt.Sprintf("the console did not start within %s; no terminal "+
				"window could run it (on macOS, allow Seelegent to control Terminal; on Linux, check $TERMINAL "+
				"or the desktop session); if a console window still opens later, close it before the next followup",
				consoleStartLimit))
			return
		}
		s.handback(id, code)
	})
	if !ok {
		w.lease.Release()
		s.mu.Lock()
		delete(s.watches, id)
		s.mu.Unlock()
	}
}

// releaseConsole gives back the account slot of a console whose task left the takeover (cancel, failure). The
// console may stay open; it no longer counts on the account.
func (s *Service) releaseConsole(id string) {
	s.mu.Lock()
	w := s.watches[id]
	s.mu.Unlock()
	if w != nil {
		w.lease.Release()
	}
}

// consoleOpen reports whether a takeover console of task id is still being watched.
func (s *Service) consoleOpen(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watches[id] != nil
}

// noteConsoleProcess stores the console's process so a restarted daemon can check that it still runs.
func (s *Service) noteConsoleProcess(id string, since time.Time, p ConsoleProcess) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if !t.Manual || t.Console == nil || !t.Console.Since.Equal(since) {
			return nil, errNoChange
		}
		t.Console.Process = &p
		return nil, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: console process", "task", id, "err", err)
	}
}

// handback ends a takeover and starts the hand-back turn on the task's own backend and account.
func (s *Service) handback(id string, code int) {
	start := false
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		start = false
		if !t.Manual || t.Console == nil {
			return nil, errNoChange // cancelled while the console ran
		}
		c := t.Console
		turn := len(t.Turns) + 1
		data := api.TakeoverData{State: "end", AccountID: c.AccountID, DurationMs: s.now().Sub(c.Since).Milliseconds()}
		if code >= 0 {
			data.ExitCode = &code
		}
		t.Manual, t.Console = false, nil
		t.AccountID = c.AccountID
		if t.Status != StatusRunning {
			if err := t.SetStatus(StatusRunning); err != nil {
				return nil, err
			}
		}
		t.Turns = append(t.Turns, &Turn{N: turn, By: ByUser, Message: brief.Handback, Status: TurnRunning,
			Model: t.Model, Tier: t.Tier, Effort: t.Effort, StartedAt: s.now(), Handback: true})
		t.Error, t.Review = "", nil
		start = true
		return []NewEvent{{Type: api.EventTakeover, Turn: turn - 1, Data: data},
			{Type: api.EventLeadMessage, Turn: turn, Data: api.LeadMessageData{Message: brief.Handback, By: ByUser,
				Model: t.Model, Effort: t.Effort, Tier: t.Tier}}}, nil
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.log.Error("task: hand back", "task", id, "err", err)
		}
		return
	}
	if start {
		s.startRun(id)
	}
}

func readExitFile(path string) (int, bool) {
	if path == "" {
		return 0, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return code, true
}
