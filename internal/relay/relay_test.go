package relay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

const limit = 20 * time.Second

// fakeDaemon accepts relay upgrades on a loopback port and hands each session's connection to the test.
type fakeDaemon struct {
	t      *testing.T
	ln     net.Listener
	port   int
	status int // the upgrade's answer: 101, or an error status

	mu         sync.Mutex
	handshakes []mcpwire.Handshake
	sessions   chan *fakeSession
}

func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDaemon{t: t, port: ln.Addr().(*net.TCPAddr).Port, status: 101, sessions: make(chan *fakeSession, 8)}
	f.serve(ln)
	return f
}

// serve accepts on ln until it is closed.
func (f *fakeDaemon) serve(ln net.Listener) {
	f.ln = ln
	f.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
}

// stop closes the listener (connections refused from then on).
func (f *fakeDaemon) stop() { _ = f.ln.Close() }

// restart listens again on the same port.
func (f *fakeDaemon) restart() {
	f.t.Helper()
	var ln net.Listener
	var err error
	for range 50 {
		if ln, err = (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:"+strconv.Itoa(f.port)); err == nil {
			f.serve(ln)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatal(err)
}

func (f *fakeDaemon) handle(c net.Conn) {
	br := bufio.NewReader(c)
	req, err := http.ReadRequest(br)
	if err != nil {
		_ = c.Close()
		return
	}
	if req.URL.Path != mcpwire.RelayPath || req.Header.Get("X-Seelegent") != "1" ||
		req.Header.Get("Upgrade") != mcpwire.UpgradeToken || req.Header.Get("Origin") != "" {
		_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n")
		_ = c.Close()
		return
	}
	if f.status != 101 {
		_, _ = io.WriteString(c, "HTTP/1.1 "+strconv.Itoa(f.status)+" Nope\r\nContent-Length: 2\r\n\r\n{}")
		_ = c.Close()
		return
	}
	_, _ = io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: seelegent-mcp\r\n\r\n")
	line, err := readLine(br, mcpwire.MaxHandshake)
	if err != nil {
		_ = c.Close()
		return
	}
	var hs mcpwire.Handshake
	if err := json.Unmarshal(line, &hs); err != nil {
		_ = c.Close()
		return
	}
	f.mu.Lock()
	f.handshakes = append(f.handshakes, hs)
	f.mu.Unlock()
	_, _ = io.WriteString(c, `{"ok":true,"version":"test"}`+"\n")
	f.sessions <- &fakeSession{c: c, r: br}
}

// next returns the next session.
func (f *fakeDaemon) next() *fakeSession {
	f.t.Helper()
	select {
	case s := <-f.sessions:
		return s
	case <-time.After(limit):
		f.t.Fatal("no relay session")
		return nil
	}
}

// fakeSession is one relayed session as the daemon sees it.
type fakeSession struct {
	c net.Conn
	r *bufio.Reader
}

// read returns the next message the relay sent.
func (s *fakeSession) read(t *testing.T) map[string]any {
	t.Helper()
	_ = s.c.SetReadDeadline(time.Now().Add(limit))
	line, err := readLine(s.r, maxLine)
	if err != nil {
		t.Fatalf("daemon side: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("daemon side: %s: %v", line, err)
	}
	return m
}

func (s *fakeSession) send(t *testing.T, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if _, err := s.c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// answer answers request m with result.
func (s *fakeSession) answer(t *testing.T, m map[string]any, result any) {
	t.Helper()
	s.send(t, map[string]any{"jsonrpc": "2.0", "id": m["id"], "result": result})
}

// host is the lead host's end of the relay.
type host struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Reader
	errs *strings.Builder
	done chan int
	mu   *sync.Mutex
	seen map[string]int // answered ids
}

// runRelay starts Run against the fake daemon's port with the given environment.
func runRelay(t *testing.T, port int, env map[string]string, adjust func(*Options)) *host {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	home := t.TempDir()
	vars := map[string]string{envHome: home, envPort: strconv.Itoa(port)}
	for k, v := range env {
		vars[k] = v
	}
	var mu sync.Mutex
	errs := &strings.Builder{}
	o := Options{Stdin: inR, Stdout: outW, Stderr: lockedWriter{&mu, errs},
		Getenv:    func(k string) string { return vars[k] },
		LookupEnv: func(k string) (string, bool) { v, ok := vars[k]; return v, ok },
		Getwd:     func() (string, error) { return filepath.Join(home, "project"), nil },
		Dir:       t.TempDir(),
		Start: func(context.Context, string, int) (<-chan error, error) {
			return nil, errors.New("no start in this test")
		},
		Fallback: func(context.Context) int {
			t.Error("unexpected fallback")
			return 9
		},
		Grace: 3 * time.Second, ProbeTimeout: time.Second, HandshakeTimeout: 2 * time.Second,
		StartTimeout: 3 * time.Second, PollInterval: 20 * time.Millisecond, MaxBackoff: 100 * time.Millisecond}
	if adjust != nil {
		adjust(&o)
	}
	h := &host{t: t, in: inW, out: bufio.NewReader(outR), errs: errs, done: make(chan int, 1), mu: &mu,
		seen: map[string]int{}}
	go func() {
		code := Run(t.Context(), o)
		_ = outW.Close()
		h.done <- code
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return h
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (h *host) send(v any) {
	h.t.Helper()
	b, _ := json.Marshal(v)
	if _, err := h.in.Write(append(b, '\n')); err != nil {
		h.t.Fatal(err)
	}
}

func (h *host) request(id any, method string, params any) {
	h.t.Helper()
	m := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	h.send(m)
}

// read returns the next message to the host, and fails on a second answer to one id.
func (h *host) read() map[string]any {
	h.t.Helper()
	type res struct {
		line []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() { l, err := readLine(h.out, maxLine); ch <- res{l, err} }()
	select {
	case r := <-ch:
		if r.err != nil {
			h.t.Fatalf("host side: %v (stderr: %s)", r.err, h.stderr())
		}
		var m map[string]any
		if err := json.Unmarshal(r.line, &m); err != nil {
			h.t.Fatalf("host side: %s: %v", r.line, err)
		}
		if _, isResp := m["method"]; !isResp && m["id"] != nil {
			k := idKeyOf(m["id"])
			h.seen[k]++
			if h.seen[k] > 1 {
				h.t.Errorf("id %v answered %d times", m["id"], h.seen[k])
			}
			if strings.Contains(k, "seelegent-relay") {
				h.t.Errorf("the host got the answer of a replayed request: %v", m)
			}
		}
		return m
	case <-time.After(limit):
		h.t.Fatalf("host side: nothing within %s (stderr: %s)", limit, h.stderr())
		return nil
	}
}

func (h *host) stderr() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.errs.String()
}

func idKeyOf(v any) string {
	b, _ := json.Marshal(v)
	return idKey(b)
}

func toolCallParams(name string, args map[string]any) map[string]any {
	if args == nil {
		args = map[string]any{}
	}
	return map[string]any{"name": name, "arguments": args}
}

// initialize runs the host's handshake through the relay against session s.
func (h *host) initialize(s *fakeSession) {
	h.t.Helper()
	h.request(0, "initialize", map[string]any{"protocolVersion": "2025-06-18"})
	m := s.read(h.t)
	if m["method"] != "initialize" {
		h.t.Fatalf("daemon got %v", m)
	}
	s.answer(h.t, m, map[string]any{"protocolVersion": "2025-06-18"})
	if r := h.read(); r["id"] != float64(0) {
		h.t.Fatalf("host got %v", r)
	}
	h.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if m := s.read(h.t); m["method"] != "notifications/initialized" {
		h.t.Fatalf("daemon got %v", m)
	}
}

func TestHandshakeForwardsOnlySessionEnv(t *testing.T) {
	f := newFakeDaemon(t)
	h := runRelay(t, f.port, map[string]string{mcpwire.EnvWorker: "1", mcpwire.EnvLead: "claude",
		mcpwire.EnvClaudeSession: "sess-1", "GITHUB_TOKEN": "secret", "ANTHROPIC_API_KEY": "secret",
		"PATH": "/bin"}, nil)
	s := f.next()
	h.initialize(s)
	f.mu.Lock()
	hs := f.handshakes[0]
	f.mu.Unlock()
	if hs.Protocol != mcpwire.Protocol || !strings.HasSuffix(hs.Cwd, "project") || hs.Home == "" || hs.PID == 0 {
		t.Errorf("handshake = %+v", hs)
	}
	want := map[string]string{mcpwire.EnvWorker: "1", mcpwire.EnvLead: "claude", mcpwire.EnvClaudeSession: "sess-1"}
	if len(hs.Env) != len(want) {
		t.Errorf("env = %v, want %v", hs.Env, want)
	}
	for k, v := range want {
		if hs.Env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, hs.Env[k], v)
		}
	}
	for k := range hs.Env {
		if !mcpwire.Forwarded(k) {
			t.Errorf("forwarded %s", k)
		}
	}
	_ = h.in.Close()
	if code := <-h.done; code != 0 {
		t.Errorf("exit %d", code)
	}
}

func TestPassThrough(t *testing.T) {
	f := newFakeDaemon(t)
	h := runRelay(t, f.port, nil, nil)
	s := f.next()
	h.initialize(s)
	h.request(1, "tools/list", nil)
	m := s.read(t)
	s.answer(t, m, map[string]any{"tools": []any{}})
	if r := h.read(); r["id"] != float64(1) {
		t.Fatalf("host got %v", r)
	}
	h.request("x", "tools/call", toolCallParams("status", nil))
	m = s.read(t)
	// A server notification and a server request reach the host as they are; the host's answer reaches the daemon.
	s.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/message", "params": map[string]any{"data": 1}})
	s.send(t, map[string]any{"jsonrpc": "2.0", "id": 77, "method": "ping"})
	if r := h.read(); r["method"] != "notifications/message" {
		t.Fatalf("host got %v", r)
	}
	if r := h.read(); r["method"] != "ping" {
		t.Fatalf("host got %v", r)
	}
	h.send(map[string]any{"jsonrpc": "2.0", "id": 77, "result": map[string]any{}})
	if r := s.read(t); r["id"] != float64(77) {
		t.Fatalf("daemon got %v", r)
	}
	s.answer(t, m, map[string]any{"content": []any{}})
	// An answer to an id nobody waits for is dropped; the next answer still arrives.
	if r := h.read(); r["id"] != "x" {
		t.Fatalf("host got %v", r)
	}
	s.answer(t, map[string]any{"id": "x"}, map[string]any{})
	h.request(2, "ping", nil)
	m = s.read(t)
	s.answer(t, m, map[string]any{})
	if r := h.read(); r["id"] != float64(2) {
		t.Fatalf("host got %v", r)
	}
	// A cancelled call reaches the daemon; its late answer is dropped.
	h.request(3, "tools/call", toolCallParams("wait", nil))
	m = s.read(t)
	h.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 3}})
	if r := s.read(t); r["method"] != "notifications/cancelled" {
		t.Fatalf("daemon got %v", r)
	}
	s.answer(t, m, map[string]any{})
	h.request(4, "ping", nil)
	m = s.read(t)
	s.answer(t, m, map[string]any{})
	if r := h.read(); r["id"] != float64(4) {
		t.Fatalf("host got %v (the cancelled call's answer must be dropped)", r)
	}
}

