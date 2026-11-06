//go:build !linux

package platform

import (
	"errors"
	"fmt"
)

// lockHolders cannot tell lock holders apart outside Linux.
func lockHolders(string) ([]int, error) {
	return nil, fmt.Errorf("lock holders: %w", errors.ErrUnsupported)
}
