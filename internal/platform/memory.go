package platform

// MemoryStatus is the free memory of the machine. Known is false when the system gave no reading; the numbers are
// 0 then, and a caller must never hold work back on an unknown reading.
type MemoryStatus struct {
	// AvailPhysMB is the physical memory available to new processes, in MiB (Windows: ullAvailPhys of
	// GlobalMemoryStatusEx; Linux: MemAvailable of /proc/meminfo; macOS: best effort).
	AvailPhysMB int64
	// AvailCommitMB is the commit still available (RAM plus page file), in MiB: Windows only (ullAvailPageFile of
	// GlobalMemoryStatusEx); 0 elsewhere, where commit is unknown.
	AvailCommitMB int64
	// Known is true when the reading succeeded.
	Known bool
}

// Memory reads the free memory of the machine. It is cheap (one system call or one small file read) but not
// free: callers that check often cache it.
func Memory() MemoryStatus { return memoryStatus() }
