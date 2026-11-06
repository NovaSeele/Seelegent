package platform

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSafeTitle(t *testing.T) {
	cases := map[string]string{
		"Seelegent - sign in acc1": "Seelegent - sign in acc1",
		`bad"&|<>title`:            "badtitle",
		"":                         "Seelegent",
		"đăng nhập":                "đăng nhập",
		strings.Repeat("a", 150):   strings.Repeat("a", 100),
	}
	for in, want := range cases {
		if got := safeTitle(in); got != want {
			t.Errorf("safeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShQuote(t *testing.T) {
	if got := shQuote("it's"); got != `'it'\''s'` {
		t.Fatalf("shQuote = %s", got)
	}
}

func TestCmdQuote(t *testing.T) {
	if got, err := cmdQuote(`C:\a b\100%`); err != nil || got != `"C:\a b\100%%"` {
		t.Fatalf("cmdQuote = %s, %v", got, err)
	}
	for _, bad := range []string{`a"b`, "a\nb", "a\rb"} {
		if _, err := cmdQuote(bad); err == nil {
			t.Errorf("cmdQuote(%q) should fail", bad)
		}
	}
}

func TestBatchEcho(t *testing.T) {
	if got := batchEcho("a & b | c > d < e ^ 5%"); got != "echo a ^& b ^| c ^> d ^< e ^^ 5%%" {
		t.Fatalf("batchEcho = %s", got)
	}
	if got := batchEcho("  "); got != "echo." {
		t.Fatalf("batchEcho blank = %s", got)
	}
}

func TestEnvDelta(t *testing.T) {
	set, unset := envDelta([]string{"A=1", "B=2", "C=3"}, []string{"A=1", "B=20", "D=4"})
	if strings.Join(set, ",") != "B=20,D=4" || strings.Join(unset, ",") != "C" {
		t.Fatalf("set %v unset %v", set, unset)
	}
}

func TestValidEnvName(t *testing.T) {
	for name, want := range map[string]bool{"PATH": true, "_x1": true, "1A": false, "A-B": false, "": false, "ProgramFiles(x86)": false} {
		if got := validEnvName(name); got != want {
			t.Errorf("validEnvName(%q) = %v", name, got)
		}
	}
}

func TestNoTerminalError(t *testing.T) {
	err := error(&NoTerminalError{Command: "sh x", Reason: "no display"})
	if !errors.Is(err, ErrNoTerminal) || !strings.Contains(err.Error(), "sh x") {
		t.Fatalf("NoTerminalError = %v", err)
	}
}

func TestOpenTerminalValidates(t *testing.T) {
	if _, err := OpenTerminal(context.Background(), TerminalSpec{ScriptDir: t.TempDir()}); err == nil {
		t.Fatal("empty argv should fail")
	}
	if _, err := OpenTerminal(context.Background(), TerminalSpec{Argv: []string{"x"}}); err == nil {
		t.Fatal("missing script dir should fail")
	}
}

// TestTerminalScriptRuns runs the generated script without a window and checks that the command ran with its
// arguments, its output went to StdoutPath and the exit code was recorded.
func TestTerminalScriptRuns(t *testing.T) {
	dir := t.TempDir()
	exitFile := filepath.Join(dir, "t.exit")
	out := filepath.Join(dir, "out 1%.txt")
	spec := TerminalSpec{
		Title:      "test",
		Banner:     []string{"Sign in & paste the code > here", ""},
		Argv:       []string{os.Args[0], "a b", "c&d", "50%"},
		Dir:        dir,
		Env:        append(os.Environ(), roleEnv+"=print"),
		StdoutPath: out,
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		script, err := windowsScript(spec, exitFile)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "t.cmd")
		if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.CommandContext(context.Background(), "cmd.exe", "/d", "/c", path)
		cmd.Env = spec.Env
	} else {
		path := filepath.Join(dir, "t.sh")
		if err := os.WriteFile(path, []byte(unixScript(spec, exitFile)), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd = exec.CommandContext(context.Background(), "/bin/sh", path)
	}
	HideWindow(cmd)
	_ = cmd.Run() // exits with the command's code after a short pause
	term := &Terminal{exitFile: exitFile}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	code, err := term.Wait(ctx)
	if err != nil || code != 3 {
		t.Fatalf("Wait = %d, %v; want 3", code, err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "a b|c&d|50%" {
		t.Fatalf("command saw %q", got)
	}
}

type fakeWaiter struct{}

func (fakeWaiter) Wait() error { return nil }

func TestTerminalWaitWindowClosed(t *testing.T) {
	term := &Terminal{exitFile: filepath.Join(t.TempDir(), "none"), process: fakeWaiter{}}
	if _, err := term.Wait(context.Background()); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Wait = %v, want window closed", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := (&Terminal{exitFile: filepath.Join(t.TempDir(), "none")}).Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want deadline", err)
	}
}
