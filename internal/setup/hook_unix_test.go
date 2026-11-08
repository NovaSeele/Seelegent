//go:build !windows

package setup

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestHookCommandRunsUnderSh runs the hook command line through sh, as Claude Code does on macOS and Linux, with
// the executable in a folder whose name holds $, a backquote, a quote and a space (F15): the shell runs that very
// file, with the hook's arguments, and expands nothing.
func TestHookCommandRunsUnderSh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a $HOME `touch pwned` 'q' b")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "seelegent")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"ran $*\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	line, err := hookCommandLine(exe, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "sh", "-c", line)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ran hook "+HookCommand {
		t.Fatalf("sh -c %s: %s %v", line, out, err)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "pwned")); err == nil {
		t.Fatal("the backquote in the path ran a command")
	}
}
