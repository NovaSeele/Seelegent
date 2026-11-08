package httpapi

import (
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// fakeNow is the hub's clock in tests: Now, and AfterFunc timers that fire only through fire.
type fakeNow struct {
	mu     sync.Mutex
	t      time.Time
	timers []*fakeAfter
}

// fakeAfter is one AfterFunc timer.
type fakeAfter struct {
	at      time.Time
	f       func()
	stopped bool
	fired   bool
}

func (f *fakeNow) afterFunc(d time.Duration, fn func()) func() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeAfter{at: f.t.Add(d), f: fn}
	f.timers = append(f.timers, t)
	return func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		was := !t.stopped && !t.fired
		t.stopped = true
		return was
	}
}

// armed returns the timers neither stopped nor fired.
func (f *fakeNow) armed() []*fakeAfter {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeAfter
	for _, t := range f.timers {
		if !t.stopped && !t.fired {
			out = append(out, t)
		}
	}
	return out
}

// fire runs the armed timers that are due now.
func (f *fakeNow) fire() {
	f.mu.Lock()
	var due []*fakeAfter
	for _, t := range f.timers {
		if !t.stopped && !t.fired && !t.at.After(f.t) {
			t.fired = true
			due = append(due, t)
		}
	}
	f.mu.Unlock()
	for _, t := range due {
		t.f()
	}
}

func (f *fakeNow) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeNow) add(d time.Duration) {
	f.mu.Lock()
	f.t = f.t.Add(d)
	f.mu.Unlock()
}

func newTestHub(t *testing.T, o HubOptions) (*Hub, *fakeNow) {
	t.Helper()
	clk := &fakeNow{t: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)}
	o.Now, o.AfterFunc, o.Instance = clk.now, clk.afterFunc, "abcd1234"
	h, err := NewHub(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, clk
}

func publishN(t *testing.T, h *Hub, n int) {
	t.Helper()
	for range n {
		if err := h.Publish(api.StreamTaskEvent, map[string]int{"x": 1}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHubIDsAndResume(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{RingFrames: 4})
	if h.LastID() != "abcd1234.0" {
		t.Fatalf("first id %s", h.LastID())
	}
	publishN(t, h, 3)
	if h.LastID() != "abcd1234.3" {
		t.Fatalf("id after 3 = %s", h.LastID())
	}
	sub, ok := h.subscribe("abcd1234.1")
	if !ok || sub.reset != "" || sub.helloID != "abcd1234.1" || len(sub.replay) != 2 || sub.replay[0].seq != 2 {
		t.Fatalf("resume = %+v", sub)
	}
	if !strings.HasPrefix(string(sub.replay[0].wire), "id: abcd1234.2\nevent: task.event\ndata: {\"x\":1}\n\n") {
		t.Fatalf("wire = %q", sub.replay[0].wire)
	}
	h.unsubscribe(sub.client)

	cases := map[string]string{
		"abcd1234.3":  "",               // up to date
		"ffff0000.2":  api.ResetRestart, // another daemon start
		"abcd1234":    api.ResetRestart, // no seq
		"abcd1234.x":  api.ResetRestart,
		"abcd1234.99": api.ResetRestart, // from the future
	}
	for id, want := range cases {
		sub, _ := h.subscribe(id)
		if sub.reset != want || (want == "" && len(sub.replay) != 0) {
			t.Errorf("%s: reset %q replay %d, want %q", id, sub.reset, len(sub.replay), want)
		}
		if want != "" && (sub.helloID != "abcd1234.3" || sub.resetID != "abcd1234.3") {
			t.Errorf("%s: ids %s %s", id, sub.helloID, sub.resetID)
		}
		h.unsubscribe(sub.client)
	}

	// Frames 1..7 published; the ring keeps the last 4 (4..7): from 3 still resumes, from 2 is too old.
	publishN(t, h, 4)
	if sub, _ := h.subscribe("abcd1234.3"); sub.reset != "" || len(sub.replay) != 4 {
		t.Fatalf("edge resume = %q %d", sub.reset, len(sub.replay))
	}
	if sub, _ := h.subscribe("abcd1234.2"); sub.reset != api.ResetTooOld {
		t.Fatalf("too old = %q", sub.reset)
	}
}

func TestHubRingAgeAndBytes(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{RingAge: time.Minute, RingBytes: 1 << 20})
	publishN(t, h, 2)
	clk.add(2 * time.Minute)
	if sub, _ := h.subscribe("abcd1234.1"); sub.reset != api.ResetTooOld {
		t.Fatalf("aged out = %+v", sub)
	}
	h2, _ := newTestHub(t, HubOptions{RingBytes: 100})
	publishN(t, h2, 10) // each frame is about 50 bytes
	h2.mu.Lock()
	n, size := len(h2.kept()), h2.ringBytes
	h2.mu.Unlock()
	if n == 0 || n > 2 || size > 100 {
		t.Fatalf("ring %d frames, %d bytes", n, size)
	}
}

// TestHubRingAgesOutWhenIdle: frames age out without another Publish; the empty ring releases its backing array and
// no timer stays armed.
func TestHubRingAgesOutWhenIdle(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{RingAge: time.Minute})
	publishN(t, h, 2)
	clk.add(30 * time.Second)
	publishN(t, h, 1)
	if n := len(clk.armed()); n != 1 {
		t.Fatalf("%d timers armed after 3 publishes, want 1", n)
	}
	clk.add(30 * time.Second)
	clk.fire() // exactly a minute: not due yet (a frame RingAge old is kept; the timer fires just after)
	clk.add(time.Millisecond)
	clk.fire()
	h.mu.Lock()
	kept := len(h.kept())
	h.mu.Unlock()
	if kept != 1 {
		t.Fatalf("after the first frames aged out: %d kept, want 1", kept)
	}
	armed := clk.armed()
	if len(armed) != 1 || !armed[0].at.Equal(clk.now().Add(30*time.Second)) {
		t.Fatalf("re-armed timers: %+v", armed)
	}
	clk.add(30 * time.Second)
	clk.fire()
	h.mu.Lock()
	ring, size := h.ring, h.ringBytes
	h.mu.Unlock()
	if ring != nil || size != 0 {
		t.Fatalf("aged-out ring: %d frames (cap %d), %d bytes; want released", len(ring), cap(ring), size)
	}
	if n := len(clk.armed()); n != 0 {
		t.Fatalf("%d timers armed on an empty ring", n)
	}
	if sub, _ := h.subscribe("abcd1234.1"); sub.reset != api.ResetTooOld {
		t.Fatalf("resume after the ring emptied = %+v", sub)
	}
	// A new frame arms the timer again.
	publishN(t, h, 1)
	if n := len(clk.armed()); n != 1 {
		t.Fatalf("%d timers armed after a new frame, want 1", n)
	}
}

