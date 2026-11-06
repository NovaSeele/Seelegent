package platform

import (
	"errors"
	"fmt"
	"os"
)

// LinkInto makes dst point at the existing file or directory src, so a tool that reads dst (inside an isolated
// profile) sees the user's real configuration. Directories become symbolic links, or junctions on Windows when
// symbolic links need a privilege the user lacks; files become symbolic links on Unix and hard links on Windows.
// An existing dst is left alone and reported as fs.ErrExist.
func LinkInto(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("link %s: %w", dst, os.ErrExist)
	}
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("link %s: %w", dst, err)
	}
	if st.IsDir() {
		err = linkDir(src, dst)
	} else {
		err = linkFile(src, dst)
	}
	if err != nil {
		return fmt.Errorf("link %s -> %s: %w", dst, src, err)
	}
	return nil
}

// errNoLink is returned when no link kind works on this system.
var errNoLink = errors.New("cannot create a link here")
