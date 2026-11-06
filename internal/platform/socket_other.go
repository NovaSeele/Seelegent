//go:build unix && !linux

package platform

import (
	"errors"
	"fmt"
	"net/netip"
)

// lookupOptions lets tests step into a socket owner lookup.
type lookupOptions struct {
	// beforeRecheck runs after the first look found no worker, before that answer is checked again.
	beforeRecheck func()
}

// socketOwnerInTrees is not supported on macOS and the BSDs: callers treat every caller as the lead.
func socketOwnerInTrees(netip.AddrPort, netip.AddrPort, []*WorkerTree, lookupOptions) (id string, isWorker bool, err error) {
	return "", false, fmt.Errorf("socket owner lookup: %w", errors.ErrUnsupported)
}
