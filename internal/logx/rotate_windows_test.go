//go:build windows

package logx

import (
	"os"
	"path/filepath"
	"testing"
)

// A reader that holds the log open without delete sharing blocks the rename on Windows. Writes must go on, and
// rotation must happen once the reader is gone.
func TestRotationBlockedByReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	r, err := OpenRotatingFile(path, 50, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	line := []byte("0123456789012345678901234567890123456789\n") // 41 bytes
	if _, err := r.Write(line); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write(line); err != nil {
		t.Fatalf("Write while rotation is blocked: %v", err)
	}
	_ = reader.Close()
	if lines := readLines(t, path); len(lines) != 2 {
		t.Fatalf("both lines must be in the current file while rotation is blocked, got %d", len(lines))
	}
	if _, err := r.Write(line); err != nil {
		t.Fatal(err)
	}
	if lines := readLines(t, path); len(lines) != 1 {
		t.Fatalf("rotation did not happen after the reader left: %d lines", len(lines))
	}
	if lines := readLines(t, path+".1"); len(lines) != 2 {
		t.Fatalf("rotated copy has %d lines, want 2", len(lines))
	}
}
