package platform

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// recheckRounds bounds how often a "lead" answer is checked again while new processes keep appearing in the trees.
const recheckRounds = 4

// lookupOptions lets tests step into a socket owner lookup.
type lookupOptions struct {
	// beforeRecheck runs after the first look found no worker, before that answer is checked again.
	beforeRecheck func()
}

// tcpSocket is a client socket found in /proc/net/tcp{,6}.
type tcpSocket struct {
	inode uint64
	uid   int // the user that created the socket
}

// socketOwnerInTrees finds the inode of the client socket in /proc/net/tcp{,6}, then looks for it among the open
// files of the processes in the trees only (see SocketOwnerInTrees for the answers).
func socketOwnerInTrees(local, remote netip.AddrPort, trees []*WorkerTree, opt lookupOptions) (id string, isWorker bool, err error) {
	sock, err := clientSocket(local, remote)
	if err != nil {
		return "", true, err
	}
	s := treeScan{link: "socket:[" + strconv.FormatUint(sock.inode, 10) + "]", scanned: make(map[int]bool)}
	for round := 0; ; round++ {
		if round > 0 {
			// Check the "not in any tree" answer again: the same socket still exists, and processes that joined
			// the trees since the last look (a fork that took the socket along) are scanned too.
			again, err := clientSocket(local, remote)
			if err != nil || again.inode != sock.inode {
				return "", true, fmt.Errorf("%w: the client socket changed while it was looked up", ErrSocketOwnerUnknown)
			}
		}
		procs, err := listProcs()
		if err != nil {
			return "", true, fmt.Errorf("socket owner: %w", err)
		}
		s.procs = procs
		id, found, fresh := s.scanTrees(trees)
		if found {
			return id, true, nil
		}
		if round > 0 && fresh == 0 {
			break
		}
		if round == recheckRounds {
			return "", true, fmt.Errorf("%w: the worker trees keep changing", ErrSocketOwnerUnknown)
		}
		if round == 0 && opt.beforeRecheck != nil {
			opt.beforeRecheck()
		}
	}
	if len(s.treeErrs) > 0 {
		return "", true, errors.Join(s.treeErrs...)
	}
	return s.classifyUnreadable(sock)
}

// treeScan looks for one socket among the open files of the processes in the trees.
type treeScan struct {
	link       string
	procs      []procInfo
	scanned    map[int]bool // processes already looked at
	inTree     map[int]bool // processes that belong to a tree (latest walk)
	unreadable []int        // tree processes whose open files could not be read
	treeErrs   []error      // trees that cannot be walked at all
}

// scanTrees walks every tree and scans the processes not scanned before. It returns the ID of the first tree with
// a readable process that holds the socket, and how many processes were scanned for the first time.
func (s *treeScan) scanTrees(trees []*WorkerTree) (id string, found bool, fresh int) {
	s.inTree = make(map[int]bool)
	s.treeErrs = nil
	for _, w := range trees {
		if w.RootPID <= 0 {
			s.treeErrs = append(s.treeErrs, fmt.Errorf("worker tree %q: no root process", w.ID))
			continue
		}
		for _, p := range treeOf(s.procs, w.RootPID, w.RootStart) {
			s.inTree[p.pid] = true
			if s.scanned[p.pid] || p.zombie { // a zombie has no open files
				continue
			}
			s.scanned[p.pid] = true
			fresh++
			has, err := hasOpenFile(p.pid, s.link)
			if err != nil {
				s.unreadable = append(s.unreadable, p.pid)
				continue
			}
			if has && !found {
				id, found = w.ID, true
			}
		}
	}
	return id, found, fresh
}

// classifyUnreadable decides when no readable tree process holds the socket. Unreadable tree processes that run
// as another user than the socket's cannot have created it and are ignored. If any are left, the socket must be
// found in a readable process outside the trees (positive evidence for the lead); only processes of the socket's
// user are looked at there. Otherwise the caller may be one of them and counts as a worker of no particular task.
func (s *treeScan) classifyUnreadable(sock tcpSocket) (id string, isWorker bool, err error) {
	var suspects []int
	for _, pid := range s.unreadable {
		if ok, err := mayOwn(pid, sock.uid); errors.Is(err, fs.ErrNotExist) || (err == nil && !ok) {
			continue // gone, or another user's
		}
		suspects = append(suspects, pid)
	}
	if len(suspects) == 0 {
		return "", false, nil
	}
	for _, p := range s.procs {
		if s.inTree[p.pid] || p.zombie {
			continue
		}
		if ok, err := mayOwn(p.pid, sock.uid); err != nil || !ok {
			continue
		}
		if has, err := hasOpenFile(p.pid, s.link); err == nil && has {
			return "", false, nil
		}
	}
	return "", true, fmt.Errorf("%w: cannot read the open files of worker tree processes %v", ErrSocketOwnerUnknown, suspects)
}

