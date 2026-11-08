//go:build bench

package bench

import (
	"strings"
	"testing"
	"time"
)

// Unit tests of the report's arithmetic, with made-up numbers. Run them with
// `go test -tags bench -run Test ./scripts/bench` (TestMain runs the bench itself without -run).

func checkNamed(t *testing.T, r *result, part string) *budgetCheck {
	t.Helper()
	for i := range r.Budget {
		if strings.Contains(r.Budget[i].Name, part) {
			return &r.Budget[i]
		}
	}
	return nil
}

func markdown(r *result) string {
	var b strings.Builder
	writeMarkdown(&b, r)
	return b.String()
}

// TestBudgetShortWindow: under minSlopeWindow the slope budgets are information only, with their raw value; a long
// enough idle window judges them. The per-10k-events growth is judged whatever the window.
func TestBudgetShortWindow(t *testing.T) {
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	const short = "information only: window too short"
	r := &result{Scenario: "load", WindowStart: start, WindowEnd: start.Add(5 * time.Minute),
		Daemon: daemonSummary{PrivSlope: 42.5, HeapSlope: 17.25, PeakPrivMB: 60},
		Load:   &loadSummary{Tasks: 8, EventsStored: 30000, PrivPer10kEvts: 7}}
	r.Budget = r.budget()
	for _, part := range []string{"private bytes slope", "live heap slope"} {
		if c := checkNamed(t, r, part); c == nil || !c.Info || c.Note != short {
			t.Fatalf("%s = %+v", part, c)
		}
	}
	if c := checkNamed(t, r, "private bytes slope"); c.Value != 42.5 {
		t.Errorf("raw slope not kept: %+v", c)
	}
	if c := checkNamed(t, r, "per 10k events"); c == nil || c.OK || c.Info {
		t.Errorf("growth per 10k events = %+v", c)
	}
	md := markdown(r)
	if !strings.Contains(md, "("+short+") | 10 | 42.5 | info |") || !strings.Contains(md, "| 7 | **over** |") {
		_, table, _ := strings.Cut(md, "## Budget")
		t.Errorf("budget table:%s", table)
	}

	// A long enough idle window judges the slopes; a long load window keeps them as information (retained heap).
	r.Scenario, r.Load, r.WindowEnd = "idle", nil, start.Add(20*time.Minute)
	r.Budget = r.budget()
	for _, part := range []string{"private bytes slope", "live heap slope"} {
		if c := checkNamed(t, r, part); c == nil || c.OK || c.Info {
			t.Errorf("20-minute idle window, %s = %+v", part, c)
		}
	}
	r.Scenario = "load"
	r.Budget = r.budget()
	if c := checkNamed(t, r, "live heap slope"); c == nil || !c.Info || !strings.Contains(c.Note, "retained heap") {
		t.Errorf("20-minute load window = %+v", c)
	}
}

// TestRecorderTicks: the first tick after the start and the first after each hour boundary; a tick predicted just
// before a boundary is taken with the next one.
func TestRecorderTicks(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*3600)
	at := func(h, m, s int) time.Time { return time.Date(2026, 11, 8, h, m, s, 0, loc) }
	// Ticks at 10:58:30, 11:03:30, 11:08:30 ...: 10:58:30 starts hour 10, 11:03:30 hour 11.
	got := recorderTicks(at(10, 57, 30), at(10, 0, 0), at(11, 30, 0), loc)
	if want := []time.Time{at(10, 58, 30), at(11, 3, 30)}; !equalTimes(got, want) {
		t.Errorf("ticks = %v, want %v", got, want)
	}
	if got := recorderTicks(at(10, 57, 30), at(11, 0, 0), at(11, 30, 0), loc); !equalTimes(got,
		[]time.Time{at(11, 3, 30)}) {
		t.Errorf("window from 11:00 = %v", got)
	}
	// Ticks at 10:54:50, 10:59:50 (10 s before the boundary), 11:04:50.
	if got, want := recorderTicks(at(10, 53, 50), at(10, 56, 0), at(11, 30, 0), loc),
		[]time.Time{at(10, 59, 50), at(11, 4, 50)}; !equalTimes(got, want) {
		t.Errorf("edge ticks = %v, want %v", got, want)
	}
	if got := recorderTicks(time.Time{}, at(10, 0, 0), at(11, 0, 0), loc); got != nil {
		t.Errorf("no start = %v", got)
	}
}

func equalTimes(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// TestIdleCostSplit: a floor of 10 ms per 5 s sample, a recorder tick that costs 0.5 s and a quota cycle of two
// reads that cost 0.3 s each. The tick stays out of the floor and the cycle.
func TestIdleCostSplit(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 11, 0, 0, 0, time.UTC)
	tick := t0.Add(3*time.Minute + 30*time.Second)
	read1, read2 := t0.Add(10*time.Minute), t0.Add(10*time.Minute+time.Second)
	var win []*sample
	cpu := time.Duration(0)
	for at := t0; !at.After(t0.Add(30 * time.Minute)); at = at.Add(5 * time.Second) {
		if at.After(t0) {
			cpu += 10 * time.Millisecond
		}
		switch at {
		case tick.Add(5 * time.Second):
			cpu += 500 * time.Millisecond
		case read1.Add(5 * time.Second):
			cpu += 600 * time.Millisecond
		}
		win = append(win, &sample{At: at, DaemonCPU: cpu})
	}
	runs := []spawnRun{{Role: "quota-reader", At: read1}, {Role: "quota-reader", At: read2}}
	x := idleCost(nil, win, runs, []time.Time{tick})
	if x == nil {
		t.Fatal("no idle split")
	}
	if x.FloorPercent != 0.2 || x.Cycles != 1 || x.QuotaReads != 2 || x.Ticks != 1 {
		t.Fatalf("split = %+v", x)
	}
	if x.CPUPerTick != 0.5 || x.TickSeconds != 25 {
		t.Errorf("tick = %v s over %v s of samples", x.CPUPerTick, x.TickSeconds)
	}
	if x.CPUPerCycle != 0.6 || x.CPUPerRead != 0.3 || x.BusySeconds != 30 {
		t.Errorf("cycle = %v s, read %v s, %v s of samples", x.CPUPerCycle, x.CPUPerRead, x.BusySeconds)
	}
	md := markdown(&result{Scenario: "idle", Idle: x})
	if !strings.Contains(md, "Per quota-reader spawn: 0.3000 daemon CPU s") || !strings.Contains(md, "1 ticks") {
		t.Errorf("idle report:\n%s", md)
	}
}
