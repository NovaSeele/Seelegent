//go:build bench

package bench

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"runtime"
	"runtime/pprof"
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

// TestBudgetShortWindow: under minSlopeWindow the slope budgets are reported, not judged; the per-10k-events
// growth is judged whatever the window.
func TestBudgetShortWindow(t *testing.T) {
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	r := &result{Scenario: "load", WindowStart: start, WindowEnd: start.Add(5 * time.Minute),
		Daemon: daemonSummary{PrivSlope: 42.5, HeapSlope: 17.25, PeakPrivMB: 60},
		Load:   &loadSummary{EventsStored: 30000, PrivPer10kEvts: 7}}
	r.Budget = r.budget()
	for _, part := range []string{"private bytes slope", "live heap slope"} {
		c := checkNamed(t, r, part)
		if c == nil || !c.OK || c.Verdict != "n/a: window too short" {
			t.Fatalf("%s = %+v", part, c)
		}
	}
	if c := checkNamed(t, r, "private bytes slope"); c.Value != 42.5 {
		t.Errorf("raw slope not kept: %+v", c)
	}
	if c := checkNamed(t, r, "per 10k events"); c == nil || c.OK || c.Verdict != "over" {
		t.Errorf("growth per 10k events = %+v", c)
	}
	md := markdown(r)
	if !strings.Contains(md, "| 42.5 | n/a: window too short |") || !strings.Contains(md, "| 7 | **over** |") {
		_, table, _ := strings.Cut(md, "## Budget")
		t.Errorf("budget table:%s", table)
	}

	// A long enough window judges the slopes.
	r.WindowEnd = start.Add(20 * time.Minute)
	r.Budget = r.budget()
	if c := checkNamed(t, r, "private bytes slope"); c == nil || c.OK || c.Verdict != "over" {
		t.Errorf("20-minute window: %+v", c)
	}
	if c := checkNamed(t, r, "live heap slope"); c == nil || c.OK || c.Verdict != "over" {
		t.Errorf("20-minute window: %+v", c)
	}
}

// TestRetainedHeap: end - start after a forced GC, per 10k events, against 1 MB.
func TestRetainedHeap(t *testing.T) {
	start := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	newResult := func(startHeap, endHeap int64, ok bool) *result {
		r := &result{Scenario: "load", WindowStart: start, WindowEnd: start.Add(20 * time.Minute),
			Load: &loadSummary{EventsStored: 20000}}
		r.Load.setRetained(startHeap, endHeap, ok)
		r.Budget = r.budget()
		return r
	}
	r := newResult(10*mib, 13*mib, true)
	if l := r.Load; !l.RetainedMeasured || l.RetainedHeapMB != 3 || l.RetainedPer10k != 1.5 ||
		l.HeapAfterGCStartMB != 10 || l.HeapAfterGCEndMB != 13 {
		t.Fatalf("load = %+v", l)
	}
	if c := checkNamed(t, r, "retained heap"); c == nil || c.OK || c.Limit != 1 || c.Value != 1.5 {
		t.Fatalf("over budget = %+v", c)
	}
	md := markdown(r)
	if !strings.Contains(md, "+3.00 MB retained = 1.500 MB per 10k events") ||
		!strings.Contains(md, heapStartFile) || !strings.Contains(md, "| 1.5 | **over** |") {
		t.Errorf("report:\n%s", md)
	}

	if c := checkNamed(t, newResult(10*mib, 11*mib, true), "retained heap"); c == nil || !c.OK || c.Value != 0.5 {
		t.Errorf("within budget = %+v", c)
	}
	r = newResult(0, 0, false)
	if c := checkNamed(t, r, "retained heap"); c != nil || r.Load.RetainedMeasured {
		t.Errorf("unmeasured = %+v, %+v", c, r.Load)
	}
	if md := markdown(r); !strings.Contains(md, "Retained heap after a forced GC: not measured") {
		t.Errorf("report without the profiles:\n%s", md)
	}
}

// heapProfile encodes a minimal heap profile.proto: the four heap sample types and samples of [alloc_objects,
// alloc_space, inuse_objects, inuse_space]; packed false writes the values unpacked.
func heapProfile(samples [][4]int64, packed bool) []byte {
	key := func(b []byte, field, wire int) []byte { return binary.AppendUvarint(b, uint64(field<<3|wire)) }
	bytesField := func(b []byte, field int, v []byte) []byte {
		b = key(b, field, 2)
		return append(binary.AppendUvarint(b, uint64(len(v))), v...)
	}
	strs := []string{"", "alloc_objects", "count", "alloc_space", "bytes", "inuse_objects", "inuse_space"}
	var p []byte
	for _, st := range [][2]uint64{{1, 2}, {3, 4}, {5, 2}, {6, 4}} {
		var vt []byte
		vt = binary.AppendUvarint(key(vt, 1, 0), st[0])
		vt = binary.AppendUvarint(key(vt, 2, 0), st[1])
		p = bytesField(p, 1, vt)
	}
	for i, s := range samples {
		var sm []byte
		sm = bytesField(sm, 1, binary.AppendUvarint(nil, uint64(i+1)))
		if packed {
			var vals []byte
			for _, v := range s {
				vals = binary.AppendUvarint(vals, uint64(v))
			}
			sm = bytesField(sm, 2, vals)
		} else {
			for _, v := range s {
				sm = binary.AppendUvarint(key(sm, 2, 0), uint64(v))
			}
		}
		p = bytesField(p, 2, sm)
	}
	p = binary.AppendUvarint(key(p, 9, 0), 1762560000) // time_nanos: a field the reader skips
	for _, s := range strs {
		p = bytesField(p, 6, []byte(s))
	}
	return p
}

func TestInuseSpace(t *testing.T) {
	samples := [][4]int64{{10, 4096, 2, 1000}, {7, 9000, 3, 2500}}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(heapProfile(samples, true))
	_ = zw.Close()
	if n, err := inuseSpace(gz.Bytes()); err != nil || n != 3500 {
		t.Errorf("gzipped, packed = %d, %v", n, err)
	}
	if n, err := inuseSpace(heapProfile(samples, false)); err != nil || n != 3500 {
		t.Errorf("plain, unpacked = %d, %v", n, err)
	}
	if _, err := inuseSpace([]byte{0x0a, 0x05, 0x01}); err == nil {
		t.Error("a cut profile was read")
	}
	if _, err := inuseSpace(nil); err == nil {
		t.Error("an empty profile has inuse_space")
	}
	// A real heap profile, as the daemon's /debug/pprof/heap writes it.
	keep := make([][]byte, 64)
	for i := range keep {
		keep[i] = make([]byte, 64<<10)
	}
	runtime.GC()
	var prof bytes.Buffer
	if err := pprof.Lookup("heap").WriteTo(&prof, 0); err != nil {
		t.Fatal(err)
	}
	if n, err := inuseSpace(prof.Bytes()); err != nil || n <= 0 {
		t.Errorf("runtime heap profile = %d, %v", n, err)
	}
	runtime.KeepAlive(keep)
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
