//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestReadRegularFIFO: a FIFO without a writer, or a link to one, is refused at once instead of blocking.
func TestReadRegularFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fifo, link} {
		done := make(chan error, 1)
		go func() {
			_, _, err := ReadRegular(p, 64)
			done <- err
		}()
		select {
		case err := <-done:
			if !errors.Is(err, ErrNotRegular) {
				t.Fatalf("%s: err = %v", p, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: ReadRegular blocked on a FIFO", p)
		}
	}
	// The open itself, past the Lstat check (the path swapped to a FIFO after it): O_NONBLOCK returns at once and
	// the Stat of the open file refuses it.
	done := make(chan error, 1)
	go func() {
		f, err := openNoBlock(fifo)
		if err == nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("open: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("openNoBlock blocked on a FIFO")
	}
}
