package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Hub limits (contract §7). A resume from before the ring gets a too_old reset and the client reloads its state, so
// the ring only has to cover short disconnects: 2 MiB is plenty, and it is what an idle daemon may hold until the
// frames age out.
const (
	ringFrames   = 2048
	ringBytes    = 2 << 20
	ringAge      = 10 * time.Minute
	clientFrames = 512
	clientBytes  = 4 << 20
)

// HubOptions configure a Hub; zero values take the contract's limits.
type HubOptions struct {
	// Instance is the id prefix of this daemon start (8 random hex digits when empty).
	Instance string
	Now      func() time.Time
	// RingFrames, RingBytes and RingAge bound the replay ring; ClientFrames and ClientBytes each client's queue.
	RingFrames   int
	RingBytes    int
	RingAge      time.Duration
	ClientFrames int
	ClientBytes  int
	// AfterFunc runs f once after d and returns a function that cancels it (time.AfterFunc when nil). The ring's
	// age limit uses it, so frames expire on an idle daemon too; tests fake it together with Now.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)
}

// frame is one SSE frame, encoded once.
type frame struct {
	seq  uint64
	at   time.Time
	wire []byte
}

// Hub is the SSE fan-out: every frame gets the id <instance>.<seq>, is kept in a bounded ring for resumes and is
// queued to every client without blocking (a client whose queue overflows is dropped with a reset).
type Hub struct {
	o        HubOptions
	instance string

	mu  sync.Mutex
	seq uint64
	// ring[head:] are the kept frames, oldest first. Trimming moves head; the slice is compacted only when more
	// than half of it is dropped, so a full ring costs O(1) per Publish (amortized), not a copy of the ring.
	ring      []frame
	head      int
	ringBytes int
	clients   map[*hubClient]struct{}
	closed    bool
	// stopExpiry cancels the armed age timer (nil when none is armed): one timer for the whole ring, armed while it
	// keeps frames, for when the oldest one ages out. expiryGen tells a stale timer from the armed one.
	stopExpiry func() bool
	expiryGen  uint64
}

// hubClient is one connected stream.
type hubClient struct {
	ch       chan frame
	queued   atomic.Int64
	overflow atomic.Bool
}

// NewHub returns a hub.
func NewHub(o HubOptions) (*Hub, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.RingFrames <= 0 {
		o.RingFrames = ringFrames
	}
	if o.RingBytes <= 0 {
		o.RingBytes = ringBytes
	}
	if o.RingAge <= 0 {
		o.RingAge = ringAge
	}
	if o.ClientFrames <= 0 {
		o.ClientFrames = clientFrames
	}
	if o.ClientBytes <= 0 {
		o.ClientBytes = clientBytes
	}
	if o.AfterFunc == nil {
		o.AfterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	}
	if o.Instance == "" {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("sse instance id: %w", err)
		}
		o.Instance = hex.EncodeToString(b[:])
	}
	return &Hub{o: o, instance: o.Instance, clients: map[*hubClient]struct{}{}}, nil
}

// Instance returns the id prefix of this daemon start.
func (h *Hub) Instance() string { return h.instance }

// LastID returns the id of the last frame (seq 0 before the first one).
func (h *Hub) LastID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.idLocked(h.seq)
}

func (h *Hub) idLocked(seq uint64) string { return h.instance + "." + strconv.FormatUint(seq, 10) }

// encode builds the wire form of a frame.
func encode(id, event string, data []byte) []byte {
	var b bytes.Buffer
	b.Grow(len(id) + len(event) + len(data) + 24)
	b.WriteString("id: ")
	b.WriteString(id)
	b.WriteString("\nevent: ")
	b.WriteString(event)
	b.WriteString("\ndata: ")
	b.Write(data) // encoding/json never writes a raw newline
	b.WriteString("\n\n")
	return b.Bytes()
}

