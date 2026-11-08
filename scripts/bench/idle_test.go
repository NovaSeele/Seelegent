//go:build bench

package bench

import (
	"os"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/usage"
)

// spawnRun is one quota read or gh run, by its start time.
type spawnRun struct {
	Role  string    `json:"role"`
	At    time.Time `json:"at"`
	CPUMs float64   `json:"cpuMs,omitempty"`
}

// spawnRuns lists the quota reads and gh runs of the spawn log in time order.
func spawnRuns(spawns []spawnRecord) []spawnRun {
	var out []spawnRun
	for _, sp := range spawns {
		if sp.Role == "quota-reader" || sp.Role == "gh" {
			out = append(out, spawnRun{Role: sp.Role, At: sp.At, CPUMs: sp.CPUMs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// idleSummary splits the idle CPU: the floor between quota cycles and the daemon's periodic work, the cost of one
// cycle, of one quota read and of one usage recorder tick, and the window's average.
type idleSummary struct {
	// Cycles are the quota cycles that started inside the window (runs closer than cycleGap are one cycle).
	Cycles       int      `json:"quotaCycles"`
	CycleStarts  []string `json:"cycleStarts"`
	FloorPercent float64  `json:"floorDaemonCpuPercentOfCore"`
	CPUPerCycle  float64  `json:"daemonCpuSecondsPerCycle"`
	TreeFloor    float64  `json:"floorTreeCpuPercentOfCore,omitempty"`
	TreePerCycle float64  `json:"treeCpuSecondsPerCycle,omitempty"`
	BusySeconds  float64  `json:"cycleSampleSeconds"`
	QuietSeconds float64  `json:"floorSampleSeconds"`
	Runs         int      `json:"runsInWindow"`
	// QuotaReads are the quota-reader runs in the window; CPUPerRead is the daemon's CPU above the floor in the
	// cycles' intervals per quota read (the few gh runs that fall in a cycle are included).
	QuotaReads int     `json:"quotaReadsInWindow"`
	CPUPerRead float64 `json:"daemonCpuSecondsPerQuotaRead"`
	// Ticks are the usage recorder's hourly snapshot and prune runs in the window (recorderTicks); their intervals
	// are kept out of the floor and the cycles. CPUPerTick is the daemon's CPU above the floor per tick.
	Ticks       int      `json:"recorderTicks"`
	TickTimes   []string `json:"recorderTickTimes,omitempty"`
	TickSeconds float64  `json:"recorderSampleSeconds"`
	CPUPerTick  float64  `json:"daemonCpuSecondsPerRecorderTick"`
	runTimes    []time.Time
	tickTimes   []time.Time
}

// recorderTicks are the usage recorder's ticks in [from, to] that do work: the first one after the daemon's start
// and the first one after each local hour boundary (it writes the hour's snapshot and prunes; the other ticks
// return at once). The recorder runs usage.DefaultStartDelay after the start, then every usage.DefaultCheck, so
// its ticks are predicted from the daemon's start time. A real tick is a little later (the daemon's own start, and
// each tick's time adds up), which near's cycleTail covers; a tick predicted less than cycleTail before an hour
// boundary may really run after it, so both it and the next one are taken. A zero start gives none.
func recorderTicks(start, from, to time.Time, loc *time.Location) []time.Time {
	if start.IsZero() || !to.After(from) {
		return nil
	}
	hourOf := func(t time.Time) string { return t.In(loc).Format("2006-01-02T15 -0700") }
	var out []time.Time
	hour := ""
	for t := start.Add(usage.DefaultStartDelay); !t.After(to); t = t.Add(usage.DefaultCheck) {
		key := hourOf(t)
		edge := hourOf(t.Add(cycleTail)) != key
		if (key != hour || edge) && !t.Before(from) {
			out = append(out, t)
		}
		hour = key
		if edge {
			hour = ""
		}
	}
	return out
}

const (
	// cycleGap separates two quota cycles: the reads of one cycle start within seconds of each other.
	cycleGap = time.Minute
	// cycleTail is how long after a run the daemon is still busy with it (storing, publishing).
	cycleTail = 20 * time.Second
)

// idleCost computes the idle split from the samples of the window (and the one before), the runs and the
// recorder's ticks. A sample interval is part of a cycle when a run started in it or up to cycleTail before it, else
// part of a recorder tick when a tick did; the floor is the other intervals.
func idleCost(base *sample, win []*sample, runs []spawnRun, ticks []time.Time) *idleSummary {
	pts := win
	if base != nil {
		pts = append([]*sample{base}, win...)
	}
	if len(pts) < 2 {
		return nil
	}
	from, to := pts[0].At, pts[len(pts)-1].At
	x := &idleSummary{}
	var last time.Time
	for _, r := range runs {
		if r.At.Before(from) || r.At.After(to) {
			continue
		}
		x.Runs++
		x.runTimes = append(x.runTimes, r.At)
		if r.Role == "quota-reader" {
			x.QuotaReads++
		}
		if last.IsZero() || r.At.Sub(last) > cycleGap {
			x.Cycles++
			x.CycleStarts = append(x.CycleStarts, r.At.Format("15:04:05"))
		}
		last = r.At
	}
	for _, t := range ticks {
		if !t.Before(from) && !t.After(to) {
			x.Ticks++
			x.tickTimes = append(x.tickTimes, t)
			x.TickTimes = append(x.TickTimes, t.Format("15:04:05"))
		}
	}
	var busyCPU, quietCPU, busyTree, quietTree, tickCPU time.Duration
	var busySecs, quietSecs, tickSecs float64
	tree := true
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		cpu := b.DaemonCPU - a.DaemonCPU
		var tcpu time.Duration
		if a.Job != nil && b.Job != nil {
			tcpu = b.Job.CPU - a.Job.CPU
		} else {
			tree = false
		}
		secs := b.At.Sub(a.At).Seconds()
		switch {
		case near(x.runTimes, a.At, b.At):
			busyCPU, busyTree, busySecs = busyCPU+cpu, busyTree+tcpu, busySecs+secs
		case near(x.tickTimes, a.At, b.At):
			tickCPU, tickSecs = tickCPU+cpu, tickSecs+secs
		default:
			quietCPU, quietTree, quietSecs = quietCPU+cpu, quietTree+tcpu, quietSecs+secs
		}
	}
	x.BusySeconds, x.QuietSeconds, x.TickSeconds = round(busySecs, 1), round(quietSecs, 1), round(tickSecs, 1)
	if quietSecs > 0 {
		x.FloorPercent = round(100*quietCPU.Seconds()/quietSecs, 4)
		if tree {
			x.TreeFloor = round(100*quietTree.Seconds()/quietSecs, 4)
		}
	}
	// A cycle's (a read's, a tick's) cost is what its intervals used above the floor.
	floor, treeFloor := x.FloorPercent/100, x.TreeFloor/100
	if x.Cycles > 0 {
		x.CPUPerCycle = round((busyCPU.Seconds()-floor*busySecs)/float64(x.Cycles), 3)
		if tree {
			x.TreePerCycle = round((busyTree.Seconds()-treeFloor*busySecs)/float64(x.Cycles), 3)
		}
	}
	if x.QuotaReads > 0 {
		x.CPUPerRead = round((busyCPU.Seconds()-floor*busySecs)/float64(x.QuotaReads), 4)
	}
	if x.Ticks > 0 {
		x.CPUPerTick = round((tickCPU.Seconds()-floor*tickSecs)/float64(x.Ticks), 4)
	}
	return x
}

// near reports whether one of times lies in (a, b] or up to cycleTail before a.
func near(times []time.Time, a, b time.Time) bool {
	for _, t := range times {
		if t.After(a.Add(-cycleTail)) && !t.After(b) {
			return true
		}
	}
	return false
}

// writeIdle writes the idle split.
func writeIdle(p func(string, ...any), r *result) {
	x := r.Idle
	if x == nil {
		return
	}
	p("\n## Idle split\n\n%d quota cycles in the window (starting %v), %d runs. Daemon floor between cycles %.4f %% "+
		"of one core (%.0f s of samples); one cycle costs the daemon %.3f CPU s above the floor (%.0f s of samples); "+
		"window average %.4f %%.", x.Cycles, x.CycleStarts, x.Runs, x.FloorPercent, x.QuietSeconds, x.CPUPerCycle,
		x.BusySeconds, r.Daemon.CPUPercent)
	if x.TreeFloor > 0 || x.TreePerCycle > 0 {
		p(" Whole tree: floor %.4f %%, %.3f CPU s per cycle.", x.TreeFloor, x.TreePerCycle)
	}
	p("\n\nPer quota-reader spawn: %.4f daemon CPU s above the floor (%d reads). Usage recorder (hourly snapshot and "+
		"prune, kept out of the floor): %d ticks %v, %.4f daemon CPU s each above the floor (%.0f s of samples).\n",
		x.CPUPerRead, x.QuotaReads, x.Ticks, x.TickTimes, x.CPUPerTick, x.TickSeconds)
	if len(r.Runs) > 0 {
		p("\nRuns (start times): ")
		for i, run := range r.Runs {
			if i > 0 {
				p(", ")
			}
			p("%s %s", run.Role, run.At.Format("15:04:05"))
		}
		p("\n")
	}
}

// otherProcess is a program outside the bench by its CPU over the window.
type otherProcess struct {
	Name       string  `json:"name"`
	PID        int     `json:"pid"`
	CPUSeconds float64 `json:"cpuSeconds"`
	CPUPercent float64 `json:"cpuPercentOfCore"`
}

// procCPU is a process's CPU time at one moment.
type procCPU struct {
	name    string
	started time.Time
	cpu     time.Duration
}

// cpuByProcess reads the CPU time of every process this user can open.
func cpuByProcess() map[int]procCPU {
	list, err := listProcs()
	if err != nil {
		return nil
	}
	out := make(map[int]procCPU, len(list))
	for pid, e := range list {
		st, err := procStats(pid)
		if err != nil {
			continue
		}
		started, _ := startTime(pid)
		out[pid] = procCPU{name: e.Name, started: started, cpu: st.CPU}
	}
	return out
}

// topOther ranks the processes outside the bench (its own process and the daemon's tree) by the CPU they used
// between two readings; a pid reused in between is skipped.
func topOther(start, end map[int]procCPU, skip map[int]bool, window time.Duration, n int) []otherProcess {
	var out []otherProcess
	for pid, b := range end {
		a, ok := start[pid]
		if !ok || skip[pid] || !a.started.Equal(b.started) || b.cpu <= a.cpu {
			continue
		}
		d := (b.cpu - a.cpu).Seconds()
		out = append(out, otherProcess{Name: b.name, PID: pid, CPUSeconds: round(d, 1),
			CPUPercent: round(100*d/window.Seconds(), 1)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CPUSeconds > out[j].CPUSeconds })
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// treePIDs are the bench's own pid and every process of the daemon's tree.
func (s *sampler) treePIDs() map[int]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]bool{os.Getpid(): true}
	for _, t := range s.all {
		out[t.PID] = true
	}
	return out
}
