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

// socketOwnerInTrees finds the inode of the client socket in /proc/net/tcp{,6}, then looks for it among the open
// files of the processes in the trees only.
func socketOwnerInTrees(local, remote netip.AddrPort, trees []*WorkerTree) (id string, isWorker bool, err error) {
	inode, err := clientSocketInode(local, remote)
	if err != nil {
		return "", true, err
	}
	link := "socket:[" + strconv.FormatUint(inode, 10) + "]"
	procs, err := listProcs()
	if err != nil {
		return "", true, fmt.Errorf("socket owner: %w", err)
	}
	for _, w := range trees {
		if w.RootPID <= 0 {
			return w.ID, true, fmt.Errorf("worker tree %q: no root process", w.ID)
		}
		for _, p := range treeOf(procs, w.RootPID, w.RootStart) {
			if p.zombie {
				continue // a zombie has no open files
			}
			has, err := hasOpenFile(p.pid, link)
			if err != nil {
				return w.ID, true, fmt.Errorf("worker tree %q: %w", w.ID, err)
			}
			if has {
				return w.ID, true, nil
			}
		}
	}
	return "", false, nil
}

// clientSocketInode returns the inode of the TCP socket bound to remote and connected to local. A socket that has
// been closed (inode 0) or is not found gives ErrSocketOwnerUnknown.
func clientSocketInode(local, remote netip.AddrPort) (uint64, error) {
	var readErr error
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		inode, err := findSocketInode(path, remote, local)
		if errors.Is(err, fs.ErrNotExist) {
			continue // no IPv6 on this system
		}
		if err != nil {
			readErr = err
			continue
		}
		if inode != 0 {
			return inode, nil
		}
	}
	if readErr != nil {
		return 0, fmt.Errorf("%w: %w", ErrSocketOwnerUnknown, readErr)
	}
	return 0, ErrSocketOwnerUnknown
}

// findSocketInode returns the inode of the socket in the /proc/net/tcp-format file path whose local address is
// from and whose remote address is to, or 0.
func findSocketInode(path string, from, to netip.AddrPort) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
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
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err == nil && inode != 0 {
			return inode, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	return 0, nil
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
