//go:build unix && !linux

package launch

import "testing"

// checkAdopted has nothing to check without a child subreaper.
func checkAdopted(*testing.T, int, int) {}

// procReport has nothing to add without /proc.
func procReport(int) string { return "" }
