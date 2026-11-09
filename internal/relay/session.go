package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

// session copies messages between the host and the daemon's session and rides out daemon restarts.
//
// One goroutine (run) owns every field; the host reader, the daemon readers and the reconnects hand it events.
//
// What it keeps: the host's initialize request, its notifications/initialized and its last logging/setLevel
// (replayed to every new session, the replayed requests under ids of the relay whose answers are swallowed), and
// every host request that has no answer yet (pending). An answer reaches the host only for a pending request, and
// the request leaves pending with it, so no id is ever answered twice.
//
// When the connection drops: a pending call that was sent and changes something (mcpwire.Mutating, as the worker
// guard classifies it) is answered at once with a tool error saying the daemon went away during the call; the
// others are sent again on the next session. While calls are pending the relay reconnects with backoff for Grace,
// starting the daemon like the client does, except after the daemon said it was stopping (mcpwire.StoppingMethod):
// then it only looks for one that comes back, as a wait does after 503 daemon_stopping. When Grace runs out, every
// pending request is answered with an error; the next host request starts a new attempt. With nothing pending the
// relay stays disconnected until the host sends something.
type session struct {
	o *Options
	d *discovery

	conn *daemonConn
	gen  int
	// daemonCh gets the lines and the end of the current connection's reader (tagged with its gen).
	daemonCh chan daemonEvent
	dialCh   chan dialResult
	done     chan struct{}

	pending []*call
	byKey   map[string]*call
	// swallow are the ids of the replayed requests, whose answers the host never sees.
	swallow map[string]bool
	nextID  int

	initLine     []byte
	initKey      string
	initAnswered bool
	initialized  []byte
	setLevel     []byte
	// queued are host notifications that wait for a connection.
	queued [][]byte

	stopping     bool // the daemon of the current connection said it shuts down
	reconnecting bool
}

// call is a host request without an answer yet.
type call struct {
	key      string
	id       json.RawMessage
	line     []byte
	method   string
	tool     string
	readOnly bool
	sent     bool
}

type daemonEvent struct {
	gen  int
	line []byte
	err  error
}

type dialResult struct {
	conn *daemonConn
	err  error
}

// message is the part of a JSON-RPC message the relay looks at.
type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func newSession(o *Options, d *discovery, c *daemonConn) *session {
	return &session{o: o, d: d, daemonCh: make(chan daemonEvent), dialCh: make(chan dialResult, 1),
		done: make(chan struct{}), byKey: map[string]*call{}, swallow: map[string]bool{}, conn: c}
}

// run relays until the host closes stdin or ctx ends; it returns the exit code.
func (s *session) run(ctx context.Context) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer close(s.done)
	hostCh := make(chan []byte)
	hostErr := make(chan error, 1)
	go s.readHost(hostCh, hostErr)
	s.attach(s.conn)
	for {
		select {
		case <-ctx.Done():
			s.closeConn()
			return 0
		case line, ok := <-hostCh:
			if !ok {
				s.closeConn()
				if err := <-hostErr; err != nil && !errors.Is(err, io.EOF) {
					s.logf("reading the host: %v", err)
					return 1
				}
				return 0
			}
			s.fromHost(ctx, line)
		case ev := <-s.daemonCh:
			if ev.gen != s.gen {
				continue
			}
			if ev.err != nil {
				s.drop(ctx, ev.err)
				continue
			}
			s.fromDaemon(ev.line)
		case r := <-s.dialCh:
			s.dialed(ctx, r)
		}
	}
}

// readHost reads the host's lines until EOF.
func (s *session) readHost(out chan<- []byte, errc chan<- error) {
	defer close(out)
	br := bufio.NewReaderSize(s.o.Stdin, 64<<10)
	for {
		line, err := readLine(br, maxLine)
		if err != nil {
			errc <- err
			return
		}
		select {
		case out <- line:
		case <-s.done:
			errc <- nil
			return
		}
	}
}

// attach makes c the current connection and starts its reader.
func (s *session) attach(c *daemonConn) {
	s.gen++
	s.conn = c
	gen := s.gen
	go func() {
		for {
			line, err := c.readLine(maxLine)
			if err != nil && len(line) == 0 {
				select {
				case s.daemonCh <- daemonEvent{gen: gen, err: err}:
				case <-s.done:
				}
				return
			}
			select {
			case s.daemonCh <- daemonEvent{gen: gen, line: line}:
			case <-s.done:
				return
			}
		}
	}()
}

func (s *session) closeConn() {
	if s.conn != nil {
		s.conn.close()
		s.conn = nil
		s.gen++
	}
}

