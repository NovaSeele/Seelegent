//go:build bench

package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// procEntry is one process of a listing.
type procEntry struct {
	PID, PPID int
	Name      string
	Threads   int
}

// procStat are the counters of one process.
type procStat struct {
	WS, PeakWS, Private uint64
	CPU                 time.Duration
	Handles             int
}

// tracked is a process of the daemon's tree.
type tracked struct {
	PID     int       `json:"pid"`
	PPID    int       `json:"ppid"`
	Name    string    `json:"name"`
	Role    string    `json:"role"`
	Started time.Time `json:"started"`
	Seen    time.Time `json:"seen"`
	Gone    time.Time `json:"gone,omitzero"`
	// LastCPU is the CPU time read last; CPUAt the cpu points read inside the samples.
	LastCPU time.Duration `json:"lastCpuNs"`
	CPUAt   []cpuPoint    `json:"-"`
	PeakWS  uint64        `json:"peakWS"`
	h       *procHandle
}

// update records counters read at t.
func (t *tracked) update(st procStat, at time.Time) {
	t.LastCPU = st.CPU
	t.CPUAt = append(t.CPUAt, cpuPoint{At: at, CPU: st.CPU})
	t.PeakWS = max(t.PeakWS, st.PeakWS)
}

type cpuPoint struct {
	At  time.Time
	CPU time.Duration
}

// roleSample sums one role's processes in one sample.
type roleSample struct {
	Count   int    `json:"count"`
	WS      uint64 `json:"ws"`
	Private uint64 `json:"private"`
	Handles int    `json:"handles"`
	Threads int    `json:"threads"`
}

// goStats is what the daemon's expvar and goroutine profile say.
type goStats struct {
	Goroutines int    `json:"goroutines"`
	HeapAlloc  uint64 `json:"heapAlloc"`
	HeapInuse  uint64 `json:"heapInuse"`
	HeapSys    uint64 `json:"heapSys"`
	Sys        uint64 `json:"sys"`
	TotalAlloc uint64 `json:"totalAlloc"`
	Mallocs    uint64 `json:"mallocs"`
	NumGC      uint32 `json:"numGC"`
	PauseTotal uint64 `json:"pauseTotalNs"`
}

// sample is one sampling point.
type sample struct {
	At       time.Time             `json:"at"`
	Phase    string                `json:"phase"`
	Roles    map[string]roleSample `json:"roles"`
	DaemonWS uint64                `json:"daemonWS"`
	// DaemonCPU is the daemon's CPU time so far.
	DaemonCPU   time.Duration `json:"daemonCpuNs"`
	DaemonPeak  uint64        `json:"daemonPeakWS"`
	Go          goStats       `json:"go"`
	DBBytes     int64         `json:"dbBytes"`
	WALBytes    int64         `json:"walBytes"`
	TreeSize    int           `json:"treeSize"`
	SampleError string        `json:"sampleError,omitempty"`
}

// sampler watches the daemon's process tree: a fast pid scan for spawns and a slower sample of the counters.
type sampler struct {
	daemonPID int
	selfName  string
	db        string
	pprofURL  string
	client    *http.Client

	mu      sync.Mutex
	phase   string
	procs   map[int]*tracked
	all     []*tracked
	samples []sample
	errs    map[string]int
}

func newSampler(daemonPID int, selfExe, db, pprofURL string) *sampler {
	return &sampler{daemonPID: daemonPID, selfName: exeBase(selfExe), db: db, pprofURL: pprofURL,
		client: &http.Client{Timeout: 10 * time.Second}, phase: "setup", procs: map[int]*tracked{},
		errs: map[string]int{}}
}

func exeBase(name string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
}

func (s *sampler) setPhase(p string) {
	s.mu.Lock()
	s.phase = p
	s.mu.Unlock()
}

func (s *sampler) noteErr(what string) {
	s.mu.Lock()
	s.errs[what]++
	s.mu.Unlock()
}

// role names a process of the tree from its name and its parent's role.
func (s *sampler) role(name, parentRole string, pid int) string {
	if pid == s.daemonPID {
		return "daemon"
	}
	base := exeBase(name)
	switch {
	case base == "seelegent" && parentRole == "daemon":
		return "launcher"
	case base == s.selfName && parentRole == "launcher":
		return "worker"
	case base == s.selfName && parentRole == "daemon":
		return "quota-reader"
	case base == s.selfName:
		return "fake-cli-other"
	}
	return base
}

