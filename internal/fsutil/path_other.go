//go:build !windows

package fsutil

import "path/filepath"

// resolveExisting returns p with symbolic links resolved, or p unchanged if they cannot be resolved.
func resolveExisting(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
