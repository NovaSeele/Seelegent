package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Linux trees are exact: a worker tree's owner is a child subreaper, so no descendant can re-parent away from it.
const (
	treesExact         = true
	subreaperSupported = true
	canListProcs       = true
)

// parentDeathSignal makes Linux send SIGKILL to the root of the tree when the process that started it dies, so a
// worker does not outlive a launcher that crashed or was killed.
//
// Limits: the signal reaches the root process only (its own children get no signal; a CLI that dies takes its
// usual cleanup path, and Kill/Close still cover the whole tree while the owner lives), and Linux ties it to the
// OS thread that started the process, not to the whole process. The Go runtime keeps its threads unless a
// goroutine exits while locked to its thread (runtime.LockOSThread), so StartTree must not be called from such a
// goroutine.
func parentDeathSignal(cmd *exec.Cmd) {
	sysProcAttr(cmd).Pdeathsig = syscall.SIGKILL
}

// becomeSubreaper makes orphaned descendants of this process re-parent to it instead of init.
func becomeSubreaper() error {
	return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}

// signalVerified sends sig to p unless its id now names another process. A pidfd pins the process while its start
// time is compared, so the signal cannot reach a process that reused the id; kernels without pidfd_open (before
// 5.3) fall back to a start-time check followed by kill.
func signalVerified(p procInfo, sig syscall.Signal) {
	fd, err := unix.PidfdOpen(p.pid, 0)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if cur, err := readProcStat(p.pid); err == nil && cur.start == p.start {
			_ = syscall.Kill(p.pid, sig)
		}
		return
	}
	defer func() { _ = unix.Close(fd) }()
	if cur, err := readProcStat(p.pid); err != nil || cur.start != p.start {
		return
	}
	_ = unix.PidfdSendSignal(fd, sig, nil, 0)
}

// listProcs lists every process in /proc. Processes that end while it reads are left out.
func listProcs() ([]procInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	out := make([]procInfo, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if p, err := readProcStat(pid); err == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// procStart returns the start time of pid in clock ticks since boot (field 22 of /proc/<pid>/stat).
func procStart(pid int) (uint64, error) {
	p, err := readProcStat(pid)
	if err != nil {
		return 0, err
	}
	return p.start, nil
}

// readProcStat reads /proc/<pid>/stat.
func readProcStat(pid int) (procInfo, error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procInfo{}, err
	}
	p, err := parseProcStat(data)
	if err != nil {
		return procInfo{}, fmt.Errorf("/proc/%d/stat: %w", pid, err)
	}
	return p, nil
}

// parseProcStat parses a /proc/<pid>/stat line: "pid (comm) state ppid pgrp session ... starttime ...". The
// command name may hold spaces and parentheses, so the fields are counted from the last ')'.
func parseProcStat(data []byte) (procInfo, error) {
	open := bytes.IndexByte(data, '(')
	closing := bytes.LastIndexByte(data, ')')
	if open < 0 || closing < open {
		return procInfo{}, errors.New("malformed stat line")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data[:open])))
	if err != nil {
		return procInfo{}, fmt.Errorf("malformed pid: %w", err)
	}
	// After the name: [0]=state(3) [1]=ppid(4) [2]=pgrp(5) ... [19]=starttime(22).
	f := strings.Fields(string(data[closing+1:]))
	if len(f) < 20 {
		return procInfo{}, errors.New("short stat line")
	}
	ppid, err1 := strconv.Atoi(f[1])
	pgid, err2 := strconv.Atoi(f[2])
	start, err3 := strconv.ParseUint(f[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return procInfo{}, fmt.Errorf("malformed stat line: %w", err)
	}
	return procInfo{pid: pid, ppid: ppid, pgid: pgid, start: start, zombie: f[0] == "Z" || f[0] == "X"}, nil
}
