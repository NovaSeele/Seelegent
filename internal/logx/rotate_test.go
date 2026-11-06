package logx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestRotatingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	r, err := OpenRotatingFile(path, 100, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		line := fmt.Sprintf("line %02d %s\n", i, strings.Repeat("x", 30)) // 40 bytes
		if n, err := r.Write([]byte(line)); err != nil || n != len(line) {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// 10 lines of 40 bytes, two per file: current holds 8-9, .1 holds 6-7, .2 holds 4-5, older copies are gone.
	want := map[string]string{"daemon.log": "line 08", "daemon.log.1": "line 06", "daemon.log.2": "line 04"}
	for name, first := range want {
		lines := readLines(t, filepath.Join(dir, name))
		if len(lines) != 2 || !strings.HasPrefix(lines[0], first) {
			t.Errorf("%s = %q, want 2 lines starting with %q", name, lines, first)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.log.3")); !errors.Is(err, os.ErrNotExist) {
		t.Error("daemon.log.3 must not exist with keep=2")
	}
	if _, err := r.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write after Close: err = %v, want os.ErrClosed", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestRotatingFileAppendsToExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("o", 95)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := OpenRotatingFile(path, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if _, err := r.Write([]byte("new line\n")); err != nil {
		t.Fatal(err)
	}
	if lines := readLines(t, path); len(lines) != 1 || lines[0] != "new line" {
		t.Fatalf("the existing size must count towards rotation; current file = %q", lines)
	}
	if lines := readLines(t, path+".1"); len(lines) != 1 || len(lines[0]) != 95 {
		t.Fatalf("old content not rotated: %q", lines)
	}
}

func TestRotatingFileOversizedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	r, err := OpenRotatingFile(path, 10, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	big := strings.Repeat("b", 50) + "\n"
	for range 2 {
		if _, err := r.Write([]byte(big)); err != nil {
			t.Fatal(err)
		}
	}
	if lines := readLines(t, path); len(lines) != 1 {
		t.Fatalf("a line larger than the limit must be written whole into its own file: %q", lines)
	}
}

func TestRotatingFileNoBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.log")
	r, err := OpenRotatingFile(path, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	for _, l := range []string{"aaaaaaaaaaaaaaa\n", "bbbbbbbbbbbbbbb\n"} {
		if _, err := r.Write([]byte(l)); err != nil {
			t.Fatal(err)
		}
	}
	if lines := readLines(t, path); len(lines) != 1 || lines[0] != "bbbbbbbbbbbbbbb" {
		t.Fatalf("current = %q", lines)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("keep=0 must leave one file, found %d", len(entries))
	}
}

func TestOpenRotatingFileErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name     string
		path     string
		maxBytes int64
		keep     int
	}{
		{"zero size", filepath.Join(dir, "a.log"), 0, 1},
		{"negative keep", filepath.Join(dir, "a.log"), 10, -1},
		{"missing dir", filepath.Join(dir, "missing", "a.log"), 10, 1},
		{"directory", dir, 10, 1},
	}
	for _, tt := range tests {
		if _, err := OpenRotatingFile(tt.path, tt.maxBytes, tt.keep); err == nil {
			t.Errorf("%s: want an error", tt.name)
		}
	}
}

// Concurrent loggers on a small file: every line in every file must be a whole JSON record.
func TestFileLoggerConcurrentRotation(t *testing.T) {
	dir := t.TempDir()
	log, closer, err := NewFileLogger(FileOptions{Dir: dir, MaxBytes: 2048, Keep: 50})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				log.Info("tick", "goroutine", g, "i", i, "pad", strings.Repeat("p", 40))
			}
		})
	}
	wg.Wait()
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, e := range entries {
		f, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var rec map[string]any
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				t.Errorf("%s: broken line %q", e.Name(), sc.Text())
			}
			total++
		}
		_ = f.Close()
		if info, _ := e.Info(); info.Size() > 2048 {
			t.Errorf("%s is %d bytes, over the limit", e.Name(), info.Size())
		}
	}
	if total != 8*50 {
		t.Fatalf("found %d records, want %d", total, 8*50)
	}
}

func TestNewFileLogger(t *testing.T) {
	dir := t.TempDir()
	var console bytes.Buffer
	log, closer, err := NewFileLogger(FileOptions{Dir: dir, Console: &console, Level: slog.LevelDebug})
	if err != nil {
		t.Fatal(err)
	}
	log.Debug("signed in", "email", fakeEmail)
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, DefaultFileName))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("file line is not JSON: %q", data)
	}
	if rec["email"] != MaskedEmail || rec["level"] != "DEBUG" {
		t.Errorf("record = %v", rec)
	}
	if !strings.Contains(console.String(), "email="+MaskedEmail) || strings.Contains(console.String(), fakeEmail) {
		t.Errorf("console = %q", console.String())
	}
}

func TestNewFileLoggerOptions(t *testing.T) {
	if _, _, err := NewFileLogger(FileOptions{}); err == nil {
		t.Fatal("want an error without a directory")
	}
	if _, _, err := NewFileLogger(FileOptions{Dir: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("want an error for a missing directory")
	}
	dir := t.TempDir()
	log, closer, err := NewFileLogger(FileOptions{Dir: dir, Name: "x.log", MaxBytes: 1, Keep: NoBackups, Secrets: []string{"literal-secret-1"}})
	if err != nil {
		t.Fatal(err)
	}
	log.Info("first literal-secret-1")
	log.Info("second")
	log.Debug("hidden at the default level")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, filepath.Join(dir, "x.log"))
	if len(lines) != 1 || !strings.Contains(lines[0], `"msg":"second"`) {
		t.Fatalf("x.log = %q", lines)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("NoBackups must keep a single file, found %d", len(entries))
	}
}

func TestNewConsoleLogger(t *testing.T) {
	var buf bytes.Buffer
	log := NewConsoleLogger(&buf, nil, "literal-secret-2")
	log.Debug("not shown")
	log.Info("token in use", "key", "literal-secret-2", "url", "https://x.test/?api_key=abcdef123456")
	out := buf.String()
	if strings.Contains(out, "not shown") || strings.Contains(out, "literal-secret-2") || strings.Contains(out, "abcdef123456") {
		t.Fatalf("console output = %q", out)
	}
	if !strings.Contains(out, "level=INFO") {
		t.Fatalf("console output = %q", out)
	}
}