// scan lists the processes and tracks the new members of the daemon's tree.
func (s *sampler) scan(now time.Time) {
	list, err := listProcs()
	if err != nil {
		s.noteErr("list: " + err.Error())
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for pid, t := range s.procs {
		if _, ok := list[pid]; !ok {
			t.Gone = now
			delete(s.procs, pid)
			// The final counters of a process that exited since the last scan (Windows keeps them while a
			// handle is open).
			if t.h != nil {
				if st, err := t.h.stats(); err == nil {
					t.update(st, now)
				}
				t.h.close()
				t.h = nil
			}
		}
	}
	if _, ok := s.procs[s.daemonPID]; !ok {
		if e, ok := list[s.daemonPID]; ok && len(s.all) == 0 {
			st, _ := startTime(e.PID)
			t := &tracked{PID: e.PID, PPID: e.PPID, Name: e.Name, Role: "daemon", Started: st, Seen: now}
			s.procs[e.PID], s.all = t, append(s.all, t)
		}
	}
	// Children of tree members, breadth first, until no new member is found; a reused pid started before its
	// claimed parent is not a child.
	for added := true; added; {
		added = false
		for pid, e := range list {
			if _, known := s.procs[pid]; known {
				continue
			}
			parent, ok := s.procs[e.PPID]
			if !ok {
				continue
			}
			st, err := startTime(pid)
			if err != nil || st.Before(parent.Started) {
				continue
			}
			t := &tracked{PID: pid, PPID: e.PPID, Name: e.Name, Role: s.role(e.Name, parent.Role, pid), Started: st,
				Seen: now}
			if h, err := openProc(pid); err == nil {
				t.h = h
				if st, err := h.stats(); err == nil {
					t.update(st, now)
				}
			}
			s.procs[pid], s.all = t, append(s.all, t)
			added = true
		}
	}
}

// sampleNow reads the counters of the tree, the daemon's Go runtime and the database files.
func (s *sampler) sampleNow(ctx context.Context, now time.Time) {
	s.scan(now)
	s.mu.Lock()
	phase := s.phase
	smp := sample{At: now, Phase: phase, Roles: map[string]roleSample{}, TreeSize: len(s.procs)}
	list, _ := listProcs()
	// Under the lock: the handles are closed by scan, which the end of a run may call at the same time.
	for _, t := range s.procs {
		var st procStat
		var err error
		if t.h != nil {
			st, err = t.h.stats()
		} else {
			st, err = procStats(t.PID)
		}
		if err != nil {
			continue
		}
		r := smp.Roles[t.Role]
		r.Count++
		r.WS += st.WS
		r.Private += st.Private
		r.Handles += st.Handles
		r.Threads += list[t.PID].Threads
		smp.Roles[t.Role] = r
		t.update(st, now)
		if t.Role == "daemon" {
			smp.DaemonWS, smp.DaemonCPU, smp.DaemonPeak = st.WS, st.CPU, st.PeakWS
		}
	}
	s.mu.Unlock()
	g, err := s.goStats(ctx)
	if err != nil {
		smp.SampleError = err.Error()
		s.noteErr("pprof: " + err.Error())
	}
	smp.Go = g
	smp.DBBytes, smp.WALBytes = fileSize(s.db), fileSize(s.db+"-wal")
	s.mu.Lock()
	s.samples = append(s.samples, smp)
	s.mu.Unlock()
}

// fileSize is the size of an open file as its handle sees it: on Windows the directory entry that os.Stat reads
// lags behind a file another process keeps open.
func fileSize(p string) int64 {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return 0
	}
	return st.Size()
}

// goStats reads /debug/vars (memstats) and the goroutine count from the daemon's pprof server.
func (s *sampler) goStats(ctx context.Context) (goStats, error) {
	var g goStats
	body, err := s.get(ctx, "/debug/vars")
	if err != nil {
		return g, err
	}
	var vars struct {
		Memstats struct {
			HeapAlloc, HeapInuse, HeapSys, Sys, TotalAlloc, Mallocs, PauseTotalNs uint64
			NumGC                                                                 uint32
		} `json:"memstats"`
	}
	if err := json.Unmarshal(body, &vars); err != nil {
		return g, err
	}
	m := vars.Memstats
	g = goStats{HeapAlloc: m.HeapAlloc, HeapInuse: m.HeapInuse, HeapSys: m.HeapSys, Sys: m.Sys,
		TotalAlloc: m.TotalAlloc, Mallocs: m.Mallocs, NumGC: m.NumGC, PauseTotal: m.PauseTotalNs}
	body, err = s.get(ctx, "/debug/pprof/goroutine?debug=1")
	if err != nil {
		return g, err
	}
	line, _, _ := strings.Cut(string(body), "\n")
	if n, ok := strings.CutPrefix(line, "goroutine profile: total "); ok {
		g.Goroutines, _ = strconv.Atoi(strings.TrimSpace(n))
	}
	return g, nil
}

func (s *sampler) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.pprofURL+path, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", path, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// saveProfile writes a pprof endpoint's answer to a file of dir.
func (s *sampler) saveProfile(ctx context.Context, path, file string, timeout time.Duration) error {
	c := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.pprofURL+path, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", path, resp.Status)
	}
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// run scans for spawns every scanEvery and samples every interval until ctx ends.
func (s *sampler) run(ctx context.Context, interval, scanEvery time.Duration) {
	scan := time.NewTicker(scanEvery)
	defer scan.Stop()
	smp := time.NewTicker(interval)
	defer smp.Stop()
	s.sampleNow(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-scan.C:
			s.scan(now)
		case now := <-smp.C:
			s.sampleNow(ctx, now)
		}
	}
}

// readSpawnLog reads the fake CLI's spawn records.
func readSpawnLog(path string) []spawnRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []spawnRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r spawnRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}
