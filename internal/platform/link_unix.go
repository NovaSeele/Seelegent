//go:build unix

package platform

import (
	"fmt"
	"io/fs"
	"os"
)

// IsLink reports whether fi (from os.Lstat) is a symbolic link.
func IsLink(fi fs.FileInfo) bool { return fi.Mode()&fs.ModeSymlink != 0 }

func linkDir(src, dst string) error { return symlink(src, dst) }

func linkFile(src, dst string) error { return symlink(src, dst) }

func symlink(src, dst string) error {
	if err := os.Symlink(src, dst); err != nil {
		return fmt.Errorf("%w: %w", errNoLink, err)
	}
	return nil
}
