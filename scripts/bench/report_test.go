//go:build bench

package bench

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const mib = 1 << 20

// roleSummary is one role of the daemon's tree over the measured window.
type roleSummary struct {
	Role         string  `json:"role"`
	PeakCount    int     `json:"peakCount"`
	MeanWSMB     float64 `json:"meanWSMB"`
	PeakWSMB     float64 `json:"peakWSMB"`
	MeanPrivMB   float64 `json:"meanPrivateMB"`
	PeakPrivMB   float64 `json:"peakPrivateMB"`
	CPUSeconds   float64 `json:"cpuSeconds"`
	CPUPercent   float64 `json:"cpuPercentOfCore"`
	PeakHandles  int     `json:"peakHandles"`
	PeakThreads  int     `json:"peakThreads"`
	Spawns       int     `json:"spawns"`
	SeenInRun    int     `json:"seenInRun"`
	SpawnsPerHr  float64 `json:"spawnsPerHour"`
	ProcPeakWSMB float64 `json:"processPeakWSMB"`
}

// daemonSummary is the daemon process itself over the measured window.
type daemonSummary struct {
	MeanWSMB       float64 `json:"meanWSMB"`
	PeakWSMB       float64 `json:"peakWSMB"`
	OSPeakWSMB     float64 `json:"osPeakWSMB"`
	MeanPrivMB     float64 `json:"meanPrivateMB"`
	PeakPrivMB     float64 `json:"peakPrivateMB"`
	CPUSeconds     float64 `json:"cpuSeconds"`
	CPUPercent     float64 `json:"cpuPercentOfCore"`
	Handles        int     `json:"peakHandles"`
	Threads        int     `json:"peakThreads"`
	GoroutinesMean float64 `json:"goroutinesMean"`
	GoroutinesMax  int     `json:"goroutinesMax"`
	HeapAllocMean  float64 `json:"heapAllocMeanMB"`
	HeapAllocMax   float64 `json:"heapAllocMaxMB"`
	HeapLiveMean   float64 `json:"heapLiveMeanMB"`
	HeapLiveMax    float64 `json:"heapLiveMaxMB"`
	HeapSysMax     float64 `json:"heapSysMaxMB"`
	SysMax         float64 `json:"goSysMaxMB"`
	AllocMBps      float64 `json:"allocMBPerSecond"`
	MallocsPerSec  float64 `json:"mallocsPerSecond"`
	GCPerMin       float64 `json:"gcPerMinute"`
	WSGrowthMB     float64 `json:"wsGrowthMB"`
	PrivGrowthMB   float64 `json:"privateGrowthMB"`
	HeapGrowthMB   float64 `json:"heapGrowthMB"`
	// WSSlope and HeapSlope are least-squares slopes over the second half of the window, in MB per hour: a
	// leak shows there once the start-up growth is over. HeapSlope is the live heap's (marked at the last GC), not
	// the allocated heap's, whose GC sawtooth swamps a few minutes of samples.
	WSSlope   float64 `json:"wsSlopeSecondHalfMBPerHour"`
	PrivSlope float64 `json:"privateSlopeSecondHalfMBPerHour"`
	HeapSlope float64 `json:"heapSlopeSecondHalfMBPerHour"`
}

// setupPhase is the cost of one setup phase (G1): the daemon's CPU and the whole tree's (Windows job).
type setupPhase struct {
	Name             string  `json:"name"`
	Unit             string  `json:"unit"`
	Count            int     `json:"count"`
	Seconds          float64 `json:"seconds"`
	DaemonCPUSeconds float64 `json:"daemonCpuSeconds"`
	DaemonCPUPerUnit float64 `json:"daemonCpuSecondsPerUnit"`
	TreeCPUSeconds   float64 `json:"treeCpuSeconds,omitempty"`
	TreeCPUPerUnit   float64 `json:"treeCpuSecondsPerUnit,omitempty"`
	ProcessesStarted int     `json:"processesStarted,omitempty"`
}

// machineSummary is the machine profile and its state over the measured window: the defaults are computed per
// machine, and StartTree's cost grows with the machine's thread count.
type machineSummary struct {
	OS             string  `json:"os"`
	OSVersion      string  `json:"osVersion"`
	LogicalCPUs    int     `json:"logicalCpus"`
	TotalMemMB     float64 `json:"totalMemoryMB"`
	CommitLimitMB  float64 `json:"commitLimitMB"`
	FreeMemMinMB   float64 `json:"freeMemoryMinMB"`
	FreeMemMeanMB  float64 `json:"freeMemoryMeanMB"`
	FreeCommMinMB  float64 `json:"freeCommitMinMB"`
	FreeCommMeanMB float64 `json:"freeCommitMeanMB"`
	ThreadsMean    float64 `json:"systemThreadsMean"`
	ThreadsMax     int     `json:"systemThreadsMax"`
	ProcessesMean  float64 `json:"systemProcessesMean"`
	// TopOther are the programs outside the bench that used the most CPU during the window.
	TopOther []otherProcess `json:"topOtherByCpu"`
}

// treeSummary is the Windows job's accounting of the whole tree over the window (G4): exact CPU, short-lived
// processes included, and every process started.
type treeSummary struct {
	CPUSeconds     float64 `json:"cpuSeconds"`
	CPUPercent     float64 `json:"cpuPercentOfCore"`
	ScanCPUSeconds float64 `json:"pidScanCpuSeconds"`
	Started        int     `json:"processesStarted"`
	StartedPerHour float64 `json:"processesPerHour"`
	ActiveMax      int     `json:"activeMax"`
	PeakCommitMB   float64 `json:"peakJobCommitMB"`
	RunStarted     int     `json:"processesStartedWholeRun"`
	RunCPUSeconds  float64 `json:"cpuSecondsWholeRun"`
}

