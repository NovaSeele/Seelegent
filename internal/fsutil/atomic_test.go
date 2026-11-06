package fsutil

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWriteFileAtomic(t *testing.T) {
	tests := []struct {
		name     string
		existing []byte // nil means no file before the write
		data     []byte
	}{
		{name: "new file", data: []byte(`{"a":1}`)},
		{name: "replace longer file", existing: []byte(strings.Repeat("x", 4096)), data: []byte("short")},
		{name: "replace with empty", existing: []byte("old"), data: []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			if tt.existing != nil {
				if err := os.WriteFile(path, tt.existing, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := WriteFileAtomic(path, tt.data, 0o600); err != nil {
				t.Fatalf("WriteFileAtomic: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.data) {
				t.Fatalf("content = %q, want %q", got, tt.data)
			}
			assertOnlyFile(t, dir, "config.json")
		})
	}
}

func TestWriteFileAtomicPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := WriteFileAtomic(path, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("mode = %v, want 0640", got)
	}
}

func TestWriteFileAtomicMissingDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "config.json")
	err := WriteFileAtomic(path, []byte("x"), 0o600)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteFileAtomicConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	const writers = 8
	contents := make([][]byte, writers)
	for i := range contents {
		contents[i] = bytes.Repeat([]byte{byte('a' + i)}, 1000+i)
	}
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Go(func() {
			if err := WriteFileAtomic(path, contents[i], 0o600); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("writer: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range contents {
		if bytes.Equal(got, c) {
			found = true
		}
	}
	if !found {
		t.Fatalf("file holds a mix of writes (%d bytes)", len(got))
	}
	assertOnlyFile(t, dir, "state.json")
}

// A reader that holds the file open for a moment (as antivirus scanners do on Windows) must not make the write fail.
func TestWriteFileAtomicWhileFileBrieflyOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = f.Close()
		close(released)
	}()
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	<-released
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want new", got)
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if _, err := ReadFile(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: err = %v, want fs.ErrNotExist", err)
	}
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil || string(got) != "hello" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
}

func TestRetryIf(t *testing.T) {
	errBusy := errors.New("busy")
	errFatal := errors.New("fatal")
	transient := func(err error) bool { return errors.Is(err, errBusy) }
	tests := []struct {
		name      string
		failures  []error // errors returned by the first calls; later calls succeed
		wantErr   error
		wantCalls int
	}{
		{name: "first try", wantCalls: 1},
		{name: "transient then ok", failures: []error{errBusy, errBusy}, wantCalls: 3},
		{name: "fatal stops at once", failures: []error{errFatal}, wantErr: errFatal, wantCalls: 1},
		{name: "transient then fatal", failures: []error{errBusy, errFatal}, wantErr: errFatal, wantCalls: 2},
		{
			name:      "budget spent",
			failures:  []error{errBusy, errBusy, errBusy, errBusy, errBusy, errBusy, errBusy, errBusy, errBusy},
			wantErr:   errBusy,
			wantCalls: retryAttempts,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := retryIf(func() error {
				calls++
				if calls <= len(tt.failures) {
					return tt.failures[calls-1]
				}
				return nil
			}, transient)
			if tt.wantErr == nil && err != nil || !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if fmt.Sprint(names) != fmt.Sprint([]string{name}) {
		t.Fatalf("directory holds %v, want only %s (temp files left behind?)", names, name)
	}
}
