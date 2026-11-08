package cli

import (
	"context"
	"encoding/json"
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

func pprofGet(t *testing.T, url, host string) (code int, body string) {
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
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
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
	if code, body := pprofGet(t, base+"/debug/vars", ""); code != http.StatusOK || !strings.Contains(body, `"memstats"`) ||
		!strings.Contains(body, `"goroutines"`) {
		t.Fatalf("/debug/vars = %d %.80q", code, body)
	}
	// light=1: the goroutine count and runtime/metrics, without memstats (ReadMemStats stops the world).
	code, body := pprofGet(t, base+"/debug/vars?light=1", "")
	var light struct {
		Goroutines int               `json:"goroutines"`
		Runtime    map[string]uint64 `json:"runtime"`
		Memstats   json.RawMessage   `json:"memstats"`
	}
	if code != http.StatusOK || json.Unmarshal([]byte(body), &light) != nil || light.Goroutines < 1 ||
		light.Memstats != nil || light.Runtime["heapAllocBytesTotal"] == 0 || light.Runtime["gcCycles"] == 0 &&
		light.Runtime["heapObjectsBytes"] == 0 {
		t.Fatalf("/debug/vars?light=1 = %d %.200q", code, body)
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

// The Host check accepts the loopback address the server listens on, not only 127.0.0.1 (M19 review: a server on
// 127.0.0.2 answered 421 to every request).
func TestPprofServerOtherLoopback(t *testing.T) {
	p, err := startPprof(t.Context(), "127.0.0.2:0", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Skipf("127.0.0.2 is not usable here: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	if code, _ := pprofGet(t, "http://"+p.Addr()+"/debug/pprof/", ""); code != http.StatusOK {
		t.Fatalf("own address = %d", code)
	}
	_, port, _ := strings.Cut(p.Addr(), ":")
	if code, _ := pprofGet(t, "http://"+p.Addr()+"/debug/pprof/", "127.0.0.3:"+port); code != http.StatusMisdirectedRequest {
		t.Fatalf("another loopback address = %d, want 421", code)
	}
}

// startPprof refuses a public address itself too, not only the flag check.
func TestStartPprofRefusesPublic(t *testing.T) {
	if _, err := startPprof(t.Context(), "0.0.0.0:0", slog.New(slog.DiscardHandler)); err == nil {
		t.Fatal("startPprof(0.0.0.0:0) = nil error")
	}
}
