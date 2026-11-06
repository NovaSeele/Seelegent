//go:build windows

package fsutil

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransient reports whether err is a Windows error that usually clears within moments: another process (antivirus,
// search indexer, backup tool, an editor) has the file open without sharing, or is locking a range of it.
func isTransient(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// syncDir is a no-op on Windows: NTFS journals the rename itself and directories cannot be flushed through a handle
// opened by the standard library.
func syncDir(string) error { return nil }
