package platform

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// lockHolders reads /proc/locks: "1: FLOCK  ADVISORY  WRITE 1234 08:01:5678 0 EOF" (pid, then
// major:minor:inode). Waiting requests ("1: -> FLOCK ...") and OFD locks (pid -1) are skipped.
func lockHolders(path string) ([]int, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return nil, fmt.Errorf("lock holders of %s: %w", path, err)
	}
	data, err := os.ReadFile("/proc/locks")
	if err != nil {
		return nil, fmt.Errorf("lock holders of %s: %w", path, err)
	}
	return parseProcLocks(data, st.Ino), nil
}

// parseProcLocks returns the pids that hold a lock on inode ino.
func parseProcLocks(data []byte, ino uint64) []int {
	var out []int
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || f[1] == "->" {
			continue
		}
		pid, err1 := strconv.Atoi(f[4])
		dev := f[5]
		i := strings.LastIndexByte(dev, ':')
		if err1 != nil || pid <= 0 || i < 0 {
			continue
		}
		if n, err := strconv.ParseUint(dev[i+1:], 10, 64); err == nil && n == ino {
			out = append(out, pid)
		}
	}
	return out
}
