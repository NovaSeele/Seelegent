package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pprofServer is the debug server of the hidden `daemon --pprof-addr` flag: net/http/pprof and /debug/vars on their own
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
// (internal/httpapi Server.hostAllowed): 127.0.0.1, localhost or [::1] with the server's own port, plus the
// loopback address the server actually listens on (127.0.0.2:6060 is reached as Host 127.0.0.2:6060). It is a copy
// on purpose, so the debug server does not reach into httpapi.
func pprofHostAllowed(host string, listen *net.TCPAddr) bool {
	p := strconv.Itoa(listen.Port)
	switch strings.ToLower(host) {
	case "127.0.0.1:" + p, "localhost:" + p, "[::1]:" + p:
		return true
	}
	h, port, err := net.SplitHostPort(host)
	if err != nil || port != p {
		return false
	}
	ip, err := netip.ParseAddr(h)
	own, ok := netip.AddrFromSlice(listen.IP)
	return err == nil && ok && ip.IsLoopback() && ip.Unmap() == own.Unmap()
}

// runtimeSamples are the runtime/metrics values /debug/vars reports under "runtime": cheap to read (no
// stop-the-world, unlike runtime.ReadMemStats), so a sampler can poll them often with ?light=1.
func runtimeSamples() []struct{ key, name string } {
	return []struct{ key, name string }{
		{"heapObjectsBytes", "/memory/classes/heap/objects:bytes"},
		{"heapLiveBytes", "/gc/heap/live:bytes"},
		{"heapAllocBytesTotal", "/gc/heap/allocs:bytes"},
		{"heapAllocObjectsTotal", "/gc/heap/allocs:objects"},
		{"gcCycles", "/gc/cycles/total:gc-cycles"},
		{"totalBytes", "/memory/classes/total:bytes"},
		{"goroutines", "/sched/goroutines:goroutines"},
	}
}

// debugVarsSource adds the daemon's own values to /debug/vars (app.App: the SSE replay ring). The cli builds the
// debug server and the app owns the values, so the server never reaches into httpapi.
type debugVarsSource interface {
	DebugVars() map[string]any
}

// debugVars returns the handler of /debug/vars. It writes expvar-style JSON: "pid", "goroutines", "runtime"
// (runtime/metrics) and "daemon" (src's values, when src is set) always, "cmdline" and "memstats"
// (runtime.ReadMemStats, which stops the world briefly) unless the query has light=1. The values are built per
// request, never published in expvar's process-wide registry.
func debugVars(src debugVarsSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeDebugVars(w, r, src) }
}

func writeDebugVars(w http.ResponseWriter, r *http.Request, src debugVarsSource) {
	names := runtimeSamples()
	samples := make([]metrics.Sample, len(names))
	for i, s := range names {
		samples[i].Name = s.name
	}
	metrics.Read(samples)
	rt := make(map[string]uint64, len(samples))
	for i, s := range samples {
		if s.Value.Kind() == metrics.KindUint64 {
			rt[names[i].key] = s.Value.Uint64()
		}
	}
	out := map[string]any{"pid": os.Getpid(), "goroutines": runtime.NumGoroutine(), "runtime": rt}
	if src != nil {
		out["daemon"] = src.DebugVars()
	}
	if r.URL.Query().Get("light") != "1" {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		out["memstats"], out["cmdline"] = &m, os.Args
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}

// pprofHandler serves /debug/pprof/... and /debug/vars behind the Host check for the listen address.
func pprofHandler(listen *net.TCPAddr, src debugVarsSource) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("/debug/vars", debugVars(src))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !pprofHostAllowed(r.Host, listen) {
			http.Error(w, "misdirected request: the Host header must name this loopback address",
				http.StatusMisdirectedRequest)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// startPprof checks addr, listens on it and serves the debug handlers until Close; src adds the daemon's own values
// to /debug/vars (nil for none).
func startPprof(ctx context.Context, addr string, log *slog.Logger, src debugVarsSource) (*pprofServer, error) {
	if err := checkPprofAddr(addr); err != nil {
		return nil, err
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("--pprof-addr: %w", err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcp.IP.IsLoopback() {
		// "localhost" resolved to something else (a hosts file can say anything).
		_ = ln.Close()
		return nil, fmt.Errorf("--pprof-addr %q: listens on %v, which is not a loopback address", addr, ln.Addr())
	}
	p := &pprofServer{addr: ln.Addr().String(), done: make(chan error, 1)}
	p.srv = &http.Server{
		Handler:           pprofHandler(tcp, src),
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
