//go:build !windows

package account

import "io/fs"

// isLink reports whether fi (from os.Lstat) is a symbolic link, which removeTree deletes as itself.
func isLink(fi fs.FileInfo) bool { return fi.Mode()&fs.ModeSymlink != 0 }
