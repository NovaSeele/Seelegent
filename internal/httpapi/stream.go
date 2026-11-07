package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// serveEvents is GET /api/v1/events (contract §7): retry, hello, a reset or the missed frames, then live frames
// and a ping comment every heartbeat until the client leaves, its queue overflows or the server closes.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	lastID := r.Header.Get("Last-Event-ID")
	if lastID == "" {
		lastID = r.URL.Query().Get("lastEventId")
	}
	sub, ok := s.hub.subscribe(lastID)
	if !ok {
		s.writeError(w, r, translated(api.CodeUnavailable, "shutting_down"))
		return
	}
	c := sub.client
	defer s.hub.unsubscribe(c)
	// An SSE stream has no write deadline: it lives as long as the client.
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	write := func(b []byte) bool {
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	hello, _ := json.Marshal(api.Hello{Instance: s.hub.Instance(), PID: s.o.PID, Build: s.build(),
		Version: s.o.Version, ServerTime: api.FormatTime(s.clock())})
	start := append([]byte("retry: 2000\n"), encode(sub.helloID, string(api.StreamHello), hello)...)
	if sub.reset != "" {
		data, _ := json.Marshal(api.Reset{Reason: sub.reset})
		start = append(start, encode(sub.resetID, string(api.StreamReset), data)...)
	}
	for _, f := range sub.replay {
		start = append(start, f.wire...)
	}
	if !write(start) {
		return
	}

	ping := time.NewTicker(s.o.Heartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-ping.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		case f, open := <-c.ch:
			if !open {
				if c.overflow.Load() {
					data, _ := json.Marshal(api.Reset{Reason: api.ResetOverflow})
					write(encode(s.hub.LastID(), string(api.StreamReset), data))
				}
				return
			}
			c.queued.Add(-int64(len(f.wire)))
			buf := f.wire
			// Send what is already queued in one write.
			for more := true; more; {
				select {
				case g, open := <-c.ch:
					if !open {
						more = false
						break
					}
					c.queued.Add(-int64(len(g.wire)))
					buf = append(buf[:len(buf):len(buf)], g.wire...)
				default:
					more = false
				}
			}
			if !write(buf) {
				return
			}
			if c.overflow.Load() {
				data, _ := json.Marshal(api.Reset{Reason: api.ResetOverflow})
				write(encode(s.hub.LastID(), string(api.StreamReset), data))
				return
			}
		}
	}
}
