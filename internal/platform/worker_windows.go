//go:build windows

package platform

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"slices"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobObjectQuery is the JOB_OBJECT_QUERY access right: enough to list a job's processes, not to change or end it.
const jobObjectQuery = 0x0004

// jobPIDList is the head of JOBOBJECT_BASIC_PROCESS_ID_LIST; the ids follow as ULONG_PTRs.
type jobPIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIDsInList  uint32
	ProcessIDList             [1]uintptr
}

// pids opens the named job for a moment and lists its processes.
func (w *WorkerTree) pids() ([]int, error) { return w.pidsWith(processSession) }

// pidsWith is pids with the session lookup passed in (tests). A job that does not exist holds no process: the
// launcher has not created it yet (its worker starts suspended and joins the job before it runs) or has closed it
// (kill-on-close, no breakaway). That holds only in the launcher's own session, though: Local\ names are per
// session, so when the launcher still runs in another session than this process (or its session cannot be read),
// the job may exist out of sight, and that is a JobUnavailableError rather than "no processes".
func (w *WorkerTree) pidsWith(sessionOf func(pid uint32) (uint32, error)) ([]int, error) {
	if w.Job == "" {
		return nil, fmt.Errorf("worker tree %q: no job name", w.ID)
	}
	name, err := windows.UTF16PtrFromString(w.Job)
	if err != nil {
		return nil, fmt.Errorf("worker tree %q: %w", w.ID, err)
	}
	r, _, callErr := procOpenJobObjectW.Call(jobObjectQuery, 0, uintptr(unsafe.Pointer(name)))
	job := windows.Handle(r)
	if job == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) {
			if w.rootRunning() && !w.sameSession(sessionOf) {
				return nil, &JobUnavailableError{Tree: w.ID, Job: w.Job, RootPID: w.RootPID, Err: callErr}
			}
			return nil, nil // not created yet, or closed: no process is in the tree
		}
		return nil, fmt.Errorf("worker tree %q: open job: %w", w.ID, callErr)
	}
	defer func() { _ = windows.CloseHandle(job) }()
	return jobPIDs(job)
}

// rootRunning reports whether the tree's root process still runs with its recorded start time.
func (w *WorkerTree) rootRunning() bool {
	if w.RootPID <= 0 || !processAlive(w.RootPID) {
		return false
	}
	start, err := procStart(w.RootPID)
	if err != nil {
		return true // it runs but cannot be checked: assume it is the root
	}
	return w.RootStart == 0 || start == w.RootStart
}

// sameSession reports whether the tree's root runs in this process's session; false when either cannot be read.
func (w *WorkerTree) sameSession(sessionOf func(pid uint32) (uint32, error)) bool {
	root, err1 := sessionOf(uint32(w.RootPID))  //nolint:gosec // RootPID > 0 checked by rootRunning
	own, err2 := sessionOf(uint32(os.Getpid())) //nolint:gosec // a pid is positive
	return err1 == nil && err2 == nil && root == own
}

// processSession returns the Terminal Services session of process pid.
func processSession(pid uint32) (uint32, error) {
	var s uint32
	if err := windows.ProcessIdToSessionId(pid, &s); err != nil {
		return 0, fmt.Errorf("session of process %d: %w", pid, err)
	}
	return s, nil
}

// jobPIDs lists the processes of job, growing the buffer while processes are added between calls.
func jobPIDs(job windows.Handle) ([]int, error) {
	const word = unsafe.Sizeof(uintptr(0))
	head := int(unsafe.Offsetof(jobPIDList{}.ProcessIDList) / word)
	capacity := 64
	for range 8 {
		buf := make([]uintptr, head+capacity)
		list := (*jobPIDList)(unsafe.Pointer(&buf[0]))
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(&buf[0])), uint32(uintptr(len(buf))*word), nil) //nolint:gosec // small buffer
		if err != nil && !errors.Is(err, windows.ERROR_MORE_DATA) {
			return nil, fmt.Errorf("list job processes: %w", err)
		}
		if err == nil && list.NumberOfProcessIDsInList >= list.NumberOfAssignedProcesses {
			ids := buf[head : head+int(list.NumberOfProcessIDsInList)]
			out := make([]int, len(ids))
			for i, id := range ids {
				out[i] = int(id)
			}
			return out, nil
		}
		capacity = max(capacity*2, int(list.NumberOfAssignedProcesses)+16)
	}
	return nil, errors.New("list job processes: the job keeps growing")
}

