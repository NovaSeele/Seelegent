//go:build unix && !linux

package platform

// runLinuxRole: the Linux-only helper roles do not exist here.
func runLinuxRole(string) bool { return false }
