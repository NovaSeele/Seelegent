//go:build unix

package cli

import (
	"os"
	"syscall"
)

// stopSignals are the signals that end the command's context. On Unix a service manager, kill(1) and the shell
// stop a process with SIGTERM: without it the launcher would die at once and leave its worker tree running,
// instead of stopping the tree and writing the exit record (M18 hunt H13).
func stopSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }
