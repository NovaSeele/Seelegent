//go:build unix && !darwin

package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTerminalCommand(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	look := func(have ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, h := range have {
				if h == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	if argv, reason := terminalCommand(env(nil), look("xterm"), "/s.sh"); argv != nil || !strings.Contains(reason, "DISPLAY") {
		t.Fatalf("no display: %v %q", argv, reason)
	}
	display := env(map[string]string{"DISPLAY": ":0"})
	if argv, _ := terminalCommand(display, look("gnome-terminal"), "/s.sh"); strings.Join(argv, " ") != "/usr/bin/gnome-terminal -- /bin/sh /s.sh" {
		t.Fatalf("gnome: %v", argv)
	}
	if argv, _ := terminalCommand(display, look("xfce4-terminal"), "/s.sh"); strings.Join(argv, " ") != "/usr/bin/xfce4-terminal -x /bin/sh /s.sh" {
		t.Fatalf("xfce: %v", argv)
	}
	withTerm := env(map[string]string{"WAYLAND_DISPLAY": "w", "TERMINAL": "foot"})
	if argv, _ := terminalCommand(withTerm, look("foot", "xterm"), "/s.sh"); strings.Join(argv, " ") != "/usr/bin/foot -e /bin/sh /s.sh" {
		t.Fatalf("$TERMINAL: %v", argv)
	}
	if argv, reason := terminalCommand(display, look(), "/s.sh"); argv != nil || reason == "" {
		t.Fatalf("none: %v %q", argv, reason)
	}
}

// fakeTerminal installs an executable sh script as $TERMINAL (with a display set) and returns its path.
func fakeTerminal(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-terminal")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // must be executable
		t.Fatal(err)
	}
	t.Setenv("DISPLAY", ":0")
	t.Setenv("TERMINAL", path)
}

// TestOpenTerminalLauncherFails reports a terminal launcher that exits with an error as no terminal at once,
// instead of a window that never opens.
func TestOpenTerminalLauncherFails(t *testing.T) {
	fakeTerminal(t, "echo 'cannot open display' >&2\nexit 1")
	start := time.Now()
	_, err := OpenTerminal(context.Background(), TerminalSpec{Argv: []string{"/bin/true"}, ScriptDir: t.TempDir()})
	var nt *NoTerminalError
	if !errors.As(err, &nt) || !strings.Contains(nt.Reason, "cannot open display") {
		t.Fatalf("OpenTerminal = %v, want a NoTerminalError with the launcher's message", err)
	}
	if d := time.Since(start); d >= launcherCheck {
		t.Fatalf("took %s", d)
	}
}

// TestOpenTerminalLauncherOK returns quickly both for a launcher that exits 0 after handing the window over and
// for one that stays alive while it runs the script.
func TestOpenTerminalLauncherOK(t *testing.T) {
	for name, body := range map[string]string{
		"hands over": "shift\nnohup \"$@\" >/dev/null 2>&1 &\nexit 0",
		"stays":      "shift\nexec \"$@\"",
	} {
		t.Run(name, func(t *testing.T) {
			fakeTerminal(t, body)
			start := time.Now()
			term, err := OpenTerminal(context.Background(), TerminalSpec{Argv: []string{"/bin/sh", "-c", "exit 4"},
				ScriptDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d >= launcherCheck {
				t.Fatalf("took %s", d)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if code, err := term.Wait(ctx); err != nil || code != 4 {
				t.Fatalf("Wait = %d, %v", code, err)
			}
		})
	}
}
