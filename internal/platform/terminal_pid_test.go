package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestAttachTerminalPIDFile: a daemon that restarted finds a console's process through its pid file, with the
// start time recorded at open (Windows, old F7) or read now (the Unix script writes only its id). A recorded start
// that does not match the running process means the id was reused: the console is gone.
func TestAttachTerminalPIDFile(t *testing.T) {
	dir := t.TempDir()
	exitFile := filepath.Join(dir, "console.exit")
	pidFile := pidFileFor(exitFile)
	if pidFile != filepath.Join(dir, "console.pid") {
		t.Fatalf("pid file = %s", pidFile)
	}
	if _, _, ok := AttachTerminal(exitFile).Identity(); ok {
		t.Fatal("identity without a pid file")
	}
	me := os.Getpid()
	now, err := ProcessStartTime(me)
	if err != nil {
		t.Skipf("no process start times here: %v", err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(pidFile, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(strconv.Itoa(me)) // the Unix script's format
	if pid, start, ok := AttachTerminal(exitFile).Identity(); !ok || pid != me || start != now ||
		!ProcessRunning(pid, start) {
		t.Fatalf("id only = %d %d %v", pid, start, ok)
	}
	write(strconv.Itoa(me) + " " + strconv.FormatUint(now, 10) + "\n") // the Windows format
	if pid, start, ok := AttachTerminal(exitFile).Identity(); !ok || pid != me || start != now ||
		!ProcessRunning(pid, start) {
		t.Fatalf("id and start = %d %d %v", pid, start, ok)
	}
	write(strconv.Itoa(me) + " " + strconv.FormatUint(now+12345, 10))
	if pid, start, ok := AttachTerminal(exitFile).Identity(); !ok || pid != me || start != now+12345 ||
		ProcessRunning(pid, start) {
		t.Fatalf("reused id = %d %d %v (running %v)", pid, start, ok, ProcessRunning(pid, start))
	}
	for _, bad := range []string{"", "x", "-3 5", "0"} {
		write(bad)
		if _, _, ok := AttachTerminal(exitFile).Identity(); ok {
			t.Errorf("pid file %q gave an identity", bad)
		}
	}
}