// Publish sends one event to every client and keeps it for resumes. A value that cannot be marshaled is a bug
// and is dropped with an error.
func (h *Hub) Publish(event api.StreamEvent, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("sse %s: %w", event, err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.seq++
	f := frame{seq: h.seq, at: h.o.Now(), wire: encode(h.idLocked(h.seq), string(event), data)}
	h.ring = append(h.ring, f)
	h.ringBytes += len(f.wire)
	h.trimLocked(f.at)
	h.armLocked(f.at)
	for c := range h.clients {
		h.sendLocked(c, f)
	}
	return nil
}

// kept returns the frames in the ring, oldest first. The slice aliases the ring: copy what outlives the lock.
func (h *Hub) kept() []frame { return h.ring[h.head:] }

// trimLocked drops frames over the ring's limits.
func (h *Hub) trimLocked(now time.Time) {
	for h.head < len(h.ring) {
		f := &h.ring[h.head]
		if len(h.ring)-h.head <= h.o.RingFrames && h.ringBytes <= h.o.RingBytes && now.Sub(f.at) <= h.o.RingAge {
			break
		}
		h.ringBytes -= len(f.wire)
		*f = frame{} // let the encoded frame go
		h.head++
	}
	if h.head == len(h.ring) {
		// Empty: release the backing array (a full ring's can be large) and the age timer.
		h.ring, h.head, h.ringBytes = nil, 0, 0
		h.disarmLocked()
		return
	}
	if h.head > 0 && h.head*2 >= len(h.ring) {
		n := copy(h.ring, h.ring[h.head:])
		clear(h.ring[n:])
		h.ring, h.head = h.ring[:n], 0
	}
}

// armLocked arms the age timer for when the oldest kept frame ages out, unless one is armed already or the ring is
// empty. An armed timer is never moved: when it fires early (the oldest frame was trimmed for the frame or byte
// limit since), expire trims nothing and arms it again for the new oldest frame.
func (h *Hub) armLocked(now time.Time) {
	if h.closed || h.stopExpiry != nil || h.head == len(h.ring) {
		return
	}
	// trimLocked keeps a frame exactly RingAge old: fire just after.
	d := max(h.ring[h.head].at.Add(h.o.RingAge).Sub(now), 0) + time.Millisecond
	h.expiryGen++
	gen := h.expiryGen
	h.stopExpiry = h.o.AfterFunc(d, func() { h.expire(gen) })
}

// disarmLocked cancels the age timer.
func (h *Hub) disarmLocked() {
	if h.stopExpiry != nil {
		h.stopExpiry()
		h.stopExpiry = nil
	}
	h.expiryGen++
}

// expire is the age timer: it drops the frames that aged out and arms the timer again while frames are kept.
func (h *Hub) expire(gen uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if gen != h.expiryGen || h.closed {
		return // cancelled after it had started
	}
	h.stopExpiry = nil
	now := h.o.Now()
	h.trimLocked(now)
	h.armLocked(now)
}

// sendLocked queues f to c, or drops c when its queue is full.
func (h *Hub) sendLocked(c *hubClient, f frame) {
	if int(c.queued.Load())+len(f.wire) > h.o.ClientBytes {
		h.dropLocked(c)
		return
	}
	select {
	case c.ch <- f:
		c.queued.Add(int64(len(f.wire)))
	default:
		h.dropLocked(c)
	}
}

func (h *Hub) dropLocked(c *hubClient) {
	c.overflow.Store(true)
	delete(h.clients, c)
	close(c.ch)
}

// subscription is what a new stream starts with.
type subscription struct {
	client *hubClient
	// helloID is the id the hello frame carries: the resume id when frames are replayed, the current id otherwise.
	helloID string
	// reset is the reason of a reset frame to send after hello ("" for none); resetID its id.
	reset, resetID string
	replay         []frame
}

// subscribe registers a client. lastID is the client's Last-Event-ID ("" for a fresh start): frames after it are
// replayed when it belongs to this daemon start and is still in the ring, else the client gets a reset.
func (h *Hub) subscribe(lastID string) (*subscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	h.trimLocked(h.o.Now())
	s := &subscription{client: &hubClient{ch: make(chan frame, h.o.ClientFrames)}, helloID: h.idLocked(h.seq)}
	if lastID != "" {
		inst, seqText, ok := strings.Cut(lastID, ".")
		seq, err := strconv.ParseUint(seqText, 10, 64)
		switch {
		case !ok || err != nil || inst != h.instance || seq > h.seq:
			s.reset = api.ResetRestart
		case seq == h.seq:
		case len(h.kept()) == 0 || seq+1 < h.kept()[0].seq:
			s.reset = api.ResetTooOld
		default:
			// Seqs in the ring are consecutive, so the first missed frame is at a known index. The replay is a copy:
			// the ring is compacted in place after the lock is released.
			s.helloID = lastID
			kept := h.kept()
			s.replay = append([]frame(nil), kept[seq+1-kept[0].seq:]...)
		}
		if s.reset != "" {
			s.resetID = h.idLocked(h.seq)
		}
	}
	h.clients[s.client] = struct{}{}
	return s, true
}

// unsubscribe removes a client that went away.
func (h *Hub) unsubscribe(c *hubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.ch)
	}
}

// Clients returns the number of connected streams.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Close ends every stream; later publishes are dropped.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.disarmLocked()
	for c := range h.clients {
		delete(h.clients, c)
		close(c.ch)
	}
}