type dbSummary struct {
	DBStartMB  float64 `json:"dbStartMB"`
	DBEndMB    float64 `json:"dbEndMB"`
	WALStartMB float64 `json:"walStartMB"`
	WALEndMB   float64 `json:"walEndMB"`
	WALMaxMB   float64 `json:"walMaxMB"`
}

type spawnSummary struct {
	Role         string  `json:"role"`
	Count        int     `json:"count"`
	InWindow     int     `json:"inWindow"`
	PerHour      float64 `json:"perHour"`
	MeanMs       float64 `json:"meanMs,omitempty"`
	MeanCPUMs    float64 `json:"meanCpuMs,omitempty"`
	MeanPeakWSMB float64 `json:"meanPeakWSMB,omitempty"`
	MaxPeakWSMB  float64 `json:"maxPeakWSMB,omitempty"`
}

// budgetCheck is one row of the budget. An Info row is shown with its limit but not judged: OK still says
// whether the value is under the limit, and Note says why the row is information only.
type budgetCheck struct {
	Name  string  `json:"name"`
	Limit float64 `json:"limit"`
	Value float64 `json:"value"`
	OK    bool    `json:"ok"`
	Info  bool    `json:"info,omitempty"`
	Note  string  `json:"note,omitempty"`
}

// retainedHeap is the load scenario's retained-heap check: the daemon's live heap after forced GCs just before the
// load tasks start (after the warmup) and again once every load task ended, the SSE clients and pollers stopped and
// SettleSeconds passed. The delta is what the load left behind; unlike the slopes it does not count the state of
// attempts that are still running. The SSE replay ring keeps the last frames for 10 minutes, so at the end it still
// holds the load's frames: ExRingDeltaMB, the budgeted value, is the delta without the ring's heap at either
// reading (the raw delta when the daemon does not report its ring: RingUnknown).
type retainedHeap struct {
	StartMB         float64 `json:"startMB"`
	EndMB           float64 `json:"endMB"`
	DeltaMB         float64 `json:"deltaMB"`
	StartRingMB     float64 `json:"startSseRingMB"`
	EndRingMB       float64 `json:"endSseRingMB"`
	StartRingFrames int     `json:"startSseRingFrames"`
	EndRingFrames   int     `json:"endSseRingFrames"`
	ExRingDeltaMB   float64 `json:"deltaExcludingSseRingMB"`
	RingUnknown     bool    `json:"sseRingUnknown,omitempty"`
	SettleSeconds   float64 `json:"settleSeconds"`
	Note            string  `json:"note,omitempty"`
	// Error is why the end could not be read; the budget row is then left out.
	Error string `json:"error,omitempty"`
	// start is the first reading (the ring in bytes, for the end's arithmetic).
	start heapReading
}

// goSysBreakdown is where Go's Sys goes at the end of the run (runtime.MemStats, MB): the heap, and what lies outside
// it (goroutine stacks, span structures, per-P caches, GC metadata, profiling buckets, the rest). The *Sys fields
// add up to Sys; HeapReleased is the part of HeapSys returned to the OS.
type goSysBreakdown struct {
	SysMB          float64 `json:"sysMB"`
	HeapSysMB      float64 `json:"heapSysMB"`
	HeapInuseMB    float64 `json:"heapInuseMB"`
	HeapReleasedMB float64 `json:"heapReleasedMB"`
	StackSysMB     float64 `json:"stackSysMB"`
	MSpanSysMB     float64 `json:"mSpanSysMB"`
	MCacheSysMB    float64 `json:"mCacheSysMB"`
	GCSysMB        float64 `json:"gcSysMB"`
	BuckHashSysMB  float64 `json:"buckHashSysMB"`
	OtherSysMB     float64 `json:"otherSysMB"`
}

func breakdown(m memStatsSys) *goSysBreakdown {
	f := func(b uint64) float64 { return round(mb(b), 2) }
	return &goSysBreakdown{SysMB: f(m.Sys), HeapSysMB: f(m.HeapSys), HeapInuseMB: f(m.HeapInuse),
		HeapReleasedMB: f(m.HeapReleased), StackSysMB: f(m.StackSys), MSpanSysMB: f(m.MSpanSys),
		MCacheSysMB: f(m.MCacheSys), GCSysMB: f(m.GCSys), BuckHashSysMB: f(m.BuckHashSys), OtherSysMB: f(m.OtherSys)}
}

// measured reports whether both ends were read.
func (x *retainedHeap) measured() bool { return x != nil && x.Error == "" }

type loadSummary struct {
	Tasks          int                     `json:"tasks"`
	EventsStored   int64                   `json:"eventsStored"`
	EventsPerSec   float64                 `json:"eventsPerSecond"`
	SSEClients     int                     `json:"sseClients"`
	SSEConnects    int64                   `json:"sseConnects"`
	SSEFrames      int64                   `json:"sseFrames"`
	SSEFramesPerS  float64                 `json:"sseFramesPerSecond"`
	SSEMB          float64                 `json:"sseMB"`
	API            map[string]latencyStats `json:"api"`
	WSMBPer10kEvts float64                 `json:"daemonWSGrowthMBPer10kEvents"`
	PrivPer10kEvts float64                 `json:"daemonPrivateGrowthMBPer10kEvents"`
}

