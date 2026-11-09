//go:build !unix

package platform

import "os"

// StopSignals are the signals that end a command's context (every seelegent command and the launcher). Windows
// delivers Ctrl+C and Ctrl+Break as os.Interrupt; it has no SIGTERM.
func StopSignals() []os.Signal { return []os.Signal{os.Interrupt} }
