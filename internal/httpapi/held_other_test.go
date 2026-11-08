//go:build !windows && !linux

package httpapi_test

import "testing"

// fileHeld cannot tell here: there is no portable way to list a process's open files.
func fileHeld(t *testing.T, _ string) (held, known bool) {
	t.Helper()
	return false, false
}
