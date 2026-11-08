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
	HeapSysMax     float64 `json:"heapSysMaxMB"`
	SysMax         float64 `json:"goSysMaxMB"`
	AllocMBps      float64 `json:"allocMBPerSecond"`
	MallocsPerSec  float64 `json:"mallocsPerSecond"`
	GCPerMin       float64 `json:"gcPerMinute"`
	WSGrowthMB     float64 `json:"wsGrowthMB"`
	HeapGrowthMB   float64 `json:"heapGrowthMB"`
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

type budgetCheck struct {
	Name  string  `json:"name"`
	Limit float64 `json:"limit"`
	Value float64 `json:"value"`
	OK    bool    `json:"ok"`
}

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
}

// result is one bench run (result.json).
type result struct {
	Scenario    string            `json:"scenario"`
	Commit      string            `json:"commit"`
	GOOS        string            `json:"goos"`
	GOARCH      string            `json:"goarch"`
	NumCPU      int               `json:"numCPU"`
	Options     map[string]string `json:"options"`
	Started     time.Time         `json:"started"`
	WindowStart time.Time         `json:"windowStart"`
	WindowEnd   time.Time         `json:"windowEnd"`
	Daemon      daemonSummary     `json:"daemon"`
	Roles       []roleSummary     `json:"roles"`
	Spawns      []spawnSummary    `json:"spawnLog"`
	DB          dbSummary         `json:"db"`
	Load        *loadSummary      `json:"load,omitempty"`
	Budget      []budgetCheck     `json:"budget"`
	Errors      map[string]int    `json:"sampleErrors,omitempty"`
	Processes   []*tracked        `json:"processes"`
	Samples     []sample          `json:"samples"`
}

