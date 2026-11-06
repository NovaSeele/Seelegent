package launch

import (
	"bytes"
	"context"
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

// checkAdopted checks that the daemonised pid was re-parented to the launcher, the child subreaper.
func checkAdopted(t *testing.T, pid, launcher int) {
	t.Helper()
	if _, ppid, ok := statOf(pid); !ok || ppid != launcher {
		t.Fatalf("process %d has parent %d, want the launcher %d", pid, ppid, launcher)
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