// result is one bench run (result.json).
type result struct {
	Scenario string            `json:"scenario"`
	Commit   string            `json:"commit"`
	GOOS     string            `json:"goos"`
	GOARCH   string            `json:"goarch"`
	NumCPU   int               `json:"numCPU"`
	Options  map[string]string `json:"options"`
	Started  time.Time         `json:"started"`
	// DaemonStarted is when the bench started the daemon: the usage recorder's ticks are timed from it.
	DaemonStarted time.Time       `json:"daemonStarted,omitzero"`
	WindowStart   time.Time       `json:"windowStart"`
	WindowEnd     time.Time       `json:"windowEnd"`
	Machine       machineSummary  `json:"machine"`
	Setup         []setupPhase    `json:"setup"`
	Daemon        daemonSummary   `json:"daemon"`
	Tree          *treeSummary    `json:"tree,omitempty"`
	TreeFinal     *jobStat        `json:"treeFinal,omitempty"`
	Roles         []roleSummary   `json:"roles"`
	Spawns        []spawnSummary  `json:"spawnLog"`
	Runs          []spawnRun      `json:"runs"`
	Idle          *idleSummary    `json:"idle,omitempty"`
	DB            dbSummary       `json:"db"`
	Load          *loadSummary    `json:"load,omitempty"`
	Retained      *retainedHeap   `json:"retainedHeap,omitempty"`
	GoSysEnd      *goSysBreakdown `json:"goSysEnd,omitempty"`
	Budget        []budgetCheck   `json:"budget"`
	Errors        map[string]int  `json:"sampleErrors,omitempty"`
	Processes     []*tracked      `json:"processes"`
	Samples       []sample        `json:"samples"`
}

func optionsOf(o *options) map[string]string {
	return map[string]string{"duration": o.duration.String(), "warmup": o.warmup.String(),
		"cpuprofile": o.cpuProfile.String(), "interval": o.interval.String(), "accounts": fmt.Sprint(o.accounts),
		"tasks": fmt.Sprint(o.tasks), "rate": fmt.Sprint(o.rate), "sse": fmt.Sprint(o.sse), "poll": o.poll.String(),
		"seedTasks": fmt.Sprint(o.seedTasks), "setupProfile": o.setupProf.String()}
}

func mb(b uint64) float64 { return float64(b) / mib }

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// cpuIn is a process's CPU time spent inside [from, to], from its cpu points.
func cpuIn(t *tracked, from, to time.Time) time.Duration {
	var before, last time.Duration
	seen := false
	for _, p := range t.CPUAt {
		switch {
		case !p.At.After(from):
			before = p.CPU
		case !p.At.After(to):
			last, seen = p.CPU, true
		}
	}
	if !seen {
		return 0
	}
	return last - before
}

// slopePerHour is the least-squares slope of v over the samples' times, per hour.
func slopePerHour(win []*sample, v func(*sample) float64) float64 {
	if len(win) < 3 {
		return 0
	}
	t0 := win[0].At
	var n, sx, sy, sxx, sxy float64
	for _, smp := range win {
		x, y := smp.At.Sub(t0).Hours(), v(smp)
		n, sx, sy, sxx, sxy = n+1, sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0
	}
	return (n*sxy - sx*sy) / den
}

// liveHeap is the sample's live heap, or its allocated heap from a daemon without /gc/heap/live.
func liveHeap(x *sample) uint64 {
	if x.Go.HeapLive > 0 {
		return x.Go.HeapLive
	}
	return x.Go.HeapAlloc
}

// fillMachine summarizes the machine over the window.
func (r *result) fillMachine(win []*sample) {
	m := &r.Machine
	m.OS, m.LogicalCPUs = r.GOOS, r.NumCPU
	var free, comm, threads, procs float64
	n := 0
	for _, smp := range win {
		ms := smp.Machine
		if ms.TotalPhys == 0 {
			continue
		}
		f, c := mb(ms.AvailPhys), mb(ms.CommitAvail)
		if n == 0 || f < m.FreeMemMinMB {
			m.FreeMemMinMB = f
		}
		if n == 0 || c < m.FreeCommMinMB {
			m.FreeCommMinMB = c
		}
		m.TotalMemMB, m.CommitLimitMB = mb(ms.TotalPhys), mb(ms.CommitLimit)
		m.ThreadsMax = max(m.ThreadsMax, ms.Threads)
		free, comm, threads, procs = free+f, comm+c, threads+float64(ms.Threads), procs+float64(ms.Processes)
		n++
	}
	if n > 0 {
		k := float64(n)
		m.FreeMemMeanMB, m.FreeCommMeanMB = round(free/k, 0), round(comm/k, 0)
		m.ThreadsMean, m.ProcessesMean = round(threads/k, 0), round(procs/k, 0)
	}
	m.FreeMemMinMB, m.FreeCommMinMB = round(m.FreeMemMinMB, 0), round(m.FreeCommMinMB, 0)
	m.TotalMemMB, m.CommitLimitMB = round(m.TotalMemMB, 0), round(m.CommitLimitMB, 0)
}

// fillTree summarizes the job's accounting over the window.
func (r *result) fillTree(base *sample, win []*sample, scanCPU float64) {
	var first, last *jobStat
	if base != nil {
		first = base.Job
	}
	t := &treeSummary{}
	for _, smp := range win {
		if smp.Job == nil {
			continue
		}
		if first == nil {
			first = smp.Job
		}
		last = smp.Job
		t.ActiveMax = max(t.ActiveMax, smp.Job.Active)
		t.PeakCommitMB = max(t.PeakCommitMB, round(mb(smp.Job.PeakCommit), 1))
	}
	if first == nil || last == nil {
		return
	}
	secs := r.WindowEnd.Sub(r.WindowStart).Seconds()
	t.CPUSeconds = round((last.CPU - first.CPU).Seconds(), 3)
	t.CPUPercent = round(100*t.CPUSeconds/secs, 4)
	t.ScanCPUSeconds = round(scanCPU, 3)
	t.Started = last.Started - first.Started
	t.StartedPerHour = round(float64(t.Started)/(secs/3600), 1)
	if f := r.TreeFinal; f != nil {
		t.RunStarted, t.RunCPUSeconds = f.Started, round(f.CPU.Seconds(), 3)
	}
	r.Tree = t
}

