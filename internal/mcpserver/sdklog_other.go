//go:build !windows

package mcpserver

// osConnGone adds nothing outside Windows: syscall.ECONNRESET and its kin are the real codes there.
func osConnGone(error) bool { return false }