// TestHubCloseStopsTheAgeTimer: Close cancels the armed timer, and a timer that fires after Close does nothing.
func TestHubCloseStopsTheAgeTimer(t *testing.T) {
	h, clk := newTestHub(t, HubOptions{RingAge: time.Minute})
	publishN(t, h, 1)
	armed := clk.armed()
	if len(armed) != 1 {
		t.Fatalf("%d timers armed, want 1", len(armed))
	}
	h.Close()
	if n := len(clk.armed()); n != 0 {
		t.Fatalf("%d timers armed after Close", n)
	}
	armed[0].f() // a timer already running when Close stopped it
	h.mu.Lock()
	kept := len(h.kept())
	h.mu.Unlock()
	if kept != 1 || len(clk.armed()) != 0 {
		t.Fatalf("after a stale timer: %d kept, %d armed", kept, len(clk.armed()))
	}
}

// TestHubRingStaysBounded: a full ring trims by moving its head and compacts now and then, so its backing array
// stays small however many frames pass, and resumes still replay exactly the missed frames.
func TestHubRingStaysBounded(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{RingFrames: 8})
	publishN(t, h, 1000)
	h.mu.Lock()
	kept, capacity, head := len(h.kept()), cap(h.ring), h.head
	first := h.kept()[0].seq
	h.mu.Unlock()
	if kept != 8 || first != 993 || capacity > 32 || head >= 8 {
		t.Fatalf("ring: %d kept from seq %d, cap %d, head %d", kept, first, capacity, head)
	}
	for _, from := range []uint64{992, 995, 999} {
		sub, ok := h.subscribe(fmt.Sprintf("abcd1234.%d", from))
		if !ok || sub.reset != "" || len(sub.replay) != int(1000-from) || sub.replay[0].seq != from+1 ||
			sub.replay[len(sub.replay)-1].seq != 1000 {
			t.Fatalf("resume from %d = %+v", from, sub)
		}
		h.unsubscribe(sub.client)
	}
	if sub, _ := h.subscribe("abcd1234.991"); sub.reset != api.ResetTooOld {
		t.Fatalf("resume before the ring = %+v", sub)
	}
}

func TestHubOverflowDropsTheClient(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{ClientFrames: 3})
	slow, _ := h.subscribe("")
	publishN(t, h, 5)
	if !slow.client.overflow.Load() {
		t.Fatal("the slow client did not overflow")
	}
	n := 0
	for range slow.client.ch { // closed after the queued frames
		n++
	}
	later, _ := h.subscribe("")
	publishN(t, h, 1)
	if n != 3 || h.Clients() != 1 || len(later.client.ch) != 1 {
		t.Fatalf("slow got %d frames; %d clients left", n, h.Clients())
	}

	byBytes, _ := newTestHub(t, HubOptions{ClientBytes: 120})
	c, _ := byBytes.subscribe("")
	publishN(t, byBytes, 4)
	if !c.client.overflow.Load() {
		t.Fatal("the byte limit did not drop the client")
	}

	h.Close()
	if _, ok := h.subscribe(""); ok {
		t.Fatal("subscribe after Close")
	}
	if err := h.Publish(api.StreamHello, struct{}{}); err != nil {
		t.Fatal(err)
	}
}

type trees struct{ list []*platform.WorkerTree }

func (t trees) WorkerTrees() []*platform.WorkerTree { return t.list }

func TestTreeClassifierWithoutWorkers(t *testing.T) {
	c := NewTreeClassifier(trees{}, nil)
	got := c.Classify(netip.MustParseAddrPort("127.0.0.1:47831"), netip.MustParseAddrPort("127.0.0.1:50000"), time.Now())
	if got.Worker {
		t.Fatalf("no workers, caller = %+v", got)
	}
}

func TestParseTasks(t *testing.T) {
	got := parseTasks("## Phase 1\r\n- [ ] T001 One\r\n  - [x] T002 [P] Two\r\n- [ ] not a task\r\n")
	if len(got) != 2 || got[0].Phase != "Phase 1" || got[0].Title != "One" || !got[1].Done || !got[1].Parallel {
		t.Fatalf("tasks = %+v", got)
	}
}
