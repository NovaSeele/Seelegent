package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/mcpwire"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// relayConn is a raw relay upgrade: its answer status, and after a 101 the connection and its reader.
type relayConn struct {
	status int
	header http.Header
	c      net.Conn
	r      *bufio.Reader
}

// upgrade opens POST /api/v1/mcp/relay with hdr (nil: the relay's own headers).
func (e *env) upgrade(hdr map[string]string) *relayConn {
	e.t.Helper()
	c, err := (&net.Dialer{}).DialContext(e.t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(e.port))
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = c.Close() })
	if hdr == nil {
		hdr = map[string]string{"Connection": "Upgrade", "Upgrade": mcpwire.UpgradeToken, "X-Seelegent": "1"}
	}
	req := "POST " + mcpwire.RelayPath + " HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(e.port) + "\r\n"
	for k, v := range hdr {
		req += k + ": " + v + "\r\n"
	}
	if _, err := io.WriteString(c, req+"Content-Length: 0\r\n\r\n"); err != nil {
		e.t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(limit))
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	_ = resp.Body.Close()
	return &relayConn{status: resp.StatusCode, header: resp.Header, c: c, r: br}
}

// hello sends a handshake and returns the daemon's reply.
func (rc *relayConn) hello(t *testing.T, hs mcpwire.Handshake) mcpwire.Reply {
	t.Helper()
	b, _ := json.Marshal(hs)
	if _, err := rc.c.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
	line, err := rc.r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var r mcpwire.Reply
	if err := json.Unmarshal(line, &r); err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	_ = rc.c.SetDeadline(time.Time{})
	return r
}

// session opens a relayed session with env and connects an MCP client to it.
func (e *env) session(env map[string]string) (*mcp.ClientSession, *relayConn) {
	e.t.Helper()
	rc := e.upgrade(nil)
	if rc.status != http.StatusSwitchingProtocols {
		e.t.Fatalf("upgrade answered %d", rc.status)
	}
	if r := rc.hello(e.t, mcpwire.Handshake{Protocol: mcpwire.Protocol, Env: env, Cwd: e.cwd, PID: 1,
		Home: e.paths.Home}); !r.OK {
		e.t.Fatalf("handshake refused: %+v", r)
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "relay-test", Version: "1"}, nil)
	cs, err := cl.Connect(e.t.Context(), &mcp.IOTransport{Reader: readCloser{rc.r, rc.c}, Writer: rc.c}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = cs.Close() })
	return cs, rc
}

type readCloser struct {
	r io.Reader
	c io.Closer
}

func (r readCloser) Read(p []byte) (int, error) { return r.r.Read(p) }
func (r readCloser) Close() error               { return r.c.Close() }

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text + "\n")
		}
	}
	return b.String(), res.IsError
}

