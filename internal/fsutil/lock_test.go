package fsutil

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLockExcludes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	first, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	if first.Path() != path || first.File() == nil {
		t.Fatalf("Path/File = %q/%v", first.Path(), first.File())
	}
	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock: err = %v, want ErrLocked", err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := first.Unlock(); err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after Unlock: %v", err)
	}
	if err := again.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestTryLockMissingDir(t *testing.T) {
	_, err := TryLock(filepath.Join(t.TempDir(), "missing", "x.lock"))
	if err == nil || errors.Is(err, ErrLocked) {
		t.Fatalf("err = %v, want a not-exist error", err)
	}
	if _, err := Lock(t.Context(), filepath.Join(t.TempDir(), "missing", "x.lock")); err == nil {
		t.Fatal("Lock: want a not-exist error")
	}
}

func TestLockWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	unlocked := make(chan struct{})
	go func() {
		defer close(unlocked)
		time.Sleep(120 * time.Millisecond)
		_ = held.Unlock()
	}()
	// Unlock closes its handle after releasing the lock: wait for it before t.TempDir is removed.
	defer func() { <-unlocked }()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	l, err := Lock(ctx, path)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if err := l.Unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestLockHonorsContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := Lock(ctx, path); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

const helperEnv = "SEELEGENT_FSUTIL_LOCK_HELPER"

// TestLockHelperProcess is not a real test: TestLockAcrossProcesses runs the test binary with helperEnv set so that
// this function holds the lock in another process until it is killed.
func TestLockHelperProcess(t *testing.T) {
	path := os.Getenv(helperEnv)
	if path == "" {
		t.Skip("helper process only")
	}
	l, err := TryLock(path)
	if err != nil {
		_, _ = os.Stdout.WriteString("error " + err.Error() + "\n")
		os.Exit(2)
	}
	_, _ = os.Stdout.WriteString("locked\n")
	time.Sleep(time.Minute)
	_ = l.Unlock()
	os.Exit(0)
}

func TestLockAcrossProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.lock")
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestLockHelperProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+path)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil || line != "locked\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("helper said %q, %v", line, err)
	}

	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("TryLock while another process holds it: err = %v, want ErrLocked", err)
	}

	// A crashed holder must not leave the lock stuck: the operating system releases it with the process.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	l, err := Lock(ctx, path)
	if err != nil {
		t.Fatalf("Lock after the holder died: %v", err)
	}
	if err := l.Unlock(); err != nil {
		t.Fatal(err)
	}
}
