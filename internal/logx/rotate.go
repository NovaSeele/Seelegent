package logx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"sync"
)

// RotatingFile is an append-only log file that rotates by size: when a write would push it past maxBytes, name is
// renamed to name.1 (name.1 to name.2, and so on), copies beyond keep are deleted, and a fresh file is started.
// Each Write lands whole in one file, so a log line is never split across files. It is safe for concurrent use.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
}

// OpenRotatingFile opens (creating if needed) the log file at path for appending. maxBytes must be positive; keep
// is the number of rotated copies to keep (0 keeps none). The directory must exist.
func OpenRotatingFile(path string, maxBytes int64, keep int) (*RotatingFile, error) {
	if maxBytes <= 0 || keep < 0 {
		return nil, fmt.Errorf("open log %s: invalid rotation limits (max %d bytes, keep %d)", path, maxBytes, keep)
	}
	r := &RotatingFile{path: path, maxBytes: maxBytes, keep: keep}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

// open opens the current file for appending and records its size.
func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", r.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("open log %s: %w", r.path, err)
	}
	r.f, r.size = f, info.Size()
	return nil
}

// Write appends p, rotating first if p would not fit. If rotation fails (on Windows, another process may hold the
// file open without delete sharing), the write still goes to the current file so no log line is lost, and rotation
// is tried again on the next write. Renames are not retried here: a failed attempt costs one system call, not a
// stalled logger.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		if err := r.rotate(); err != nil && r.f == nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// rotate shifts the copies and starts a new file. On failure the current file stays open when possible.
func (r *RotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		r.f = nil
		return errors.Join(err, r.open())
	}
	r.f = nil
	shiftErr := r.shift()
	if openErr := r.open(); openErr != nil {
		return errors.Join(shiftErr, openErr)
	}
	return shiftErr
}

// shift renames name.(i) to name.(i+1) for the kept copies and name to name.1. With keep 0 it deletes name.
func (r *RotatingFile) shift() error {
	if r.keep == 0 {
		if err := os.Remove(r.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.Remove(r.copyName(r.keep)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for i := r.keep - 1; i >= 1; i-- {
		if err := os.Rename(r.copyName(i), r.copyName(i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return os.Rename(r.path, r.copyName(1))
}

// copyName returns the name of the i-th rotated copy.
func (r *RotatingFile) copyName(i int) string { return r.path + "." + strconv.Itoa(i) }

// Close closes the file. Later writes fail with os.ErrClosed.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
