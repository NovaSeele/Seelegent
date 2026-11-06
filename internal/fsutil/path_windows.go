//go:build windows

package fsutil

import (
	"strings"

	"golang.org/x/sys/windows"
)

// Flags of GetFinalPathNameByHandle (both zero) that golang.org/x/sys/windows does not name.
const (
	fileNameNormalized = 0x0
	volumeNameDOS      = 0x0
)

// resolveExisting returns the final path of an existing file or directory: links and junctions resolved, short
// names expanded and the on-disk letter case restored. It falls back to GetLongPathName, and then to p unchanged,
// on volumes that cannot report a final path.
func resolveExisting(p string) string {
	if final, err := finalPath(p); err == nil {
		return final
	}
	if long, err := longPath(p); err == nil {
		return long
	}
	return p
}

// finalPath asks the file system for the normalised path of p through an open handle.
func finalPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(name, 0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = windows.CloseHandle(h) }()

	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), //nolint:gosec // small buffer
			fileNameNormalized|volumeNameDOS)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return stripExtendedPrefix(windows.UTF16ToString(buf[:n])), nil
		}
		buf = make([]uint16, n)
	}
}

// longPath expands 8.3 short names in p with GetLongPathName.
func longPath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetLongPathName(name, &buf[0], uint32(len(buf))) //nolint:gosec // small buffer
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}

// stripExtendedPrefix turns \\?\C:\x into C:\x and \\?\UNC\server\share into \\server\share.
func stripExtendedPrefix(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}
