//go:build !windows

package platform

import (
	"errors"
	"testing"
)

func TestListProcessesUnsupported(t *testing.T) {
	if _, err := ListProcesses(t.Context(), nil); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ListProcesses = %v, want ErrUnsupported", err)
	}
}