// fromHost handles one line of the host.
func (s *session) fromHost(ctx context.Context, line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var m message
	if line[0] != '{' || json.Unmarshal(line, &m) != nil {
		s.toDaemonOrQueue(ctx, line) // a batch or something the daemon answers with a parse error
		return
	}
	hasID := len(m.ID) > 0 && !bytes.Equal(m.ID, []byte("null"))
	switch {
	case m.Method != "" && hasID:
		c := &call{key: idKey(m.ID), id: m.ID, line: line, method: m.Method, readOnly: true}
		switch m.Method {
		case "tools/call":
			c.tool, c.readOnly = toolCall(m.Params)
		case "initialize":
			s.initLine, s.initKey, s.initAnswered = line, c.key, false
		case "logging/setLevel":
			s.setLevel = line
		}
		if old := s.byKey[c.key]; old != nil {
			s.remove(old) // a reused id: the newer request is the one the host waits for
		}
		s.pending = append(s.pending, c)
		s.byKey[c.key] = c
		if s.conn != nil {
			s.send(ctx, c)
		} else {
			s.reconnect(ctx, s.o.Start != nil)
		}
	case m.Method == "notifications/initialized":
		s.initialized = line
		s.toDaemonOrQueue(ctx, line)
	case m.Method == "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		c := s.byKey[idKey(p.RequestID)]
		if c == nil {
			return
		}
		s.remove(c) // the host expects no answer; one that comes anyway is dropped
		if c.sent && s.conn != nil {
			s.write(ctx, line)
		}
	case m.Method != "":
		s.toDaemonOrQueue(ctx, line)
	default:
		// The host's answer to a request of the server: only the session that asked can take it.
		if s.conn != nil {
			s.write(ctx, line)
		}
	}
}

// toolCall returns the tool's name and whether the call only reads.
func toolCall(params json.RawMessage) (string, bool) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Action any `json:"action"`
		} `json:"arguments"`
	}
	if json.Unmarshal(params, &p) != nil {
		return "", true // the server refuses it as invalid params
	}
	action, _ := p.Arguments.Action.(string)
	return p.Name, !mcpwire.Mutating(p.Name, action)
}

func (s *session) toDaemonOrQueue(ctx context.Context, line []byte) {
	if s.conn != nil {
		s.write(ctx, line)
		return
	}
	s.queued = append(s.queued, line)
}

// send writes a pending call.
func (s *session) send(ctx context.Context, c *call) {
	c.sent = true
	s.write(ctx, c.line)
}

// write sends one line to the daemon; a failed write drops the connection.
func (s *session) write(ctx context.Context, line []byte) {
	if s.conn == nil {
		return
	}
	if err := s.conn.write(line); err != nil {
		s.drop(ctx, err)
	}
}

// fromDaemon handles one line of the daemon.
func (s *session) fromDaemon(line []byte) {
	var m message
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &m) != nil {
		s.toHost(line)
		return
	}
	if m.Method == mcpwire.StoppingMethod {
		s.stopping = true
		return
	}
	if m.Method != "" {
		s.toHost(line) // a notification or a request of the server
		return
	}
	key := idKey(m.ID)
	if s.swallow[key] {
		delete(s.swallow, key)
		return
	}
	c := s.byKey[key]
	if c == nil {
		return // answered already, or cancelled by the host
	}
	s.remove(c)
	if c.key == s.initKey && c.method == "initialize" {
		s.initAnswered = true
	}
	s.toHost(line)
}

// remove takes c out of pending.
func (s *session) remove(c *call) {
	delete(s.byKey, c.key)
	for i, p := range s.pending {
		if p == c {
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			return
		}
	}
}

// drop handles a lost connection.
func (s *session) drop(ctx context.Context, why error) {
	if s.conn == nil {
		return
	}
	stopping := s.stopping
	s.stopping = false
	s.closeConn()
	for _, c := range append([]*call(nil), s.pending...) {
		switch {
		case c.sent && !c.readOnly:
			s.remove(c)
			s.answerTool(c, "daemon_restarted", "the connection to the Seelegent daemon was lost while this call "+
				"ran (the daemon restarted or stopped); it may already have taken effect: check task or status "+
				"before calling it again")
		default:
			c.sent = false
		}
	}
	s.swallow = map[string]bool{}
	if len(s.pending) == 0 {
		s.logf("the connection to the daemon was lost (%v); it is opened again on the next request", why)
		return
	}
	s.logf("the connection to the daemon was lost (%v); reconnecting", why)
	s.reconnect(ctx, s.o.Start != nil && !stopping)
}

