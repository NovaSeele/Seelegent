package httpapi

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// TestHealthMemory: GET /health carries the free-memory reading, and only a known one.
func TestHealthMemory(t *testing.T) {
	reading := platform.MemoryStatus{AvailPhysMB: 3000, AvailCommitMB: 9000, Known: true}
	s := &Server{o: Options{FreeMemory: func() platform.MemoryStatus { return reading }}}
	if got := s.health().Memory; got != (api.MemoryStatus{AvailPhysMB: 3000, AvailCommitMB: 9000, Known: true}) {
		t.Fatalf("memory = %+v", got)
	}
	reading = platform.MemoryStatus{AvailPhysMB: 5, Known: false}
	if got := s.health().Memory; got != (api.MemoryStatus{}) {
		t.Fatalf("an unknown reading must be all zero: %+v", got)
	}
	if h := (&Server{}).health(); h.Memory.Known || h.LowMemory {
		t.Fatalf("no readers: %+v", h)
	}
	if h := (&Server{o: Options{LowMemory: func() bool { return true }}}).health(); !h.LowMemory {
		t.Fatalf("lowMemory not reported: %+v", h)
	}
}
