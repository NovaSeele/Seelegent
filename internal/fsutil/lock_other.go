//go:build !windows && !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package fsutil

import (
	"errors"
	"os"
)

// lockFile reports that file locks are not supported on this platform.
func lockFile(*os.File) error { return errors.ErrUnsupported }

// unlockFile does nothing on platforms without file locks.
func unlockFile(*os.File) error { return nil }
