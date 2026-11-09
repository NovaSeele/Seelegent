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
	// coalesceLive is how long a task's live notices (its worker's active step changed: task.NoticeLive) are merged
	// into one task.updated frame. The step label needs no 100 ms freshness, and a worker that streams tools and
	// messages changes it many times a second: each frame reads the task's summary from the database.
	coalesceLive = time.Second
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
	// pending are the tasks whose record changed (sent within coalesceTask); live those whose worker's active step
	// changed only (sent within coalesceLive). A task in pending is dropped from live when its frame goes out.
	pending, live := map[string]bool{}, map[string]bool{}
	taskTimer, liveTimer := newFeedTimer(coalesceTask), newFeedTimer(coalesceLive)
	defer taskTimer.stop()
	defer liveTimer.stop()
	send := func(ids map[string]bool) {
		for id := range ids {
			delete(ids, id)
			delete(live, id)
			sum, err := s.o.Tasks.Summary(ctx, id)
			if err != nil {
				if !errors.Is(err, task.ErrNotFound) && ctx.Err() == nil {
					s.log.Warn("sse: task summary", "task", id, "err", err)
				}
				continue
			}
			if !task.Hidden(sum.Kind) { // hidden runs are read through what started them
				s.publish(api.StreamTaskUpdated, sum)
			}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-taskTimer.c():
			taskTimer.fired()
			send(pending)
		case <-liveTimer.c():
			liveTimer.fired()
			send(live)
		case n, open := <-sub.C():
			if !open {
				if ctx.Err() != nil {
					return
				}
				s.log.Warn("sse: the task feed fell behind; clients reload")
				sub = bus.Subscribe(taskBuffer)
				clear(pending)
				clear(live)
				s.publish(api.StreamReset, api.Reset{Reason: api.ResetOverflow})
				continue
			}
			switch {
			case n.Kind == task.NoticeEvent && n.Event != nil:
				s.publish(api.StreamTaskEvent, n.Event)
			case n.Kind == task.NoticeTask && n.Created:
				delete(pending, n.TaskID)
				delete(live, n.TaskID)
				if sum, err := s.o.Tasks.Summary(ctx, n.TaskID); err == nil && !task.Hidden(sum.Kind) {
					s.publish(api.StreamTaskCreated, sum)
				}
			case n.Kind == task.NoticeTask:
				pending[n.TaskID] = true
				taskTimer.arm()
			case n.Kind == task.NoticeLive && !pending[n.TaskID]:
				live[n.TaskID] = true
				liveTimer.arm()
			}
		}
	}
}

// feedTimer is a coalescing timer: arm starts it unless it already runs; c is nil while it does not run.
type feedTimer struct {
	d     time.Duration
	timer *time.Timer
	fire  <-chan time.Time
}

func newFeedTimer(d time.Duration) *feedTimer { return &feedTimer{d: d} }

func (t *feedTimer) c() <-chan time.Time { return t.fire }

func (t *feedTimer) arm() {
	if t.fire != nil {
		return
	}
	if t.timer == nil {
		t.timer = time.NewTimer(t.d)
	} else {
		t.timer.Reset(t.d)
	}
	t.fire = t.timer.C
}

func (t *feedTimer) fired() { t.fire = nil }

func (t *feedTimer) stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

// accountCache is what the clients were last told about each account, so account.updated goes out only on a
// change and account.deleted for every removal, whether the feed or a handler sees it first.
type accountCache struct {
	mu   sync.Mutex
	last map[string]sentAccount
}

// sentAccount is the JSON last sent for an account and the provider it used then.
type sentAccount struct {
	data     []byte
	provider string
}

// providerMoved reports whether an account that used provider was (seen before) now uses another one, counting
// added and removed accounts: Provider.accounts changed, so clients must hear providers.changed.
func providerMoved(old sentAccount, seen bool, provider string) bool {
	if !seen {
		return provider != ""
	}
	return old.provider != provider
}

// accountSent publishes account.updated when a differs from what was last sent, and providers.changed when the
// account is new to a provider or moved to another one. Handlers call it with the account they answer with, so a
// change is never lost between two feed snapshots.
func (s *Server) accountSent(a *api.Account) {
	data, err := json.Marshal(a)
	if err != nil {
		return
	}
	s.acc.mu.Lock()
	defer s.acc.mu.Unlock()
	old, seen := s.acc.last[a.ID]
	if seen && bytes.Equal(old.data, data) {
		return
	}
	s.acc.last[a.ID] = sentAccount{data: data, provider: a.Provider}
	s.publish(api.StreamAccountUpdated, a)
	if providerMoved(old, seen, a.Provider) {
		s.changed(api.StreamProvidersChanged)
	}
}

// accountGone publishes account.deleted, and providers.changed when the account used a provider (provider is what
// the caller knew of it, for an account the clients were never told about).
func (s *Server) accountGone(id, provider string) {
	s.acc.mu.Lock()
	defer s.acc.mu.Unlock()
	if old, ok := s.acc.last[id]; ok && old.provider != "" {
		provider = old.provider
	}
	delete(s.acc.last, id)
	s.publish(api.StreamAccountDeleted, api.Deleted{ID: id})
	if provider != "" {
		s.changed(api.StreamProvidersChanged)
	}
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
	providers := false // an account was added to, removed from or moved between providers
	for i := range accounts {
		a := &accounts[i]
		seen[a.ID] = true
		data, err := json.Marshal(a)
		if err != nil {
			continue
		}
		old, ok := s.acc.last[a.ID]
		if ok && bytes.Equal(old.data, data) {
			continue
		}
		s.acc.last[a.ID] = sentAccount{data: data, provider: a.Provider}
		providers = providers || providerMoved(old, ok, a.Provider)
		if publish {
			s.publish(api.StreamAccountUpdated, a)
		}
	}
	for id, old := range s.acc.last {
		if !seen[id] {
			delete(s.acc.last, id)
			providers = providers || old.provider != ""
			if publish {
				s.publish(api.StreamAccountDeleted, api.Deleted{ID: id})
			}
		}
	}
	if publish && providers {
		s.changed(api.StreamProvidersChanged)
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
