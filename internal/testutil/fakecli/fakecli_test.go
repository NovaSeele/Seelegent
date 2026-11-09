package fakecli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestMain(m *testing.M) { Main(m) }

// TestPidAndAlloc: #!pid writes the fake CLI's process id, #!alloc takes memory and the script goes on.
func TestPidAndAlloc(t *testing.T) {
	q := NewQueue(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	q.Push(t, "#!pid "+pidFile+"\n#!alloc 16\nhello\n")
	cmd := exec.CommandContext(context.Background(), Exe(t))
	cmd.Env = q.Env(os.Environ())
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("output %q, %v", out, err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil || strings.TrimSpace(string(data)) != strconv.Itoa(cmd.Process.Pid) {
		t.Fatalf("pid file %q (%v), want %d", data, err, cmd.Process.Pid)
	}
	if n := len(touched(3)); n != 3<<20 {
		t.Fatalf("touched(3) = %d bytes", n)
	}
}

// Runs that start together each take a different script, and every script goes to one run only (bugs F23).
func TestTakeClaimsEachScriptOnce(t *testing.T) {
	q := NewQueue(t)
	const scripts, runs = 20, 40
	for i := range scripts {
		q.Push(t, fmt.Sprintf("script %d\n", i))
	}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		start = make(chan struct{})
		got   = map[string]string{}
		empty int
	)
	for range runs {
		wg.Go(func() {
			<-start
			name, data, err := take(q.Dir())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				t.Errorf("take: %v", err)
			case name == "":
				empty++
			case got[name] != "":
				t.Errorf("%s taken twice", name)
			default:
				got[name] = string(data)
			}
		})
	}
	close(start)
	wg.Wait()
	if len(got) != scripts || empty != runs-scripts {
		t.Errorf("took %d scripts, %d runs found none", len(got), empty)
	}
	if n := q.Left(t); n != 0 {
		t.Errorf("%d scripts left", n)
	}
}
