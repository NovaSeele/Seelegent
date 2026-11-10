package launch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// statOf returns the state and parent id of pid from /proc/<pid>/stat.
func statOf(pid int) (state string, ppid int, ok bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", 0, false
	}
	f := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	if len(f) < 2 {
		return "", 0, false
	}
	ppid, err = strconv.Atoi(f[1])
	return f[0], ppid, err == nil
}

// procReport describes pid for a waitDead failure: its state, parent and nice value from /proc/<pid>/stat, the
// whole stat line and /proc/<pid>/status, and this test process's id, so that a zombie left to the test process
// (a child subreaper once a test ran Run in process) can be told from a process that is still running.
func procReport(pid int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\ntest process: %d", os.Getpid())
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		fmt.Fprintf(&b, "\n/proc/%d/stat: %v", pid, err)
		return b.String()
	}
	stat = bytes.TrimSpace(stat)
	// After the name: [0]=state(3) [1]=ppid(4) ... [16]=nice(19).
	if f := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:])); len(f) > 16 {
		fmt.Fprintf(&b, "\nstate %s, parent %s, nice %s", f[0], f[1], f[16])
	}
	fmt.Fprintf(&b, "\n/proc/%d/stat: %s", pid, stat)
	if status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status"); err == nil {
		fmt.Fprintf(&b, "\n/proc/%d/status:\n%s", pid, bytes.TrimSpace(status))
	}
	return b.String()
}

// checkAdopted checks that the daemonised pid is re-parented to the launcher, the child subreaper. The grandchild
// writes its pid file as soon as it runs, while the intermediate that started it may still be exiting (a
// race-enabled test binary lingers at its exit unless GORACE says otherwise): the adoption happens when the
// intermediate is gone, so it is waited for.
func checkAdopted(t *testing.T, pid, launcher int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, ppid, ok := statOf(pid)
		if ok && ppid == launcher {
			return
		}
		if !ok || time.Now().After(deadline) {
			t.Fatalf("process %d has parent %d, want the launcher %d", pid, ppid, launcher)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// zombiesOf lists the zombie children of parent.
func zombiesOf(parent int) []int {
	entries, _ := os.ReadDir("/proc")
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if state, ppid, ok := statOf(pid); ok && ppid == parent && state == "Z" {
			out = append(out, pid)
		}
	}
	return out
}

// TestReapsAdoptedOrphans: a daemonised grandchild that exits by itself is reaped by the launcher (no zombie),
// while the worker runs on, and the worker's own exit code still reaches the exit record.
func TestReapsAdoptedOrphans(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	childPid := filepath.Join(dir, "child.pid")
	h, err := Start(context.Background(), testOptions(dir, "escapeshort:"+childPid, "sleep:2000", "exit:4"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r, _ := h.Ready()
	grandchild := readPid(t, childPid)
	deadline := time.Now().Add(1500 * time.Millisecond)
	for platform.ProcessAlive(grandchild) { // a zombie still counts as alive: gone means reaped
		if time.Now().After(deadline) {
			state, ppid, _ := statOf(grandchild)
			t.Fatalf("orphan %d not reaped: state %s, parent %d (launcher %d)", grandchild, state, ppid, r.LauncherPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if z := zombiesOf(r.LauncherPID); len(z) > 0 {
		t.Fatalf("zombies under the launcher: %v", z)
	}
	if _, ok, _ := h.Exited(); ok || !platform.ProcessAlive(r.WorkerPID) {
		t.Fatal("the worker should still run")
	}
	if ex := wait(t, h); ex.Reason != ReasonExited || ex.Code != 4 {
		t.Fatalf("exit %+v, want exited with 4", ex)
	}
}
