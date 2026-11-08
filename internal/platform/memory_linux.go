//go:build linux

package platform

// memoryStatus is a stub of the M19 contract: the reading is not implemented yet, so it is never known.
func memoryStatus() MemoryStatus { return MemoryStatus{} }
