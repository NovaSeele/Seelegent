package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

// --pprof-addr takes loopback addresses only, so profiles (which can hold secrets) never leave the machine.
func TestCheckPprofAddr(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:6060", "127.0.0.2:0", "[::1]:6060", "localhost:6060", "LOCALHOST:1"} {
		if err := checkPprofAddr(addr); err != nil {
			t.Errorf("checkPprofAddr(%q) = %v, want nil", addr, err)
		}
	}
	for _, addr := range []string{":6060", "0.0.0.0:6060", "[::]:6060", "192.168.1.5:6060", "example.com:6060",
		"localhost.example.com:6060", "127.0.0.1", "127.0.0.1:99999", "127.0.0.1:x", ""} {
		err := checkPprofAddr(addr)
		if err == nil {
			t.Errorf("checkPprofAddr(%q) = nil, want an error", addr)
			continue
		}
		if !strings.Contains(err.Error(), "--pprof-addr") {
			t.Errorf("checkPprofAddr(%q) = %v, want the flag named", addr, err)
		}
	}
}

// The daemon command refuses a non-loopback --pprof-addr as a usage error, before it opens anything.
func TestDaemonRefusesPublicPprofAddr(t *testing.T) {
	cliEnv(t)
	t.Setenv("SEELEGENT_HOME", t.TempDir())
	r := runCLI(t, testDeps(nil), "daemon", "--pprof-addr", "0.0.0.0:6060")
	if r.code != ExitUsage || !strings.Contains(r.stderr, "only loopback addresses") {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func pprofGet(t *testing.T, url, host string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// The debug server serves pprof and expvar on loopback and refuses a foreign Host header (DNS rebinding).
func TestPprofServer(t *testing.T) {
	p, err := startPprof(t.Context(), "127.0.0.1:0", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	base := "http://" + p.Addr()

	if code, body := pprofGet(t, base+"/debug/pprof/", ""); code != http.StatusOK || !strings.Contains(body, "goroutine") {
		t.Fatalf("/debug/pprof/ = %d %q", code, body)
	}
	if code, body := pprofGet(t, base+"/debug/pprof/goroutine?debug=1", ""); code != http.StatusOK ||
		!strings.HasPrefix(body, "goroutine profile: total ") {
		t.Fatalf("goroutine = %d %.80q", code, body)
	}
	if code, body := pprofGet(t, base+"/debug/vars", ""); code != http.StatusOK || !strings.Contains(body, `"memstats"`) {
		t.Fatalf("/debug/vars = %d %.80q", code, body)
	}
	_, port, _ := strings.Cut(p.Addr(), ":")
	if code, _ := pprofGet(t, base+"/debug/pprof/", "localhost:"+port); code != http.StatusOK {
		t.Fatalf("localhost Host = %d", code)
	}
	for _, host := range []string{"evil.example:" + port, "127.0.0.1:1", "localhost"} {
		if code, _ := pprofGet(t, base+"/debug/pprof/", host); code != http.StatusMisdirectedRequest {
			t.Errorf("Host %q = %d, want 421", host, code)
		}
	}
	// The API's default mux is not served here.
	if code, _ := pprofGet(t, base+"/api/v1/health", ""); code != http.StatusNotFound {
		t.Errorf("/api/v1/health = %d, want 404", code)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
}

// startPprof refuses a public address itself too, not only the flag check.
func TestStartPprofRefusesPublic(t *testing.T) {
	if _, err := startPprof(t.Context(), "0.0.0.0:0", slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("startPprof(0.0.0.0:0) = nil error")
	}
}