// fill computes the summaries of the measured window from the samples.
func (r *result) fill(s *sampler, g *loadGen, spawns []spawnRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Samples, r.Processes, r.Errors = s.samples, s.all, s.errs
	from, to := r.WindowStart, r.WindowEnd
	secs := to.Sub(from).Seconds()
	var win []*sample
	// The sample taken as the window starts (or the last one before) is the base of the rates.
	var base *sample
	for i := range s.samples {
		smp := &s.samples[i]
		switch {
		case !smp.At.After(from):
			base = smp
		case !smp.At.After(to):
			win = append(win, smp)
		}
	}

	roles := map[string]*roleSummary{}
	sums := map[string][2]float64{}
	for _, smp := range win {
		for name, rs := range smp.Roles {
			x := roles[name]
			if x == nil {
				x = &roleSummary{Role: name}
				roles[name] = x
			}
			x.PeakCount = max(x.PeakCount, rs.Count)
			x.PeakWSMB = max(x.PeakWSMB, mb(rs.WS))
			x.PeakPrivMB = max(x.PeakPrivMB, mb(rs.Private))
			x.PeakHandles = max(x.PeakHandles, rs.Handles)
			x.PeakThreads = max(x.PeakThreads, rs.Threads)
			v := sums[name]
			sums[name] = [2]float64{v[0] + mb(rs.WS), v[1] + mb(rs.Private)}
		}
	}
	for _, t := range s.all {
		x := roles[t.Role]
		if x == nil {
			x = &roleSummary{Role: t.Role}
			roles[t.Role] = x
		}
		x.CPUSeconds += cpuIn(t, from, to).Seconds()
		x.ProcPeakWSMB = max(x.ProcPeakWSMB, mb(t.PeakWS))
		x.SeenInRun++
		if t.Role != "daemon" && !t.Seen.Before(from) && !t.Seen.After(to) {
			x.Spawns++
		}
	}
	for name, x := range roles {
		if n := float64(len(win)); n > 0 {
			x.MeanWSMB, x.MeanPrivMB = round(sums[name][0]/n, 1), round(sums[name][1]/n, 1)
		}
		x.CPUPercent = round(100*x.CPUSeconds/secs, 4)
		x.CPUSeconds = round(x.CPUSeconds, 3)
		x.SpawnsPerHr = round(float64(x.Spawns)/(secs/3600), 1)
		x.PeakWSMB, x.PeakPrivMB, x.ProcPeakWSMB = round(x.PeakWSMB, 1), round(x.PeakPrivMB, 1), round(x.ProcPeakWSMB, 1)
		r.Roles = append(r.Roles, *x)
	}
	sort.Slice(r.Roles, func(i, j int) bool { return r.Roles[i].Role < r.Roles[j].Role })
	scanCPU := 0.0
	for _, x := range r.Roles {
		scanCPU += x.CPUSeconds
	}
	r.fillMachine(win)
	r.fillTree(base, win, scanCPU)

	d := &r.Daemon
	var ws, priv, gor, heap, live float64
	for _, smp := range win {
		dr := smp.Roles["daemon"]
		ws, priv = ws+mb(smp.DaemonWS), priv+mb(dr.Private)
		gor, heap, live = gor+float64(smp.Go.Goroutines), heap+mb(smp.Go.HeapAlloc), live+mb(liveHeap(smp))
		d.HeapLiveMax = max(d.HeapLiveMax, mb(liveHeap(smp)))
		d.PeakWSMB = max(d.PeakWSMB, mb(smp.DaemonWS))
		d.OSPeakWSMB = max(d.OSPeakWSMB, mb(smp.DaemonPeak))
		d.PeakPrivMB = max(d.PeakPrivMB, mb(dr.Private))
		d.Handles, d.Threads = max(d.Handles, dr.Handles), max(d.Threads, dr.Threads)
		d.GoroutinesMax = max(d.GoroutinesMax, smp.Go.Goroutines)
		d.HeapAllocMax = max(d.HeapAllocMax, mb(smp.Go.HeapAlloc))
		d.HeapSysMax = max(d.HeapSysMax, mb(smp.Go.HeapSys))
		d.SysMax = max(d.SysMax, mb(smp.Go.Sys))
	}
	if n := float64(len(win)); n > 0 {
		d.MeanWSMB, d.MeanPrivMB = round(ws/n, 1), round(priv/n, 1)
		d.GoroutinesMean, d.HeapAllocMean, d.HeapLiveMean = round(gor/n, 1), round(heap/n, 1), round(live/n, 1)
		first, last := base, win[len(win)-1]
		if first == nil {
			first = win[0]
		}
		span := last.At.Sub(first.At).Seconds()
		if span > 0 {
			d.CPUSeconds = round((last.DaemonCPU - first.DaemonCPU).Seconds(), 3)
			d.CPUPercent = round(100*(last.DaemonCPU-first.DaemonCPU).Seconds()/span, 4)
			d.AllocMBps = round(mb(last.Go.TotalAlloc-first.Go.TotalAlloc)/span, 3)
			d.MallocsPerSec = round(float64(last.Go.Mallocs-first.Go.Mallocs)/span, 1)
			d.GCPerMin = round(float64(last.Go.NumGC-first.Go.NumGC)/(span/60), 2)
		}
		d.WSGrowthMB = round(mb(last.DaemonWS)-mb(first.DaemonWS), 1)
		d.PrivGrowthMB = round(mb(last.Roles["daemon"].Private)-mb(first.Roles["daemon"].Private), 1)
		half := win[len(win)/2:]
		d.WSSlope = round(slopePerHour(half, func(x *sample) float64 { return mb(x.DaemonWS) }), 2)
		d.PrivSlope = round(slopePerHour(half, func(x *sample) float64 { return mb(x.Roles["daemon"].Private) }), 2)
		d.HeapSlope = round(slopePerHour(half, func(x *sample) float64 { return mb(liveHeap(x)) }), 2)
		d.HeapGrowthMB = round(mb(last.Go.HeapAlloc)-mb(first.Go.HeapAlloc), 1)
		r.DB = dbSummary{DBStartMB: round(float64(first.DBBytes)/mib, 2), DBEndMB: round(float64(last.DBBytes)/mib, 2),
			WALStartMB: round(float64(first.WALBytes)/mib, 2), WALEndMB: round(float64(last.WALBytes)/mib, 2)}
		for _, smp := range win {
			r.DB.WALMaxMB = max(r.DB.WALMaxMB, round(float64(smp.WALBytes)/mib, 2))
		}
	}
	d.PeakWSMB, d.OSPeakWSMB, d.PeakPrivMB = round(d.PeakWSMB, 1), round(d.OSPeakWSMB, 1), round(d.PeakPrivMB, 1)
	d.HeapAllocMax, d.HeapSysMax, d.SysMax = round(d.HeapAllocMax, 1), round(d.HeapSysMax, 1), round(d.SysMax, 1)
	d.HeapLiveMax = round(d.HeapLiveMax, 1)

	byRole := map[string]*spawnSummary{}
	exited := map[string]int{}
	for _, sp := range spawns {
		x := byRole[sp.Role]
		if x == nil {
			x = &spawnSummary{Role: sp.Role}
			byRole[sp.Role] = x
		}
		x.Count++
		if !sp.At.Before(from) && !sp.At.After(to) {
			x.InWindow++
		}
		if sp.Exited {
			exited[sp.Role]++
			x.MeanMs += sp.Ms
			x.MeanCPUMs += sp.CPUMs
			x.MeanPeakWSMB += mb(sp.PeakWS)
			x.MaxPeakWSMB = max(x.MaxPeakWSMB, round(mb(sp.PeakWS), 1))
		}
	}
	for _, x := range byRole {
		if exited[x.Role] > 0 {
			n := float64(exited[x.Role])
			x.MeanMs, x.MeanCPUMs, x.MeanPeakWSMB = round(x.MeanMs/n, 1), round(x.MeanCPUMs/n, 1), round(x.MeanPeakWSMB/n, 1)
		}
		x.PerHour = round(float64(x.InWindow)/(secs/3600), 1)
		r.Spawns = append(r.Spawns, *x)
	}
	sort.Slice(r.Spawns, func(i, j int) bool { return r.Spawns[i].Role < r.Spawns[j].Role })
	r.Runs = spawnRuns(spawns)
	if r.idleScenario() {
		r.Idle = idleCost(base, win, r.Runs, recorderTicks(r.DaemonStarted, from, to, time.Local))
	}

	if r.Scenario == "load" || g.sseConns.Load() > 0 {
		l := &loadSummary{Tasks: len(g.ids), EventsStored: g.events, SSEClients: g.e.o.sse,
			SSEConnects: g.sseConns.Load(), SSEFrames: g.sseFrames.Load(), SSEMB: round(float64(g.sseBytes.Load())/mib, 2),
			API: map[string]latencyStats{}}
		total := to.Sub(from).Seconds() + g.e.o.cpuProfile.Seconds()
		l.EventsPerSec = round(float64(g.events)/total, 1)
		l.SSEFramesPerS = round(float64(l.SSEFrames)/total, 1)
		for name, lat := range g.lat {
			if st := lat.stats(); st.Count > 0 || st.Errors > 0 {
				l.API[name] = st
			}
		}
		if g.events > 0 {
			l.WSMBPer10kEvts = round(d.WSGrowthMB/(float64(g.events)/10000), 2)
			l.PrivPer10kEvts = round(d.PrivGrowthMB/(float64(g.events)/10000), 2)
		}
		r.Load = l
	}
	r.Budget = r.budget()
}

