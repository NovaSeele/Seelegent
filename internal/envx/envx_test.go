package envx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestBatchUnsafeArg: the one rule for arguments of a .cmd/.bat program on Windows, used by backend and setup.
func TestBatchUnsafeArg(t *testing.T) {
	for _, bad := range []string{"a&b", "50%", "!x!", "a|b", "a<b", "a>b", "x^y", `say "hi"`, "line\nbreak", "cr\r"} {
		if a, ok := BatchUnsafeArg(`C:\npm\claude.CMD`, "windows", []string{"ok", bad}); !ok || a != bad {
			t.Errorf("%q: got %q %v", bad, a, ok)
		}
		if _, ok := BatchUnsafeArg(`C:\npm\codex.bat`, "windows", []string{bad}); !ok {
			t.Errorf("%q passed for a .bat", bad)
		}
		if _, ok := BatchUnsafeArg(`C:\bin\claude.exe`, "windows", []string{bad}); ok {
			t.Errorf("%q refused for an .exe", bad)
		}
		if _, ok := BatchUnsafeArg("/usr/bin/claude.cmd", "linux", []string{bad}); ok {
			t.Errorf("%q refused off Windows", bad)
		}
	}
	if _, ok := BatchUnsafeArg(`C:\npm\claude.cmd`, "windows", []string{"-p", `C:\Users\A B\x.json`}); ok {
		t.Error("safe arguments refused")
	}
}

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLookup(t *testing.T) {
	env := []string{"A=1", "broken", "B=", "A=2", "C=x=y"}
	for _, tt := range []struct {
		name, want string
		found      bool
	}{
		{"A", "2", true}, // the last one wins
		{"B", "", true},
		{"C", "x=y", true},
		{"broken", "", false},
		{"MISSING", "", false},
	} {
		if v, ok := Lookup(env, tt.name); v != tt.want || ok != tt.found {
			t.Errorf("Lookup(%q) = %q, %v; want %q, %v", tt.name, v, ok, tt.want, tt.found)
		}
	}
	v, ok := Lookup([]string{"Path=x"}, "PATH")
	if fold := runtime.GOOS == "windows"; ok != fold || (fold && v != "x") {
		t.Errorf("Lookup(PATH) in {Path=x} = %q, %v on %s", v, ok, runtime.GOOS)
	}
}

func TestResolveCommand(t *testing.T) {
	dir := t.TempDir()
	name := "tool"
	if runtime.GOOS == "windows" {
		name = "tool.exe"
	}
	bin := filepath.Join(dir, name)
	writeFile(t, bin, 0o700)
	empty := t.TempDir()
	env := []string{"PATH=" + strings.Join([]string{"", `"` + empty + `"`, dir}, string(os.PathListSeparator))}
	if got := ResolveCommand("tool", env); got != bin {
		t.Errorf("ResolveCommand = %q, want %q", got, bin)
	}
	if got := ResolveCommand("nothing-here", env); got != "" {
		t.Errorf("ResolveCommand(nothing) = %q", got)
	}
	if runtime.GOOS == "windows" {
		writeFile(t, filepath.Join(empty, "other.cmd"), 0o600)
		if got := ResolveCommand("other", []string{"PATH=" + empty, "PATHEXT=.EXE;.CMD"}); !strings.EqualFold(got,
			filepath.Join(empty, "other.cmd")) {
			t.Errorf("PATHEXT lookup = %q", got)
		}
	} else {
		writeFile(t, filepath.Join(empty, "plain"), 0o600)
		if got := ResolveCommand("plain", []string{"PATH=" + empty}); got != "" {
			t.Errorf("non-executable file resolved: %q", got)
		}
	}
}

// TestResolveCommandSkipsRelativePath: a command in the working folder, reached through an empty or relative PATH
// entry, is never found; an absolute entry after them still is.
func TestResolveCommandSkipsRelativePath(t *testing.T) {
	cwd := t.TempDir()
	name := "tool"
	if runtime.GOOS == "windows" {
		name = "tool.exe"
	}
	writeFile(t, filepath.Join(cwd, name), 0o700)
	writeFile(t, filepath.Join(cwd, "bin", name), 0o700)
	t.Chdir(cwd)
	sep := string(os.PathListSeparator)
	for _, p := range []string{".", "", sep, "bin", "." + sep + "bin", `"."`, `".\bin"`, "./bin"} {
		if got := ResolveCommand("tool", []string{"PATH=" + p}); got != "" {
			t.Errorf("PATH=%q: ResolveCommand = %q; want nothing", p, got)
		}
	}
	abs := t.TempDir()
	bin := filepath.Join(abs, name)
	writeFile(t, bin, 0o700)
	if got := ResolveCommand("tool", []string{"PATH=." + sep + "bin" + sep + abs}); got != bin {
		t.Errorf("ResolveCommand = %q, want %q", got, bin)
	}
}
