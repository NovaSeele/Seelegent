package platform

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// canListProcs: macOS lists processes through sysctl kern.proc.
const canListProcs = true

// sZomb is the p_stat value of a zombie (SZOMB in sys/proc.h).
const sZomb = 5

// listProcs lists every process through sysctl kern.proc.all.
func listProcs() ([]procInfo, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	out := make([]procInfo, 0, len(kps))
	for i := range kps {
		out = append(out, kinfoToProc(&kps[i]))
	}
	return out, nil
}

// procStart returns the start time of pid in microseconds since the epoch.
func procStart(pid int) (uint64, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, fmt.Errorf("process %d: %w", pid, err)
	}
	if int(kp.Proc.P_pid) != pid {
		return 0, fmt.Errorf("process %d: %w", pid, syscall.ESRCH)
	}
	return kinfoToProc(kp).start, nil
}

func kinfoToProc(kp *unix.KinfoProc) procInfo {
	st := kp.Proc.P_starttime
	return procInfo{
		pid:    int(kp.Proc.P_pid),
		ppid:   int(kp.Eproc.Ppid),
		pgid:   int(kp.Eproc.Pgid),
		start:  uint64(st.Sec)*1_000_000 + uint64(st.Usec), //nolint:gosec // start times are positive
		zombie: kp.Proc.P_stat == sZomb,
	}
}