// The load scenario's peak private budget grows with the tasks that run at once: loadPrivateBase MB plus
// loadPrivatePerTask MB per task (8 tasks: 100 MB). Each running attempt holds its own state (parser, launcher
// buffers, task bus). To be tuned from a 16-task run.
const (
	loadPrivateBase    = 80.0
	loadPrivatePerTask = 2.5
)

// minSlopeWindow is the shortest measured window whose slopes are judged (a 5-minute load gave false OVER results).
const minSlopeWindow = 15 * time.Minute

// budget is the plan's budget (m19-resources.md §7); a violation is flagged, never a failure.
func (r *result) budget() []budgetCheck {
	var out []budgetCheck
	add := func(name string, limit, value float64) {
		out = append(out, budgetCheck{Name: name, Limit: limit, Value: value, OK: value < limit})
	}
	// Memory is judged on private bytes (commit): the working set jumps with what the OS keeps resident, not with
	// what the daemon allocates, so it is reported as information only. A leak shows as a slope once the start is
	// over; the limits allow for the noise of a few minutes of samples. Under load the slopes also count the state
	// of the attempts that run all through the window, so there they are information only and the live heap
	// retained after the load is the leak check.
	// A window shorter than minSlopeWindow extrapolates start-up growth and GC noise to a whole hour, so its slopes
	// are information only too.
	add("daemon private bytes slope over the second half MB/h", 10, r.Daemon.PrivSlope)
	add("daemon live heap slope over the second half MB/h", 5, r.Daemon.HeapSlope)
	slopeNote := ""
	switch {
	case r.WindowEnd.Sub(r.WindowStart) < minSlopeWindow:
		slopeNote = "information only: window too short"
	case r.Scenario == "load":
		slopeNote = "information only: per-attempt state grows while tasks run; see retained heap"
	}
	if slopeNote != "" {
		for i := len(out) - 2; i < len(out); i++ {
			out[i].Info, out[i].Note = true, slopeNote
		}
	}
	if x := r.Retained; x.measured() {
		c := budgetCheck{Name: "live heap retained after load, excluding the SSE replay ring (after GC) MB", Limit: 3,
			Value: x.ExRingDeltaMB, OK: x.ExRingDeltaMB <= 3}
		if x.RingUnknown {
			c.Note = "the daemon does not report its SSE ring: raw delta"
		}
		out = append(out, c)
	}
	if r.idleScenario() {
		add("idle daemon CPU % of one core (window average)", 0.1, r.Daemon.CPUPercent)
		add("idle daemon peak private MB", 100, r.Daemon.PeakPrivMB)
	} else {
		tasks := 0
		if r.Load != nil {
			tasks = r.Load.Tasks
		}
		add(fmt.Sprintf("load daemon peak private MB (%g + %g × %d tasks)", loadPrivateBase, loadPrivatePerTask, tasks),
			loadPrivateBase+loadPrivatePerTask*float64(tasks), r.Daemon.PeakPrivMB)
		if r.Load != nil && r.Load.EventsStored > 0 {
			// Not linear in the events: growth per 10k events must stay small.
			add("load daemon private growth MB per 10k events", 5, r.Load.PrivPer10kEvts)
		}
	}
	return out
}

