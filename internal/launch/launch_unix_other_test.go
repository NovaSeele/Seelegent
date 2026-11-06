//go:build unix && !linux

package launch

import "testing"

// checkAdopted has nothing to check without a child subreaper.
func checkAdopted(*testing.T, int, int) {}
