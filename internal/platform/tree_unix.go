//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Timing of Tree.Kill on Unix.
const (
	// groupPoll is how often kill checks whether the tree is gone.
	groupPoll = 20 * time.Millisecond
	// killFor bounds the SIGKILL rounds: a tree that keeps forking is walked and killed again until it is empty or
	// this much time has passed.
	killFor = 2 * time.Second
	// closeReapWait bounds how long Close waits for the killed root to be reaped before it stops the reaper.
	closeReapWait = 2 * time.Second
	// freezeRounds bounds the walks that stop a tree before it is signaled.
	freezeRounds = 10
)

// lowNice is the nice value of a low-priority tree.
const lowNice = 10

// treeOS holds the process group of a tree and, for worker trees, its owner.
type treeOS struct {
	pgid      int
	rootStart uint64 // start time of the root; 0 when unknown
	// owner is the calling process of StartWorkerTree (0 for StartTree): its descendants make the tree.
	owner      int
	ownerStart uint64
	// reaper reaps the root and the orphans the owner adopts as a child subreaper (Linux worker trees only).
	reaper *reaper
}

// start runs cmd as the leader of a new process group; its descendants inherit the group. On Linux the root also
// gets SIGKILL when the owner of the tree dies (parentDeathSignal), and a worker tree makes the owner a subreaper.
// A low-priority tree starts at nice 10 (StartTreeWith, StartWorkerTreeWith); the caps of opt do not exist here.
func (t *treeOS) start(cmd *exec.Cmd, worker bool, _ string, opt TreeOptions) error {
	low := opt.Priority == PriorityLow
	sysProcAttr(cmd).Setpgid = true
	parentDeathSignal(cmd)
	niced := false
	if low && !worker {
		niced = runNiced(cmd)
	}
	var r *reaper
	if worker {
		t.owner = os.Getpid()
		t.ownerStart, _ = procStart(t.owner)
		var err error
		if r, err = startReaper(cmd); err != nil {
			return err
		}
	}
	if low && worker {
		// Linux keeps nice per thread, and the child inherits the forking thread's: lower this thread and fork
		// from it. The thread is unlocked again (never left to exit), so the root's parent-death signal, which
		// is tied to it, stays armed.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		lowerNice(unix.PRIO_PROCESS, 0)
	}
	if err := cmd.Start(); err != nil {
		r.stop()
		return err
	}
	t.pgid = cmd.Process.Pid
	t.rootStart, _ = procStart(t.pgid)
	if low && !worker && !niced {
		// No nice program: lower the group now. What the root started before this call and moved out of its
		// group by then keeps the caller's priority.
		lowerNice(unix.PRIO_PGRP, t.pgid)
	}
	if r != nil {
		r.run(t.pgid)
		t.reaper = r
	}
	return nil
}

// runNiced makes cmd run through `nice -n 10` when nice is on PATH, so the root starts at the lower priority and
// everything it starts inherits it. It reports whether it did.
func runNiced(cmd *exec.Cmd) bool {
	if cmd.Path == "" || cmd.Err != nil || len(cmd.Args) == 0 {
		return false
	}
	nice, err := exec.LookPath("nice")
	if err != nil {
		return false
	}
	cmd.Args = append([]string{nice, "-n", strconv.Itoa(lowNice), cmd.Path}, cmd.Args[1:]...)
	cmd.Path = nice
	return true
}

// lowerNice sets the nice value of who (a process or thread, or a process group) to lowNice, unless the caller
// already runs at lowNice or lower priority (an unprivileged process could not go back up). Best effort: a
// refused call leaves the priority as it was.
func lowerNice(which, who int) {
	if cur, err := niceOf(unix.PRIO_PROCESS, 0); err == nil && cur >= lowNice {
		return
	}
	_ = unix.Setpriority(which, who, lowNice)
}

// memoryLimitHit is always false: Unix trees have no memory cap.
func (*treeOS) memoryLimitHit(int) bool { return false }

func (t *treeOS) describe(id string, root int) *WorkerTree {
	if t.owner > 0 {
		return &WorkerTree{ID: id, RootPID: t.owner, RootStart: t.ownerStart}
	}
	return &WorkerTree{ID: id, RootPID: root, RootStart: t.rootStart}
}

func (t *treeOS) wait(cmd *exec.Cmd) error {
	if t.reaper != nil {
		return t.reaper.wait()
	}
	return cmd.Wait()
}

func (t *treeOS) exitCode(cmd *exec.Cmd) int {
	if t.reaper != nil {
		return t.reaper.exitCode()
	}
	if cmd.ProcessState == nil {
		return -1
	}
	return cmd.ProcessState.ExitCode()
}

// kill sends SIGTERM to the tree, waits up to grace for it to empty, then sends SIGKILL until nothing is left.
func (t *treeOS) kill(grace time.Duration) error {
	if t.pgid <= 0 {
		return ErrNotStarted
	}
	if grace > 0 {
		if err := t.signal(syscall.SIGTERM); err != nil {
			return err
		}
		deadline := time.Now().Add(grace)
		for time.Now().Before(deadline) {
			if !t.alive() {
				return nil
			}
			time.Sleep(groupPoll)
		}
	}
	if canListProcs && !t.alive() {
		return nil // nothing left: spare the signal round and its pause
	}
	deadline := time.Now().Add(killFor)
	for {
		if err := t.signal(syscall.SIGKILL); err != nil {
			return err
		}
		if !canListProcs {
			return nil // the group is all we can reach, and its zombies would look alive until the root is reaped
		}
		time.Sleep(groupPoll)
		if !t.alive() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process tree %d: processes still running after SIGKILL", t.pgid)
		}
	}
}

