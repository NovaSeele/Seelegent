//go:build !windows

package fsutil

import (
	"errors"
	"os"
	"syscall"
)

// isTransient reports whether err usually clears within moments. Unix file systems do not refuse access to open
// files, so only an interrupted system call qualifies.
func isTransient(err error) bool {
	return errors.Is(err, syscall.EINTR)
}

// unprotect is a no-op outside Windows: a rename replaces a file whatever its mode.
func unprotect(string) (undo func(), err error) { return func() {}, nil }

// syncDir flushes the directory entry so that a rename into dir survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
