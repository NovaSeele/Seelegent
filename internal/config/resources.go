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
//     8 GB of memory (CPUs only when the memory is unknown), whichever is less, from 1 to 4 (8 GB: 1, 16 GB: 2,
//     31 GB with 24 CPUs: 3).
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
		GateConcurrency:      clamp(autoGates(m), 1, autoGateConcurrencyHi),
	}
}

// autoGates is an eighth of the CPUs, or one gate per 8 GB when the memory is known, whichever is less.
func autoGates(m Machine) int {
	if m.TotalPhysMB <= 0 {
		return m.CPUs / 8
	}
	return min(m.CPUs/8, int(m.TotalPhysMB/1024/8))
}

// Sources of an effective resource value (ResourceSources).
const (
	// SourceAuto: the setting is auto; the value comes from the machine.
	SourceAuto = "auto"
	// SourceExplicit: the user set the value.
	SourceExplicit = "explicit"
	// SourceDerived: the setting is auto, but its value comes from the user's other memory threshold (half of an
	// explicit minFreeMemoryMB, or twice an explicit criticalFreeMemoryMB), so the floor stays below the minimum.
	SourceDerived = "derived"
)

// ResourceSources say where each effective resource value comes from (SourceAuto, SourceExplicit, SourceDerived).
type ResourceSources struct {
	MinFreeMemoryMB      string
	CriticalFreeMemoryMB string
	MinFreeCommitMB      string
	TestParallelism      string
	GateConcurrency      string
}

// EffectiveResources are the values the daemon uses on m (EffectiveResourceSources without the sources).
func EffectiveResources(s Settings, m Machine) Resources {
	r, _ := EffectiveResourceSources(s, m)
	return r
}

// EffectiveResourceSources are the values the daemon uses on m and where each comes from: each explicit setting
// wins, the others are auto. When only one of minFreeMemoryMB and criticalFreeMemoryMB is set, the other is
// derived from it so the floor stays below: a derived floor is half an explicit minimum above 0 (with the minimum
// off the floor stays auto), and a derived minimum is twice an explicit floor at or above the auto minimum (below
// it the auto minimum is already above the floor). Validate keeps twice a lone floor within MemoryMBMax.
func EffectiveResourceSources(s Settings, m Machine) (Resources, ResourceSources) {
	r := AutoResources(m)
	src := ResourceSources{SourceAuto, SourceAuto, SourceAuto, SourceAuto, SourceAuto}
	autoMin := r.MinFreeMemoryMB
	set := func(dst *int, from *string, v *int) {
		if v != nil {
			*dst, *from = *v, SourceExplicit
		}
	}
	set(&r.MinFreeCommitMB, &src.MinFreeCommitMB, s.MinFreeCommitMB)
	set(&r.TestParallelism, &src.TestParallelism, s.TestParallelism)
	set(&r.GateConcurrency, &src.GateConcurrency, s.GateConcurrency)
	set(&r.MinFreeMemoryMB, &src.MinFreeMemoryMB, s.MinFreeMemoryMB)
	set(&r.CriticalFreeMemoryMB, &src.CriticalFreeMemoryMB, s.CriticalFreeMemoryMB)
	switch minV, critV := s.MinFreeMemoryMB, s.CriticalFreeMemoryMB; {
	case minV != nil && critV == nil && *minV > 0:
		r.CriticalFreeMemoryMB, src.CriticalFreeMemoryMB = *minV/2, SourceDerived
	case critV != nil && minV == nil && *critV >= autoMin:
		r.MinFreeMemoryMB, src.MinFreeMemoryMB = min(2**critV, MemoryMBMax), SourceDerived
	}
	return r, src
}

// AutoSettingKeys are the JSON keys of the auto settings (nil = auto, never written to config.json).
func AutoSettingKeys() []string {
	return []string{"gateConcurrency", "minFreeMemoryMB", "minFreeCommitMB", "criticalFreeMemoryMB", "testParallelism"}
}

func clamp(n, lo, hi int) int { return max(lo, min(n, hi)) }

// Int returns a pointer to n: an explicit value of an auto setting.
func Int(n int) *int { return &n }
