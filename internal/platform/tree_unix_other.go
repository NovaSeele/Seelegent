//go:build unix && !linux

package platform

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// macOS and the BSDs have no child subreaper: a worker tree is the owner's descendants by parent id, best effort.
const (
	treesExact         = false
	subreaperSupported = false
)

// parentDeathSignal does nothing: macOS and the BSDs have no parent-death signal, so a tree whose owner dies keeps
// running until it ends by itself (the daemon reports such a launch as lost).
func parentDeathSignal(*exec.Cmd) {}

// becomeSubreaper is never called where subreaperSupported is false.
func becomeSubreaper() error { return nil }

// signalVerified sends sig to p if its id still names the same process. There is a short window between the check
// and the signal; Linux closes it with a pidfd.
func signalVerified(p procInfo, sig syscall.Signal) {
	if start, err := procStart(p.pid); err == nil && start == p.start {
		_ = syscall.Kill(p.pid, sig)
	}
}

// niceOf returns the nice value of who.
func niceOf(which, who int) (int, error) { return unix.Getpriority(which, who) }
