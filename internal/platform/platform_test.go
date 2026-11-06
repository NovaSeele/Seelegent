package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// roleEnv selects what the test binary does when it is started as a helper process.
const roleEnv = "PLATFORM_TEST_ROLE"

func TestMain(m *testing.M) {
	switch os.Getenv(roleEnv) {
	case "":
		os.Exit(m.Run())
	case "sleeper":
		writePids(os.Args[len(os.Args)-1], os.Getpid())
		time.Sleep(time.Minute)
		os.Exit(0)
	case "parent", "parent-exit":
		pidFile := os.Args[len(os.Args)-1]
		child := exec.CommandContext(context.Background(), os.Args[0], pidFile+".child")
		child.Env = append(os.Environ(), roleEnv+"=sleeper")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		waitFile(pidFile + ".child")
		writePids(pidFile, os.Getpid(), child.Process.Pid)
		if os.Getenv(roleEnv) == "parent" {
			time.Sleep(time.Minute)
		}
		os.Exit(0)
	case "print":
		fmt.Println(strings.Join(os.Args[1:], "|"))
		os.Exit(3)
	}
	os.Exit(2)
}

// writePids writes the pids to path atomically enough for the test (temp file, then rename).
func writePids(path string, pids ...int) {
	parts := make([]string, len(pids))
	for i, p := range pids {
		parts[i] = strconv.Itoa(p)
	}
	tmp := path + ".tmp"
	_ = os.WriteFile(tmp, []byte(strings.Join(parts, " ")), 0o600)
	_ = os.Rename(tmp, path)
}

// waitFile waits up to 10 s for path to exist.
func waitFile(path string) {
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// readPids waits for the pid file and parses it.
func readPids(t *testing.T, path string) []int {
	t.Helper()
	waitFile(path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pids: %v", err)
	}
	var pids []int
	for _, f := range strings.Fields(string(data)) {
		p, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parse pid %q: %v", f, err)
		}
		pids = append(pids, p)
	}
	return pids
}

// helper builds a command that runs this test binary in the given role.
func helper(role string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), os.Args[0], args...)
	cmd.Env = append(os.Environ(), roleEnv+"="+role)
	return cmd
}

// waitDead waits up to 10 s for every pid to be gone.
func waitDead(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, p := range pids {
		for ProcessAlive(p) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d still alive", p)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestTreeKillStopsGrandchildren(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, err := StartTree(helper("parent", pidFile))
	if err != nil {
		t.Fatalf("StartTree: %v", err)
	}
	defer func() { _ = tree.Close() }()
	pids := readPids(t, pidFile)
	if len(pids) != 2 || pids[0] != tree.Pid() {
		t.Fatalf("pids = %v, root %d", pids, tree.Pid())
	}
	for _, p := range pids {
		if !ProcessAlive(p) {
			t.Fatalf("process %d not alive before kill", p)
		}
	}
	waited := make(chan error, 1)
	go func() { waited <- tree.Wait() }()
	if err := tree.Kill(2 * time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case werr := <-waited:
		t.Logf("root exited: %v", werr)
	case <-time.After(10 * time.Second):
		t.Fatal("root did not exit after Kill")
	}
	waitDead(t, pids...)
	if err := tree.Kill(time.Second); err != nil && runtime.GOOS != "windows" {
		t.Fatalf("second Kill: %v", err)
	}
}

func TestTreeCloseStopsLeftovers(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, err := StartTree(helper("parent-exit", pidFile))
	if err != nil {
		t.Fatalf("StartTree: %v", err)
	}
	pids := readPids(t, pidFile)
	if err := tree.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !ProcessAlive(pids[1]) {
		t.Fatalf("grandchild %d should outlive the root until Close", pids[1])
	}
	if err := tree.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitDead(t, pids[1])
	if err := tree.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := tree.Kill(0); err != nil {
		t.Fatalf("Kill after Close: %v", err)
	}
}

func TestTreeNil(t *testing.T) {
	var tree *Tree
	if tree.Pid() != 0 || tree.Close() != nil {
		t.Fatal("nil tree should be inert")
	}
	if !errors.Is(tree.Wait(), ErrNotStarted) || !errors.Is(tree.Kill(0), ErrNotStarted) {
		t.Fatal("nil tree should report ErrNotStarted")
	}
}

func TestStartTreeFailure(t *testing.T) {
	if _, err := StartTree(exec.CommandContext(context.Background(), filepath.Join(t.TempDir(), "missing-binary"))); err == nil {
		t.Fatal("StartTree of a missing binary should fail")
	}
}

func TestStartDetachedAndKillTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd, err := StartDetached(func() *exec.Cmd { return helper("sleeper", pidFile) })
	if err != nil {
		t.Fatalf("StartDetached: %v", err)
	}
	pids := readPids(t, pidFile)
	if pids[0] != cmd.Process.Pid {
		t.Fatalf("pid %d, want %d", pids[0], cmd.Process.Pid)
	}
	if err := KillTree(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatalf("KillTree: %v", err)
	}
	_ = cmd.Wait()
	waitDead(t, pids[0])
	if err := KillTree(context.Background(), 0); err == nil {
		t.Fatal("KillTree(0) should fail")
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("own process should be alive")
	}
	if ProcessAlive(0) || ProcessAlive(-1) {
		t.Fatal("non-positive pids are never alive")
	}
	cmd := helper("print")
	HideWindow(cmd)
	_ = cmd.Run()
	if ProcessAlive(cmd.Process.Pid) {
		t.Skip("pid reused immediately; cannot check an exited process")
	}
}

func TestOpenURLRejectsOtherSchemes(t *testing.T) {
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "https://", "::"} {
		if err := OpenURL(context.Background(), u); err == nil {
			t.Errorf("OpenURL(%q) should fail", u)
		}
	}
}

func TestLinkInto(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := LinkInto(filepath.Join(src, "f.txt"), filepath.Join(dst, "f.txt")); err != nil {
		t.Fatalf("link file: %v", err)
	}
	if err := LinkInto(src, filepath.Join(dst, "dir")); err != nil {
		t.Fatalf("link dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "dir", "f.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("read through dir link: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dst, "f.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("read through file link: %q, %v", data, err)
	}
	if err := LinkInto(src, filepath.Join(dst, "dir")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing dst: %v, want ErrExist", err)
	}
	if err := LinkInto(filepath.Join(src, "missing"), filepath.Join(dst, "x")); err == nil {
		t.Fatal("missing src should fail")
	}
}