func TestReconnectReplaysAndResends(t *testing.T) {
	f := newFakeDaemon(t)
	var starts atomic.Int32
	h := runRelay(t, f.port, nil, func(o *Options) {
		o.Start = func(context.Context, string, int) (<-chan error, error) {
			starts.Add(1)
			f.restart()
			return nil, nil
		}
	})
	s := f.next()
	h.initialize(s)
	h.request("lvl", "logging/setLevel", map[string]any{"level": "info"})
	m := s.read(t)
	s.answer(t, m, map[string]any{})
	if r := h.read(); r["id"] != "lvl" {
		t.Fatalf("host got %v", r)
	}
	h.request(10, "tools/call", toolCallParams("wait", map[string]any{"task_ids": []string{"t1"}}))
	h.request(11, "tools/call", toolCallParams("delegate", map[string]any{"prompt": "x"}))
	h.request(12, "tools/call", toolCallParams("memory", map[string]any{"action": "read"}))
	h.request(13, "tools/call", toolCallParams("memory", map[string]any{"action": "append"}))
	for range 4 {
		s.read(t)
	}
	// The daemon crashes: nothing listens until the relay starts it again.
	f.stop()
	_ = s.c.Close()
	got := map[float64]map[string]any{}
	for range 2 {
		r := h.read()
		got[r["id"].(float64)] = r
	}
	for _, id := range []float64{11, 13} {
		res, _ := got[id]["result"].(map[string]any)
		text := ""
		if c, ok := res["content"].([]any); ok && len(c) > 0 {
			text, _ = c[0].(map[string]any)["text"].(string)
		}
		if res["isError"] != true || !strings.Contains(text, "check task or status") {
			t.Errorf("mutating call %v answered %v", id, got[id])
		}
	}
	s2 := f.next()
	if starts.Load() != 1 {
		t.Errorf("starts = %d, want 1 (a crash restarts the daemon)", starts.Load())
	}
	// The new session first gets the replayed initialize (another id), initialized and setLevel, then the reads.
	init := s2.read(t)
	if init["method"] != "initialize" || init["id"] == float64(0) {
		t.Fatalf("replay: %v", init)
	}
	if p, _ := init["params"].(map[string]any); p["protocolVersion"] != "2025-06-18" {
		t.Errorf("replayed params %v", init["params"])
	}
	if m := s2.read(t); m["method"] != "notifications/initialized" {
		t.Fatalf("replay: %v", m)
	}
	lvl := s2.read(t)
	if lvl["method"] != "logging/setLevel" || lvl["id"] == "lvl" {
		t.Fatalf("replay: %v", lvl)
	}
	s2.answer(t, init, map[string]any{})
	s2.answer(t, lvl, map[string]any{})
	w := s2.read(t)
	r := s2.read(t)
	if w["id"] != float64(10) || r["id"] != float64(12) {
		t.Fatalf("re-sent %v and %v, want 10 and 12", w["id"], r["id"])
	}
	s2.answer(t, r, map[string]any{"content": []any{}})
	s2.answer(t, w, map[string]any{"content": []any{}})
	if a, b := h.read(), h.read(); a["id"] != float64(12) || b["id"] != float64(10) {
		t.Fatalf("host got %v and %v", a, b)
	}
	// The host goes on as before.
	h.request(14, "tools/list", nil)
	m = s2.read(t)
	s2.answer(t, m, map[string]any{"tools": []any{}})
	if r := h.read(); r["id"] != float64(14) {
		t.Fatalf("host got %v", r)
	}
}

