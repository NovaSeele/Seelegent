package httpapi

import (
	"context"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
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

// fixedSettings is an httpapi.Settings with fixed settings.
type fixedSettings struct{ s config.Settings }

func (f fixedSettings) Settings() config.Settings { return f.s }
func (f fixedSettings) PatchSettings(context.Context, []byte) (config.Settings, error) {
	return f.s, nil
}

// TestHealthResources: GET /health carries the effective resource values with their source and the machine.
func TestHealthResources(t *testing.T) {
	set := config.Defaults()
	set.TestParallelism = config.Int(3)
	m := config.Machine{TotalPhysMB: 16 * 1024, TotalCommitMB: 24 * 1024, CPUs: 8, OS: "windows"}
	s := &Server{o: Options{Settings: fixedSettings{set}, Machine: func() config.Machine { return m }}}
	h := s.health()
	auto := func(v int) api.EffectiveValue { return api.EffectiveValue{Value: v, Source: api.ResourceAuto} }
	want := api.EffectiveResources{MinFreeMemoryMB: auto(2048), CriticalFreeMemoryMB: auto(1024),
		MinFreeCommitMB: auto(2457), TestParallelism: api.EffectiveValue{Value: 3, Source: api.ResourceExplicit},
		GateConcurrency: auto(1)}
	if h.Resources != want || h.Machine != (api.Machine{TotalPhysMB: 16384, TotalCommitMB: 24576, CPUs: 8}) {
		t.Fatalf("resources = %+v machine = %+v", h.Resources, h.Machine)
	}
}
