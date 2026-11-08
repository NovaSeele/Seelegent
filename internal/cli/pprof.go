package cli

import (
	"context"
	"errors"
	"expvar"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pprofServer is the debug server of the hidden `daemon --pprof-addr` flag: net/http/pprof and expvar on their own
// mux and their own http.Server, never on http.DefaultServeMux and never on the API's port. It is a development
// tool: heap and goroutine profiles can hold secrets (tokens in memory, prompts), so it listens on loopback only and
// is never a setting.
type pprofServer struct {
	srv  *http.Server
	addr string
	done chan error

	closeOnce sync.Once
	closeErr  error
}

// checkPprofAddr accepts host:port where host is a loopback IP literal or "localhost" and port is 0..65535.
func checkPprofAddr(addr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("--pprof-addr %q: want host:port on loopback, e.g. 127.0.0.1:6060 (%w)", addr, err)
	}
	if p, err := strconv.Atoi(portText); err != nil || p < 0 || p > 65535 {
		return fmt.Errorf("--pprof-addr %q: the port must be 0..65535", addr)
	}
	if !strings.EqualFold(host, "localhost") {
		ip, err := netip.ParseAddr(host)
		if err != nil || !ip.IsLoopback() {
			return fmt.Errorf("--pprof-addr %q: only loopback addresses are allowed (127.0.0.1, [::1] or "+
				"localhost); profiles can hold secrets", addr)
		}
	}
	return nil
}

// pprofHostAllowed is the DNS-rebinding defense of the debug server, the same rule as the API's Host check
// (internal/httpapi Server.hostAllowed): 127.0.0.1, localhost or [::1] with the server's own port. It is a copy on
// purpose, so the debug server does not reach into httpapi.
func pprofHostAllowed(host string, port int) bool {
	p := strconv.Itoa(port)
	switch strings.ToLower(host) {
	case "127.0.0.1:" + p, "localhost:" + p, "[::1]:" + p:
		return true
	}
	return false
}

// pprofHandler serves /debug/pprof/... and /debug/vars behind the Host check for port.
func pprofHandler(port int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/vars", expvar.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !pprofHostAllowed(r.Host, port) {
			http.Error(w, "misdirected request: the Host header must name this loopback address",
				http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// startPprof checks addr, listens on it and serves the debug handlers until Close.
func startPprof(ctx context.Context, addr string, log *slog.Logger) (*pprofServer, error) {
	if err := checkPprofAddr(addr); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("--pprof-addr: %w", err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("--pprof-addr: unexpected listener address %v", ln.Addr())
	}
	p := &pprofServer{addr: ln.Addr().String(), done: make(chan error, 1)}
	p.srv = &http.Server{
		Handler:           pprofHandler(tcp.Port),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: /debug/pprof/profile and /trace stream for as many seconds as asked.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
		ErrorLog:    slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	go func() {
		err := p.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		p.done <- err
	}()
	log.Warn("daemon: --pprof-addr serves profiles; heap and goroutine profiles can hold secrets, use it for "+
		"development only", "addr", p.addr)
	return p, nil
}

// Addr is the address the server listens on.
func (p *pprofServer) Addr() string { return p.addr }

// Close stops the server, ending profiles in progress. It is safe to call more than once.
func (p *pprofServer) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.srv.Close()
		if err := <-p.done; err != nil && p.closeErr == nil {
			p.closeErr = err
		}
	})
	return p.closeErr
}
