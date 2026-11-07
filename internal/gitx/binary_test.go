package gitx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestBinaryNotFoundAndCache: a PATH without git is ErrNoGit and is tried again next time; a found git is kept
// (the environment is read once).
func TestBinaryNotFoundAndCache(t *testing.T) {
	empty := t.TempDir()
	reads := 0
	path := "PATH=" + empty
	b := NewBinary(func() []string {
		reads++
		return []string{path}
	}, nil)
	if _, err := b.Path(); !errors.Is(err, ErrNoGit) {
		t.Fatalf("no git: err = %v", err)
	}
	if _, err := b.Path(); !errors.Is(err, ErrNoGit) || reads != 2 {
		t.Fatalf("a failure was cached: err = %v, reads %d", err, reads)
	}

	realGit := gitExe(t)
	path = "PATH=" + dirOf(realGit)
	got, err := b.Path()
	if err != nil || got != realGit {
		t.Fatalf("Path = %q, %v; want %q", got, err, realGit)
	}
	if again, _ := b.Path(); again != got || reads != 3 {
		t.Fatalf("the found git was not kept: %q, reads %d", again, reads)
	}
}

// TestBinarySkipsRelativePath: a git in the current folder, reached through an empty or relative PATH entry, is
// never used.
func TestBinarySkipsRelativePath(t *testing.T) {
	dir := t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	sep := string(filepath.ListSeparator)
	for _, p := range []string{".", "", sep, "." + sep + "sub", `"."`} {
		if got, err := NewBinary(func() []string { return []string{"PATH=" + p} }, nil).Path(); !errors.Is(err, ErrNoGit) {
			t.Errorf("PATH=%q: Path = %q, %v; want ErrNoGit", p, got, err)
		}
	}
}

// fakeGit writes a file named like git (never run) into a new folder and returns the folder and the file.
func fakeGit(t *testing.T) (dir, exe string) {
	t.Helper()
	dir = t.TempDir()
	name := "git"
	if runtime.GOOS == "windows" {
		name = "git.exe"
	}
	exe = filepath.Join(dir, name)
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, exe
}

// TestBinaryRunFindsMovedGit: a kept git that was removed since it was found is dropped by the first run that
// fails to start it; that run finds git again and succeeds, and later calls use the new git.
func TestBinaryRunFindsMovedGit(t *testing.T) {
	realGit := gitExe(t)
	stale, fake := fakeGit(t)
	reads := 0
	b := NewBinary(func() []string {
		reads++
		return []string{"PATH=" + stale + string(filepath.ListSeparator) + dirOf(realGit)}
	}, nil)
	if got, err := b.Path(); err != nil || got != fake {
		t.Fatalf("Path = %q, %v; want %q", got, err, fake)
	}
	if err := os.Remove(fake); err != nil {
		t.Fatal(err)
	}
	dir := newRepo(t)
	if _, err := New(nil, b).Root(t.Context(), dir); err != nil {
		t.Fatalf("Root after git moved: %v", err)
	}
	if got, err := b.Path(); err != nil || !strings.EqualFold(got, realGit) || reads != 2 {
		t.Fatalf("Path = %q, %v (reads %d); want %q found once more", got, err, reads, realGit)
	}
}

// TestBinaryRunKeepsGit: an error that is not a missing executable, or a "not exist" error while the git is still
// there (a missing working folder), runs fn once and keeps the git; with no git left at all, Run is ErrNoGit.
func TestBinaryRunKeepsGit(t *testing.T) {
	dir, exe := fakeGit(t)
	reads := 0
	b := NewBinary(func() []string { reads++; return []string{"PATH=" + dir} }, nil)
	boom := errors.New("boom")
	gone := &fs.PathError{Op: "chdir", Path: filepath.Join(dir, "missing"), Err: fs.ErrNotExist}
	for _, want := range []error{boom, gone, nil} {
		calls := 0
		err := b.Run(func(git string) error {
			calls++
			if git != exe {
				t.Errorf("fn got %q, want %q", git, exe)
			}
			return want
		})
		if !errors.Is(err, want) || calls != 1 || reads != 1 {
			t.Fatalf("fn error %v: Run = %v after %d calls, %d reads", want, err, calls, reads)
		}
	}

	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	calls := 0
	err := b.Run(func(git string) error {
		calls++
		return &fs.PathError{Op: "fork/exec", Path: git, Err: fs.ErrNotExist}
	})
	if !errors.Is(err, ErrNoGit) || calls != 1 {
		t.Fatalf("no git left: Run = %v after %d calls", err, calls)
	}
}

// TestBinaryRunConcurrent: runs that hit a removed git at once all succeed with the git found again.
func TestBinaryRunConcurrent(t *testing.T) {
	realGit := gitExe(t)
	stale, fake := fakeGit(t)
	path := "PATH=" + stale + string(filepath.ListSeparator) + dirOf(realGit)
	b := NewBinary(func() []string { return []string{path} }, nil)
	if got, _ := b.Path(); got != fake {
		t.Fatalf("Path = %q, want %q", got, fake)
	}
	if err := os.Remove(fake); err != nil {
		t.Fatal(err)
	}
	r := New(nil, b)
	dir := newRepo(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := r.Root(t.Context(), dir); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