func TestStoppingDaemonIsNotStarted(t *testing.T) {
	f := newFakeDaemon(t)
	var starts atomic.Int32
	h := runRelay(t, f.port, nil, func(o *Options) {
		o.Start = func(context.Context, string, int) (<-chan error, error) {
			starts.Add(1)
			return nil, errors.New("not here")
		}
	})
	s := f.next()
	h.initialize(s)
	h.request(20, "tools/call", toolCallParams("status", nil))
	s.read(t)
	f.stop()
	s.send(t, map[string]any{"jsonrpc": "2.0", "method": mcpwire.StoppingMethod})
	_ = s.c.Close()
	time.Sleep(300 * time.Millisecond)
	f.restart() // `seelegent restart` brings the next daemon up
	s2 := f.next()
	init := s2.read(t)
	s2.read(t) // initialized
	s2.answer(t, init, map[string]any{})
	m := s2.read(t)
	if m["id"] != float64(20) {
		t.Fatalf("re-sent %v", m)
	}
	s2.answer(t, m, map[string]any{})
	if r := h.read(); r["id"] != float64(20) {
		t.Fatalf("host got %v", r)
	}
	if starts.Load() != 0 {
		t.Errorf("the relay started a daemon that said it was stopping (%d)", starts.Load())
	}
	if strings.Contains(h.stderr(), mcpwire.StoppingMethod) {
		t.Error("the stopping notice leaked")
	}
}

