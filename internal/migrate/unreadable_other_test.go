//go:build !windows

package migrate

import (
	"os"
	"testing"
)

// unreadable takes every permission from path until release is called.
func unreadable(t *testing.T, path string) (release func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads every file")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chmod(path, 0o600) }
}
