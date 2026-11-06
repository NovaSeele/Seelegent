//go:build unix && !linux

package platform

import "os/exec"

// parentDeathSignal does nothing: macOS and the BSDs have no parent-death signal, so a tree whose owner dies keeps
// running until it ends by itself (the daemon reports such a launch as lost).
func parentDeathSignal(*exec.Cmd) {}
