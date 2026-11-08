//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// listProcesses walks a Toolhelp snapshot of the processes and reads the command line of each one match accepts.
func listProcesses(ctx context.Context, match func(string) bool) ([]Process, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []Process
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := windows.UTF16ToString(e.ExeFile[:])
		if match != nil && !match(name) {
			continue
		}
		p := Process{PID: int(e.ProcessID), Name: name}
		if line, lerr := commandLine(e.ProcessID); lerr == nil {
			p.CommandLine = line
			if args, derr := windows.DecomposeCommandLine(line); derr == nil {
				p.Args = args
			}
		}
		out = append(out, p)
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	return out, nil
}

// commandLine reads the command line of process pid (ProcessCommandLineInformation, Windows 8.1 and later). It
// needs only PROCESS_QUERY_LIMITED_INFORMATION, which every process of the same user grants.
func commandLine(pid uint32) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()
	size := uint32(1024)
	for range 4 {
		// A []uint64 keeps the UNICODE_STRING header at the start of the buffer aligned.
		size = (size + 7) &^ 7
		buf := make([]uint64, size/8)
		var n uint32
		err := windows.NtQueryInformationProcess(h, windows.ProcessCommandLineInformation,
			unsafe.Pointer(&buf[0]), size, &n)
		if err == nil {
			return (*windows.NTUnicodeString)(unsafe.Pointer(&buf[0])).String(), nil
		}
		if !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) && !errors.Is(err, windows.STATUS_BUFFER_TOO_SMALL) &&
			!errors.Is(err, windows.STATUS_BUFFER_OVERFLOW) {
			return "", err
		}
		if n <= size {
			n = size * 2
		}
		size = n
	}
	return "", fmt.Errorf("the command line of process %d keeps growing", pid)
}
