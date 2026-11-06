//go:build unix && !linux && !darwin

package platform

import (
	"errors"
	"fmt"
)

// canListProcs: the BSDs and other Unix systems are not supported; trees fall back to their process group.
const canListProcs = false

func listProcs() ([]procInfo, error) {
	return nil, fmt.Errorf("list processes: %w", errors.ErrUnsupported)
}

func procStart(int) (uint64, error) {
	return 0, fmt.Errorf("process start time: %w", errors.ErrUnsupported)
}
