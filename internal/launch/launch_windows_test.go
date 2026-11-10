package launch

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// detach starts cmd without a console and in its own process group, like a daemonising program. It stays in the
// worker's job: the job does not allow breakaway.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

// checkAdopted has nothing to check on Windows: membership comes from the job, not from parent ids.
func checkAdopted(*testing.T, int, int) {}

// procReport has nothing to add on Windows.
func procReport(int) string { return "" }

// TestWorkerMemoryLimitCancelNearCap: a worker whose job peak reached 93% of its cap, never refused memory, and that
// the launcher then stopped (cancel) is no memory failure: the kill's exit code is not the cap's (review N1).
func TestWorkerMemoryLimitCancelNearCap(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	pid := filepath.Join(dir, "worker.pid")
	opts := testOptions(dir, "alloc:64", "pid:"+pid, "sleep:60000")
	opts.Spec.Tree = platform.TreeOptions{MemoryLimitMB: 1024}
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	readPid(t, pid)
	r, _ := h.Ready()
	capAbovePeak(t, r.Job, 93)
	if err := h.Cancel(); err != nil {
		t.Fatal(err)
	}
	if ex := wait(t, h); ex.Reason != ReasonCanceled || ex.MemoryLimitHit {
		t.Fatalf("cancelled at 93%% of the cap: %+v", ex)
	}
}

// capAbovePeak opens the worker's named job and moves its memory cap so that its peak so far is pct percent of it.
func capAbovePeak(t *testing.T, job string, pct uint64) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(job)
	if err != nil {
		t.Fatal(err)
	}
	const access = 0x1F003F // JOB_OBJECT_ALL_ACCESS
	r, _, callErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW").Call(access, 0,
		uintptr(unsafe.Pointer(name)))
	if r == 0 {
		t.Fatalf("open job %s: %v", job, callErr)
	}
	h := windows.Handle(r)
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		t.Fatal(err)
	}
	info.JobMemoryLimit = info.PeakJobMemoryUsed * 100 / uintptr(pct)
	if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		t.Fatal(err)
	}
	t.Logf("peak %d MiB, cap %d MiB", info.PeakJobMemoryUsed>>20, info.JobMemoryLimit>>20)
}
