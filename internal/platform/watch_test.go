package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestWatchProcess: the watch of a running process stays open until the process is killed, then closes; a start
// time that does not match (a reused id) closes it at once; an unknown start time is refused.
func TestWatchProcess(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	cmd := exec.CommandContext(t.Context(), selfExe(), pidFile)
	cmd.Env = append(os.Environ(), roleEnv+"=sleeper")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitFile(pidFile)
	pid := cmd.Process.Pid
	start, err := ProcessStartTime(pid)
	if err != nil {
		t.Fatal(err)
	}
	done, err := WatchProcess(t.Context(), pid, start)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skipf("no process watch here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("the watch closed while the process runs")
	case <-time.After(200 * time.Millisecond):
	}
	if other, err := WatchProcess(t.Context(), pid, start+1); err != nil {
		t.Fatal(err)
	} else if !isClosed(other) {
		t.Fatal("a watch with another start time stays open")
	}
	if _, err := WatchProcess(t.Context(), pid, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("an unknown start time = %v, want ErrUnsupported", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not close after the process was killed")
	}
}

// TestWatchProcessStopsWithContext: a cancelled watch ends its goroutine and leaves the channel open.
func TestWatchProcessStopsWithContext(t *testing.T) {
	start, err := ProcessStartTime(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done, err := WatchProcess(ctx, os.Getpid(), start)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skipf("no process watch here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	time.Sleep(300 * time.Millisecond)
	if isClosed(done) {
		t.Fatal("a cancelled watch of a running process closed")
	}
}

func isClosed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
