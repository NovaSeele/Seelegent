//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package fsutil

import "os"

// openNoBlock opens path read-only. Windows has no FIFOs in the file system (its named pipes live under
// \\.\pipe\), so a plain open cannot wait for a writer; OpenRegular's Lstat and the Stat of the open file keep
// links and other non-regular files out.
func openNoBlock(path string) (*os.File, error) {
	return os.Open(path)
}

func isSymlinkLoop(error) bool { return false }
