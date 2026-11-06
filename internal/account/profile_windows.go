//go:build windows

package account

import (
	"io/fs"
	"syscall"
)

// isLink reports whether fi (from os.Lstat) is a symbolic link, a junction or any other reparse point: something
// removeTree must delete as itself and never walk into.
func isLink(fi fs.FileInfo) bool {
	if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return true
	}
	if d, ok := fi.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
	}
	return false
}
