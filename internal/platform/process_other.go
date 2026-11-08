//go:build !windows

package platform

import (
	"context"
	"errors"
	"fmt"
)

// listProcesses is not supported outside Windows.
func listProcesses(context.Context, func(string) bool) ([]Process, error) {
	return nil, fmt.Errorf("list processes: %w", errors.ErrUnsupported)
}