// reconnect looks for the daemon (and starts it when start is true) with backoff for Grace, unless an attempt
// runs already.
func (s *session) reconnect(ctx context.Context, start bool) {
	if s.reconnecting {
		return
	}
	s.reconnecting = true
	go func() {
		end := time.Now().Add(s.o.Grace)
		pause := s.o.PollInterval
		var last error
		for {
			actx, cancel := context.WithDeadline(ctx, end)
			c, err := s.d.connect(actx, start)
			cancel()
			if err == nil {
				s.dialCh <- dialResult{conn: c}
				return
			}
			last = err
			if ctx.Err() != nil || !time.Now().Add(pause).Before(end) {
				s.dialCh <- dialResult{err: last}
				return
			}
			select {
			case <-ctx.Done():
			case <-time.After(pause):
			}
			pause = min(pause*2, s.o.MaxBackoff)
		}
	}()
}

// dialed handles the end of a reconnect.
func (s *session) dialed(ctx context.Context, r dialResult) {
	s.reconnecting = false
	if r.err != nil {
		s.logf("no daemon came back within %s (%v); the open calls get an error", s.o.Grace, r.err)
		for _, c := range append([]*call(nil), s.pending...) {
			s.remove(c)
			msg := fmt.Sprintf("the Seelegent daemon cannot be reached (%v); try again shortly", r.err)
			if c.method == "tools/call" {
				s.answerTool(c, "daemon_unavailable", msg)
			} else {
				s.answerError(c, msg)
			}
		}
		s.queued = nil
		return
	}
	s.attach(r.conn)
	s.logf("connected to the daemon again (port %d)", r.conn.port)
	s.replay(ctx)
}

// replay brings a new session to where the host thinks it is, then sends the pending calls and queued
// notifications. The SDK handles initialize before anything after it, so all of it goes at once.
func (s *session) replay(ctx context.Context) {
	gen := s.gen
	if s.initLine != nil && s.initAnswered {
		s.write(ctx, s.relayCopy(s.initLine))
		if s.initialized != nil {
			s.write(ctx, s.initialized)
		}
		if s.setLevel != nil {
			s.write(ctx, s.relayCopy(s.setLevel))
		}
	}
	for _, c := range append([]*call(nil), s.pending...) {
		if s.gen != gen {
			return // the connection dropped again
		}
		if !c.sent && s.byKey[c.key] == c {
			s.send(ctx, c)
		}
	}
	q := s.queued
	s.queued = nil
	for _, line := range q {
		if s.gen != gen {
			return
		}
		s.write(ctx, line)
	}
}

// relayCopy is line with a fresh id of the relay, whose answer is swallowed.
func (s *session) relayCopy(line []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return line
	}
	s.nextID++
	id := strconv.Quote("seelegent-relay-" + strconv.Itoa(s.nextID))
	m["id"] = json.RawMessage(id)
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	s.swallow[idKey(json.RawMessage(id))] = true
	return out
}

// answerTool answers a tools/call with a tool error in the server's shape ("Seelegent error: ...", with
// structuredContent {error: {code, message}}).
func (s *session) answerTool(c *call, code, msg string) {
	res := map[string]any{
		"content":           []map[string]string{{"type": "text", "text": "Seelegent error: " + msg}},
		"isError":           true,
		"structuredContent": map[string]any{"error": map[string]string{"code": code, "message": msg}},
	}
	s.answer(map[string]any{"jsonrpc": "2.0", "id": c.id, "result": res})
}

// answerError answers any other request with a JSON-RPC internal error.
func (s *session) answerError(c *call, msg string) {
	s.answer(map[string]any{"jsonrpc": "2.0", "id": c.id,
		"error": map[string]any{"code": -32603, "message": "Seelegent error: " + msg}})
}

func (s *session) answer(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.toHost(line)
}

// toHost writes one line to the host.
func (s *session) toHost(line []byte) {
	_, _ = s.o.Stdout.Write(append(append(make([]byte, 0, len(line)+1), line...), '\n'))
}

func (s *session) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.o.Stderr, "seelegent-launch mcp: "+format+"\n", args...)
}

// idKey is a JSON-RPC id as a map key: 1 and 1.0 are one id, "1" another.
func idKey(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return "raw:" + string(raw)
	}
	switch x := v.(type) {
	case string:
		return "s:" + x
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return "n:" + strconv.FormatInt(i, 10)
		}
		if f, err := x.Float64(); err == nil && f == float64(int64(f)) {
			return "n:" + strconv.FormatInt(int64(f), 10)
		}
		return "n:" + x.String()
	}
	return "raw:" + string(raw)
}
