//go:build windows

package gitx

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dirOf(p string) string { return filepath.Dir(p) }

// shimEnv is an environment whose PATH is dirs, with the usual PATHEXT (a batch file would be found first).
func shimEnv(dirs ...string) func() []string {
	return func() []string {
		return []string{"PATH=" + strings.Join(dirs, ";"), "PATHEXT=.COM;.EXE;.BAT;.CMD"}
	}
}

// TestBinarySkipsBatchShim: a git.cmd shim before the real git on PATH is never run, and a diff that needs HEAD
// (cmd.exe ate the ^ of HEAD^{commit}, so the diff fell back to the empty tree) shows the real change.
func TestBinarySkipsBatchShim(t *testing.T) {
	realGit := gitExe(t)
	shimDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "shim-ran")
	write(t, filepath.Join(shimDir, "git.cmd"), "@echo off\r\necho x> \""+marker+"\"\r\nexit /b 7\r\n")
	bin := NewBinary(shimEnv(shimDir, dirOf(realGit)), nil)
	got, err := bin.Path()
	if err != nil || !strings.EqualFold(got, realGit) {
		t.Fatalf("Path = %q, %v; want %q", got, err, realGit)
	}

	dir := newRepo(t)
	write(t, filepath.Join(dir, "a.txt"), "one\nTWO\nthree\n")
	r := New(nil, bin)
	d, err := r.Diff(context.Background(), dir, "a.txt", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hunks) != 1 || d.Hunks[0].OldStart != 1 || d.Hunks[0].OldLines != 3 {
		t.Fatalf("diff against HEAD = %+v", d)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the batch shim ran (%v)", err)
	}
}

// TestBinaryShimOnly: with only a shim on PATH, the git.exe it names (absolute or relative to %~dp0) is used; a
// shim that names none is a clear error.
func TestBinaryShimOnly(t *testing.T) {
	root := t.TempDir()
	exe := filepath.Join(root, "packages", "git", "cmd", "git.exe")
	write(t, exe, "not run by this test")
	shimDir := filepath.Join(root, "bin")
	write(t, filepath.Join(shimDir, "git.cmd"), "@echo off\r\n\"%~dp0..\\packages\\git\\cmd\\git.exe\" %*\r\n")
	if got, err := NewBinary(shimEnv(shimDir), nil).Path(); err != nil || got != exe {
		t.Fatalf("relative shim: Path = %q, %v; want %q", got, err, exe)
	}

	absDir := filepath.Join(root, "abs")
	write(t, filepath.Join(absDir, "git.bat"), "@\""+exe+"\" %*\r\n")
	if got, err := NewBinary(shimEnv(absDir), nil).Path(); err != nil || got != exe {
		t.Fatalf("absolute shim: Path = %q, %v; want %q", got, err, exe)
	}

	bare := filepath.Join(root, "bare")
	shim := filepath.Join(bare, "git.cmd")
	write(t, shim, "@echo off\r\ngit.exe %*\r\n")
	_, err := NewBinary(shimEnv(bare), nil).Path()
	if !errors.Is(err, ErrNoGit) || !strings.Contains(err.Error(), "batch shim at "+shim) ||
		!strings.Contains(err.Error(), "put git.exe on PATH") {
		t.Fatalf("bare shim: err = %v", err)
	}

	// Git for Windows' default place is used when PATH has only that shim, or nothing.
	pf := filepath.Join(root, "pf")
	fallback := filepath.Join(pf, "Git", "cmd", "git.exe")
	write(t, fallback, "not run by this test")
	for _, dirs := range [][]string{{bare}, {}} {
		env := func() []string { return append(shimEnv(dirs...)(), "ProgramFiles="+pf) }
		if got, err := NewBinary(env, nil).Path(); err != nil || got != fallback {
			t.Fatalf("PATH %q: Path = %q, %v; want %q", dirs, got, err, fallback)
		}
	}
}

// TestBinaryLogsHowGitWasFound: a git found through a shim or a default place is logged once per resolution, a
// plain PATH hit never.
func TestBinaryLogsHowGitWasFound(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	lines := func() []string {
		var out []string
		for l := range strings.Lines(buf.String()) {
			if strings.Contains(l, "level=INFO") {
				out = append(out, l)
			}
		}
		return out
	}

	onPath := filepath.Join(root, "path", "git.exe")
	write(t, onPath, "not run by this test")
	b := NewBinary(shimEnv(filepath.Dir(onPath)), log)
	for range 2 {
		if got, err := b.Path(); err != nil || got != onPath {
			t.Fatalf("PATH hit: Path = %q, %v", got, err)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("a PATH hit was logged: %s", buf.String())
	}

	target := filepath.Join(root, "packages", "git.exe")
	write(t, target, "not run by this test")
	shim := filepath.Join(root, "bin", "git.cmd")
	write(t, shim, "@\""+target+"\" %*\r\n")
	b = NewBinary(shimEnv(filepath.Dir(shim)), log)
	for range 2 {
		if got, err := b.Path(); err != nil || got != target {
			t.Fatalf("shim: Path = %q, %v", got, err)
		}
	}
	if l := lines(); len(l) != 1 || !strings.Contains(l[0], target) || !strings.Contains(l[0], "batch shim") {
		t.Fatalf("shim: logged %q", l)
	}

	pf := filepath.Join(root, "pf")
	fallback := filepath.Join(pf, "Git", "cmd", "git.exe")
	write(t, fallback, "not run by this test")
	b = NewBinary(func() []string { return append(shimEnv()(), "ProgramFiles="+pf) }, log)
	for range 2 {
		if got, err := b.Path(); err != nil || got != fallback {
			t.Fatalf("ProgramFiles: Path = %q, %v", got, err)
		}
	}
	if l := lines(); len(l) != 2 || !strings.Contains(l[1], fallback) || !strings.Contains(l[1], "%ProgramFiles%") {
		t.Fatalf("ProgramFiles: logged %q", l)
	}
}

// TestBinarySpecialName: with a git.cmd first on PATH, a file whose name cmd.exe would rewrite or run goes
// through Status, Diff and Commit with its name intact, and the shim never runs.
func TestBinarySpecialName(t *testing.T) {
	realGit := gitExe(t)
	shimDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "shim-ran")
	write(t, filepath.Join(shimDir, "git.cmd"), "@echo off\r\necho x> \""+marker+"\"\r\nexit /b 7\r\n")
	r := New(nil, NewBinary(shimEnv(shimDir, dirOf(realGit)), nil))
	ctx := context.Background()

	dir := newRepo(t)
	const name = "a&b^c%PATH%!x.txt"
	write(t, filepath.Join(dir, name), "special\n")
	st, err := r.Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 1 || st.Files[0].Path != name || st.Files[0].Status != "untracked" {
		t.Fatalf("status files = %+v", st.Files)
	}
	d, err := r.Diff(ctx, dir, name, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != name || len(d.Hunks) != 1 || len(d.Hunks[0].Lines) != 1 || d.Hunks[0].Lines[0].Text != "special" {
		t.Fatalf("diff = %+v", d)
	}
	tf := TaskFiles{Cwd: dir, Files: map[string]int{name: 1}}
	if _, err := r.Commit(ctx, tf, "Special name", []string{name}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, dir, "-c", "core.quotepath=false", "show", "--name-only", "--format=", "HEAD")); got != name {
		t.Fatalf("committed %q, want %q", got, name)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the batch shim ran (%v)", err)
	}
}