func TestRelaySession(t *testing.T) {
	e := newEnv(t)
	cs, _ := e.session(map[string]string{mcpwire.EnvLead: "claude", "GITHUB_TOKEN": "x"})
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	if !slices.Contains(names, "delegate") || !slices.Contains(names, "wait") || len(names) != 17 {
		t.Errorf("tools = %v", names)
	}
	if text, isErr := callTool(t, cs, "status", nil); isErr || !strings.Contains(text, "accounts") {
		t.Errorf("status = %v %s", isErr, text)
	}
	// Relative paths resolve against the relay's folder, never the daemon's.
	sub := filepath.Join(e.cwd, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	e.addAccount("a")
	e.queue.Push(t, ok("c1", "done"))
	text, isErr := callTool(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": "sub", "wait_seconds": 60})
	if isErr {
		t.Fatalf("delegate: %s", text)
	}
	var page api.TaskPage
	e.must(e.get("/api/v1/tasks"), http.StatusOK).json(t, &page)
	if len(page.Items) != 1 {
		t.Fatalf("tasks = %+v", page)
	}
	var d api.TaskDetail
	e.must(e.get("/api/v1/tasks/"+page.Items[0].ID), http.StatusOK).json(t, &d)
	if same, err := fsutil.SamePath(d.Cwd, sub); err != nil || !same {
		t.Errorf("task cwd = %q, want %q", d.Cwd, sub)
	}
	if d.Session == nil || d.Session.Lead != api.LeadClaude {
		t.Errorf("task session = %+v (SEELEGENT_LEAD from the handshake)", d.Session)
	}
}

func TestRelayWorkerEnvRefusesMutating(t *testing.T) {
	e := newEnv(t)
	cs, _ := e.session(map[string]string{mcpwire.EnvWorker: "1"})
	text, isErr := callTool(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": e.cwd})
	if !isErr || strings.TrimSpace(text) != ops.WorkerMessage {
		t.Errorf("delegate in a worker env = %v %q", isErr, text)
	}
	// Reads go on to the daemon, which sees the relay as the lead here.
	if text, isErr := callTool(t, cs, "status", nil); isErr {
		t.Errorf("status = %s", text)
	}
}

// TestRelayWorkerCaller: the session keeps the classification of the upgrade's connection (a process inside a
// worker's tree), whatever its environment says and however callers are classified later: every tool call gets the
// worker allow-list, as `seelegent mcp` calling the API from that process does.
func TestRelayWorkerCaller(t *testing.T) {
	e := newEnv(t)
	e.who.set(&httpapi.Caller{Worker: true, TaskID: "t-other"})
	cs, _ := e.session(nil)
	e.who.set(nil) // callers classified from now on are the lead; the session must not be
	text, isErr := callTool(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": e.cwd})
	if !isErr || strings.TrimSpace(text) != ops.WorkerMessage {
		t.Errorf("delegate from a worker's relay = %v %q", isErr, text)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("status from a worker's relay = %+v, %v", res, err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"caller":"worker"`) || !strings.Contains(string(b), `"status":403`) {
		t.Errorf("structured = %s", b)
	}
	var page api.TaskPage
	e.must(e.get("/api/v1/tasks"), http.StatusOK).json(t, &page)
	if len(page.Items) != 0 {
		t.Errorf("a worker's relay created %d tasks", len(page.Items))
	}
}

func TestRelayRouteChecks(t *testing.T) {
	e := newEnv(t)
	base := func() map[string]string {
		return map[string]string{"Connection": "Upgrade", "Upgrade": mcpwire.UpgradeToken, "X-Seelegent": "1"}
	}
	for name, c := range map[string]struct {
		edit   func(map[string]string)
		status int
	}{
		"origin":    {func(h map[string]string) { h["Origin"] = "http://127.0.0.1:" + strconv.Itoa(e.port) }, 403},
		"foreign":   {func(h map[string]string) { h["Origin"] = "https://evil.example" }, 403},
		"websocket": {func(h map[string]string) { h["Upgrade"] = "websocket" }, http.StatusUpgradeRequired},
		"no upgrade": {func(h map[string]string) { delete(h, "Connection"); delete(h, "Upgrade") },
			http.StatusUpgradeRequired},
		"no header": {func(h map[string]string) { delete(h, "X-Seelegent") }, 403},
		"host":      {func(h map[string]string) { h["Host"] = "evil.example" }, http.StatusMisdirectedRequest},
	} {
		h := base()
		c.edit(h)
		if host, ok := h["Host"]; ok {
			delete(h, "Host")
			rc := e.rawUpgrade(host, h)
			if rc != c.status {
				t.Errorf("%s: %d, want %d", name, rc, c.status)
			}
			continue
		}
		if rc := e.upgrade(h); rc.status != c.status {
			t.Errorf("%s: %d, want %d", name, rc.status, c.status)
		}
	}
	// A bad handshake is refused, and the connection closed.
	for name, c := range map[string]struct {
		hs   mcpwire.Handshake
		code string
	}{
		"protocol": {mcpwire.Handshake{Protocol: 2, Cwd: e.cwd}, mcpwire.CodeProtocol},
		"cwd":      {mcpwire.Handshake{Protocol: 1, Cwd: "rel"}, mcpwire.CodeBadHello},
		"home":     {mcpwire.Handshake{Protocol: 1, Cwd: e.cwd, Home: t.TempDir()}, mcpwire.CodeOtherHome},
	} {
		rc := e.upgrade(nil)
		r := rc.hello(t, c.hs)
		if r.OK || r.Code != c.code {
			t.Errorf("%s: %+v", name, r)
		}
		_ = rc.c.SetReadDeadline(time.Now().Add(limit))
		if _, err := rc.r.ReadByte(); err == nil {
			t.Errorf("%s: the connection stayed open", name)
		}
	}
	// GET is not an upgrade route.
	if r := e.get(mcpwire.RelayPath); r.status != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", r.status)
	}
}

// rawUpgrade sends the upgrade with another Host header and returns the status.
func (e *env) rawUpgrade(host string, hdr map[string]string) int {
	e.t.Helper()
	c, err := (&net.Dialer{}).DialContext(e.t.Context(), "tcp", "127.0.0.1:"+strconv.Itoa(e.port))
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	req := "POST " + mcpwire.RelayPath + " HTTP/1.1\r\nHost: " + host + "\r\n"
	for k, v := range hdr {
		req += k + ": " + v + "\r\n"
	}
	_, _ = io.WriteString(c, req+"Content-Length: 0\r\n\r\n")
	_ = c.SetDeadline(time.Now().Add(limit))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestRelaySessionsEnd: sessions the relay closes leave no goroutine behind, and the daemon's shutdown tells the
// open ones it stops and closes them before its services stop.
func TestRelaySessionsEnd(t *testing.T) {
	e := newEnv(t)
	settle := func(want int) int {
		var n int
		for range 100 {
			runtime.GC()
			if n = runtime.NumGoroutine(); n <= want {
				return n
			}
			time.Sleep(50 * time.Millisecond)
		}
		return n
	}
	// Warm up the client paths (keep-alive pools, timers) before the baseline.
	cs, _ := e.session(nil)
	callTool(t, cs, "status", nil)
	_ = cs.Close()
	time.Sleep(200 * time.Millisecond)
	base := runtime.NumGoroutine()
	for range 5 {
		cs, _ := e.session(nil)
		callTool(t, cs, "status", nil)
		_ = cs.Close()
	}
	if n := settle(base + 2); n > base+2 {
		t.Errorf("goroutines %d after the sessions closed, baseline %d", n, base)
	}
	// Shutdown: an open session gets the stopping notice, then EOF.
	rc := e.upgrade(nil)
	if r := rc.hello(t, mcpwire.Handshake{Protocol: mcpwire.Protocol, Cwd: e.cwd}); !r.OK {
		t.Fatalf("handshake: %+v", r)
	}
	done := make(chan []byte, 1)
	go func() {
		var got []byte
		for {
			line, err := rc.r.ReadBytes('\n')
			got = append(got, line...)
			if err != nil {
				done <- got
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := e.a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if !strings.Contains(string(got), mcpwire.StoppingMethod) {
			t.Errorf("before EOF the relay got %q", got)
		}
	case <-time.After(limit):
		t.Fatal("the session stayed open after Close")
	}
}