// idleScenario reports whether the run is one of the idle scenarios.
func (r *result) idleScenario() bool { return strings.HasPrefix(r.Scenario, "idle") }

func writeResult(dir string, r *result) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o600); err != nil {
		return err
	}
	var b strings.Builder
	writeMarkdown(&b, r)
	return os.WriteFile(filepath.Join(dir, "result.md"), []byte(b.String()), 0o600)
}

func writeMarkdown(w io.Writer, r *result) {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }
	p("# Seelegent bench: %s\n\n", r.Scenario)
	p("Commit %s, %s/%s, %d CPUs, started %s. Measured %s (window %s to %s). Options: %v.\n\n", r.Commit, r.GOOS,
		r.GOARCH, r.NumCPU, r.Started.Format(time.RFC3339), r.WindowEnd.Sub(r.WindowStart).Round(time.Second),
		r.WindowStart.Format("15:04:05"), r.WindowEnd.Format("15:04:05"), r.Options)
	mc := r.Machine
	p("## Machine\n\n%s (%s), %d logical CPUs, %.0f MB RAM, commit limit %.0f MB. Over the window: free memory "+
		"min / mean %.0f / %.0f MB, free commit min / mean %.0f / %.0f MB, %.0f processes and %.0f threads on the "+
		"system (max %d threads).\n\n", mc.OSVersion, mc.OS, mc.LogicalCPUs, mc.TotalMemMB, mc.CommitLimitMB,
		mc.FreeMemMinMB, mc.FreeMemMeanMB, mc.FreeCommMinMB, mc.FreeCommMeanMB, mc.ProcessesMean, mc.ThreadsMean,
		mc.ThreadsMax)
	if len(mc.TopOther) > 0 {
		p("Other programs by CPU over the window:")
		for _, x := range mc.TopOther {
			p(" %s (pid %d) %.1f s = %.1f %% of one core;", x.Name, x.PID, x.CPUSeconds, x.CPUPercent)
		}
		p("\n\n")
	}
	if len(r.Setup) > 0 {
		p("## Setup cost\n\n| phase | n | seconds | daemon CPU s | per unit | tree CPU s | per unit | processes |\n" +
			"|---|---|---|---|---|---|---|---|\n")
		for _, x := range r.Setup {
			p("| %s | %d %s | %.1f | %.3f | %.3f | %.3f | %.3f | %d |\n", x.Name, x.Count, x.Unit, x.Seconds,
				x.DaemonCPUSeconds, x.DaemonCPUPerUnit, x.TreeCPUSeconds, x.TreeCPUPerUnit, x.ProcessesStarted)
		}
		p("\nsetup-cpu.pprof profiles the daemon from its start (-setup-profile).\n\n")
	}
	d := r.Daemon
	p("## Daemon\n\n| metric | value |\n|---|---|\n")
	p("| working set mean / peak / OS peak | %.1f / %.1f / %.1f MB |\n", d.MeanWSMB, d.PeakWSMB, d.OSPeakWSMB)
	p("| private mean / peak | %.1f / %.1f MB |\n", d.MeanPrivMB, d.PeakPrivMB)
	p("| CPU | %.3f s, %.4f %% of one core |\n", d.CPUSeconds, d.CPUPercent)
	p("| handles / threads (peak) | %d / %d |\n", d.Handles, d.Threads)
	p("| goroutines mean / max | %.1f / %d |\n", d.GoroutinesMean, d.GoroutinesMax)
	p("| heap alloc mean / max, live heap mean / max | %.1f / %.1f, %.1f / %.1f MB |\n", d.HeapAllocMean,
		d.HeapAllocMax, d.HeapLiveMean, d.HeapLiveMax)
	p("| heap sys max, Go sys max | %.1f, %.1f MB |\n", d.HeapSysMax, d.SysMax)
	p("| allocation rate | %.3f MB/s, %.1f mallocs/s, %.2f GC/min |\n", d.AllocMBps, d.MallocsPerSec, d.GCPerMin)
	p("| growth over the window | private %+.1f MB, WS %+.1f MB, heap %+.1f MB |\n", d.PrivGrowthMB, d.WSGrowthMB,
		d.HeapGrowthMB)
	p("| slope over the second half | private %+.2f MB/h, live heap %+.2f MB/h (WS %+.2f MB/h, information) |\n",
		d.PrivSlope, d.HeapSlope, d.WSSlope)
	if x := r.Retained; x != nil {
		if x.measured() {
			p("| live heap retained after load (after GC) start / end / delta | %.2f / %.2f / %+.2f MB |\n", x.StartMB,
				x.EndMB, x.DeltaMB)
			if x.RingUnknown {
				p("| SSE replay ring start / end | not reported by this daemon |\n")
			} else {
				p("| SSE replay ring start / end | %.2f / %.2f MB (%d / %d frames) |\n", x.StartRingMB, x.EndRingMB,
					x.StartRingFrames, x.EndRingFrames)
			}
			p("| live heap retained after load, excluding the SSE replay ring | %+.2f MB |\n", x.ExRingDeltaMB)
		} else {
			p("| live heap retained after load (after GC) | start %.2f MB, end not read: %s |\n", x.StartMB, x.Error)
		}
	}
	p("| database start / end | %.2f / %.2f MB, WAL %.2f / %.2f MB (max %.2f) |\n\n", r.DB.DBStartMB, r.DB.DBEndMB,
		r.DB.WALStartMB, r.DB.WALEndMB, r.DB.WALMaxMB)
	if x := r.Retained; x != nil {
		p("Retained heap: the live heap after forced GCs (`/debug/pprof/heap?gc=1` twice, then `/gc/heap/live` from "+
			"`/debug/vars?light=1`) just before the load tasks start and %.0f s after every load task ended and the SSE "+
			"clients and pollers stopped; profiles in heap-before-load.pprof and heap-after-load.pprof. The SSE replay "+
			"ring (daemon.sseRing in the same answer) keeps the last frames for 10 minutes, so the budget is the delta "+
			"without it.", x.SettleSeconds)
		if x.Note != "" {
			p(" Note: %s.", x.Note)
		}
		p("\n\n")
	}
	if g := r.GoSysEnd; g != nil {
		p("## Go sys breakdown (end)\n\n| Sys | HeapSys | HeapInuse | HeapReleased | StackSys | MSpanSys | MCacheSys | " +
			"GCSys | BuckHashSys | OtherSys |\n|---|---|---|---|---|---|---|---|---|---|\n")
		p("| %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f | %.2f |\n\n", g.SysMB, g.HeapSysMB,
			g.HeapInuseMB, g.HeapReleasedMB, g.StackSysMB, g.MSpanSysMB, g.MCacheSysMB, g.GCSysMB, g.BuckHashSysMB,
			g.OtherSysMB)
		p("MB, from runtime.MemStats at the end of the run (memstats-end.json; in the load scenario after the retained-" +
			"heap reading). The *Sys columns add up to Sys; HeapReleased is the part of HeapSys returned to the OS.\n\n")
	}
	p("## Process tree by role\n\n| role | peak n | WS mean / peak MB | private mean / peak MB | CPU s | %% core | " +
		"handles | threads | spawns (window) | spawns/h | seen (run) | max proc peak WS MB |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, x := range r.Roles {
		p("| %s | %d | %.1f / %.1f | %.1f / %.1f | %.3f | %.4f | %d | %d | %d | %.1f | %d | %.1f |\n", x.Role,
			x.PeakCount, x.MeanWSMB, x.PeakWSMB, x.MeanPrivMB, x.PeakPrivMB, x.CPUSeconds, x.CPUPercent, x.PeakHandles,
			x.PeakThreads, x.Spawns, x.SpawnsPerHr, x.SeenInRun, x.ProcPeakWSMB)
	}
	if t := r.Tree; t != nil {
		p("\nWhole tree (Windows job, exact): CPU %.3f s = %.4f %% of one core (the pid scan above sums %.3f s), "+
			"%d processes started (%.1f/h), at most %d alive, peak job commit %.1f MB. Whole run: %d processes, "+
			"%.3f CPU s.\n", t.CPUSeconds, t.CPUPercent, t.ScanCPUSeconds, t.Started, t.StartedPerHour, t.ActiveMax,
			t.PeakCommitMB, t.RunStarted, t.RunCPUSeconds)
	}
	p("\nSpawns are counted by a pid scan every 250 ms (a process that lives less than that can be missed); the " +
		"fake CLI's own log below is exact.\n\n")
	p("## Fake CLI runs (spawn log)\n\n| role | runs | in window | per hour | mean ms | mean CPU ms | " +
		"mean / max peak WS MB |\n|---|---|---|---|---|---|---|\n")
	for _, x := range r.Spawns {
		p("| %s | %d | %d | %.1f | %.1f | %.1f | %.1f / %.1f |\n", x.Role, x.Count, x.InWindow, x.PerHour, x.MeanMs,
			x.MeanCPUMs, x.MeanPeakWSMB, x.MaxPeakWSMB)
	}
	writeIdle(p, r)
	if l := r.Load; l != nil {
		p("\n## Load\n\n%d tasks, %d events stored (%.1f/s), %d SSE clients (%d connects): %d frames (%.1f/s), %.2f MB. "+
			"Daemon private growth %.2f MB per 10k events (WS %.2f).\n\n", l.Tasks, l.EventsStored, l.EventsPerSec,
			l.SSEClients, l.SSEConnects, l.SSEFrames, l.SSEFramesPerS, l.SSEMB, l.PrivPer10kEvts, l.WSMBPer10kEvts)
		p("| request | count | errors | p50 ms | p95 ms | p99 ms | max ms |\n|---|---|---|---|---|---|---|\n")
		names := make([]string, 0, len(l.API))
		for n := range l.API {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			s := l.API[n]
			p("| %s | %d | %d | %.2f | %.2f | %.2f | %.2f |\n", n, s.Count, s.Errors, s.P50, s.P95, s.P99, s.Max)
		}
	}
	p("\n## Budget\n\n| check | limit | value | |\n|---|---|---|---|\n")
	for _, b := range r.Budget {
		mark := "ok"
		switch {
		case b.Info:
			mark = "info"
		case !b.OK:
			mark = "**over**"
		}
		name := b.Name
		if b.Note != "" {
			name += " (" + b.Note + ")"
		}
		p("| %s | %g | %g | %s |\n", name, b.Limit, b.Value, mark)
	}
	if len(r.Errors) > 0 {
		p("\nSample errors: %v\n", r.Errors)
	}
	p("\n## Notes\n\n" +
		"- The fake agy answers a quota read instantly from a canned answer. A real agy is a Node.js process that " +
		"signs in and calls Google: its reads cost far more CPU, memory and time, so the quota-reader numbers here " +
		"are lower bounds.\n" +
		"- gh is faked too (a copy of the bench named gh, first on PATH; GH_CONFIG_DIR, APPDATA and LOCALAPPDATA " +
		"point to temp folders): the bench never uses this machine's GitHub sign-in or reaches GitHub.\n" +
		"- The daemon's /debug/vars is read with ?light=1 (runtime/metrics, no stop-the-world) every sample and " +
		"with memstats once a minute.\n" +
		"- Windows CPU profiles count goroutines blocked in a system call (ReadFile on a pipe) as CPU: use the CPU " +
		"seconds above, which come from the process counters, for the cost, and do not chase " +
		"bytes.Buffer.ReadFrom or os.(*File).Read in a Windows profile.\n" +
		"- Memory is judged on private bytes; the working set is shown as information (it jumps with what the OS " +
		"keeps resident).\n")
}

