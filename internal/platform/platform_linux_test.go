package platform

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// runLinuxRole runs the Linux-only helper roles:
//
//	nondumpable <pidFile>             makes itself non-dumpable (like ssh-agent), writes its pid and sleeps
//	nondumpable-dial <pidFile> <addr> the same, after connecting to addr
func runLinuxRole(role string) bool {
	args := os.Args[1:]
	switch role {
	case "nondumpable", "nondumpable-dial":
		if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
			os.Exit(3)
		}
		if role == "nondumpable-dial" {
			conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", args[1])
			if err != nil {
				os.Exit(3)
			}
			defer func() { _ = conn.Close() }()
		}
		writePids(args[0], os.Getpid())
		time.Sleep(time.Minute)
		return true
	}
	return false
}

// TestSocketOwnerWithUnreadableTreeProcess: a tree holding a process whose open files cannot be read (tree A, like
// an ssh-agent a worker started) neither turns every caller into a worker nor lends its ID to another tree's
// connection. Only a socket that cannot be placed anywhere fails closed, without a task ID.
func TestSocketOwnerWithUnreadableTreeProcess(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()
	dir := t.TempDir()
	accept := func() (local, remote netip.AddrPort) {
		t.Helper()
		c, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return addrPorts(t, c)
	}

	treeA, wtA := startDescribedTree(t, helper("nondumpable", filepath.Join(dir, "a")))
	defer func() { _ = treeA.Close() }()
	wtA.ID = "A"
	hidden := readPids(t, filepath.Join(dir, "a"))[0]
	if _, err := hasOpenFile(hidden, "x"); err == nil {
		t.Skip("non-dumpable processes are readable here (running as root?)")
	}

	treeB, wtB := startDescribedTree(t, helper("connect", filepath.Join(dir, "b"), addr))
	defer func() { _ = treeB.Close() }()
	wtB.ID = "B"
	workerL, workerR := accept()

	leadConn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = leadConn.Close() }()
	leadL, leadR := accept()

	outsider := helper("nondumpable-dial", filepath.Join(dir, "o"), addr) // unreadable and outside every tree
	if err := outsider.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outsider.Process.Kill(); _ = outsider.Wait() }()
	outL, outR := accept()
	readPids(t, filepath.Join(dir, "o"))

	for _, trees := range [][]*WorkerTree{{wtA, wtB}, {wtB, wtA}} {
		if id, isWorker, err := SocketOwnerInTrees(workerL, workerR, trees); err != nil || !isWorker || id != "B" {
			t.Errorf("tree B's connection: id %q, worker %v, err %v", id, isWorker, err)
		}
		if id, isWorker, err := SocketOwnerInTrees(leadL, leadR, trees); err != nil || isWorker || id != "" {
			t.Errorf("lead connection: id %q, worker %v, err %v", id, isWorker, err)
		}
		if id, isWorker, err := SocketOwnerInTrees(outL, outR, trees); !isWorker || id != "" || !errors.Is(err, ErrSocketOwnerUnknown) {
			t.Errorf("unplaceable connection: id %q, worker %v, err %v", id, isWorker, err)
		}
	}
	// Without an unreadable process in the trees, a socket outside them is the lead's even if it is unreadable.
	if id, isWorker, err := SocketOwnerInTrees(outL, outR, []*WorkerTree{wtB}); err != nil || isWorker || id != "" {
		t.Errorf("outsider without unreadable tree processes: id %q, worker %v, err %v", id, isWorker, err)
	}
}

func TestParseStatusUIDs(t *testing.T) {
	euid, fsuid, err := procUIDs(os.Getpid())
	if err != nil || euid != os.Geteuid() || fsuid != os.Geteuid() {
		t.Fatalf("own uids %d %d, %v", euid, fsuid, err)
	}
	euid, fsuid, err = parseStatusUIDs([]byte("Name:\tx\nUid:\t1000\t0\t0\t7\nGid:\t1\t1\t1\t1\n"))
	if err != nil || euid != 0 || fsuid != 7 {
		t.Fatalf("parse: %d %d, %v", euid, fsuid, err)
	}
	if _, _, err := parseStatusUIDs([]byte("Name:\tx\n")); err == nil {
		t.Fatal("missing Uid line should fail")
	}
}

func TestLockHolders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	lock, err := fsutil.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	holders, err := LockHolders(path)
	if err != nil || !slices.Contains(holders, os.Getpid()) {
		t.Fatalf("holders %v, %v", holders, err)
	}
	_ = lock.Unlock()
	if holders, err := LockHolders(path); err != nil || slices.Contains(holders, os.Getpid()) {
		t.Fatalf("after unlock: %v, %v", holders, err)
	}
	locks := "1: FLOCK  ADVISORY  WRITE 42 00:2d:777 0 EOF\n1: -> FLOCK  ADVISORY  WRITE 43 00:2d:777 0 EOF\n" +
		"2: OFDLCK ADVISORY  WRITE -1 00:2d:777 0 EOF\n3: POSIX  ADVISORY  WRITE 44 00:2d:778 0 EOF\n"
	if got := parseProcLocks([]byte(locks), 777); !slices.Equal(got, []int{42}) {
		t.Fatalf("parse: %v", got)
	}
}

func TestParseProcStat(t *testing.T) {
	p, err := readProcStat(os.Getpid())
	if err != nil || p.pid != os.Getpid() || p.ppid != os.Getppid() || p.start == 0 || p.zombie {
		t.Fatalf("own stat: %+v, %v", p, err)
	}
	line := "42 (a) (b c) Z 7 9 9 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 12345 0 0\n"
	p, err = parseProcStat([]byte(line))
	if err != nil || p != (procInfo{pid: 42, ppid: 7, pgid: 9, start: 12345, zombie: true}) {
		t.Fatalf("parse %q: %+v, %v", line, p, err)
	}
	for _, bad := range []string{"", "42 no name", "42 (x) S 1 2"} {
		if _, err := parseProcStat([]byte(bad)); err == nil {
			t.Errorf("parse %q should fail", bad)
		}
	}
}

func TestParseProcNetAddr(t *testing.T) {
	cases := map[string]string{
		"0100007F:1F90":                         "127.0.0.1:8080",
		"0000000000000000FFFF00000100007F:0050": "127.0.0.1:80",
		"00000000000000000000000001000000:01BB": "[::1]:443",
	}
	for in, want := range cases {
		got, err := parseProcNetAddr(in)
		if err != nil || got != netip.MustParseAddrPort(want) {
			t.Errorf("parse %s: %v, %v; want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0100007F", "XYZ:1", "0100:1", "0100007F:FFFFF"} {
		if _, err := parseProcNetAddr(bad); err == nil {
			t.Errorf("parse %q should fail", bad)
		}
	}
}
