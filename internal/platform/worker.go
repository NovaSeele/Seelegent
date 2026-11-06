package platform

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

// ErrSocketOwnerUnknown is returned by SocketOwnerInTrees, together with isWorker = true, when worker trees are
// live but the process owning the client end of the connection cannot be found: the client may have sent its
// request and closed the socket already, which must not let a worker pass as the lead.
var ErrSocketOwnerUnknown = errors.New("cannot find the process that owns the client socket")

// JobUnavailableError is returned (possibly joined with others, use errors.As) when a Windows worker tree's job
// cannot be opened while its launcher still runs in another session than the caller (or a session that cannot be
// read): Local\ names are per session, so the job may exist out of sight. The tree then fails closed. Callers log
// it once per launch; doctor reports it. In the caller's own session a missing job is an empty tree (not created
// yet, or already closed).
type JobUnavailableError struct {
	Tree    string // WorkerTree.ID
	Job     string // the job name
	RootPID int    // the launcher that still runs
	Err     error  // why the job could not be opened
}

func (e *JobUnavailableError) Error() string {
	return fmt.Sprintf("worker tree %q: job %s cannot be opened while its launcher %d runs (another session?): %v",
		e.Tree, e.Job, e.RootPID, e.Err)
}

func (e *JobUnavailableError) Unwrap() error { return e.Err }

// WorkerTree identifies the process tree of one live worker so that any process of the same user (the daemon,
// also after a restart) can list the processes in it. The launcher records the fields (see Tree.WorkerTree); the
// daemon rebuilds the value from that record.
type WorkerTree struct {
	// ID is what SocketOwnerInTrees reports for a connection made from this tree (the task or launch id).
	ID string `json:"id"`
	// RootPID is the process whose descendants make the tree on Unix (the launcher); the tree includes it.
	RootPID int `json:"rootPid,omitempty"`
	// RootStart is RootPID's start time as ProcessStartTime reports it. A process with the same id and another
	// start time is a different process, and the tree is then gone. Zero skips the check.
	RootStart uint64 `json:"rootStart,omitempty"`
	// Job is the name of the tree's Windows Job Object (see NewTreeName). Windows lists the job's processes.
	Job string `json:"job,omitempty"`
}

// PIDs returns the ids of the processes in the tree: on Windows every process in the job, on Unix RootPID and its
// descendants by parent id, each checked against its start time. A tree that has ended returns no ids and no
// error. On macOS and the BSDs descendants that re-parented away from the tree are missed (WorkerTreesExact).
func (w *WorkerTree) PIDs() ([]int, error) {
	if w == nil {
		return nil, ErrNotStarted
	}
	return w.pids()
}

// WorkerTreesExact reports whether worker trees hold every descendant of a worker: true on Windows (job without
// breakaway) and Linux (child subreaper); false on macOS and the BSDs, where a descendant that daemonises leaves
// the tree (best effort; `seelegent doctor` says so).
func WorkerTreesExact() bool { return treesExact }

// NewTreeName returns a fresh random name for StartWorkerTree: `Local\Seelegent-<32 hex digits>` on Windows. Unix
// trees have no name and get "".
func NewTreeName() (string, error) { return newTreeName() }

// ProcessStartTime returns an opaque value that tells two processes with the same id apart: the start time of
// process pid (Linux: clock ticks since boot; macOS: microseconds since the epoch; Windows: the creation FILETIME).
func ProcessStartTime(pid int) (uint64, error) { return procStart(pid) }

// LockHolders returns the ids of the processes holding a file lock (fsutil.Lock) on the file at path, from
// /proc/locks on Linux. Elsewhere it returns errors.ErrUnsupported. The daemon uses it to check that a launcher
// record names the process that really holds the launch lock.
func LockHolders(path string) ([]int, error) { return lockHolders(path) }

// SocketOwnerInTrees finds out whether a TCP connection accepted by this process was made from a worker tree.
// local and remote are the connection's addresses as this (server) process sees them (net.Conn LocalAddr and
// RemoteAddr); the client end is the socket bound to remote and connected to local.
//
// It looks the other way round from a process scan: Windows finds the client socket's owner process in the TCP
// table and checks whether a tree's job holds it; Linux finds the socket's inode in /proc/net/tcp{,6} and scans
// the open files of the processes in the trees only. It returns the matching tree's ID and isWorker = true, or
// isWorker = false when no tree holds the client.
//
// Every tree is looked at before an answer is given: a match in a readable process of a tree gives that tree's ID
// (never the ID of another tree whose processes could not be read). It fails closed only where it matters, and
// then with isWorker = true, an empty ID (no task's allow-list) and an error:
//
//   - the client socket is gone or has no owner any more (ErrSocketOwnerUnknown): a client that sent its request
//     and closed the socket must not pass as the lead;
//   - a tree cannot be listed at all (Windows: its job cannot be opened while its launcher runs in another
//     session, a JobUnavailableError);
//   - Linux: some processes of a tree cannot be read (a non-dumpable program such as ssh-agent), they run as the
//     socket's user, and the socket was not found in a readable process of the same user outside the trees either.
//     Unreadable tree processes of another user are ignored, and finding the socket outside the trees is positive
//     evidence for the lead.
//
// A "lead" answer is checked again before it is returned, because processes and sockets change while they are
// read: the client socket must still exist (Windows: with the same owner; Linux: with the same inode) and processes
// new in the trees are scanned as well (Linux, a bounded number of rounds). Otherwise the answer is
// ErrSocketOwnerUnknown. Failures to read processes outside the trees never make a caller a worker. With no trees
// it returns at once. On macOS and the BSDs it returns errors.ErrUnsupported (callers then treat everyone as the
// lead).
func SocketOwnerInTrees(local, remote netip.AddrPort, trees []*WorkerTree) (id string, isWorker bool, err error) {
	return socketOwner(local, remote, trees, lookupOptions{})
}

func socketOwner(local, remote netip.AddrPort, trees []*WorkerTree, opt lookupOptions) (id string, isWorker bool, err error) {
	trees = slices.DeleteFunc(slices.Clone(trees), func(w *WorkerTree) bool { return w == nil })
	if len(trees) == 0 {
		return "", false, nil
	}
	return socketOwnerInTrees(normalize(local), normalize(remote), trees, opt)
}

// normalize unmaps IPv4-mapped IPv6 addresses and drops zones, so that addresses from every source compare equal.
func normalize(ap netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port())
}
