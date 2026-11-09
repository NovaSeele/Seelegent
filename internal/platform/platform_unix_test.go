//go:build unix

package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/NovaSeele/Seelegent/internal/testutil/priority"
)

// runOSRole runs the Unix-only helper roles:
//
//	escape-root <pidFile> <long|short> <exit|sleep>  starts a daemonised grandchild (setsid + double fork), writes
//	                                                 "self grandchild" to pidFile, then exits 5 at once or sleeps
//	                                                 1.5 s and exits 4
//	daemonize <pidFile> <long|short>                 starts the grandchild in a new session and exits at once
//	short-sleeper <pidFile>                          writes its pid and exits after 300 ms
//	subreaper-owner <dir> <kill|reap>                owns a worker tree of escape-root and checks it (see below)
func runOSRole(role string) bool {
	args := os.Args[1:]
	switch role {
	case "escape-root":
		pidFile, life, action := args[0], args[1], args[2]
		mid := exec.CommandContext(context.Background(), selfExe(), pidFile+".child", life)
		mid.Env = append(os.Environ(), roleEnv+"=daemonize")
		mid.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := mid.Run(); err != nil {
			os.Exit(3)
		}
		waitFile(pidFile + ".child")
		grandchild := readPidsNoT(pidFile + ".child")
		writePids(pidFile, os.Getpid(), grandchild)
		if action == "sleep" {
			time.Sleep(1500 * time.Millisecond)
			os.Exit(4)
		}
		os.Exit(5)
	case "daemonize":
		pidFile, life := args[0], args[1]
		role := "sleeper"
		if life == "short" {
			role = "short-sleeper"
		}
		g := exec.CommandContext(context.Background(), selfExe(), pidFile)
		g.Env = append(os.Environ(), roleEnv+"="+role)
		if err := g.Start(); err != nil {
			os.Exit(3)
		}
		os.Exit(0) // without waiting: the grandchild is orphaned
	case "setsid-parent":
		pidFile := args[len(args)-1]
		child := exec.CommandContext(context.Background(), selfExe(), pidFile+".child")
		child.Env = append(os.Environ(), roleEnv+"=sleeper")
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		waitFile(pidFile + ".child")
		writePids(pidFile, os.Getpid(), child.Process.Pid)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "short-sleeper":
		writePids(args[len(args)-1], os.Getpid())
		time.Sleep(300 * time.Millisecond)
		os.Exit(0)
	case "subreaper-owner":
		if err := subreaperOwner(args[0], args[1]); err != nil {
			fmt.Fprintln(os.Stderr, "FAIL:", err)
			os.Exit(1)
		}
		return true
	}
	return runLinuxRole(role)
}

// readPidsNoT reads the first pid of a pid file.
func readPidsNoT(path string) int {
	var pid int
	data, _ := os.ReadFile(path)
	_, _ = fmt.Sscan(string(data), &pid)
	return pid
}

