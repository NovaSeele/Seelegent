package config

// Machine is what the auto resource settings are computed from (docs/configuration.md "Resources"). The caller
// reads it from the system: config stays free of platform.
type Machine struct {
	// TotalPhysMB is the machine's physical memory in MiB, or the cgroup memory limit when that is lower; 0 =
	// unknown.
	TotalPhysMB int64
	// TotalCommitMB is the commit limit (RAM plus page file) in MiB, rounded down to a whole GiB so the page file
	// growing a little does not move the auto values: Windows only, 0 = unknown.
	TotalCommitMB int64
	// CPUs are the logical CPUs this process may use.
	CPUs int
	// OS is runtime.GOOS.
	OS string
}

// NewMachine is the machine profile of a reading: the commit total is rounded down to a whole GiB, so a page file
// that grows a little does not move the auto values.
func NewMachine(totalPhysMB, totalCommitMB int64, cpus int, goos string) Machine {
	return Machine{TotalPhysMB: totalPhysMB, TotalCommitMB: totalCommitMB / 1024 * 1024, CPUs: cpus, OS: goos}
}

// Resources are the effective values of the auto resource settings: the user's value when set, else the auto one.
type Resources struct {
	MinFreeMemoryMB      int
	CriticalFreeMemoryMB int
	MinFreeCommitMB      int
	TestParallelism      int
	GateConcurrency      int
}

// Bounds of the auto values.
const (
	autoMinFreeLo, autoMinFreeHi             = 512, 2048
	autoMinFreeCommitLo, autoMinFreeCommitHi = 1024, 4096
	autoTestParallelismHi                    = 8
	autoGateConcurrencyHi                    = 4
)

// AutoResources are the auto values on m, before any user value:
//
//   - minFreeMemoryMB: 12.5% of the physical memory, from 512 to 2048 MB; criticalFreeMemoryMB: half of it.
//   - minFreeCommitMB: Windows with a known commit limit only, 10% of it, from 1024 to 4096 MB; 0 elsewhere.
//   - testParallelism: a quarter of the CPUs, from 1 to 8; gateConcurrency: an eighth of the CPUs or one gate per
//     8 GB of memory, whichever is less, from 1 to 4 (8 GB: 1, 16 GB: 2, 31 GB with 24 CPUs: 3).
func AutoResources(m Machine) Resources {
	minFree := clamp(int(m.TotalPhysMB/8), autoMinFreeLo, autoMinFreeHi)
	commit := 0
	if m.OS == "windows" && m.TotalCommitMB > 0 {
		commit = clamp(int(m.TotalCommitMB/10), autoMinFreeCommitLo, autoMinFreeCommitHi)
	}
	return Resources{
		MinFreeMemoryMB:      minFree,
		CriticalFreeMemoryMB: minFree / 2,
		MinFreeCommitMB:      commit,
		TestParallelism:      clamp(m.CPUs/4, 1, autoTestParallelismHi),
		GateConcurrency:      clamp(min(m.CPUs/8, int(m.TotalPhysMB/1024/8)), 1, autoGateConcurrencyHi),
	}
}

// EffectiveResources are the values the daemon uses on m: each explicit setting wins, the others are auto. When
// only one of minFreeMemoryMB and criticalFreeMemoryMB is set, the auto one is derived so the floor stays below:
// an auto floor is half the minimum (half the auto minimum when the minimum is off), and an auto minimum is twice
// a floor at or above the auto minimum.
func EffectiveResources(s Settings, m Machine) Resources {
	r := AutoResources(m)
	autoMin := r.MinFreeMemoryMB
	set := func(dst *int, v *int) {
		if v != nil {
			*dst = *v
		}
	}
	set(&r.MinFreeCommitMB, s.MinFreeCommitMB)
	set(&r.TestParallelism, s.TestParallelism)
	set(&r.GateConcurrency, s.GateConcurrency)
	switch minV, critV := s.MinFreeMemoryMB, s.CriticalFreeMemoryMB; {
	case minV != nil && critV != nil:
		r.MinFreeMemoryMB, r.CriticalFreeMemoryMB = *minV, *critV
	case minV != nil:
		r.MinFreeMemoryMB = *minV
		if *minV > 0 {
			r.CriticalFreeMemoryMB = *minV / 2
		}
	case critV != nil:
		r.CriticalFreeMemoryMB = *critV
		if *critV >= autoMin {
			r.MinFreeMemoryMB = min(2**critV, MemoryMBMax)
		}
	}
	return r
}

// AutoSettingKeys are the JSON keys of the auto settings (nil = auto, never written to config.json).
func AutoSettingKeys() []string {
	return []string{"gateConcurrency", "minFreeMemoryMB", "minFreeCommitMB", "criticalFreeMemoryMB", "testParallelism"}
}

func clamp(n, lo, hi int) int { return max(lo, min(n, hi)) }

// Int returns a pointer to n: an explicit value of an auto setting.
func Int(n int) *int { return &n }
