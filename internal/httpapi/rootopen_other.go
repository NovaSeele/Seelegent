//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package httpapi

import "os"

// imageOpenFlags opens a working-tree image read-only. Windows has no FIFOs in the file system (its named pipes live
// under \\.\pipe\), so a plain open cannot wait for a writer.
const imageOpenFlags = os.O_RDONLY
