//go:build bench

package bench

import (
	"os"
	"sort"
	"time"
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

// idleSummary splits the idle CPU: the floor between quota cycles, the cost of one cycle, and the window's average.
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
	runTimes     []time.Time
}

const (
	// cycleGap separates two quota cycles: the reads of one cycle start within seconds of each other.
	cycleGap = time.Minute
	// cycleTail is how long after a run the daemon is still busy with it (storing, publishing).
	cycleTail = 20 * time.Second
)

// idleCost computes the idle split from the samples of the window (and the one before) and the runs. A sample
// interval is part of a cycle when a run started in it or up to cycleTail before it.
func idleCost(base *sample, win []*sample, runs []spawnRun) *idleSummary {
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
		if last.IsZero() || r.At.Sub(last) > cycleGap {
			x.Cycles++
			x.CycleStarts = append(x.CycleStarts, r.At.Format("15:04:05"))
		}
		last = r.At
	}
	var busyCPU, quietCPU, busyTree, quietTree time.Duration
	var busySecs, quietSecs float64
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
		if x.busy(a.At, b.At) {
			busyCPU, busyTree, busySecs = busyCPU+cpu, busyTree+tcpu, busySecs+secs
		} else {
			quietCPU, quietTree, quietSecs = quietCPU+cpu, quietTree+tcpu, quietSecs+secs
		}
	}
	x.BusySeconds, x.QuietSeconds = round(busySecs, 1), round(quietSecs, 1)
	if quietSecs > 0 {
		x.FloorPercent = round(100*quietCPU.Seconds()/quietSecs, 4)
		if tree {
			x.TreeFloor = round(100*quietTree.Seconds()/quietSecs, 4)
		}
	}
	if x.Cycles > 0 {
		// A cycle's cost is what its intervals used above the floor.
		floor, treeFloor := x.FloorPercent/100, x.TreeFloor/100
		x.CPUPerCycle = round((busyCPU.Seconds()-floor*busySecs)/float64(x.Cycles), 3)
		if tree {
			x.TreePerCycle = round((busyTree.Seconds()-treeFloor*busySecs)/float64(x.Cycles), 3)
		}
	}
	return x
}

// busy reports whether a run started in (a, b] or up to cycleTail before a.
func (x *idleSummary) busy(a, b time.Time) bool {
	for _, t := range x.runTimes {
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
	p("\n")
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
