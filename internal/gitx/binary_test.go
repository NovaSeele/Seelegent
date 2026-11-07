package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
