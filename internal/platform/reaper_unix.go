//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// reapPoll is how often the reaper looks for exited children even without SIGCHLD (signals can coalesce).
const reapPoll = time.Second

// errReaperStopped is what Wait returns when the tree was closed before its root could be reaped.
var errReaperStopped = errors.New("process tree closed before its root was reaped")

// reaper reaps every child of a process that is a child subreaper: the root of its worker tree and every orphan it
// adopts. It owns waiting for the root as well, with wait4, so that reaping orphans (wait4(-1)) never steals the
// root's exit status; exec.Cmd.Wait is never called for that root.
type reaper struct {
	sigs     chan os.Signal
	quit     chan struct{}
	exited   chan struct{} // closed when the loop has ended
	rootDone chan struct{} // closed when the root was reaped (or the reaper stopped)

	// Written by the loop goroutine before rootDone is closed; read only after it is.
	root    int
	status  syscall.WaitStatus
	reaped  bool
	waitErr error
}

// startReaper makes the calling process a child subreaper and starts listening for SIGCHLD, before cmd is started
// so that no exit is missed. It returns nil where there is no subreaper (macOS, the BSDs).
func startReaper(cmd *exec.Cmd) (*reaper, error) {
	if !subreaperSupported {
		return nil, nil
	}
	if cmd.Cancel != nil {
		return nil, errors.New("start worker tree: the command must not have a context")
	}
	for _, s := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
		if _, isFile := s.(*os.File); s != nil && !isFile {
			return nil, errors.New("start worker tree: standard streams must be files or nil")
		}
	}
	if err := becomeSubreaper(); err != nil {
		return nil, fmt.Errorf("start worker tree: become child subreaper: %w", err)
	}
	r := &reaper{
		sigs:     make(chan os.Signal, 1),
		quit:     make(chan struct{}),
		exited:   make(chan struct{}),
		rootDone: make(chan struct{}),
	}
	signal.Notify(r.sigs, syscall.SIGCHLD)
	return r, nil
}

// run starts reaping, now that the root has the id root.
func (r *reaper) run(root int) {
	r.root = root
	go r.loop()
}

func (r *reaper) loop() {
	defer close(r.exited)
	ticker := time.NewTicker(reapPoll)
	defer ticker.Stop()
	for {
		r.reapAll()
		select {
		case <-r.sigs:
		case <-ticker.C:
		case <-r.quit:
			r.reapAll()
			return
		}
	}
}

// reapAll reaps every child that has exited, without blocking.
func (r *reaper) reapAll() {
	for {
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return // ECHILD: no children; 0: none has exited
		}
		if pid == r.root && !r.reaped {
			r.status, r.reaped = ws, true
			close(r.rootDone)
		}
	}
}

// wait waits until the root was reaped and returns its result like exec.Cmd.Wait.
func (r *reaper) wait() error {
	<-r.rootDone
	if r.waitErr != nil {
		return r.waitErr
	}
	switch {
	case r.status.Exited() && r.status.ExitStatus() == 0:
		return nil
	case r.status.Signaled():
		return fmt.Errorf("signal: %v", r.status.Signal())
	default:
		return fmt.Errorf("exit status %d", r.status.ExitStatus())
	}
}

// exitCode returns the root's exit code, or -1 if it has not been reaped or was ended by a signal.
func (r *reaper) exitCode() int {
	select {
	case <-r.rootDone:
	default:
		return -1
	}
	if r.waitErr != nil || !r.status.Exited() {
		return -1
	}
	return r.status.ExitStatus()
}

// waitRoot waits up to d for the root to be reaped.
func (r *reaper) waitRoot(d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-r.rootDone:
	case <-t.C:
	}
}

// stop ends the reaper after a last pass. A root that was never reaped makes Wait return errReaperStopped. It is
// safe on a nil reaper.
func (r *reaper) stop() {
	if r == nil {
		return
	}
	if r.root == 0 { // never started: the command did not start
		signal.Stop(r.sigs)
		return
	}
	close(r.quit)
	<-r.exited
	signal.Stop(r.sigs)
	if !r.reaped {
		r.waitErr = errReaperStopped
		r.reaped = true
		close(r.rootDone)
	}
}
