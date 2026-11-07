package automation

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// minTick keeps a due run from spinning the timer.
const minTick = 250 * time.Millisecond

// Start fills in missing next runs and starts the scheduler and the task follower (it tells the stream when an
// automation's task ends).
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()
	now := s.now()
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		recs, err := listRecs(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Enabled && r.NextRun == "" {
				if r.NextRun = nextRun(r, now); r.NextRun != "" {
					if err := saveRec(ctx, tx, r); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.wg.Add(2)
	go func() { defer s.wg.Done(); s.loop() }()
	go func() { defer s.wg.Done(); s.follow() }()
	return nil
}

// Close stops the scheduler and waits for runs that are starting.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// loop waits for the next due run (at most MaxTick) and ticks.
func (s *Service) loop() {
	for {
		delay := s.delay()
		timer := s.o.Clock.NewTimer(delay)
		select {
		case <-s.ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
			continue
		case <-timer.C():
		}
		if err := s.tick(s.ctx); err != nil && s.ctx.Err() == nil {
			s.log.Warn("automation: tick", "err", err)
		}
	}
}

// delay is the time until the earliest enabled next run, between minTick and MaxTick.
func (s *Service) delay() time.Duration {
	d := MaxTick
	var recs []*rec
	if err := s.o.DB.Read(s.ctx, func(tx *sql.Tx) error {
		var err error
		recs, err = listRecs(s.ctx, tx)
		return err
	}); err != nil {
		return d
	}
	now := s.now()
	for _, r := range recs {
		if !r.Enabled || r.NextRun == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, r.NextRun); err == nil {
			d = min(d, max(0, t.Sub(now)))
		}
	}
	return max(d, minTick)
}

// pending is a run the tick decided on.
type pending struct {
	id      string
	trigger string
	reset   *resetCandidate
}

// tick advances due automations (running those within their grace window) and checks the before-reset rule,
// then starts the runs. A run missed by more than graceMinutes is recorded as skipped_late.
func (s *Service) tick(ctx context.Context) error {
	now := s.now()
	master := s.o.MasterOn()
	var runs []pending
	var late []api.AutomationRun
	var lateIDs []string
	var changed []*rec
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		recs, err := listRecs(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if !r.Enabled || r.NextRun == "" {
				continue
			}
			due, err := time.Parse(time.RFC3339Nano, r.NextRun)
			if err != nil || due.After(now) {
				continue
			}
			r.NextRun = nextRun(r, now)
			if err := saveRec(ctx, tx, r); err != nil {
				return err
			}
			changed = append(changed, r)
			if !master {
				continue
			}
			behind := now.Sub(due)
			switch {
			case behind > time.Duration(r.GraceMinutes)*time.Minute:
				late = append(late, api.AutomationRun{Trigger: api.TriggerMissed, Outcome: api.AutoSkippedLate,
					Detail: fmt.Sprintf("missed by %s (grace %d min)", behind.Round(time.Minute), r.GraceMinutes)})
				lateIDs = append(lateIDs, r.ID)
			case behind > MaxTick:
				runs = append(runs, pending{id: r.ID, trigger: api.TriggerMissed})
			default:
				runs = append(runs, pending{id: r.ID, trigger: api.TriggerSchedule})
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, r := range changed {
		s.o.Changed(s.view(ctx, r))
	}
	for i, run := range late {
		if _, err := s.record(ctx, lateIDs[i], run); err != nil {
			s.log.Warn("automation: record", "automation", lateIDs[i], "err", err)
		}
	}
	if master {
		runs = append(runs, s.beforeReset(ctx, now, runs)...)
	}
	for _, p := range runs {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runOne(p)
		}()
	}
	return nil
}

// beforeReset finds the automations with quota.useBeforeReset whose backend has a weekly bucket about to reset
// with quota left, and that did not run for that bucket yet.
func (s *Service) beforeReset(ctx context.Context, now time.Time, scheduled []pending) []pending {
	var recs []*rec
	if err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		recs, err = listRecs(ctx, tx)
		return err
	}); err != nil {
		return nil
	}
	var out []pending
	for _, r := range recs {
		if !r.Enabled || !r.Quota.UseBeforeReset || slices.ContainsFunc(scheduled, func(p pending) bool {
			return p.id == r.ID
		}) {
			continue
		}
		if busy, _ := s.running(ctx, r); busy {
			continue
		}
		_, rows := s.quotaRows(ctx, r)
		c := beforeResetCandidate(rows, now)
		if c == nil || slices.Contains(r.BeforeResetFired, c.key) {
			continue
		}
		if t := r.BeforeResetTried; t != nil && t.Key == c.key && now.Sub(t.At) < beforeResetRetry {
			continue
		}
		out = append(out, pending{id: r.ID, trigger: api.TriggerBeforeReset, reset: c})
	}
	return out
}

// runOne fires a run; a before-reset run marks its bucket used only when the task actually started (old bug D9:
// the bucket was marked before, so a skipped run used it up), else it waits a while before trying again.
func (s *Service) runOne(p pending) {
	run, err := s.fire(s.ctx, p.id, p.trigger, p.reset)
	if err != nil {
		if s.ctx.Err() == nil {
			s.log.Warn("automation: run", "automation", p.id, "err", err)
		}
		return
	}
	if p.reset == nil {
		return
	}
	now := s.now()
	_, err = s.update(context.WithoutCancel(s.ctx), p.id, func(r *rec) error {
		if run.Outcome == api.AutoStarted {
			r.BeforeResetFired = append(r.BeforeResetFired, p.reset.key)
			if len(r.BeforeResetFired) > 10 {
				r.BeforeResetFired = r.BeforeResetFired[len(r.BeforeResetFired)-10:]
			}
			r.BeforeResetTried = nil
			return nil
		}
		r.BeforeResetTried = &resetTried{Key: p.reset.key, At: now}
		return nil
	})
	if err != nil && s.ctx.Err() == nil {
		s.log.Warn("automation: record before-reset", "automation", p.id, "err", err)
	}
}

// follow tells the stream when the task of an automation changes (running turns false when it ends).
func (s *Service) follow() {
	bus := s.o.Tasks.Bus()
	sub := bus.Subscribe(64)
	defer func() { sub.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case n, open := <-sub.C():
			if !open {
				if s.ctx.Err() != nil {
					return
				}
				sub = bus.Subscribe(64)
				continue
			}
			if n.Kind != task.NoticeTask {
				continue
			}
			sum, err := s.o.Tasks.Summary(s.ctx, n.TaskID)
			if err != nil || sum.AutomationID == "" {
				continue
			}
			r, err := s.get(s.ctx, sum.AutomationID)
			if err != nil || r.ActiveTaskID != sum.ID {
				continue
			}
			s.o.Changed(s.view(s.ctx, r))
		}
	}
}
