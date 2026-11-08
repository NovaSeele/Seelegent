//go:build !unix

package cli

import "os"

// stopSignals are the signals that end the command's context. Windows delivers Ctrl+C and Ctrl+Break as
// os.Interrupt; it has no SIGTERM.
func stopSignals() []os.Signal { return []os.Signal{os.Interrupt} }