// TestIdleRelayStartsNoDaemonAfterStopping: the stopping notice holds for Grace whether calls were pending or not,
// so the host's next request after `seelegent stop` (or during an update) only looks for a daemon; after Grace the
// relay may start one again.
func TestIdleRelayStartsNoDaemonAfterStopping(t *testing.T) {
	f := newFakeDaemon(t)
	var starts atomic.Int32
	h := runRelay(t, f.port, nil, func(o *Options) {
		o.Grace = time.Second
		o.Start = func(context.Context, string, int) (<-chan error, error) {
			starts.Add(1)
			return nil, errors.New("not here")
		}
	})
	s := f.next()
	h.initialize(s)
	f.stop()
	s.send(t, map[string]any{"jsonrpc": "2.0", "method": mcpwire.StoppingMethod})
	_ = s.c.Close()
	time.Sleep(200 * time.Millisecond) // the relay drops the idle connection
	h.request(40, "tools/list", nil)
	time.Sleep(400 * time.Millisecond)
	if n := starts.Load(); n != 0 {
		t.Fatalf("the relay started a daemon %d times right after the stopping notice", n)
	}
	r := h.read() // Grace runs out: the request gets an error
	if r["id"] != float64(40) || r["error"] == nil {
		t.Fatalf("host got %v", r)
	}
	if starts.Load() == 0 {
		t.Error("the relay never tried to start a daemon once Grace after the notice had passed")
	}
}

