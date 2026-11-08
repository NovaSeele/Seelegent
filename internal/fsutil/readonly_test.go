package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Replacing a read-only file works and leaves it read-only (M18 hunt H20: Windows answered "Access is denied"
// after the whole retry budget, so restoring a read-only file failed).
func TestWriteFileAtomicOverReadOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ro.txt")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })
	start := time.Now()
	if err := WriteFileAtomic(p, []byte("new"), 0o400); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("the write took %s (retried a refusal?)", d)
	}
	data, err := os.ReadFile(p)
	if err != nil || string(data) != "new" {
		t.Fatalf("content %q, err %v", data, err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o200 != 0 {
		t.Errorf("the file is writable after the write: %v", st.Mode())
	}
	assertOnlyFile(t, dir, "ro.txt")

	// A writable mode makes it writable.
	if err := WriteFileAtomic(p, []byte("newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm()&0o200 == 0 {
		t.Errorf("mode after a writable write: %v, %v", st.Mode(), err)
	}
}
