package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

// MCPRelay serves the MCP sessions of the companion's relay (mcpserver.Relay). conn carries newline-delimited
// JSON-RPC; env holds the host's session variables (only mcpwire.SessionEnv), cwd the relay's folder, and hc calls
// this daemon's API in process on behalf of the relay's process (the caller of the upgrade).
type MCPRelay interface {
	ServeMCP(ctx context.Context, conn io.ReadWriteCloser, env map[string]string, cwd string, hc *http.Client) error
}

// relayHandshakeTimeout bounds the wait for the relay's handshake line after the upgrade.
const relayHandshakeTimeout = 10 * time.Second

// maxRelaySessions bounds the relayed sessions at once (one per lead host window); more answer 503.
const maxRelaySessions = 256

// stoppingLine is the control notification written before a session is closed for the daemon's shutdown.
var stoppingLine = []byte(`{"jsonrpc":"2.0","method":"` + mcpwire.StoppingMethod + `"}` + "\n")

// serveRelay is POST /api/v1/mcp/relay: the upgrade of the companion's MCP relay (docs/api.md, "MCP relay").
//
// The route checks for itself what serve checks only for some requests: X-Seelegent: 1 always, no Origin header
// at all (no page may open it, whatever its origin; browsers send Origin on every POST), and exactly its own
// Upgrade token (a WebSocket upgrade is refused). serve has done the Host check and classified the caller from
// this connection's socket: a worker may open a session (the allow-list lets it), and every tool call of the
// session then runs with that caller (inProcessClient), so the worker allow-list applies as for `seelegent mcp`.
func (s *Server) serveRelay(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Header.Get("X-Seelegent") != "1":
		s.writeError(w, r, translated(api.CodeForbidden, "no_header"))
		return
	case len(r.Header.Values("Origin")) > 0:
		s.writeError(w, r, translated(api.CodeForbidden, "origin"))
		return
	case !headerHasToken(r.Header, "Connection", "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), mcpwire.UpgradeToken):
		w.Header().Set("Upgrade", mcpwire.UpgradeToken)
		s.writeErrorStatus(w, r, http.StatusUpgradeRequired, newError(api.CodeInvalidArgument,
			"this endpoint only upgrades to %s (Connection: Upgrade, Upgrade: %s)", mcpwire.UpgradeToken,
			mcpwire.UpgradeToken).with("reason", "upgrade_required"))
		return
	}
	caller := s.callerOf(r)
	if !s.relayStart() {
		s.writeError(w, r, newError(api.CodeUnavailable, "the daemon is stopping, or holds %d relayed MCP "+
			"sessions already", maxRelaySessions))
		return
	}
	defer s.relayDone()
	conn, brw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		s.log.Warn("mcp relay: hijack", "err", err)
		s.writeError(w, r, err)
		return
	}
	// The server's read and write deadlines stay on a hijacked connection: clear them.
	_ = conn.SetDeadline(time.Time{})
	rc := &relayConn{c: conn, r: brw.Reader}
	defer func() { _ = rc.Close() }()
	if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+
		mcpwire.UpgradeToken+"\r\n\r\n"); err != nil {
		return
	}
	hs, refusal := s.relayHandshake(conn, brw.Reader)
	reply := mcpwire.Reply{OK: refusal == nil, Version: s.o.Version, Revision: s.o.Commit}
	if refusal != nil {
		reply.Code, reply.Error = refusal.code, refusal.msg
	}
	line, _ := json.Marshal(reply)
	if _, err := rc.Write(append(line, '\n')); err != nil || refusal != nil {
		if refusal != nil {
			s.log.Info("mcp relay: handshake refused", "code", refusal.code, "err", refusal.msg)
		}
		return
	}
	env := map[string]string{}
	for k, v := range hs.Env {
		if mcpwire.Forwarded(k) {
			env[k] = v
		}
	}
	// The daemon's shutdown (Close) ends every session at once, before the services stop: the relay is told it
	// was a shutdown, then sees the connection close and reconnects to the next daemon. The session's context
	// ends only after the notice, so the SDK does not close the connection under it.
	ctx, cancel := context.WithCancel(context.WithoutCancel(s.ctx))
	defer cancel()
	stop := context.AfterFunc(s.ctx, func() {
		rc.stopping()
		cancel()
	})
	defer stop()
	s.log.Info("mcp relay: session opened", "pid", hs.PID, "worker", caller.Worker, "task", caller.TaskID)
	err = s.o.MCP.ServeMCP(ctx, rc, env, hs.Cwd, s.inProcessClient(caller))
	if err != nil && s.ctx.Err() == nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) {
		s.log.Info("mcp relay: session ended", "pid", hs.PID, "err", err)
	} else {
		s.log.Info("mcp relay: session closed", "pid", hs.PID)
	}
}

