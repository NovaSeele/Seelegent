// Package gate runs the gate after a worker's turn (milestone 13, docs/gates.md): it builds the plan of steps from
// the task's lane, the turn's hygiene findings, syntax checks of the changed files, the frozen doctrine and the
// gate commands, runs them in order from the repository root (the first failing step stops the rest), keeps the
// end of each command's output, and writes the prompt of an automatic fix turn.
package gate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Limits of a command run.
const (
	// MaxLogBytes caps a command's log file; the rest of the output is dropped from it (the tail keeps the end).
	MaxLogBytes = 256 << 10
	// DefaultTailBytes is the output tail kept when the setting is not given.
	DefaultTailBytes = 2000
	// killGrace is how long a timed-out or cancelled command's tree gets to stop before it is killed.
	killGrace = 2 * time.Second
	// pipeGrace is how long the output pipes may stay open after the command exited (a background child).
	pipeGrace = 3 * time.Second
)

// ExecOptions configure one command run.
type ExecOptions struct {
	Dir     string
	Env     []string
	Timeout time.Duration
	// TailBytes is how much of the end of the output to keep.
	TailBytes int
	// LogFile, when set, receives the output (capped at MaxLogBytes).
	LogFile string
	// Tree are the resource limits of the command's process tree (gatePriority); the zero value runs it without
	// limits.
	Tree platform.TreeOptions
}

// ExecResult is how a command ended.
type ExecResult struct {
	// Exit is the exit code; nil when the command did not exit by itself (timeout, cancel, start failure).
	Exit      *int
	Ms        int64
	TimedOut  bool
	Cancelled bool
	Tail      string
	// Err says why the command could not run, or "timeout"/"cancelled".
	Err string
}

// OK reports whether the command exited with code 0.
func (r ExecResult) OK() bool { return r.Exit != nil && *r.Exit == 0 }

// Exec runs cmdline through the system shell (cmd.exe on Windows, /bin/sh elsewhere) in its own process tree, which
// is killed when the timeout passes or ctx ends. It never returns an error: failures are in the result.
func Exec(ctx context.Context, cmdline string, o ExecOptions) ExecResult {
	if o.TailBytes <= 0 {
		o.TailBytes = DefaultTailBytes
	}
	out := &output{max: o.TailBytes}
	if o.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(o.LogFile), 0o700); err == nil {
			if f, err := os.OpenFile(o.LogFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600); err == nil {
				out.log = f
				defer func() { _ = f.Close() }()
			}
		}
	}
	cmd := shellCommand(cmdline)
	cmd.Dir, cmd.Env = o.Dir, o.Env
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = pipeGrace
	began := time.Now()
	res := ExecResult{}
	if err := ctx.Err(); err != nil {
		res.Cancelled, res.Err = true, "cancelled"
		return res
	}
	tree, err := platform.StartTreeWith(cmd, o.Tree)
	if err != nil {
		res.Err = fmt.Sprintf("cannot start the command: %v", err)
		res.Tail = res.Err
		return res
	}
	defer func() { _ = tree.Close() }()
	var timer <-chan time.Time
	if o.Timeout > 0 {
		t := time.NewTimer(o.Timeout)
		defer t.Stop()
		timer = t.C
	}
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer:
		res.TimedOut = true
		_ = tree.Kill(killGrace)
		waitErr = <-done
	case <-ctx.Done():
		res.Cancelled = true
		_ = tree.Kill(killGrace)
		waitErr = <-done
	}
	_ = tree.Close() // whatever the command left running in the background
	res.Ms = time.Since(began).Milliseconds()
	res.Tail = out.tail()
	switch {
	case res.TimedOut:
		res.Err = fmt.Sprintf("timeout after %s", o.Timeout)
	case res.Cancelled:
		res.Err = "cancelled"
	default:
		var ee *exec.ExitError
		if waitErr == nil || errors.As(waitErr, &ee) || errors.Is(waitErr, exec.ErrWaitDelay) {
			code := tree.ExitCode()
			res.Exit = &code
		} else {
			res.Err = waitErr.Error()
		}
	}
	if res.Tail == "" && res.Err != "" {
		res.Tail = res.Err
	}
	return res
}

// output collects a command's output: the log file (capped) and the tail.
type output struct {
	mu     sync.Mutex
	max    int
	buf    []byte
	log    *os.File
	logged int
}

func (o *output) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.log != nil && o.logged < MaxLogBytes {
		n := min(len(p), MaxLogBytes-o.logged)
		if _, err := o.log.Write(p[:n]); err == nil {
			o.logged += n
			if o.logged >= MaxLogBytes {
				_, _ = o.log.WriteString("\n[log truncated]\n")
			}
		}
	}
	o.buf = append(o.buf, p...)
	if len(o.buf) > 2*o.max {
		o.buf = append(o.buf[:0], o.buf[len(o.buf)-o.max:]...)
	}
	return len(p), nil
}

// tail returns the last max bytes as valid UTF-8 (a character cut at the start is dropped).
func (o *output) tail() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return Tail(string(o.buf), o.max)
}

// Tail returns the end of s, at most n bytes, starting at a character boundary.
func Tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for s != "" && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}
