//go:build !windows

package gitx

import "path/filepath"

func dirOf(p string) string { return filepath.Dir(p) }
