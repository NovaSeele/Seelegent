package task

import (
	"sync"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// NoticeKind says what a notice reports.
type NoticeKind string

// Notice kinds.
const (
	// NoticeTask: the task record changed (status, attention, review, ...).
	NoticeTask NoticeKind = "task"
	// NoticeEvent: an event was appended to the task's log (Event).
	NoticeEvent NoticeKind = "event"
)

// Notice is one change the engine publishes.
type Notice struct {
	Kind   NoticeKind
	TaskID string
	// Event is set for NoticeEvent.
	Event *api.TaskEvent
}

// Bus is the one in-process notifier of the engine. Waiters use Changed (no per-wait listeners pile up); streams
// (SSE) subscribe with a bounded buffer and learn when they fell behind. It is safe for concurrent use.
type Bus struct {
	mu      sync.Mutex
	changed chan struct{}
	subs    map[*Subscription]struct{}
}

// NewBus returns a bus.
func NewBus() *Bus {
	return &Bus{changed: make(chan struct{}), subs: map[*Subscription]struct{}{}}
}

// Changed returns a channel that is closed at the next Publish.
func (b *Bus) Changed() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// Publish delivers notices to every subscriber and wakes the waiters. It never blocks: a subscriber whose buffer
// is full is marked as overflowed and gets nothing more until it resubscribes.
func (b *Bus) Publish(ns ...Notice) {
	if len(ns) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for s := range b.subs {
		for _, n := range ns {
			select {
			case s.ch <- n:
			default:
				s.overflow = true
				delete(b.subs, s)
				close(s.ch)
			}
			if s.overflow {
				break
			}
		}
	}
	close(b.changed)
	b.changed = make(chan struct{})
}

// Subscription receives notices.
type Subscription struct {
	b        *Bus
	ch       chan Notice
	overflow bool
}

// Subscribe returns a subscription with room for buffer notices (at least 1).
func (b *Bus) Subscribe(buffer int) *Subscription {
	s := &Subscription{b: b, ch: make(chan Notice, max(buffer, 1))}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// C returns the channel of notices. It is closed when the subscription overflowed or was closed.
func (s *Subscription) C() <-chan Notice { return s.ch }

// Overflowed reports whether notices were dropped because the subscriber fell behind; the subscriber must then
// reload what it shows.
func (s *Subscription) Overflowed() bool {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	return s.overflow
}

// Close ends the subscription. It is safe to call more than once.
func (s *Subscription) Close() {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	if _, ok := s.b.subs[s]; ok {
		delete(s.b.subs, s)
		close(s.ch)
	}
}
