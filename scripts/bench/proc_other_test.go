//go:build bench && !windows && !linux

package bench

import (
	"errors"
	"time"
)

// errNoProcs: process sampling is implemented for Windows and Linux only; on other systems the bench still records
// the daemon's goroutines, heap, database and API latency.
var errNoProcs = errors.New("process sampling is not implemented on this OS")

func listProcs() (map[int]procEntry, error) { return nil, errNoProcs }

func startTime(int) (time.Time, error) { return time.Time{}, errNoProcs }

func procStats(int) (procStat, error) { return procStat{}, errNoProcs }

func selfStats() (procStat, error) { return procStat{}, errNoProcs }

func machineMemory() (machineStat, error) { return machineStat{}, errNoProcs }

type procHandle struct{}

func openProc(int) (*procHandle, error) { return nil, errNoProcs }

func (*procHandle) stats() (procStat, error) { return procStat{}, errNoProcs }

func (*procHandle) close() {}

func osVersion() string { return "" }
