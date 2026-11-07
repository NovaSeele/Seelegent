//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRecordPID: what OpenTerminal records on Windows is what AttachTerminal reads back (old F7).
func TestRecordPID(t *testing.T) {
	exitFile := filepath.Join(t.TempDir(), "console.exit")
	me := os.Getpid()
	if err := recordPID(pidFileFor(exitFile), me); err != nil {
		t.Fatal(err)
	}
	want, err := ProcessStartTime(me)
	if err != nil {
		t.Fatal(err)
	}
	pid, start, ok := AttachTerminal(exitFile).Identity()
	if !ok || pid != me || start != want || !ProcessRunning(pid, start) {
		t.Fatalf("identity = %d %d %v, want %d %d", pid, start, ok, me, want)
	}
}
