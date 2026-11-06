//go:build unix && !linux

package platform

import (
	"errors"
	"fmt"
	"net/netip"
)

// socketOwnerInTrees is not supported on macOS and the BSDs: callers treat every caller as the lead.
func socketOwnerInTrees(netip.AddrPort, netip.AddrPort, []*WorkerTree) (id string, isWorker bool, err error) {
	return "", false, fmt.Errorf("socket owner lookup: %w", errors.ErrUnsupported)
}
