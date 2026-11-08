//go:build windows

package launch

import (
	"context"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// askStop stops a launcher with its process tree. Windows refuses to remove a launch directory while its launcher
// runs, so this is a safety net only, and Windows has no polite stop for a console program without a console of
// our own.
func askStop(pid int) error { return forceStop(pid) }

// forceStop kills one process of a worker tree, with its children.
func forceStop(pid int) error {
	return platform.KillTree(context.Background(), pid) //nolint:contextcheck // a quick kill, no caller context
}
