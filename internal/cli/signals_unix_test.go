//go:build unix

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// A launcher stopped with SIGTERM, as a service manager or kill(1) stops it, stops its worker tree and records the
// turn as canceled instead of dying at once and leaving the worker running (M18 hunt H13).
func TestLauncherStopsItsWorkerTreeOnSIGTERM(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "child.pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	h, err := launch.Start(ctx, launch.Options{
		Launcher: []string{exe, launchCommandName},
		Dir:      filepath.Join(dir, "launch"),
		Spec: launch.Spec{
			// A worker with a child of its own: the whole tree has to go.
			Argv:      []string{"/bin/sh", "-c", `sleep 120 & echo $! > "$1"; wait`, "sh", child},
			Dir:       dir,
			KillGrace: time.Second,
		},
		Env:          append(os.Environ(), envExecute+"=1"),
		ReadyTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid := waitPID(ctx, t, child)
	tree, ok := h.Tree()
	if !ok {
		t.Fatal("no launcher tree")
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if err := syscall.Kill(tree.RootPID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	ex, err := h.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Reason != launch.ReasonCanceled {
		t.Errorf("exit reason %q (%s), want %q", ex.Reason, ex.Error, launch.ReasonCanceled)
	}
	for alive(pid) {
		if ctx.Err() != nil {
			t.Fatalf("the worker's child %d still runs after the launcher got SIGTERM", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitPID waits until the worker wrote its child's process id into path.
func waitPID(ctx context.Context, t *testing.T, path string) int {
	t.Helper()
	for {
		data, err := os.ReadFile(path)
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && perr == nil && pid > 0 {
			return pid
		}
		if ctx.Err() != nil {
			t.Fatalf("the worker never wrote %s: %v", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// alive reports whether a process with id pid exists (a zombie counts as gone once its parent reaps it).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
