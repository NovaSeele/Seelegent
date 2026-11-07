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

// Hub limits (contract §7).
const (
	ringFrames   = 2048
	ringBytes    = 8 << 20
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

	mu        sync.Mutex
	seq       uint64
	ring      []frame
	ringBytes int
	clients   map[*hubClient]struct{}
	closed    bool
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
	for c := range h.clients {
		h.sendLocked(c, f)
	}
	return nil
}

// trimLocked drops frames over the ring's limits.
func (h *Hub) trimLocked(now time.Time) {
	drop := 0
	for drop < len(h.ring) {
		f := h.ring[drop]
		if len(h.ring)-drop > h.o.RingFrames || h.ringBytes > h.o.RingBytes || now.Sub(f.at) > h.o.RingAge {
			h.ringBytes -= len(f.wire)
			drop++
			continue
		}
		break
	}
	if drop > 0 {
		h.ring = append(h.ring[:0:0], h.ring[drop:]...)
	}
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
		case len(h.ring) == 0 || seq+1 < h.ring[0].seq:
			s.reset = api.ResetTooOld
		default:
			s.helloID = lastID
			for _, f := range h.ring {
				if f.seq > seq {
					s.replay = append(s.replay, f)
				}
			}
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
	for c := range h.clients {
		delete(h.clients, c)
		close(c.ch)
	}
}
