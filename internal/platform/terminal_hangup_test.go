//go:build unix

package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestTerminalScriptHangup closes the "window" (SIGHUP to the script's process group) while the command runs: the
// script still records exit code 129, so Terminal.Wait returns, and Identity names the shell until it is gone.
func TestTerminalScriptHangup(t *testing.T) {
	dir := t.TempDir()
	exitFile := filepath.Join(dir, "t.exit")
	spec := TerminalSpec{Title: "t", Argv: []string{selfExe(), filepath.Join(dir, "sleeper.pid")},
		Env: append(os.Environ(), roleEnv+"=sleeper")}
	path := filepath.Join(dir, "t.sh")
	if err := os.WriteFile(path, []byte(unixScript(spec, exitFile)), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "/bin/sh", path)
	cmd.Env = spec.Env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	term := &Terminal{exitFile: exitFile, pidFile: pidFileFor(exitFile)}
	var pid int
	var start uint64
	deadline := time.Now().Add(10 * time.Second)
	for {
		var ok bool
		if pid, start, ok = term.Identity(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the script never wrote its pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid != cmd.Process.Pid || !ProcessRunning(pid, start) {
		t.Fatalf("identity %d (start %d), shell %d", pid, start, cmd.Process.Pid)
	}
	time.Sleep(200 * time.Millisecond) // let the command start
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := term.Wait(ctx)
	if err != nil || code != 129 {
		t.Fatalf("Wait = %d, %v; want 129", code, err)
	}
	_ = cmd.Wait()
	if ProcessRunning(pid, start) {
		t.Fatal("the shell still runs")
	}
}