func TestGraceRunsOut(t *testing.T) {
	f := newFakeDaemon(t)
	h := runRelay(t, f.port, map[string]string{mcpwire.EnvWorker: "1"}, func(o *Options) {
		o.Grace = 300 * time.Millisecond
	})
	s := f.next()
	h.initialize(s)
	h.request(30, "tools/call", toolCallParams("task", map[string]any{"task_id": "t"}))
	h.request(31, "tools/list", nil)
	s.read(t)
	s.read(t)
	f.stop()
	_ = s.c.Close()
	for range 2 {
		r := h.read()
		switch r["id"] {
		case float64(30):
			res, _ := r["result"].(map[string]any)
			sc, _ := res["structuredContent"].(map[string]any)
			e, _ := sc["error"].(map[string]any)
			if res["isError"] != true || e["code"] != "daemon_unavailable" {
				t.Errorf("tool call answered %v", r)
			}
		case float64(31):
			if r["error"] == nil {
				t.Errorf("tools/list answered %v", r)
			}
		default:
			t.Fatalf("host got %v", r)
		}
	}
	// The next request tries again.
	f.restart()
	h.request(32, "tools/list", nil)
	s2 := f.next()
	init := s2.read(t)
	s2.read(t)
	s2.answer(t, init, map[string]any{})
	m := s2.read(t)
	s2.answer(t, m, map[string]any{})
	if r := h.read(); r["id"] != float64(32) {
		t.Fatalf("host got %v", r)
	}
}

// TestFallbackOnOlderDaemon: a daemon without the route (404), or one that refuses this relay's session (503, as
// when it cannot classify the caller), makes the relay run `seelegent mcp`.
func TestFallbackOnOlderDaemon(t *testing.T) {
	for _, status := range []int{404, 503} {
		f := newFakeDaemon(t)
		f.status = status
		var ran atomic.Bool
		h := runRelay(t, f.port, nil, func(o *Options) {
			o.Fallback = func(context.Context) int { ran.Store(true); return 7 }
		})
		if code := <-h.done; code != 7 || !ran.Load() {
			t.Fatalf("%d: exit %d, fallback %v", status, code, ran.Load())
		}
		if !strings.Contains(h.stderr(), "HTTP "+strconv.Itoa(status)) || !strings.Contains(h.stderr(), "seelegent mcp") {
			t.Errorf("%d: stderr = %q", status, h.stderr())
		}
	}
}

func TestFallbackWhenNoDaemonInsideWorker(t *testing.T) {
	f := newFakeDaemon(t)
	f.stop()
	var started, ran atomic.Bool
	h := runRelay(t, f.port, map[string]string{mcpwire.EnvWorker: "1"}, func(o *Options) {
		o.Start = func(context.Context, string, int) (<-chan error, error) { started.Store(true); return nil, nil }
		o.Fallback = func(context.Context) int { ran.Store(true); return 0 }
	})
	if code := <-h.done; code != 0 || !ran.Load() || started.Load() {
		t.Fatalf("exit %d, fallback %v, started %v", code, ran.Load(), started.Load())
	}
}

func TestAutostart(t *testing.T) {
	f := newFakeDaemon(t)
	f.stop()
	var gotPort atomic.Int32
	var gotHome atomic.Value
	h := runRelay(t, f.port, nil, func(o *Options) {
		o.Start = func(_ context.Context, home string, port int) (<-chan error, error) {
			gotPort.Store(int32(port))
			gotHome.Store(home)
			go func() { time.Sleep(100 * time.Millisecond); f.restart() }()
			return make(chan error), nil
		}
	})
	s := f.next()
	h.initialize(s)
	if int(gotPort.Load()) != f.port || gotHome.Load() == nil {
		t.Errorf("started on %d, home %v", gotPort.Load(), gotHome.Load())
	}
}

func TestConfiguredPort(t *testing.T) {
	dir := t.TempDir()
	env := func(string) string { return "" }
	if p, err := configuredPort(dir, env); err != nil || p != defaultPort {
		t.Errorf("no config: %d, %v", p, err)
	}
	write := func(s string) {
		if err := writeFile(filepath.Join(dir, "config.json"), s); err != nil {
			t.Fatal(err)
		}
	}
	write("\xef\xbb\xbf{\"port\": 47999, \"other\": 1}")
	if p, err := configuredPort(dir, env); err != nil || p != 47999 {
		t.Errorf("config: %d, %v", p, err)
	}
	if p, err := configuredPort(dir, func(k string) string { return map[string]string{envPort: "47834"}[k] }); err != nil ||
		p != 47834 {
		t.Errorf("env: %d, %v", p, err)
	}
	write("{bad")
	if _, err := configuredPort(dir, env); err == nil {
		t.Error("a bad config.json passed")
	}
	if _, err := configuredPort(dir, func(string) string { return "x" }); err == nil {
		t.Error("a bad SEELEGENT_PORT passed")
	}
}

func TestIDKey(t *testing.T) {
	if idKey([]byte("1")) != idKey([]byte("1.0")) || idKey([]byte("1")) == idKey([]byte(`"1"`)) {
		t.Error("idKey")
	}
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o600) }
