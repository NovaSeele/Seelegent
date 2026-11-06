package fsutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// ErrLocked is returned by TryLock when another holder (in this or another process) has the lock.
var ErrLocked = errors.New("file is locked by another holder")

// lockPollInterval is how often Lock retries while the lock is held elsewhere.
const lockPollInterval = 50 * time.Millisecond

// FileLock is an exclusive advisory lock on a lock file, held across processes. It is implemented with LockFileEx on
// Windows and flock on Unix, so the operating system releases it when the holder exits or crashes; a stale lock file
// left on disk never blocks the next holder. Two FileLocks on the same path exclude each other even inside one
// process.
//
// The lock file is never deleted: removing it would let a second holder lock a fresh file while the first still
// holds the old one.
type FileLock struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// TryLock takes the lock on path without waiting. It creates the file if needed (the parent directory must exist)
// and returns an error matching ErrLocked if another holder has it.
func TryLock(path string) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // lock path chosen by the caller
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return &FileLock{f: f, path: path}, nil
}

// Lock takes the lock on path, waiting until it is free or ctx is done. It polls instead of blocking in the
// operating system so that cancellation is always honored.
func Lock(ctx context.Context, path string) (*FileLock, error) {
	ticker := time.NewTicker(lockPollInterval)
	defer ticker.Stop()
	for {
		l, err := TryLock(path)
		if err == nil || !errors.Is(err, ErrLocked) {
			return l, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("lock %s: %w", path, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// Path returns the lock file path.
func (l *FileLock) Path() string { return l.path }

// File returns the open lock file, for example to write the holder's process id into it. The file stays owned by
// the lock; do not close it.
func (l *FileLock) File() *os.File { return l.f }

// Unlock releases the lock. It is safe to call more than once; later calls do nothing.
func (l *FileLock) Unlock() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	f := l.f
	l.f = nil
	uerr := unlockFile(f)
	cerr := f.Close()
	if err := errors.Join(uerr, cerr); err != nil {
		return fmt.Errorf("unlock %s: %w", l.path, err)
	}
	return nil
}