func (t *treeOS) close(cmd *exec.Cmd) error {
	if t.pgid <= 0 {
		return nil
	}
	err := t.kill(0)
	if t.reaper != nil {
		t.reaper.waitRoot(closeReapWait)
		t.reaper.stop()
		_ = cmd.Process.Release() // exec.Cmd.Wait is never called for a reaped root
	}
	return err
}

// signal sends sig to the process group and to every member of the tree found by walking the process list. The
// tree is frozen first, so that no member can exit (re-parenting its children away from the tree) or fork between
// the walk and the signal; members that were stopped for it are continued afterwards to handle sig.
func (t *treeOS) signal(sig syscall.Signal) error {
	if !canListProcs {
		return t.signalGroup(sig) // the group is all we can reach
	}
	members := t.freeze()
	err := t.signalGroup(sig)
	for _, p := range members {
		signalVerified(p, sig)
	}
	if sig != syscall.SIGKILL {
		_ = t.signalGroup(syscall.SIGCONT)
		for _, p := range members {
			signalVerified(p, syscall.SIGCONT)
		}
	}
	return err
}

// freeze stops the tree with SIGSTOP, walking it again until no new member appears (bounded), and returns the
// members it stopped.
func (t *treeOS) freeze() []procInfo {
	_ = t.signalGroup(syscall.SIGSTOP)
	seen := make(map[int]bool)
	var all []procInfo
	for range freezeRounds {
		members, err := t.members()
		if err != nil {
			break
		}
		fresh := 0
		for _, p := range members {
			if p.zombie || seen[p.pid] {
				continue
			}
			seen[p.pid] = true
			all = append(all, p)
			fresh++
			signalVerified(p, syscall.SIGSTOP)
		}
		if fresh == 0 {
			break
		}
	}
	return all
}

// signalGroup sends sig to the tree's process group; an empty group is not an error.
func (t *treeOS) signalGroup(sig syscall.Signal) error {
	if err := syscall.Kill(-t.pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) && !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("signal process group %d: %w", t.pgid, err)
	}
	return nil
}

// alive reports whether a member of the tree is still running (zombies waiting to be reaped do not count).
func (t *treeOS) alive() bool {
	members, err := t.members()
	if err != nil {
		return groupAlive(t.pgid)
	}
	for _, p := range members {
		if !p.zombie {
			return true
		}
	}
	return false
}

// members returns the processes of the tree: the members of its process group and the descendants of its owner
// (worker trees) or of its root. The calling process is never a member.
func (t *treeOS) members() ([]procInfo, error) {
	procs, err := listProcs()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	seen := make(map[int]bool)
	var out []procInfo
	add := func(p procInfo) {
		if p.pid != self && p.pid > 1 && !seen[p.pid] {
			seen[p.pid] = true
			out = append(out, p)
		}
	}
	for _, p := range procs {
		// A group outlives its leader; a process started before the root cannot be in a group that reuses its id.
		if p.pgid == t.pgid && (t.rootStart == 0 || p.start >= t.rootStart) {
			add(p)
		}
	}
	root, rootStart := t.pgid, t.rootStart
	if t.owner > 0 {
		root, rootStart = t.owner, t.ownerStart
	}
	for _, p := range treeOf(procs, root, rootStart) {
		add(p)
	}
	return out, nil
}

// groupAlive reports whether any process (zombies included) is left in group pgid.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// procInfo is one process of a process listing.
type procInfo struct {
	pid, ppid, pgid int
	start           uint64
	zombie          bool
}

// treeOf returns root (when its start time is rootStart, or rootStart is 0) and its descendants by parent id. A
// child must not have started before its parent: such an entry names a parent whose id was reused. A root that is
// gone or has another start time gives an empty tree.
func treeOf(procs []procInfo, root int, rootStart uint64) []procInfo {
	children := make(map[int][]procInfo)
	var top procInfo
	found := false
	for _, p := range procs {
		if p.pid == root {
			top, found = p, true
		}
		if p.pid != p.ppid {
			children[p.ppid] = append(children[p.ppid], p)
		}
	}
	if !found || (rootStart != 0 && top.start != rootStart) {
		return nil
	}
	out := []procInfo{top}
	seen := map[int]bool{top.pid: true}
	for i := 0; i < len(out); i++ {
		parent := out[i]
		for _, c := range children[parent.pid] {
			if !seen[c.pid] && c.start >= parent.start {
				seen[c.pid] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// pids lists the tree of a WorkerTree by parent ids.
func (w *WorkerTree) pids() ([]int, error) {
	if w.RootPID <= 0 {
		return nil, fmt.Errorf("worker tree %q: no root process", w.ID)
	}
	procs, err := listProcs()
	if err != nil {
		return nil, fmt.Errorf("worker tree %q: %w", w.ID, err)
	}
	tree := treeOf(procs, w.RootPID, w.RootStart)
	out := make([]int, 0, len(tree))
	for _, p := range tree {
		out = append(out, p.pid)
	}
	return out, nil
}

func newTreeName() (string, error) { return "", nil }
