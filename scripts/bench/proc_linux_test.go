//go:build bench

package bench

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// procRoot is the proc file system.
const procRoot = "/proc"

// clockTicks is USER_HZ, 100 on every Linux the daemon targets.
const clockTicks = 100

// statFields returns the fields of /proc/<pid>/stat after the command name (field 3 onwards, index 0 = state).
func statFields(pid string) (fields []string, name string, err error) {
	data, err := os.ReadFile(filepath.Join(procRoot, pid, "stat"))
	if err != nil {
		return nil, "", err
	}
	open, end := bytes.IndexByte(data, '('), bytes.LastIndexByte(data, ')')
	if open < 0 || end < open {
		return nil, "", fmt.Errorf("/proc/%s/stat: unexpected format", pid)
	}
	return strings.Fields(string(data[end+1:])), string(data[open+1 : end]), nil
}

// listProcs lists every process from /proc.
func listProcs() (map[int]procEntry, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	out := map[int]procEntry{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		f, name, err := statFields(e.Name())
		if err != nil || len(f) < 18 {
			continue
		}
		ppid, _ := strconv.Atoi(f[1])
		threads, _ := strconv.Atoi(f[17])
		out[pid] = procEntry{PID: pid, PPID: ppid, Name: name, Threads: threads}
	}
	return out, nil
}

// bootTime is when the machine started (btime in /proc/stat).
func bootTime() (time.Time, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return time.Unix(n, 0), err
		}
	}
	return time.Time{}, fmt.Errorf("/proc/stat: no btime")
}

func startTime(pid int) (time.Time, error) {
	f, _, err := statFields(strconv.Itoa(pid))
	if err != nil || len(f) < 20 {
		return time.Time{}, fmt.Errorf("/proc/%d/stat: %w", pid, err)
	}
	ticks, _ := strconv.ParseInt(f[19], 10, 64)
	boot, err := bootTime()
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(time.Duration(ticks) * time.Second / clockTicks), nil
}

// statusKB reads the kB values of /proc/<pid>/status by key.
func statusKB(pid string) (map[string]uint64, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, pid, "status"))
	if err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 2 && fs[1] == "kB" {
			n, _ := strconv.ParseUint(fs[0], 10, 64)
			out[k] = n << 10
		}
	}
	return out, nil
}

func pidStats(pid string) (procStat, error) {
	var st procStat
	f, _, err := statFields(pid)
	if err != nil || len(f) < 13 {
		return st, fmt.Errorf("/proc/%s/stat: %w", pid, err)
	}
	ut, _ := strconv.ParseInt(f[11], 10, 64)
	stt, _ := strconv.ParseInt(f[12], 10, 64)
	st.CPU = time.Duration(ut+stt) * time.Second / clockTicks
	kb, err := statusKB(pid)
	if err != nil {
		return st, err
	}
	st.WS, st.PeakWS, st.Private = kb["VmRSS"], kb["VmHWM"], kb["RssAnon"]
	if fds, err := os.ReadDir(filepath.Join(procRoot, pid, "fd")); err == nil {
		st.Handles = len(fds)
	}
	return st, nil
}

func procStats(pid int) (procStat, error) { return pidStats(strconv.Itoa(pid)) }

func selfStats() (procStat, error) { return pidStats("self") }

// procHandle reads a process by pid while it lives; Linux keeps nothing of a process its parent reaped, so the last
// counters are the ones read at the last scan.
type procHandle struct{ pid int }

func openProc(pid int) (*procHandle, error) { return &procHandle{pid: pid}, nil }

func (p *procHandle) stats() (procStat, error) { return procStats(p.pid) }

func (*procHandle) close() {}

// machineMemory reads MemTotal, MemAvailable, CommitLimit and Committed_AS from /proc/meminfo.
func machineMemory() (machineStat, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, "meminfo"))
	if err != nil {
		return machineStat{}, err
	}
	kb := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if fs := strings.Fields(v); ok && len(fs) >= 1 {
			n, _ := strconv.ParseUint(fs[0], 10, 64)
			kb[k] = n << 10
		}
	}
	m := machineStat{TotalPhys: kb["MemTotal"], AvailPhys: kb["MemAvailable"], CommitLimit: kb["CommitLimit"]}
	if c := kb["Committed_AS"]; c < m.CommitLimit {
		m.CommitAvail = m.CommitLimit - c
	}
	return m, nil
}

// osVersion is the kernel release (a WSL kernel names itself microsoft-standard-WSL2).
func osVersion() string {
	data, err := os.ReadFile(filepath.Join(procRoot, "sys", "kernel", "osrelease"))
	if err != nil {
		return "Linux"
	}
	return "Linux " + strings.TrimSpace(string(data))
}