// subreaperOwner is a process dedicated to one worker tree, like the launcher.
//
// kill: the root exits 5 at once and leaves a daemonised grandchild; the grandchild must have been adopted by this
// process and still run, the exit code must be 5, Kill must stop the grandchild and no zombie may remain.
//
// reap: the daemonised grandchild exits by itself after 300 ms while the root sleeps; it must be reaped (no
// zombie) without stealing the root's exit status (4).
func subreaperOwner(dir, mode string) error {
	pidFile := filepath.Join(dir, "pids")
	life, action := "long", "exit"
	if mode == "reap" {
		life, action = "short", "sleep"
	}
	cmd := exec.Command(selfExe(), pidFile, life, action) //nolint:noctx // worker trees take no context
	cmd.Env = append(os.Environ(), roleEnv+"=escape-root")
	tree, err := StartWorkerTree(cmd, "")
	if err != nil {
		return err
	}
	defer func() { _ = tree.Close() }()
	waitFile(pidFile)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return err
	}
	var root, grandchild int
	if _, err := fmt.Sscan(string(data), &root, &grandchild); err != nil {
		return fmt.Errorf("pids %q: %w", data, err)
	}
	self := os.Getpid()

	if mode == "reap" {
		deadline := time.Now().Add(time.Second)
		for ProcessAlive(grandchild) { // a zombie still counts as alive: gone means reaped
			if time.Now().After(deadline) {
				return fmt.Errorf("orphan %d not reaped (zombies %v)", grandchild, zombiesOf(self))
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !ProcessAlive(root) {
			return errors.New("the root should still be running")
		}
		if err := tree.Wait(); err == nil || tree.ExitCode() != 4 {
			return fmt.Errorf("root exit: %w, code %d, want 4", err, tree.ExitCode())
		}
		return nil
	}

	if err := tree.Wait(); err == nil || tree.ExitCode() != 5 {
		return fmt.Errorf("root exit: %w, code %d, want 5", err, tree.ExitCode())
	}
	time.Sleep(100 * time.Millisecond)
	p, ok := procOf(grandchild)
	if !ok || p.zombie {
		return fmt.Errorf("grandchild %d should outlive the root", grandchild)
	}
	if p.ppid != self {
		return fmt.Errorf("grandchild %d has parent %d, want this subreaper %d", grandchild, p.ppid, self)
	}
	if pids, err := tree.WorkerTree("w").PIDs(); err != nil || !slices.Contains(pids, grandchild) || !slices.Contains(pids, self) {
		return fmt.Errorf("tree pids %v (%w) should hold %d and %d", pids, err, self, grandchild)
	}
	if err := tree.Kill(time.Second); err != nil {
		return fmt.Errorf("kill: %w", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for ProcessAlive(grandchild) {
		if time.Now().After(deadline) {
			return fmt.Errorf("grandchild %d survived Kill (zombies %v)", grandchild, zombiesOf(self))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if z := zombiesOf(self); len(z) > 0 {
		return fmt.Errorf("zombies left: %v", z)
	}
	return tree.Close()
}

func procOf(pid int) (procInfo, bool) {
	procs, _ := listProcs()
	for _, p := range procs {
		if p.pid == pid {
			return p, true
		}
	}
	return procInfo{}, false
}

func zombiesOf(parent int) []int {
	procs, _ := listProcs()
	var out []int
	for _, p := range procs {
		if p.ppid == parent && p.zombie {
			out = append(out, p.pid)
		}
	}
	return out
}

// startDescribedTree starts cmd as a plain tree (a worker tree would make this test process a subreaper and reap
// the children of other tests); the description's root is cmd's process.
func startDescribedTree(t *testing.T, cmd *exec.Cmd) (*Tree, *WorkerTree) {
	t.Helper()
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatalf("StartTree: %v", err)
	}
	go func() { _ = tree.Wait() }()
	wt := tree.WorkerTree("task-1")
	if wt.RootPID != tree.Pid() || wt.RootStart == 0 {
		t.Fatalf("description %+v, root %d", wt, tree.Pid())
	}
	return tree, wt
}

func runSubreaperOwner(t *testing.T, mode string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("no child subreaper on " + runtime.GOOS)
	}
	out, err := helper("subreaper-owner", t.TempDir(), mode).CombinedOutput()
	if err != nil {
		t.Fatalf("subreaper owner (%s): %v\n%s", mode, err, out)
	}
	if strings.Contains(string(out), "FAIL") {
		t.Fatal(string(out))
	}
}

// TestWorkerTreeKillsDaemonisedChild: a setsid + double-forked grandchild outlives the root, stays in the tree and
// is stopped by Kill; the root's exit code is exact and no zombie remains.
func TestWorkerTreeKillsDaemonisedChild(t *testing.T) { runSubreaperOwner(t, "kill") }

// TestWorkerTreeReapsOrphans: an adopted orphan that exits by itself is reaped without stealing the root's status.
func TestWorkerTreeReapsOrphans(t *testing.T) { runSubreaperOwner(t, "reap") }

// TestTreeKillReachesNewSession: Kill of a plain tree also reaches a child that moved to its own session (out of
// the tree's process group) while its parent, the root, is still alive.
func TestTreeKillReachesNewSession(t *testing.T) {
	if !canListProcs {
		t.Skip("no process list on " + runtime.GOOS)
	}
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, err := StartTree(helper("setsid-parent", pidFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tree.Close() }()
	go func() { _ = tree.Wait() }()
	pids := readPids(t, pidFile)
	if err := tree.Kill(time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	waitDead(t, pids...)
}

// TestRunNiced: a command is wrapped in `nice -n 10` with its own path and arguments.
func TestRunNiced(t *testing.T) {
	if _, err := exec.LookPath("nice"); err != nil {
		t.Skip("no nice on PATH")
	}
	cmd := exec.Command("/bin/sh", "-c", "echo a b") //nolint:noctx // never started
	if !runNiced(cmd) {
		t.Fatal("not wrapped")
	}
	if got := strings.Join(cmd.Args[1:], "|"); got != "-n|10|/bin/sh|-c|echo a b" || filepath.Base(cmd.Path) != "nice" {
		t.Fatalf("path %s, args %q", cmd.Path, cmd.Args)
	}
	if runNiced(&exec.Cmd{}) {
		t.Fatal("an empty command must not be wrapped")
	}
}

// TestLowerNiceGroup: without a nice program the process group is lowered right after the start; the processes
// already in it (the root and its child) get nice 10.
func TestLowerNiceGroup(t *testing.T) {
	priority.AtNormal(t)
	if self := priority.MustOf(t, os.Getpid()); priority.IsLow(self) {
		t.Skip("the test already runs at low priority")
	}
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, err := StartTree(helper("parent", pidFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tree.Close() }()
	pids := readPids(t, pidFile)
	if err := lowerNice(unix.PRIO_PGRP, tree.Pid()); err != nil {
		t.Fatal(err)
	}
	for _, p := range pids {
		if got := priority.MustOf(t, p); got != lowNice {
			t.Errorf("process %d: nice %d, want %d", p, got, lowNice)
		}
	}
}
