package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadRegular covers ReadRegular: the limit, truncation, and refusing folders and symbolic links.
func TestReadRegular(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, truncated, err := ReadRegular(file, 64)
	if err != nil || truncated || string(data) != "hello world" {
		t.Fatalf("full read = %q %v %v", data, truncated, err)
	}
	data, truncated, err = ReadRegular(file, 5)
	if err != nil || !truncated || string(data) != "hello" {
		t.Fatalf("limited read = %q %v %v", data, truncated, err)
	}
	if data, truncated, err = ReadRegular(file, 11); err != nil || truncated || len(data) != 11 {
		t.Fatalf("exact read = %q %v %v", data, truncated, err)
	}
	if _, _, err := ReadRegular(file, 0); err == nil {
		t.Fatal("a zero limit was accepted")
	}
	if _, _, err := ReadRegular(dir, 64); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("folder: err = %v", err)
	}
	if _, _, err := ReadRegular(filepath.Join(dir, "missing"), 64); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: err = %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Logf("no symbolic link test: %v", err) // Windows without the privilege
		return
	}
	if _, _, err := ReadRegular(link, 64); !errors.Is(err, ErrNotRegular) || !strings.Contains(err.Error(), "link") {
		t.Fatalf("symbolic link: err = %v", err)
	}
}
