//go:build !windows

package fsutil

import "path/filepath"

// shortName is empty: 8.3 short names exist only on Windows.
func shortName(string) string { return "" }

// resolveExisting returns p with symbolic links resolved, or p unchanged if they cannot be resolved.
func resolveExisting(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
