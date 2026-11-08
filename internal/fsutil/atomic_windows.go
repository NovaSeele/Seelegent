//go:build windows

package fsutil

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// isTransient reports whether err is a Windows error that usually clears within moments: another process (antivirus,
// search indexer, backup tool, an editor) has the file open without sharing, or is locking a range of it.
func isTransient(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// unprotect clears the read-only attribute of an existing file at path, so a rename can replace it, and returns the
// function that sets the file's attributes back (for a failed replace). A missing file needs nothing.
func unprotect(path string) (undo func(), err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil || attrs&windows.FILE_ATTRIBUTE_READONLY == 0 || attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return func() {}, nil //nolint:nilerr // a missing or unreadable target is the rename's to report
	}
	if err := windows.SetFileAttributes(p, attrs&^windows.FILE_ATTRIBUTE_READONLY); err != nil {
		return nil, fmt.Errorf("clear the read-only attribute: %w", err)
	}
	return func() { _ = windows.SetFileAttributes(p, attrs) }, nil
}

// syncDir is a no-op on Windows: NTFS journals the rename itself and directories cannot be flushed through a handle
// opened by the standard library.
func syncDir(string) error { return nil }
