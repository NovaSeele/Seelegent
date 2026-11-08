//go:build !windows && !linux && !darwin

package platform

import (
	"context"
	"errors"
	"fmt"
)

// watchProcess is not available here: callers poll.
func watchProcess(_ context.Context, pid int, _ uint64) (<-chan struct{}, error) {
	return nil, fmt.Errorf("watch process %d: %w", pid, errors.ErrUnsupported)
}
