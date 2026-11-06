//go:build unix

package platform

import (
	"fmt"
	"os"
)

func linkDir(src, dst string) error { return symlink(src, dst) }

func linkFile(src, dst string) error { return symlink(src, dst) }

func symlink(src, dst string) error {
	if err := os.Symlink(src, dst); err != nil {
		return fmt.Errorf("%w: %w", errNoLink, err)
	}
	return nil
}
