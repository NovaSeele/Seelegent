package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Feed timings.
const (
	// coalesceTask is how long task.updated frames of one task are merged (contract §7).
	coalesceTask = 100 * time.Millisecond
	// coalesceAccounts merges bursts of account changes into one diff.
	coalesceAccounts = 500 * time.Millisecond
	// taskBuffer is the room of the task bus subscription.
	taskBuffer = 4096
)

// startFeeds starts the goroutines that turn engine and pool changes into SSE frames.
func (s *Server) startFeeds() {
	s.background(s.taskFeed)
	s.background(s.accountFeed)
}

// publish sends an SSE frame; a marshal error is a bug and is logged.
func (s *Server) publish(event api.StreamEvent, v any) {
	if err := s.hub.Publish(event, v); err != nil {
		s.log.Error("sse: publish", "err", err)
	}
}

// changed publishes providers.changed, roles.changed or members.changed.
func (s *Server) changed(event api.StreamEvent) {
	s.publish(event, api.Changed{ChangedAt: api.FormatTime(s.clock())})
}

// PublishLogin sends an account.login frame (the sign-in service's Publish) and refreshes the account.
func (s *Server) PublishLogin(l api.LoginSession) {
	s.publish(api.StreamAccountLogin, l)
	s.kickAccounts()
}

// kickAccounts asks the account feed to look for changes now.
func (s *Server) kickAccounts() {
	select {
	case s.accountsKick <- struct{}{}:
	default:
	}
}

// taskFeed turns task bus notices into task.created, task.updated (coalesced per task) and task.event frames
// (never coalesced). When the subscription overflows, every client gets a reset and the feed resubscribes.
func (s *Server) taskFeed(ctx context.Context) {
	bus := s.o.Tasks.Bus()
	sub := bus.Subscribe(taskBuffer)
	defer func() { sub.Close() }()
	pending := map[string]bool{}
	var timer *time.Timer
	var fire <-chan time.Time
	flush := func() {
		for id := range pending {
			delete(pending, id)
			sum, err := s.o.Tasks.Summary(ctx, id)
			if err != nil {
				if !errors.Is(err, task.ErrNotFound) && ctx.Err() == nil {
					s.log.Warn("sse: task summary", "task", id, "err", err)
				}
				continue
			}
			s.publish(api.StreamTaskUpdated, sum)
		}
		fire = nil
	}
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-fire:
			flush()
		case n, open := <-sub.C():
			if !open {
				if ctx.Err() != nil {
					return
				}
				s.log.Warn("sse: the task feed fell behind; clients reload")
				sub = bus.Subscribe(taskBuffer)
				clear(pending)
				s.publish(api.StreamReset, api.Reset{Reason: api.ResetOverflow})
				continue
			}
			switch {
			case n.Kind == task.NoticeEvent && n.Event != nil:
				s.publish(api.StreamTaskEvent, n.Event)
			case n.Kind == task.NoticeTask && n.Created:
				delete(pending, n.TaskID)
				if sum, err := s.o.Tasks.Summary(ctx, n.TaskID); err == nil {
					s.publish(api.StreamTaskCreated, sum)
				}
			case n.Kind == task.NoticeTask:
				pending[n.TaskID] = true
				if fire == nil {
					if timer == nil {
						timer = time.NewTimer(coalesceTask)
					} else {
						timer.Reset(coalesceTask)
					}
					fire = timer.C
				}
			}
		}
	}
}

// accountCache is what the clients were last told about each account, so account.updated goes out only on a
// change and account.deleted for every removal, whether the feed or a handler sees it first.
type accountCache struct {
	mu   sync.Mutex
	last map[string][]byte
}

// accountSent publishes account.updated when a differs from what was last sent. Handlers call it with the
// account they answer with, so a change is never lost between two feed snapshots.
func (s *Server) accountSent(a *api.Account) {
	data, err := json.Marshal(a)
	if err != nil {
		return
	}
	s.acc.mu.Lock()
	defer s.acc.mu.Unlock()
	if old, ok := s.acc.last[a.ID]; ok && bytes.Equal(old, data) {
		return
	}
	s.acc.last[a.ID] = data
	s.publish(api.StreamAccountUpdated, a)
}

// accountGone publishes account.deleted.
func (s *Server) accountGone(id string) {
	s.acc.mu.Lock()
	defer s.acc.mu.Unlock()
	delete(s.acc.last, id)
	s.publish(api.StreamAccountDeleted, api.Deleted{ID: id})
}

// syncAccounts compares every account with what was last sent; publish false only records (at start).
func (s *Server) syncAccounts(ctx context.Context, publish bool) {
	accounts, err := s.accounts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("sse: accounts", "err", err)
		}
		return
	}
	s.acc.mu.Lock()
	defer s.acc.mu.Unlock()
	seen := make(map[string]bool, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		seen[a.ID] = true
		data, err := json.Marshal(a)
		if err != nil {
			continue
		}
		if old, ok := s.acc.last[a.ID]; ok && bytes.Equal(old, data) {
			continue
		}
		s.acc.last[a.ID] = data
		if publish {
			s.publish(api.StreamAccountUpdated, a)
		}
	}
	for id := range s.acc.last {
		if !seen[id] {
			delete(s.acc.last, id)
			if publish {
				s.publish(api.StreamAccountDeleted, api.Deleted{ID: id})
			}
		}
	}
}

// accountFeed sends account.updated for every account whose DTO changed and account.deleted for removed ones. It
// looks after pool notices (accounts, quota, cooldowns, released turns), task changes (turns starting), sign-in
// progress and every AccountsPoll, merging bursts.
func (s *Server) accountFeed(ctx context.Context) {
	s.syncAccounts(ctx, false)
	poll := time.NewTicker(s.o.AccountsPoll)
	defer poll.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	armed := false
	arm := func() {
		if !armed {
			armed = true
			debounce.Reset(coalesceAccounts)
		}
	}
	for {
		poolCh := s.o.Pool.Changed()
		taskCh := s.o.Tasks.Bus().Changed()
		select {
		case <-ctx.Done():
			debounce.Stop()
			return
		case <-poolCh:
			arm()
		case <-taskCh:
			arm()
		case <-s.accountsKick:
			arm()
		case <-poll.C:
			arm()
		case <-debounce.C:
			armed = false
			s.syncAccounts(ctx, true)
		}
	}
}
