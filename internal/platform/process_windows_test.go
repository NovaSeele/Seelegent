package platform

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestListProcesses: this test process is listed with its own command line, and the filter sees exe file names.
func TestListProcesses(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(exe)
	var seen []string
	procs, err := ListProcesses(t.Context(), func(name string) bool {
		seen = append(seen, name)
		return strings.EqualFold(name, base)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) < 2 {
		t.Fatalf("the filter saw only %v", seen)
	}
	i := slices.IndexFunc(procs, func(p Process) bool { return p.PID == os.Getpid() })
	if i < 0 {
		t.Fatalf("this process (%d, %s) is not listed: %+v", os.Getpid(), base, procs)
	}
	p := procs[i]
	if !strings.EqualFold(p.Name, base) || !strings.Contains(p.CommandLine, "test.") ||
		len(p.Args) != len(os.Args) || !slices.Equal(p.Args[1:], os.Args[1:]) {
		t.Fatalf("this process = %+v, want the args %q", p, os.Args)
	}
	for _, q := range procs {
		if !strings.EqualFold(q.Name, base) {
			t.Fatalf("a process the filter refused is listed: %+v", q)
		}
	}
}