// procStart returns the creation time of process pid as a FILETIME.
func procStart(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("process start time: invalid pid %d", pid)
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // pid > 0
	if err != nil {
		return 0, fmt.Errorf("process %d: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, fmt.Errorf("process %d: %w", pid, err)
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}

// Functions and constants of the IP helper API that golang.org/x/sys/windows does not wrap.
var (
	modiphlpapi             = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTCPTable = modiphlpapi.NewProc("GetExtendedTcpTable")
)

const (
	// tcpTableOwnerPIDConnections is TCP_TABLE_OWNER_PID_CONNECTIONS: connected sockets with their owner.
	tcpTableOwnerPIDConnections = 4
	// tcpRowSize and tcp6RowSize are the sizes of MIB_TCPROW_OWNER_PID and MIB_TCP6ROW_OWNER_PID.
	tcpRowSize  = 24
	tcp6RowSize = 56
)

// lookupOptions lets tests step into a socket owner lookup.
type lookupOptions struct {
	// beforeRecheck runs after the first look found no worker, before that answer is checked again.
	beforeRecheck func()
	// sessionOf replaces the session lookup of WorkerTree.PIDs (nil: ProcessIdToSessionId).
	sessionOf func(pid uint32) (uint32, error)
}

// socketOwnerInTrees finds the owner of the client socket in the TCP tables and looks for it in the trees' jobs
// (see SocketOwnerInTrees for the answers).
func socketOwnerInTrees(local, remote netip.AddrPort, trees []*WorkerTree, opt lookupOptions) (id string, isWorker bool, err error) {
	owner, err := clientSocketOwner(local, remote)
	if err != nil {
		return "", true, err
	}
	if owner == os.Getpid() {
		return "", false, nil
	}
	sessionOf := opt.sessionOf
	if sessionOf == nil {
		sessionOf = processSession
	}
	var treeErrs []error
	for _, w := range trees {
		pids, err := w.pidsWith(sessionOf)
		if err != nil {
			treeErrs = append(treeErrs, err)
			continue
		}
		if slices.Contains(pids, owner) {
			return w.ID, true, nil
		}
	}
	if len(treeErrs) > 0 {
		return "", true, errors.Join(treeErrs...)
	}
	// Check the "not in any job" answer again: an owner that exited before the jobs were listed has left them.
	if opt.beforeRecheck != nil {
		opt.beforeRecheck()
	}
	again, err := clientSocketOwner(local, remote)
	if err != nil || again != owner {
		return "", true, fmt.Errorf("%w: the client socket changed while it was looked up", ErrSocketOwnerUnknown)
	}
	return "", false, nil
}

// clientSocketOwner returns the process that owns the TCP socket bound to remote and connected to local. A
// socket that is gone or has no owner any more (pid 0) gives ErrSocketOwnerUnknown.
func clientSocketOwner(local, remote netip.AddrPort) (int, error) {
	var readErr error
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		table, err := tcpTable(family)
		if err != nil {
			readErr = err
			continue
		}
		if pid := findOwner(table, family, remote, local); pid != 0 {
			return pid, nil
		}
	}
	if readErr != nil {
		return 0, fmt.Errorf("%w: %w", ErrSocketOwnerUnknown, readErr)
	}
	return 0, ErrSocketOwnerUnknown
}

// tcpTable returns the raw MIB_TCPTABLE_OWNER_PID or MIB_TCP6TABLE_OWNER_PID of family.
func tcpTable(family uint32) ([]byte, error) {
	size := uint32(16 << 10)
	for range 8 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(family), tcpTableOwnerPIDConnections, 0)
		switch windows.Errno(r) { // the function returns a Win32 error code
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10 // connections may be added before the next call
		default:
			return nil, fmt.Errorf("read tcp table: %w", windows.Errno(r))
		}
	}
	return nil, errors.New("read tcp table: the table keeps growing")
}

// findOwner returns the owner pid of the row of table whose local address is from and remote address is to, or 0.
// Ports are stored in network byte order in the low 16 bits of a DWORD, addresses as raw bytes.
func findOwner(table []byte, family uint32, from, to netip.AddrPort) int {
	if len(table) < 4 {
		return 0
	}
	n := int(binary.LittleEndian.Uint32(table))
	rows := table[4:]
	rowSize := tcpRowSize
	if family == windows.AF_INET6 {
		rowSize = tcp6RowSize
	}
	port := func(b []byte) uint16 { return binary.BigEndian.Uint16(b) }
	for i := 0; i < n && (i+1)*rowSize <= len(rows); i++ {
		row := rows[i*rowSize : (i+1)*rowSize]
		var l, r netip.AddrPort
		var pid uint32
		if family == windows.AF_INET {
			// dwState, dwLocalAddr, dwLocalPort, dwRemoteAddr, dwRemotePort, dwOwningPid
			l = netip.AddrPortFrom(netip.AddrFrom4([4]byte(row[4:8])), port(row[8:10]))
			r = netip.AddrPortFrom(netip.AddrFrom4([4]byte(row[12:16])), port(row[16:18]))
			pid = binary.LittleEndian.Uint32(row[20:24])
		} else {
			// ucLocalAddr[16], dwLocalScopeId, dwLocalPort, ucRemoteAddr[16], dwRemoteScopeId, dwRemotePort,
			// dwState, dwOwningPid
			l = netip.AddrPortFrom(netip.AddrFrom16([16]byte(row[0:16])).Unmap(), port(row[20:22]))
			r = netip.AddrPortFrom(netip.AddrFrom16([16]byte(row[24:40])).Unmap(), port(row[44:46]))
			pid = binary.LittleEndian.Uint32(row[52:56])
		}
		if l == from && r == to && pid != 0 {
			return int(pid)
		}
	}
	return 0
}
