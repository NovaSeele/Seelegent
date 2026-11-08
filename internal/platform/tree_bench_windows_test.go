package platform

import (
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// BenchmarkThreadSnapshot is the walk resumeThreads does for every tree it starts: a toolhelp snapshot of every
// thread of the machine, read to the end. Its cost grows with the threads of everything else that runs.
func BenchmarkThreadSnapshot(b *testing.B) {
	threads := 0
	for b.Loop() {
		snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err != nil {
			b.Fatal(err)
		}
		var e windows.ThreadEntry32
		e.Size = uint32(unsafe.Sizeof(e))
		threads = 0
		for err = windows.Thread32First(snap, &e); err == nil; err = windows.Thread32Next(snap, &e) {
			threads++
		}
		_ = windows.CloseHandle(snap)
		if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(threads), "threads")
}
