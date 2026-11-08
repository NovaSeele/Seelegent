package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutoResources(t *testing.T) {
	const gb = 1024
	for _, c := range []struct {
		name string
		m    Machine
		want Resources
	}{
		{"memory unknown, 24 CPUs", Machine{CPUs: 24, OS: "darwin"}, Resources{512, 256, 0, 6, 3}},
		{"4 GB container, 2 CPUs", Machine{TotalPhysMB: 4 * gb, CPUs: 2, OS: "linux"}, Resources{512, 256, 0, 1, 1}},
		{"8 GB laptop, 4 CPUs", Machine{TotalPhysMB: 8 * gb, TotalCommitMB: 12 * gb, CPUs: 4, OS: "windows"},
			Resources{1024, 512, 1228, 1, 1}},
		{"16 GB, 8 CPUs", Machine{TotalPhysMB: 16 * gb, TotalCommitMB: 24 * gb, CPUs: 8, OS: "windows"},
			Resources{2048, 1024, 2457, 2, 1}},
		{"16 GB, 16 CPUs", Machine{TotalPhysMB: 16 * gb, CPUs: 16, OS: "linux"}, Resources{2048, 1024, 0, 4, 2}},
		{"31 GB, 24 CPUs (the user's machine)", Machine{TotalPhysMB: 31 * gb, TotalCommitMB: 36 * gb, CPUs: 24,
			OS: "windows"}, Resources{2048, 1024, 3686, 6, 3}},
		{"16 GB, commit unknown", Machine{TotalPhysMB: 16 * gb, CPUs: 8, OS: "windows"},
			Resources{2048, 1024, 0, 2, 1}},
		{"32 GB workstation, 24 CPUs", Machine{TotalPhysMB: 32 * gb, TotalCommitMB: 64 * gb, CPUs: 24,
			OS: "windows"}, Resources{2048, 1024, 4096, 6, 3}},
		{"128 GB server, 64 CPUs", Machine{TotalPhysMB: 128 * gb, CPUs: 64, OS: "linux"},
			Resources{2048, 1024, 0, 8, 4}},
		{"mac, 8 GB", Machine{TotalPhysMB: 8 * gb, TotalCommitMB: 99 * gb, CPUs: 8, OS: "darwin"},
			Resources{1024, 512, 0, 2, 1}},
		{"tiny Windows commit", Machine{TotalPhysMB: 2 * gb, TotalCommitMB: 3 * gb, CPUs: 1, OS: "windows"},
			Resources{512, 256, 1024, 1, 1}},
		{"nothing known", Machine{}, Resources{512, 256, 0, 1, 1}},
	} {
		if got := AutoResources(c.m); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestEffectiveResources(t *testing.T) {
	m := Machine{TotalPhysMB: 16 * 1024, TotalCommitMB: 24 * 1024, CPUs: 8, OS: "windows"} // auto 2048/1024/2457/2/1
	const a, e, d = SourceAuto, SourceExplicit, SourceDerived
	auto := ResourceSources{a, a, a, a, a}
	for _, c := range []struct {
		name string
		set  func(*Settings)
		want Resources
		src  ResourceSources
	}{
		{"all auto", func(*Settings) {}, Resources{2048, 1024, 2457, 2, 1}, auto},
		{"explicit values win", func(s *Settings) {
			s.MinFreeMemoryMB, s.CriticalFreeMemoryMB, s.MinFreeCommitMB = Int(3000), Int(500), Int(0)
			s.TestParallelism, s.GateConcurrency = Int(0), Int(0)
		}, Resources{3000, 500, 0, 0, 0}, ResourceSources{e, e, e, e, e}},
		{"only the minimum: the floor is half of it", func(s *Settings) { s.MinFreeMemoryMB = Int(600) },
			Resources{600, 300, 2457, 2, 1}, ResourceSources{e, d, a, a, a}},
		{"minimum off: the floor stays auto", func(s *Settings) { s.MinFreeMemoryMB = Int(0) },
			Resources{0, 1024, 2457, 2, 1}, ResourceSources{e, a, a, a, a}},
		{"only a low floor: the minimum stays auto", func(s *Settings) { s.CriticalFreeMemoryMB = Int(700) },
			Resources{2048, 700, 2457, 2, 1}, ResourceSources{a, e, a, a, a}},
		{"only a high floor: the minimum is twice it", func(s *Settings) { s.CriticalFreeMemoryMB = Int(3000) },
			Resources{6000, 3000, 2457, 2, 1}, ResourceSources{d, e, a, a, a}},
		{"the highest lone floor: the minimum is the top of the range", func(s *Settings) {
			s.CriticalFreeMemoryMB = Int(MemoryMBMax / 2)
		}, Resources{MemoryMBMax, MemoryMBMax / 2, 2457, 2, 1}, ResourceSources{d, e, a, a, a}},
		{"floor off", func(s *Settings) { s.CriticalFreeMemoryMB = Int(0) }, Resources{2048, 0, 2457, 2, 1},
			ResourceSources{a, e, a, a, a}},
		{"a tiny minimum: the derived floor is off, never at or above it", func(s *Settings) {
			s.MinFreeMemoryMB = Int(1)
		}, Resources{1, 0, 2457, 2, 1}, ResourceSources{e, d, a, a, a}},
	} {
		s := Defaults()
		c.set(&s)
		if err := s.Validate(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, src := EffectiveResourceSources(s, m)
		if got != c.want || src != c.src {
			t.Errorf("%s: %+v %+v, want %+v %+v", c.name, got, src, c.want, c.src)
		}
	}
}

// TestFloorBelowMinimum: on every machine, every pair of memory thresholds that validates has its effective floor
// below its effective minimum whenever both are on; a lone floor whose double is out of range is refused, naming
// only criticalFreeMemoryMB.
func TestFloorBelowMinimum(t *testing.T) {
	machines := []Machine{
		{}, {TotalPhysMB: 2048, CPUs: 1, OS: "linux"}, NewMachine(8*1024, 12*1024, 4, "windows"),
		NewMachine(32*1024, 64*1024, 24, "windows"), {TotalPhysMB: 1 << 20, CPUs: 256, OS: "linux"},
	}
	values := []*int{nil, Int(0), Int(1), Int(2), Int(255), Int(256), Int(511), Int(512), Int(1023), Int(1024),
		Int(2047), Int(2048), Int(4096), Int(MemoryMBMax/2 - 1), Int(MemoryMBMax / 2), Int(MemoryMBMax/2 + 1),
		Int(MemoryMBMax - 1), Int(MemoryMBMax)}
	show := func(p *int) string {
		if p == nil {
			return "auto"
		}
		return fmt.Sprint(*p)
	}
	for _, m := range machines {
		for _, minV := range values {
			for _, critV := range values {
				s := Defaults()
				s.MinFreeMemoryMB, s.CriticalFreeMemoryMB = minV, critV
				err := s.Validate()
				lone := minV == nil && critV != nil && *critV > MemoryMBMax/2
				if lone {
					var inv *ValidationError
					if !errors.As(err, &inv) || len(inv.Fields) != 1 || inv.Fields[0].Field != "criticalFreeMemoryMB" {
						t.Errorf("min %s, floor %s: %v, want only criticalFreeMemoryMB refused", show(minV),
							show(critV), err)
					}
					continue
				}
				if err != nil {
					continue
				}
				r := EffectiveResources(s, m)
				if r.CriticalFreeMemoryMB > 0 && r.MinFreeMemoryMB > 0 && r.CriticalFreeMemoryMB >= r.MinFreeMemoryMB {
					t.Errorf("%+v, min %s, floor %s: floor %d not below the minimum %d", m, show(minV), show(critV),
						r.CriticalFreeMemoryMB, r.MinFreeMemoryMB)
				}
				if r.MinFreeMemoryMB > MemoryMBMax {
					t.Errorf("min %s, floor %s: minimum %d out of range", show(minV), show(critV), r.MinFreeMemoryMB)
				}
			}
		}
	}
}

// TestAutoSettingsNeverWritten: config.json never gets a computed value: an unrelated change writes no key for an
// auto setting, and a reset to auto removes the key the file had.
func TestAutoSettingsNeverWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"minFreeMemoryMB": 1500, "testParallelism": 3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Update(t.Context(), path, func(s *Settings) error {
		if !isInt(s.MinFreeMemoryMB, 1500) || !isInt(s.TestParallelism, 3) {
			t.Errorf("loaded %+v", s)
		}
		s.WorkerPriority = PriorityNormal
		s.MinFreeMemoryMB = nil // PATCH null
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, k := range AutoSettingKeys() {
		if strings.Contains(text, `"`+k+`"`) != (k == "testParallelism") {
			t.Errorf("key %s in config.json: %s", k, text)
		}
	}
	if len(c.Unknown()) != 0 {
		t.Errorf("unknown keys %v", c.Unknown())
	}
	// The same through MergePatch.
	next, err := MergePatch(c.Settings, []byte(`{"testParallelism": null, "workerPriority": "low"}`))
	if err != nil {
		t.Fatal(err)
	}
	if next.TestParallelism != nil {
		t.Fatalf("null did not reset testParallelism: %+v", next)
	}
	if _, err := Update(t.Context(), path, func(s *Settings) error { *s = next; return nil }); err != nil {
		t.Fatal(err)
	}
	if data, _ = os.ReadFile(path); strings.Contains(string(data), "testParallelism") ||
		strings.Contains(string(data), "minFreeMemoryMB") {
		t.Errorf("auto keys written: %s", data)
	}
}
