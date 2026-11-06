package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ErrNoTerminal is matched (errors.Is) by the *NoTerminalError OpenTerminal returns when it cannot open a terminal
// window on this machine (no desktop session, no terminal emulator).
var ErrNoTerminal = errors.New("no terminal window available")

// NoTerminalError tells the user to run the sign-in command themselves.
type NoTerminalError struct {
	// Command is a shell command that runs the same thing in a terminal the user opens.
	Command string
	// Reason says why no window could be opened.
	Reason string
}

// Error implements error.
func (e *NoTerminalError) Error() string {
	return fmt.Sprintf("%s: %s; run this command in a terminal: %s", ErrNoTerminal, e.Reason, e.Command)
}

// Is reports whether target is ErrNoTerminal.
func (e *NoTerminalError) Is(target error) bool { return target == ErrNoTerminal }

// TerminalSpec describes a command to run in a new, visible terminal window so the user can interact with it (an
// OAuth sign-in that asks for a code, for example).
type TerminalSpec struct {
	// Title is the window title. Characters other than letters, digits, space, '.', '_' and '-' are dropped.
	Title string
	// Banner lines are printed before the command runs.
	Banner []string
	// Argv is the command and its arguments. Argv[0] should be an absolute path.
	Argv []string
	// Dir is the working directory of the command.
	Dir string
	// Env is the full environment of the command; nil means the environment of this process.
	Env []string
	// StdoutPath, when set, receives the command's standard output instead of the window.
	StdoutPath string
	// ScriptDir is where the small launcher script and the exit-code file are written (for example the account's
	// profile directory). It must exist.
	ScriptDir string
	// Name is the base name of those files (default "seelegent-terminal").
	Name string
}

// Terminal is a terminal window opened by OpenTerminal.
type Terminal struct {
	exitFile string
	process  waiter
	// pid is the process that keeps the command running (Windows: the hidden cmd.exe that lives until the window
	// closes); 0 on Unix, where the script writes its own id to pidFile.
	pid     int
	pidFile string
}

// Identity returns the process that runs the terminal's command and its start time (ProcessStartTime), so a
// daemon that restarted can tell with ProcessRunning whether the console still runs. On Unix it is the shell that
// runs the script, known once the script has started (ok is false before that); on Windows the hidden cmd.exe that
// lives until the window closes.
func (t *Terminal) Identity() (pid int, start uint64, ok bool) {
	pid = t.pid
	if pid == 0 && t.pidFile != "" {
		data, err := os.ReadFile(t.pidFile)
		if err != nil {
			return 0, 0, false
		}
		if pid, err = strconv.Atoi(strings.TrimSpace(string(data))); err != nil || pid <= 0 {
			return 0, 0, false
		}
	}
	if pid <= 0 {
		return 0, 0, false
	}
	start, err := ProcessStartTime(pid)
	if err != nil {
		start = 0 // unknown: ProcessRunning then checks the id only
	}
	return pid, start, true
}

// ProcessRunning reports whether process pid still runs and is the one that started at start (from
// ProcessStartTime). A start of 0 means unknown; only the id is checked then.
func ProcessRunning(pid int, start uint64) bool {
	if !ProcessAlive(pid) {
		return false
	}
	if start == 0 {
		return true
	}
	now, err := ProcessStartTime(pid)
	return err != nil || now == start
}

// waiter is the part of the window process Terminal needs (nil when the window cannot be waited for directly).
type waiter interface {
	Wait() error
}

// terminalPoll is how often Wait checks for the exit-code file.
const terminalPoll = 250 * time.Millisecond

// Wait waits until the command in the window has finished and returns its exit code. If the user closes the window
// before the command finishes, no exit code is recorded and Wait returns an error once the window process is gone
// (Windows) or blocks until ctx is done (other systems, where the window is not a child of this process): always
// pass a context with a deadline.
func (t *Terminal) Wait(ctx context.Context) (int, error) {
	gone := make(chan struct{})
	if t.process != nil {
		go func() {
			_ = t.process.Wait()
			close(gone)
		}()
	}
	ticker := time.NewTicker(terminalPoll)
	defer ticker.Stop()
	for {
		if code, ok := readExitCode(t.exitFile); ok {
			return code, nil
		}
		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("terminal: %w", context.Cause(ctx))
		case <-gone:
			if code, ok := readExitCode(t.exitFile); ok {
				return code, nil
			}
			return -1, errors.New("terminal: the window was closed before the command finished")
		case <-ticker.C:
		}
	}
}

// readExitCode reads the exit-code file the terminal script writes when the command ends.
func readExitCode(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return code, true
}

