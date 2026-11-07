package setup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// RunResult is the outcome of a host CLI that setup ran (claude mcp add, ...).
type RunResult struct {
	// Code is the exit code (0 on success).
	Code   int
	Stdout string
	Stderr string
}

// Env is everything setup reads from the machine. Every field is injectable, and the zero values are inert (no
// environment variables, no programs on PATH, no commands run), so tests and dry runs never reach the real home
// directory or the real host configs by accident. The command line fills it from the process.
type Env struct {
	// Home is the user's home directory (absolute). Required.
	Home string
	// GOOS selects the per-OS config locations; runtime.GOOS when empty.
	GOOS string
	// Getenv reads an environment variable; nil reads nothing.
	Getenv func(string) string
	// LookPath finds a program on PATH; nil finds nothing.
	LookPath func(string) (string, error)
	// Run runs a host CLI and returns its outcome; nil fails every run.
	Run func(ctx context.Context, name string, args ...string) (RunResult, error)
}

func (e Env) goos() string {
	if e.GOOS != "" {
		return e.GOOS
	}
	return runtime.GOOS
}

func (e Env) getenv(k string) string {
	if e.Getenv == nil {
		return ""
	}
	return strings.TrimSpace(e.Getenv(k))
}

// lookPath returns the path of a program on PATH, or "".
func (e Env) lookPath(name string) string {
	if e.LookPath == nil {
		return ""
	}
	p, err := e.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

func (e Env) run(ctx context.Context, name string, args ...string) (RunResult, error) {
	if e.Run == nil {
		return RunResult{}, errors.New("running host commands is not available")
	}
	return e.Run(ctx, name, args...)
}

// home joins elem onto the home directory.
func (e Env) home(elem ...string) string {
	return filepath.Join(append([]string{e.Home}, elem...)...)
}

// appData is the roaming application data folder on Windows (%APPDATA%).
func (e Env) appData() string {
	if v := e.getenv("APPDATA"); v != "" {
		return v
	}
	return e.home("AppData", "Roaming")
}

// localAppData is the local application data folder on Windows (%LOCALAPPDATA%).
func (e Env) localAppData() string {
	if v := e.getenv("LOCALAPPDATA"); v != "" {
		return v
	}
	return e.home("AppData", "Local")
}

// xdgConfig is $XDG_CONFIG_HOME or ~/.config (also used on Windows by the CLIs that follow XDG, like OpenCode).
func (e Env) xdgConfig() string {
	if v := e.getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	return e.home(".config")
}

// appConfigDir is where desktop apps keep their settings: %APPDATA% on Windows, ~/Library/Application Support on
// macOS, $XDG_CONFIG_HOME elsewhere.
func (e Env) appConfigDir() string {
	switch e.goos() {
	case "windows":
		return e.appData()
	case "darwin":
		return e.home("Library", "Application Support")
	default:
		return e.xdgConfig()
	}
}

// exists reports whether something is at p.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// isDir reports whether p is a directory.
func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// notExist reports whether err says the file is missing.
func notExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
