//go:build unix && !darwin

package platform

import (
	"errors"
	"strings"
	"testing"
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