func optionsOf(o *options) map[string]string {
	return map[string]string{"duration": o.duration.String(), "warmup": o.warmup.String(),
		"cpuprofile": o.cpuProfile.String(), "interval": o.interval.String(), "accounts": fmt.Sprint(o.accounts),
		"tasks": fmt.Sprint(o.tasks), "rate": fmt.Sprint(o.rate), "sse": fmt.Sprint(o.sse), "poll": o.poll.String(),
		"seedTasks": fmt.Sprint(o.seedTasks)}
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
		case p.At.Before(from):
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

// fill computes the summaries of the measured window from the samples.
func (r *result) fill(s *sampler, g *loadGen, spawns []spawnRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.Samples, r.Processes, r.Errors = s.samples, s.all, s.errs
	from, to := r.WindowStart, r.WindowEnd
	secs := to.Sub(from).Seconds()
	var win []*sample
	// The sample just before the window is the base of the rates.
	var base *sample
	for i := range s.samples {
		smp := &s.samples[i]
		switch {
		case smp.At.Before(from):
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

	d := &r.Daemon
	var ws, priv, gor, heap float64
	for _, smp := range win {
		dr := smp.Roles["daemon"]
		ws, priv = ws+mb(smp.DaemonWS), priv+mb(dr.Private)
		gor, heap = gor+float64(smp.Go.Goroutines), heap+mb(smp.Go.HeapAlloc)
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
		d.GoroutinesMean, d.HeapAllocMean = round(gor/n, 1), round(heap/n, 1)
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
		d.HeapGrowthMB = round(mb(last.Go.HeapAlloc)-mb(first.Go.HeapAlloc), 1)
		r.DB = dbSummary{DBStartMB: round(float64(first.DBBytes)/mib, 2), DBEndMB: round(float64(last.DBBytes)/mib, 2),
			WALStartMB: round(float64(first.WALBytes)/mib, 2), WALEndMB: round(float64(last.WALBytes)/mib, 2)}
		for _, smp := range win {
			r.DB.WALMaxMB = max(r.DB.WALMaxMB, round(float64(smp.WALBytes)/mib, 2))
		}
	}
	d.PeakWSMB, d.OSPeakWSMB, d.PeakPrivMB = round(d.PeakWSMB, 1), round(d.OSPeakWSMB, 1), round(d.PeakPrivMB, 1)
	d.HeapAllocMax, d.HeapSysMax, d.SysMax = round(d.HeapAllocMax, 1), round(d.HeapSysMax, 1), round(d.SysMax, 1)

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
		}
		r.Load = l
	}
	r.Budget = r.budget()
}

// budget is the plan's budget (m19-resources.md §7); a violation is flagged, never a failure.
func (r *result) budget() []budgetCheck {
	var out []budgetCheck
	add := func(name string, limit, value float64) {
		out = append(out, budgetCheck{Name: name, Limit: limit, Value: value, OK: value < limit})
	}
	if r.Scenario == "idle" {
		add("idle daemon CPU % of one core", 0.1, r.Daemon.CPUPercent)
		add("idle daemon peak working set MB", 100, r.Daemon.PeakWSMB)
	} else {
		add("load daemon peak working set MB", 100, r.Daemon.PeakWSMB)
		if r.Load != nil && r.Load.EventsStored > 0 {
			// Not linear in the events: growth per 10k events must stay small.
			add("load daemon WS growth MB per 10k events", 5, r.Load.WSMBPer10kEvts)
		}
	}
	return out
}

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
	d := r.Daemon
	p("## Daemon\n\n| metric | value |\n|---|---|\n")
	p("| working set mean / peak / OS peak | %.1f / %.1f / %.1f MB |\n", d.MeanWSMB, d.PeakWSMB, d.OSPeakWSMB)
	p("| private mean / peak | %.1f / %.1f MB |\n", d.MeanPrivMB, d.PeakPrivMB)
	p("| CPU | %.3f s, %.4f %% of one core |\n", d.CPUSeconds, d.CPUPercent)
	p("| handles / threads (peak) | %d / %d |\n", d.Handles, d.Threads)
	p("| goroutines mean / max | %.1f / %d |\n", d.GoroutinesMean, d.GoroutinesMax)
	p("| heap alloc mean / max, heap sys max, Go sys max | %.1f / %.1f, %.1f, %.1f MB |\n", d.HeapAllocMean,
		d.HeapAllocMax, d.HeapSysMax, d.SysMax)
	p("| allocation rate | %.3f MB/s, %.1f mallocs/s, %.2f GC/min |\n", d.AllocMBps, d.MallocsPerSec, d.GCPerMin)
	p("| growth over the window | WS %+.1f MB, heap %+.1f MB |\n", d.WSGrowthMB, d.HeapGrowthMB)
	p("| database start / end | %.2f / %.2f MB, WAL %.2f / %.2f MB (max %.2f) |\n\n", r.DB.DBStartMB, r.DB.DBEndMB,
		r.DB.WALStartMB, r.DB.WALEndMB, r.DB.WALMaxMB)
	p("## Process tree by role\n\n| role | peak n | WS mean / peak MB | private mean / peak MB | CPU s | %% core | " +
		"handles | threads | spawns (window) | spawns/h | seen (run) | max proc peak WS MB |\n" +
		"|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, x := range r.Roles {
		p("| %s | %d | %.1f / %.1f | %.1f / %.1f | %.3f | %.4f | %d | %d | %d | %.1f | %d | %.1f |\n", x.Role,
			x.PeakCount, x.MeanWSMB, x.PeakWSMB, x.MeanPrivMB, x.PeakPrivMB, x.CPUSeconds, x.CPUPercent, x.PeakHandles,
			x.PeakThreads, x.Spawns, x.SpawnsPerHr, x.SeenInRun, x.ProcPeakWSMB)
	}
	p("\nSpawns are counted by a pid scan every 250 ms (a process that lives less than that can be missed); the " +
		"fake CLI's own log below is exact.\n\n")
	p("## Fake CLI runs (spawn log)\n\n| role | runs | in window | per hour | mean ms | mean CPU ms | " +
		"mean / max peak WS MB |\n|---|---|---|---|---|---|---|\n")
	for _, x := range r.Spawns {
		p("| %s | %d | %d | %.1f | %.1f | %.1f | %.1f / %.1f |\n", x.Role, x.Count, x.InWindow, x.PerHour, x.MeanMs,
			x.MeanCPUMs, x.MeanPeakWSMB, x.MaxPeakWSMB)
	}
	if l := r.Load; l != nil {
		p("\n## Load\n\n%d tasks, %d events stored (%.1f/s), %d SSE clients (%d connects): %d frames (%.1f/s), %.2f MB. "+
			"Daemon WS growth %.2f MB per 10k events.\n\n", l.Tasks, l.EventsStored, l.EventsPerSec, l.SSEClients,
			l.SSEConnects, l.SSEFrames, l.SSEFramesPerS, l.SSEMB, l.WSMBPer10kEvts)
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
		if !b.OK {
			mark = "**over**"
		}
		p("| %s | %g | %g | %s |\n", b.Name, b.Limit, b.Value, mark)
	}
	if len(r.Errors) > 0 {
		p("\nSample errors: %v\n", r.Errors)
	}
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
