//go:build unix && !darwin

package platform

import (
	"context"
	"os/exec"
	"path/filepath"
)

// knownTerminals lists the emulators tried, in order, after $TERMINAL and x-terminal-emulator.
func knownTerminals() []string {
	return []string{"gnome-terminal", "konsole", "xfce4-terminal", "kitty", "alacritty", "xterm"}
}

// terminalCommand picks a terminal emulator and returns the command that runs script in it, or nil and the reason
// none can be used.
func terminalCommand(getenv func(string) string, lookPath func(string) (string, error), script string) (argv []string, reason string) {
	if getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "" {
		return nil, "no graphical session (DISPLAY and WAYLAND_DISPLAY are not set)"
	}
	candidates := make([]string, 0, 8)
	if t := getenv("TERMINAL"); t != "" {
		candidates = append(candidates, t)
	}
	candidates = append(candidates, "x-terminal-emulator")
	candidates = append(candidates, knownTerminals()...)
	for _, c := range candidates {
		path, err := lookPath(c)
		if err != nil {
			continue
		}
		switch filepath.Base(c) {
		case "gnome-terminal":
			return []string{path, "--", "/bin/sh", script}, ""
		case "xfce4-terminal":
			return []string{path, "-x", "/bin/sh", script}, ""
		default:
			return []string{path, "-e", "/bin/sh", script}, ""
		}
	}
	return nil, "no terminal emulator found (set $TERMINAL)"
}

func openURLCommand(ctx context.Context, u string) *exec.Cmd {
	return exec.CommandContext(ctx, "xdg-open", u)
}
