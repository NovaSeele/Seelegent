package platform

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// addrPorts returns the addresses of a connection accepted by this process.
func addrPorts(t *testing.T, c net.Conn) (local, remote netip.AddrPort) {
	t.Helper()
	l, err1 := netip.ParseAddrPort(c.LocalAddr().String())
	r, err2 := netip.ParseAddrPort(c.RemoteAddr().String())
	if err := errors.Join(err1, err2); err != nil {
		t.Fatal(err)
	}
	return l, r
}

// TestSocketOwnerInTrees: a connection made by a grandchild in a worker tree is a worker's; one made by this
// process (outside every tree) is the lead's.
func TestSocketOwnerInTrees(t *testing.T) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	pidFile := filepath.Join(t.TempDir(), "pids")
	tree, wt := startDescribedTree(t, helper("connect", pidFile, ln.Addr().String()))
	defer func() { _ = tree.Close() }()
	if wt.ID != "task-1" {
		t.Fatalf("tree id %q", wt.ID)
	}
	workerConn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workerConn.Close() }()
	pids := readPids(t, pidFile)
	dialer := readPids(t, pidFile+".child")[0]
	inTree, err := wt.PIDs()
	if err != nil {
		t.Fatalf("PIDs: %v", err)
	}
	if !slices.Contains(inTree, pids[0]) || !slices.Contains(inTree, dialer) || slices.Contains(inTree, os.Getpid()) {
		t.Fatalf("tree pids %v: want %v and %d, not %d", inTree, pids, dialer, os.Getpid())
	}

	other := &WorkerTree{ID: "other", RootPID: os.Getpid() + 1_000_000, Job: `Local\Seelegent-none`}
	local, remote := addrPorts(t, workerConn)
	id, isWorker, err := SocketOwnerInTrees(local, remote, []*WorkerTree{nil, other, wt})
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("socket owner lookup is not supported on " + runtime.GOOS)
	}
	if err != nil || !isWorker || id != "task-1" {
		t.Fatalf("worker connection: id %q, worker %v, err %v", id, isWorker, err)
	}

	leadConn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = leadConn.Close() }()
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = accepted.Close() }()
	local, remote = addrPorts(t, accepted)
	if id, isWorker, err := SocketOwnerInTrees(local, remote, []*WorkerTree{other, wt}); err != nil || isWorker || id != "" {
		t.Fatalf("lead connection: id %q, worker %v, err %v", id, isWorker, err)
	}
	if id, isWorker, err := SocketOwnerInTrees(local, remote, nil); err != nil || isWorker || id != "" {
		t.Fatalf("no trees: id %q, worker %v, err %v", id, isWorker, err)
	}
	// A client socket that does not exist (closed before the lookup) fails closed while workers run.
	gone := netip.AddrPortFrom(remote.Addr(), 1)
	if _, isWorker, err := SocketOwnerInTrees(local, gone, []*WorkerTree{wt}); !isWorker || !errors.Is(err, ErrSocketOwnerUnknown) {
		t.Fatalf("unknown socket: worker %v, err %v", isWorker, err)
	}

	if err := tree.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitDead(t, dialer)
	if left, err := wt.PIDs(); err != nil || len(left) != 0 {
		t.Fatalf("PIDs after Close: %v, %v", left, err)
	}
}

func TestWorkerTreeInfo(t *testing.T) {
	name, err := NewTreeName()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if !strings.HasPrefix(name, `Local\Seelegent-`) || len(name) != len(`Local\Seelegent-`)+32 {
			t.Fatalf("tree name %q", name)
		}
		if again, _ := NewTreeName(); again == name {
			t.Fatal("tree names must be random")
		}
	} else if name != "" {
		t.Fatalf("unix trees have no name, got %q", name)
	}
	if WorkerTreesExact() != (runtime.GOOS == "windows" || runtime.GOOS == "linux") {
		t.Fatalf("WorkerTreesExact = %v on %s", WorkerTreesExact(), runtime.GOOS)
	}
	start, err := ProcessStartTime(os.Getpid())
	if err != nil || start == 0 {
		t.Fatalf("ProcessStartTime: %d, %v", start, err)
	}
	if again, _ := ProcessStartTime(os.Getpid()); again != start {
		t.Fatalf("start time changed: %d then %d", start, again)
	}
	var nilTree *WorkerTree
	if _, err := nilTree.PIDs(); err == nil {
		t.Fatal("nil tree should fail")
	}
	if _, err := (&WorkerTree{ID: "empty"}).PIDs(); err == nil {
		t.Fatal("a tree without root or job should fail")
	}
}