// metric is one value compared with a baseline.
type metric struct {
	Name  string
	Value float64
}

// metrics are the scalar values compared with a baseline.
func (r *result) metrics() []metric {
	m := []metric{
		{"daemon WS mean MB", r.Daemon.MeanWSMB}, {"daemon WS peak MB", r.Daemon.PeakWSMB},
		{"daemon private mean MB", r.Daemon.MeanPrivMB}, {"daemon CPU % core", r.Daemon.CPUPercent},
		{"daemon goroutines max", float64(r.Daemon.GoroutinesMax)}, {"daemon heap mean MB", r.Daemon.HeapAllocMean},
		{"daemon alloc MB/s", r.Daemon.AllocMBps}, {"daemon GC/min", r.Daemon.GCPerMin},
		{"daemon handles", float64(r.Daemon.Handles)}, {"daemon threads", float64(r.Daemon.Threads)},
		{"DB end MB", r.DB.DBEndMB}, {"WAL max MB", r.DB.WALMaxMB},
		{"daemon WS slope MB/h", r.Daemon.WSSlope}, {"daemon private slope MB/h", r.Daemon.PrivSlope},
		{"daemon private peak MB", r.Daemon.PeakPrivMB}, {"daemon live heap slope MB/h", r.Daemon.HeapSlope},
		{"daemon live heap max MB", r.Daemon.HeapLiveMax},
		{"machine free memory min MB", r.Machine.FreeMemMinMB}, {"machine threads mean", r.Machine.ThreadsMean},
	}
	for _, x := range r.Setup {
		m = append(m, metric{x.Name + " daemon CPU s per " + x.Unit, x.DaemonCPUPerUnit},
			metric{x.Name + " tree CPU s per " + x.Unit, x.TreeCPUPerUnit})
	}
	if g := r.GoSysEnd; g != nil {
		m = append(m, metric{"Go StackSys end MB", g.StackSysMB}, metric{"Go GCSys end MB", g.GCSysMB},
			metric{"Go OtherSys end MB", g.OtherSysMB})
	}
	if x := r.Retained; x.measured() {
		m = append(m, metric{"live heap before load MB", x.StartMB}, metric{"live heap after load MB", x.EndMB},
			metric{"live heap retained after load MB", x.DeltaMB},
			metric{"live heap retained after load excl. SSE ring MB", x.ExRingDeltaMB},
			metric{"SSE ring after load MB", x.EndRingMB})
	}
	if t := r.Tree; t != nil {
		m = append(m, metric{"tree CPU % core", t.CPUPercent}, metric{"tree processes/h", t.StartedPerHour})
	}
	if x := r.Idle; x != nil {
		m = append(m, metric{"idle floor daemon CPU % core", x.FloorPercent},
			metric{"idle daemon CPU s per quota cycle", x.CPUPerCycle}, metric{"idle quota cycles", float64(x.Cycles)},
			metric{"idle daemon CPU s per quota-reader spawn", x.CPUPerRead},
			metric{"idle daemon CPU s per recorder tick", x.CPUPerTick})
	}
	for _, x := range r.Roles {
		if x.Role != "daemon" {
			m = append(m, metric{x.Role + " CPU % core", x.CPUPercent}, metric{x.Role + " WS peak MB", x.PeakWSMB},
				metric{x.Role + " spawns/h", x.SpawnsPerHr})
		}
	}
	if r.Load != nil {
		for _, n := range []string{"list", "detail"} {
			if s, ok := r.Load.API[n]; ok {
				m = append(m, metric{n + " p50 ms", s.P50}, metric{n + " p95 ms", s.P95}, metric{n + " p99 ms", s.P99})
			}
		}
		m = append(m, metric{"events/s", r.Load.EventsPerSec})
	}
	return m
}

func printResult(w io.Writer, r *result, baseline string) {
	writeMarkdown(w, r)
	if baseline == "" {
		return
	}
	data, err := os.ReadFile(baseline)
	var b result
	if err == nil {
		err = json.Unmarshal(data, &b)
	}
	if err != nil {
		_, _ = fmt.Fprintln(w, "\nbaseline:", err)
		return
	}
	old := map[string]float64{}
	for _, kv := range b.metrics() {
		old[kv.Name] = kv.Value
	}
	_, _ = fmt.Fprintf(w, "\n## Against %s (%s, %s)\n\n| metric | baseline | now | delta | %% |\n|---|---|---|---|---|\n",
		baseline, b.Scenario, b.Commit)
	for _, kv := range r.metrics() {
		name, now := kv.Name, kv.Value
		was, ok := old[name]
		if !ok {
			_, _ = fmt.Fprintf(w, "| %s | - | %g | | |\n", name, now)
			continue
		}
		pct := ""
		if was != 0 {
			pct = fmt.Sprintf("%+.1f", 100*(now-was)/was)
		}
		_, _ = fmt.Fprintf(w, "| %s | %g | %g | %+g | %s |\n", name, was, now, round(now-was, 4), pct)
	}
}
