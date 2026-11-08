package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// TestOldPIDFile: the old Node version's daemon.pid is reported, never deleted; whether its process is gone, is the
// old daemon, or is another process that got the pid tells what the user should do (final verification N5).
func TestOldPIDFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, OldPIDFile)
	none := func(context.Context) ([]platform.Process, error) { return nil, nil }
	if _, ok := oldPIDFile(t.Context(), home, none); ok {
		t.Fatal("a check without a pid file")
	}
	// A process that has exited: its pid is not running.
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	dead := cmd.Process.Pid
	self := os.Getpid()
	oldDaemon := func(context.Context) ([]platform.Process, error) {
		return []platform.Process{{PID: self, Name: "node.exe", Args: []string{"node.exe",
			`C:\Users\me\AppData\Local\Programs\seelegent\src\cli.js`, "daemon"}}}, nil
	}
	for _, tc := range []struct {
		name, content string
		list          func(context.Context) ([]platform.Process, error)
		message, fix  string
	}{
		{"stale", strconv.Itoa(dead) + "\n", none, "which is not running", "delete it"},
		{"old daemon runs", strconv.Itoa(self), oldDaemon, "the old Node daemon named in", "seelegent stop"},
		{"pid reused", strconv.Itoa(self), none, "is not the old daemon", "delete it"},
		{"processes unknown", strconv.Itoa(self), nil, "which is running", "if that is not the old"},
		{"no pid", "{}", none, "holds no pid", "delete it"},
	} {
		if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
			t.Fatal(err)
		}
		c, ok := oldPIDFile(t.Context(), home, tc.list)
		if !ok || c.Status != Warn || c.Name != "old_pid_file" || !strings.Contains(c.Message, tc.message) ||
			!strings.Contains(c.Fix, tc.fix) {
			t.Errorf("%s: %+v", tc.name, c)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: doctor removed the pid file: %v", tc.name, err)
		}
	}
}
