//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package httpapi

import (
	"os"
	"syscall"
)

// imageOpenFlags opens a working-tree image without waiting for a FIFO's writer: a worker can plant a FIFO under
// an image's name, and a plain open would wait for a writer forever. O_NONBLOCK does not change how a regular file
// reads.
const imageOpenFlags = os.O_RDONLY | syscall.O_NONBLOCK
