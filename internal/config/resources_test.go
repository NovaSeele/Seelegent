package config

import (
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
	for _, c := range []struct {
		name string
		set  func(*Settings)
		want Resources
	}{
		{"all auto", func(*Settings) {}, Resources{2048, 1024, 2457, 2, 1}},
		{"explicit values win", func(s *Settings) {
			s.MinFreeMemoryMB, s.CriticalFreeMemoryMB, s.MinFreeCommitMB = Int(3000), Int(500), Int(0)
			s.TestParallelism, s.GateConcurrency = Int(0), Int(0)
		}, Resources{3000, 500, 0, 0, 0}},
		{"only the minimum: the floor is half of it", func(s *Settings) { s.MinFreeMemoryMB = Int(600) },
			Resources{600, 300, 2457, 2, 1}},
		{"minimum off: the floor stays auto", func(s *Settings) { s.MinFreeMemoryMB = Int(0) },
			Resources{0, 1024, 2457, 2, 1}},
		{"only a low floor: the minimum stays auto", func(s *Settings) { s.CriticalFreeMemoryMB = Int(700) },
			Resources{2048, 700, 2457, 2, 1}},
		{"only a high floor: the minimum is twice it", func(s *Settings) { s.CriticalFreeMemoryMB = Int(3000) },
			Resources{6000, 3000, 2457, 2, 1}},
		{"floor off", func(s *Settings) { s.CriticalFreeMemoryMB = Int(0) }, Resources{2048, 0, 2457, 2, 1}},
		{"a tiny minimum: the derived floor is off, never at or above it", func(s *Settings) {
			s.MinFreeMemoryMB = Int(1)
		}, Resources{1, 0, 2457, 2, 1}},
	} {
		s := Defaults()
		c.set(&s)
		got := EffectiveResources(s, m)
		if got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
		if got.CriticalFreeMemoryMB > 0 && got.MinFreeMemoryMB > 0 && got.CriticalFreeMemoryMB >= got.MinFreeMemoryMB {
			t.Errorf("%s: the floor %d is not below the minimum %d", c.name, got.CriticalFreeMemoryMB,
				got.MinFreeMemoryMB)
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