// relayRefusal is why a handshake is refused.
type relayRefusal struct{ code, msg string }

// relayHandshake reads and checks the relay's handshake line.
func (s *Server) relayHandshake(conn net.Conn, br *bufio.Reader) (mcpwire.Handshake, *relayRefusal) {
	var hs mcpwire.Handshake
	_ = conn.SetReadDeadline(time.Now().Add(relayHandshakeTimeout))
	line, err := readLine(br, mcpwire.MaxHandshake)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return hs, &relayRefusal{mcpwire.CodeBadHello, "no handshake line: " + err.Error()}
	}
	if err := json.Unmarshal(line, &hs); err != nil {
		return hs, &relayRefusal{mcpwire.CodeBadHello, "the handshake is not JSON: " + err.Error()}
	}
	if hs.Protocol != mcpwire.Protocol {
		return hs, &relayRefusal{mcpwire.CodeProtocol, fmt.Sprintf("this daemon speaks relay protocol %d, the "+
			"relay %d", mcpwire.Protocol, hs.Protocol)}
	}
	if hs.Cwd == "" || !filepath.IsAbs(hs.Cwd) {
		return hs, &relayRefusal{mcpwire.CodeBadHello, "the handshake needs the relay's absolute working directory"}
	}
	if hs.Home != "" && s.o.HomeID != "" && config.HomeID(hs.Home) != s.o.HomeID {
		return hs, &relayRefusal{mcpwire.CodeOtherHome, "this daemon serves another data directory"}
	}
	return hs, nil
}

// readLine reads one line (without its end) of at most limit bytes.
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > limit {
			return nil, fmt.Errorf("the line is longer than %d bytes", limit)
		}
		switch {
		case err == nil:
			return bytes.TrimRight(buf, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

// headerHasToken reports whether the comma-separated header name holds token (case-insensitively).
func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for t := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// relayStart counts a new session (false once Close began, or at the cap). Close waits for the sessions through
// s.wg, which is only added to while s.closed is false, under s.mu.
func (s *Server) relayStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.relaySessions >= maxRelaySessions {
		return false
	}
	s.relaySessions++
	s.wg.Add(1)
	return true
}

func (s *Server) relayDone() {
	s.mu.Lock()
	s.relaySessions--
	s.mu.Unlock()
	s.wg.Done()
}

// relayConn is the hijacked connection of one session: reads go through the bufio.Reader of the hijack (it may
// hold bytes already), writes are whole lines under one lock (the MCP SDK writes each message with one Write), so
// the stopping notice never lands inside a message.
type relayConn struct {
	c  net.Conn
	r  *bufio.Reader
	wm sync.Mutex
	co sync.Once
}

func (c *relayConn) Read(p []byte) (int, error) { return c.r.Read(p) }

func (c *relayConn) Write(p []byte) (int, error) {
	c.wm.Lock()
	defer c.wm.Unlock()
	return c.c.Write(p)
}

func (c *relayConn) Close() error {
	var err error
	c.co.Do(func() { err = c.c.Close() })
	return err
}

// stopping tells the relay the daemon shuts down and closes the connection.
func (c *relayConn) stopping() {
	c.wm.Lock()
	_ = c.c.SetWriteDeadline(time.Now().Add(time.Second))
	_, _ = c.c.Write(stoppingLine)
	c.wm.Unlock()
	_ = c.Close()
}
