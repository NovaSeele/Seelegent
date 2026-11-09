package httpapi_test

import (
	"errors"
	"syscall"
	"testing"
)

// errSharingViolation is ERROR_SHARING_VIOLATION, which package syscall does not name.
const errSharingViolation = syscall.Errno(32)

// fileHeld reports whether any handle on path is open: an open with no sharing at all fails while one is, whatever
// sharing that handle allows (os.Root opens files with delete sharing, so a rename does not show it).
func fileHeld(t *testing.T, path string) (held, known bool) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if errors.Is(err, errSharingViolation) {
		return true, true
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = syscall.CloseHandle(h)
	return false, true
}
