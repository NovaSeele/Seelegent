//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package fsutil

import (
	"errors"
	"os"
	"syscall"
)

// openNoBlock opens path read-only without waiting for a FIFO's writer and without following a final symbolic
// link. O_NONBLOCK does not change how a regular file reads.
func openNoBlock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}

// isSymlinkLoop reports the error O_NOFOLLOW gives for a symbolic link.
func isSymlinkLoop(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK)
}