// mayOwn reports whether process pid runs as uid (effective or file-system id), the user a socket was created by.
func mayOwn(pid, uid int) (bool, error) {
	euid, fsuid, err := procUIDs(pid)
	if err != nil {
		return false, err
	}
	return euid == uid || fsuid == uid, nil
}

// procUIDs returns the effective and file-system user ids of pid from /proc/<pid>/status, which stays readable
// for non-dumpable processes.
func procUIDs(pid int) (euid, fsuid int, err error) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
	if err != nil {
		return 0, 0, err
	}
	return parseStatusUIDs(data)
}

// parseStatusUIDs reads the "Uid:" line of a /proc/<pid>/status file: real, effective, saved and file-system id.
func parseStatusUIDs(data []byte) (euid, fsuid int, err error) {
	for line := range strings.SplitSeq(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "Uid:")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 4 {
			break
		}
		euid, err1 := strconv.Atoi(f[1])
		fsuid, err2 := strconv.Atoi(f[3])
		if err := errors.Join(err1, err2); err != nil {
			return 0, 0, fmt.Errorf("malformed Uid line: %w", err)
		}
		return euid, fsuid, nil
	}
	return 0, 0, errors.New("no Uid line")
}

// clientSocket returns the TCP socket bound to remote and connected to local. A socket that has been closed
// (inode 0) or is not found gives ErrSocketOwnerUnknown.
func clientSocket(local, remote netip.AddrPort) (tcpSocket, error) {
	var readErr error
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		sock, err := findSocket(path, remote, local)
		if errors.Is(err, fs.ErrNotExist) {
			continue // no IPv6 on this system
		}
		if err != nil {
			readErr = err
			continue
		}
		if sock.inode != 0 {
			return sock, nil
		}
	}
	if readErr != nil {
		return tcpSocket{}, fmt.Errorf("%w: %w", ErrSocketOwnerUnknown, readErr)
	}
	return tcpSocket{}, ErrSocketOwnerUnknown
}

// findSocket returns the socket in the /proc/net/tcp-format file path whose local address is from and whose
// remote address is to; its inode is 0 when there is none (or it was closed).
func findSocket(path string, from, to netip.AddrPort) (tcpSocket, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return tcpSocket{}, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Scan() // header
	for sc.Scan() {
		// sl local_address rem_address st tx_queue:rx_queue tr:tm->when retrnsmt uid timeout inode ...
		f := strings.Fields(sc.Text())
		if len(f) < 10 {
			continue
		}
		l, err1 := parseProcNetAddr(f[1])
		r, err2 := parseProcNetAddr(f[2])
		if err1 != nil || err2 != nil || l != from || r != to {
			continue
		}
		inode, err1 := strconv.ParseUint(f[9], 10, 64)
		uid, err2 := strconv.Atoi(f[7])
		if err1 == nil && err2 == nil && inode != 0 {
			return tcpSocket{inode: inode, uid: uid}, nil
		}
	}
	if err := sc.Err(); err != nil {
		return tcpSocket{}, fmt.Errorf("read %s: %w", path, err)
	}
	return tcpSocket{}, nil
}

// parseProcNetAddr parses "0100007F:1F90" (IPv4) or a 32-digit IPv6 address and port. The kernel prints the
// address as 32-bit words in host byte order, the port as a plain number.
func parseProcNetAddr(s string) (netip.AddrPort, error) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, errors.New("malformed address")
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, errors.New("malformed address")
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return netip.AddrPort{}, errors.New("malformed port")
	}
	b := make([]byte, len(raw))
	for i := 0; i < len(raw); i += 4 {
		binary.NativeEndian.PutUint32(b[i:], binary.BigEndian.Uint32(raw[i:]))
	}
	addr, _ := netip.AddrFromSlice(b)
	return netip.AddrPortFrom(addr.Unmap(), uint16(port)), nil
}

// hasOpenFile reports whether process pid has a file descriptor whose link is link. A process that has exited
// has none; any other failure to read is an error.
func hasOpenFile(pid int, link string) (bool, error) {
	dir := "/proc/" + strconv.Itoa(pid) + "/fd"
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read open files of process %d: %w", pid, err)
	}
	for _, e := range entries {
		target, err := os.Readlink(dir + "/" + e.Name())
		if errors.Is(err, fs.ErrNotExist) {
			continue // closed meanwhile
		}
		if err != nil {
			return false, fmt.Errorf("read open files of process %d: %w", pid, err)
		}
		if target == link {
			return true, nil
		}
	}
	return false, nil
}
