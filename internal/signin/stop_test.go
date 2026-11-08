package signin

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// fixedTerminal is a window whose process identity is given.
type fixedTerminal struct {
	pid   int
	start uint64
}

func (fixedTerminal) Wait(context.Context) (int, error) { return 0, nil }

func (t fixedTerminal) Identity() (pid int, start uint64, ok bool) { return t.pid, t.start, true }

// Stopping a sign-in window kills its process only while it is the process the window started: a pid the system
// gave to another process since is left alone (M18 hunt, sign-in note).
func TestStopKillsOnlyTheWindowsOwnProcess(t *testing.T) {
	f := newFixture(t, time.Minute)
	f.queue.Push(t, "#!hang\n")
	cmd := exec.Command(os.Args[0]) //nolint:noctx // the test binary as the fake CLI; killed below
	cmd.Env = f.queue.Env(os.Environ())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	pid := cmd.Process.Pid
	start, err := platform.ProcessStartTime(pid)
	if err != nil || start == 0 {
		t.Skipf("no process start time here: %d, %v", start, err)
	}

	f.s.stop(fixedTerminal{pid: pid, start: start + 1}) // another process that got the same pid
	select {
	case <-exited:
		t.Fatal("stop killed a process that is not the window's")
	case <-time.After(500 * time.Millisecond):
	}

	f.s.stop(fixedTerminal{pid: pid, start: start})
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("stop did not kill the window's process")
	}
}