// OpenTerminal opens a new terminal window running spec.Argv and returns at once. On Windows it is a new console
// window; on macOS a Terminal.app window (through osascript); on Linux and other Unix systems $TERMINAL,
// x-terminal-emulator or a known emulator. When no window can be opened it returns a *NoTerminalError with a
// command the user can run instead.
func OpenTerminal(ctx context.Context, spec TerminalSpec) (*Terminal, error) {
	if len(spec.Argv) == 0 {
		return nil, errors.New("open terminal: empty command")
	}
	if spec.ScriptDir == "" {
		return nil, errors.New("open terminal: no script directory")
	}
	name := spec.Name
	if name == "" {
		name = "seelegent-terminal"
	}
	exitFile := filepath.Join(spec.ScriptDir, name+".exit")
	if err := os.Remove(exitFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	if err := os.Remove(pidFileFor(exitFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	spec.Title = safeTitle(spec.Title)
	return openTerminal(ctx, spec, filepath.Join(spec.ScriptDir, name), exitFile)
}

// safeTitle keeps letters, digits, space and ._- so a title can be put into a script without quoting.
func safeTitle(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ._-", r) {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "Seelegent"
	}
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

// pidFileFor is where a Unix terminal script writes its process id: next to the exit file, as <name>.pid.
func pidFileFor(exitFile string) string { return strings.TrimSuffix(exitFile, ".exit") + ".pid" }

// shQuote quotes s for a POSIX shell.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// envDelta returns the assignments of env that differ from base and the names in base that env drops.
func envDelta(base, env []string) (set, unset []string) {
	bm := envMap(base)
	em := envMap(env)
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		if old, ok := bm[k]; !ok || old != v {
			set = append(set, kv)
		}
	}
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := em[k]; !ok && k != "" {
			unset = append(unset, k)
		}
	}
	return set, unset
}

// envMap indexes KEY=VALUE pairs by key (the last one wins, as in exec.Cmd).
func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// validEnvName reports whether name can be exported by a POSIX shell.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		letter := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		digit := r >= '0' && r <= '9'
		if !letter && (i == 0 || !digit) {
			return false
		}
	}
	return true
}

// unixScript builds the sh script a Unix terminal runs: environment changes, banner, the command, and the exit
// code written to exitFile.
//
// The script writes its own process id next to the exit file (<name>.pid, see Terminal.Identity), and a window
// closed before the command ends (SIGHUP; also SIGINT and SIGTERM) still writes exit code 129, so Wait returns.
func unixScript(spec TerminalSpec, exitFile string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("printf '%s' \"$$\" > " + shQuote(pidFileFor(exitFile)) + "\n")
	b.WriteString("trap 'printf 129 > " + strings.ReplaceAll(shQuote(exitFile), "'", `'\''`) +
		"; exit 129' HUP INT TERM\n")
	if spec.Env != nil {
		set, unset := envDelta(os.Environ(), spec.Env)
		for _, k := range unset {
			if validEnvName(k) {
				b.WriteString("unset " + k + "\n")
			}
		}
		for _, kv := range set {
			k, v, _ := strings.Cut(kv, "=")
			if validEnvName(k) {
				b.WriteString("export " + k + "=" + shQuote(v) + "\n")
			}
		}
	}
	if spec.Dir != "" {
		b.WriteString("cd " + shQuote(spec.Dir) + " || exit 1\n")
	}
	b.WriteString("printf '\\033]0;%s\\007' " + shQuote(spec.Title) + "\n")
	for _, line := range spec.Banner {
		b.WriteString("printf '%s\\n' " + shQuote(line) + "\n")
	}
	quoted := make([]string, len(spec.Argv))
	for i, a := range spec.Argv {
		quoted[i] = shQuote(a)
	}
	b.WriteString(strings.Join(quoted, " "))
	if spec.StdoutPath != "" {
		b.WriteString(" > " + shQuote(spec.StdoutPath))
	}
	b.WriteString("\nrc=$?\n")
	b.WriteString("printf '%s' \"$rc\" > " + shQuote(exitFile) + "\n")
	b.WriteString("printf '\\nDone (exit code %s). This window can be closed.\\n' \"$rc\"\n")
	b.WriteString("sleep 3\nexit \"$rc\"\n")
	return b.String()
}

// cmdQuote quotes one argument for a line of a Windows batch file. Quotes and line breaks cannot be passed
// safely, so they are refused; % is doubled so the batch parser does not expand it.
func cmdQuote(s string) (string, error) {
	if strings.ContainsAny(s, "\"\r\n") {
		return "", fmt.Errorf("argument %q: quotes and line breaks are not supported in a terminal command", s)
	}
	return `"` + strings.ReplaceAll(s, "%", "%%") + `"`, nil
}

// batchEcho returns an echo line for a batch file with the characters cmd treats specially escaped.
func batchEcho(line string) string {
	if strings.TrimSpace(line) == "" {
		return "echo."
	}
	r := strings.NewReplacer("^", "^^", "&", "^&", "|", "^|", "<", "^<", ">", "^>", "%", "%%", "\r", "", "\n", " ")
	return "echo " + r.Replace(line)
}

// windowsScript builds the batch file a Windows console window runs.
func windowsScript(spec TerminalSpec, exitFile string) (string, error) {
	lines := []string{"@echo off", "chcp 65001 >nul", "title " + spec.Title}
	for _, l := range spec.Banner {
		lines = append(lines, batchEcho(l))
	}
	if spec.Dir != "" {
		d, err := cmdQuote(spec.Dir)
		if err != nil {
			return "", err
		}
		lines = append(lines, "cd /d "+d)
	}
	parts := make([]string, 0, len(spec.Argv)+2)
	for _, a := range spec.Argv {
		q, err := cmdQuote(a)
		if err != nil {
			return "", err
		}
		parts = append(parts, q)
	}
	if spec.StdoutPath != "" {
		q, err := cmdQuote(spec.StdoutPath)
		if err != nil {
			return "", err
		}
		parts = append(parts, ">", q)
	}
	ef, err := cmdQuote(exitFile)
	if err != nil {
		return "", err
	}
	lines = append(lines,
		strings.Join(parts, " "),
		"set SEELEGENT_RC=%errorlevel%",
		"(echo %SEELEGENT_RC%)>"+ef,
		"echo.",
		"echo Done (exit code %SEELEGENT_RC%). This window closes by itself.",
		"timeout /t 5 >nul",
		"exit /b %SEELEGENT_RC%",
		"",
	)
	return strings.Join(lines, "\r\n"), nil
}
