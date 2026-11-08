//go:build windows

package gitx

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
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

	// Then the per-user install under %LOCALAPPDATA%\Programs; the machine-wide one wins when both exist.
	lad := filepath.Join(root, "lad")
	user := filepath.Join(lad, "Programs", "Git", "cmd", "git.exe")
	write(t, user, "not run by this test")
	for _, tt := range []struct {
		extra []string
		want  string
	}{
		{[]string{"LOCALAPPDATA=" + lad}, user},
		{[]string{"ProgramFiles=" + filepath.Join(root, "empty-pf"), "LOCALAPPDATA=" + lad}, user},
		{[]string{"LOCALAPPDATA=" + lad, "ProgramFiles=" + pf}, fallback},
	} {
		env := func() []string { return append(shimEnv(bare)(), tt.extra...) }
		if got, err := NewBinary(env, nil).Path(); err != nil || got != tt.want {
			t.Fatalf("%q: Path = %q, %v; want %q", tt.extra, got, err, tt.want)
		}
	}
}

// TestBinaryLogsHowGitWasFound: a git found through a shim or a default place is logged once per resolution, a
// plain PATH hit never; a git found again after the kept one was removed is logged again.
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

	// The ProgramFiles git is removed and a per-user one installed: the run that cannot start the old one finds
	// the new one, which is logged once, after a warning about the old one.
	if err := os.Remove(fallback); err != nil {
		t.Fatal(err)
	}
	lad := filepath.Join(root, "lad")
	user := filepath.Join(lad, "Programs", "Git", "cmd", "git.exe")
	write(t, user, "not run by this test")
	b.env = func() []string { return append(shimEnv()(), "ProgramFiles="+pf, "LOCALAPPDATA="+lad) }
	var ran []string
	err := b.Run(func(git string) error {
		ran = append(ran, git)
		if git == fallback {
			return &fs.PathError{Op: "fork/exec", Path: git, Err: fs.ErrNotExist}
		}
		return nil
	})
	if err != nil || len(ran) != 2 || ran[1] != user {
		t.Fatalf("Run = %v, ran %q", err, ran)
	}
	if got, err := b.Path(); err != nil || got != user {
		t.Fatalf("Path after Run = %q, %v", got, err)
	}
	if l := lines(); len(l) != 3 || !strings.Contains(l[2], user) || !strings.Contains(l[2], "%LOCALAPPDATA%") {
		t.Fatalf("per-user git: logged %q", l)
	}
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "git is gone") {
		t.Fatalf("the removed git was not logged: %s", buf.String())
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

// TestBinaryForwardingWrapper: a git.cmd that forwards to the real git (ServBay's shape) and comes first on PATH.
// Run directly, cmd.exe drops the ^ of HEAD^{tree}; Seelegent runs the real git.exe instead, whether it is later
// on PATH or only named by the wrapper, and gets the right tree. FirstWrapper names the wrapper for doctor.
func TestBinaryForwardingWrapper(t *testing.T) {
	realGit := gitExe(t)
	dir := newRepo(t)
	want := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD:"))
	wrapperDir := t.TempDir()
	wrapper := filepath.Join(wrapperDir, "git.cmd")
	write(t, wrapper, "@echo off\r\n\""+realGit+"\" %*\r\n")

	out, err := exec.CommandContext(t.Context(), wrapper, "rev-parse", "HEAD^{tree}").CombinedOutput()
	if err == nil && strings.TrimSpace(string(out)) == want {
		t.Logf("this cmd.exe kept the ^ (%q); the wrapper is skipped anyway", out)
	}

	for name, env := range map[string]func() []string{
		"real git later on PATH":    shimEnv(wrapperDir, dirOf(realGit)),
		"only the wrapper on PATH":  shimEnv(wrapperDir),
		"real git first, then shim": shimEnv(dirOf(realGit), wrapperDir),
	} {
		bin := NewBinary(env, nil)
		got, err := bin.Path()
		if err != nil || !strings.EqualFold(got, realGit) {
			t.Fatalf("%s: Path = %q, %v; want %q", name, got, err, realGit)
		}
		tree, _, err := New(nil, bin).run(context.Background(), dir, ReadTimeout, 0, "rev-parse", "HEAD^{tree}")
		if err != nil || strings.TrimSpace(string(tree)) != want {
			t.Fatalf("%s: rev-parse HEAD^{tree} = %q, %v; want %q", name, tree, err, want)
		}
	}

	if got := FirstWrapper(shimEnv(wrapperDir, dirOf(realGit))()); got != wrapper {
		t.Errorf("FirstWrapper(wrapper first) = %q, want %q", got, wrapper)
	}
	if got := FirstWrapper(shimEnv(dirOf(realGit), wrapperDir)()); got != "" {
		t.Errorf("FirstWrapper(git.exe first) = %q", got)
	}
	// In one folder, PATHEXT decides: .EXE before .CMD is the exe.
	both := t.TempDir()
	write(t, filepath.Join(both, "git.cmd"), "@echo off\r\n")
	write(t, filepath.Join(both, "git.exe"), "not run")
	if got := FirstWrapper(shimEnv(both)()); got != "" {
		t.Errorf("FirstWrapper(exe and cmd in one folder) = %q", got)
	}
	env := []string{"PATH=" + both, "PATHEXT=.CMD;.EXE"}
	if got := FirstWrapper(env); got != filepath.Join(both, "git.cmd") {
		t.Errorf("FirstWrapper(PATHEXT .CMD first) = %q", got)
	}
	if got := FirstWrapper([]string{"PATH=relative;" + wrapperDir}); got != wrapper {
		t.Errorf("FirstWrapper(relative entry skipped) = %q", got)
	}
}
