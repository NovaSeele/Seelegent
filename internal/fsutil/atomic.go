// Package fsutil holds small file system helpers shared by the rest of Seelegent: atomic file writes, reads that
// ride out transient Windows sharing errors, cross-process file locks and path normalisation for comparisons and
// keys.
package fsutil

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Retry policy for operations that can fail for a moment on Windows while an antivirus scanner, the search indexer
// or a log viewer holds the file open. The total wait stays well under a second.
const (
	retryAttempts = 8
	retryFirst    = 10 * time.Millisecond
	retryMax      = 200 * time.Millisecond
)

// WriteFileAtomic replaces the file at path with data so that readers see either the old or the new content, never a
// mix or a truncated file, even if the process or the machine crashes half-way.
//
// The data goes to a temporary file in the same directory, which is flushed to disk and then renamed over path. On
// Windows the rename replaces an existing file; a rename that fails because another process briefly holds the file
// is retried for a short time. On Unix the directory is synced as well so the rename itself survives a crash.
// perm is applied to the new file (on Windows only the read-only bit is meaningful). If path is a symbolic link,
// the link itself is replaced by a regular file.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: create temp file: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()

	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("write %s: sync: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write %s: close: %w", path, err)
	}
	if err = os.Chmod(tmp, perm); err != nil {
		return fmt.Errorf("write %s: chmod: %w", path, err)
	}
	if err = retry(func() error { return os.Rename(tmp, path) }); err != nil {
		return fmt.Errorf("write %s: replace: %w", path, err)
	}
	if err = syncDir(dir); err != nil {
		return fmt.Errorf("write %s: sync directory: %w", path, err)
	}
	return nil
}

// ReadFile reads the whole file at path like os.ReadFile, but retries for a short time when the read fails because
// another process briefly holds the file (Windows sharing violations). A missing file is reported at once with an
// error that matches fs.ErrNotExist, so callers can tell "missing" from "unreadable".
func ReadFile(path string) ([]byte, error) {
	var data []byte
	err := retry(func() error {
		var err error
		data, err = os.ReadFile(path)
		return err
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

// retry runs op until it succeeds, fails with an error that is not transient, or the retry budget is spent.
func retry(op func() error) error {
	return retryIf(op, isTransient)
}

// retryIf is retry with the transient-error test passed in, so tests can exercise the loop on every OS.
func retryIf(op func() error, transient func(error) bool) error {
	delay := retryFirst
	var err error
	for attempt := 0; attempt < retryAttempts; attempt++ {
		if err = op(); err == nil || !transient(err) {
			return err
		}
		if attempt < retryAttempts-1 {
			time.Sleep(delay)
			delay = min(delay*2, retryMax)
		}
	}
	return err
}
