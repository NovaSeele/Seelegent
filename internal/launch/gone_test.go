package launch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A launch whose folder was removed (by hand, or by a cleanup) ends Wait with ErrGone instead of keeping it waiting
// forever on a lock it can no longer probe (M18 hunt, launch note).
func TestWaitEndsWhenTheLaunchFolderIsGone(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "launch")
	f := FilesIn(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{f.Spec, f.Ready} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Attach(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := h.Wait(ctx); !errors.Is(err, ErrGone) {
		t.Fatalf("Wait = %v, want ErrGone", err)
	}
}
