package app

import (
	"context"
	"math"
	"net/http"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/gate"
)

// countReschedule stands in for the quota refresher.
type countReschedule struct{ n atomic.Int32 }

func (c *countReschedule) Reschedule() { c.n.Add(1) }

// TestPatchSettingsReschedulesQuota: PATCH /settings re-arms the quota refresher when quotaRefreshMinutes changes,
// and only then (bugs.md F75).
func TestPatchSettingsReschedulesQuota(t *testing.T) {
	d := newDaemon(t)
	// Registered after newDaemon, so it runs before the daemon closes: no keep-alive connection of the default
	// client is left for its shutdown.
	t.Cleanup(http.DefaultClient.CloseIdleConnections)
	fake := &countReschedule{}
	d.a.rescheduler = fake
	addr, err := d.a.ListenHTTP(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body string) {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, "http://"+addr+"/api/v1/settings",
			strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-Seelegent", "1")
		r.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %s: %s", body, res.Status)
		}
	}
	cur := d.a.Settings().QuotaRefreshMinutes
	next := cur + 7
	for _, c := range []struct {
		body string
		want int32
	}{
		{`{"maxConcurrentPerAccount": 3}`, 0},
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(next) + `}`, 1},
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(next) + `}`, 1}, // unchanged
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(cur) + `}`, 2},
	} {
		patch(c.body)
		if got := fake.n.Load(); got != c.want {
			t.Errorf("after %s: %d reschedules, want %d", c.body, got, c.want)
		}
	}
	if got := d.a.Settings().QuotaRefreshMinutes; got != cur {
		t.Errorf("quotaRefreshMinutes = %d, want %d", got, cur)
	}
}

// memoryLimits records the calls of debug.SetMemoryLimit.
type memoryLimits struct{ calls []int64 }

func (m *memoryLimits) set(n int64) int64 { m.calls = append(m.calls, n); return 0 }

// TestDaemonMemoryLimit: daemonMemoryLimitMB sets the daemon's soft memory limit when it changes, 0 lifts the one it
// set, and a GOMEMLIMIT in the daemon's environment wins.
func TestDaemonMemoryLimit(t *testing.T) {
	d := newDaemon(t)
	fake := &memoryLimits{}
	d.a.setMemoryLimit = fake.set
	patch := func(body string) {
		t.Helper()
		if _, err := d.a.PatchSettings(t.Context(), []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	patch(`{"maxConcurrentPerAccount": 3}`)
	patch(`{"daemonMemoryLimitMB": 512}`)
	patch(`{"daemonMemoryLimitMB": 512}`) // unchanged
	patch(`{"daemonMemoryLimitMB": 0}`)
	patch(`{"daemonMemoryLimitMB": 0}`)
	if want := []int64{512 << 20, math.MaxInt64}; !slices.Equal(fake.calls, want) {
		t.Fatalf("SetMemoryLimit calls = %v, want %v", fake.calls, want)
	}

	// At start, and with GOMEMLIMIT set.
	start := &memoryLimits{}
	a := &App{setMemoryLimit: start.set}
	a.applyMemoryLimit(0)
	a.applyMemoryLimit(256)
	if want := []int64{256 << 20}; !slices.Equal(start.calls, want) {
		t.Fatalf("calls = %v, want %v", start.calls, want)
	}
	env := &memoryLimits{}
	a = &App{setMemoryLimit: env.set, env: []string{"PATH=/bin", "GOMEMLIMIT=1GiB"}}
	a.applyMemoryLimit(256)
	a.applyMemoryLimit(0)
	if len(env.calls) != 0 {
		t.Fatalf("GOMEMLIMIT is set, yet SetMemoryLimit was called: %v", env.calls)
	}
}

// TestProofExecStoppedForMemory: a regression run command the memory watchdog stops says so in its tail.
func TestProofExecStoppedForMemory(t *testing.T) {
	d := newDaemon(t)
	cmd := "sleep 30"
	if runtime.GOOS == "windows" {
		cmd = "ping -n 30 127.0.0.1 >nul"
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel(gate.ErrLowMemory)
	}()
	res := d.a.proofExec(ctx, cmd, d.cwd, filepath.Join(t.TempDir(), "cmd.log"))
	if res.Exit != nil || !strings.HasSuffix(res.Tail, "stopped: machine low on memory") {
		t.Fatalf("result exit=%v tail=%q", res.Exit, res.Tail)
	}
}
