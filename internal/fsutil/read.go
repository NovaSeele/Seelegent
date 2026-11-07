package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNotRegular reports a path that is not a regular file: a folder, a symbolic link, a FIFO, a socket or a device.
var ErrNotRegular = errors.New("not a regular file")

// OpenRegular opens the regular file at path for reading. It never follows a symbolic link and never blocks: on
// Unix the file is opened with O_NONBLOCK and O_NOFOLLOW, so a FIFO without a writer cannot hang the caller (a plain
// open waits for a writer forever, and no context can stop it). Anything but a regular file is ErrNotRegular, also
// when the path was swapped between the checks.
func OpenRegular(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotRegular, path, st.Mode().Type())
	}
	f, err := openNoBlock(path)
	if err != nil {
		if isSymlinkLoop(err) {
			return nil, fmt.Errorf("%w: %s (a symbolic link)", ErrNotRegular, path)
		}
		return nil, err
	}
	st, err = f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%w: %s (%s)", ErrNotRegular, path, st.Mode().Type())
	}
	return f, nil
}

// ReadRegular reads at most limit (> 0) bytes of the regular file at path, with OpenRegular's guarantees.
// truncated reports that the file has more.
func ReadRegular(path string, limit int64) (data []byte, truncated bool, err error) {
	if limit <= 0 {
		return nil, false, fmt.Errorf("fsutil: read limit %d must be positive", limit)
	}
	f, err := OpenRegular(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}
