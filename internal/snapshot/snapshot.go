// Package snapshot records the uncommitted state of a repository before and after each worker turn, so the task
// engine can tell when a turn lost work that was not the worker's (work at risk) and put it back (restore).
//
// A snapshot never touches the user's repository: no refs, no objects, no index, no stash (bugs.md D7, D25c). It
// lists the dirty files with `git status` (read-only, GIT_OPTIONAL_LOCKS=0) and copies their bytes into a
// content-addressed store in the data directory: blobs/<aa>/<sha256>, written once through fsutil.WriteFileAtomic.
// A manifest per task turn (tasks/<task-id>/t<turn>-<kind>.json) names the files, their hashes and the HEAD commit.
// Sweep removes the manifests of old tasks and every blob no manifest still names (F5 class).
package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// Limits of one snapshot.
const (
	// MaxFileBytes is the largest file whose content is kept; a larger one is recorded by size only.
	MaxFileBytes = 8 << 20
	// MaxFiles caps the files of one snapshot.
	MaxFiles = 3000
	// MaxTotalBytes caps the new content one snapshot copies.
	MaxTotalBytes = 256 << 20
)

// Manifest kinds.
const (
	KindStart = "start"
	KindEnd   = "end"
)

// ErrNotFound means there is no such snapshot.
var ErrNotFound = errors.New("snapshot not found")

// heavyDirs are folders whose untracked files are never copied (dependencies and caches that are not ignored).
var heavyDirs = map[string]bool{"node_modules": true, ".venv": true, "venv": true, "__pycache__": true,
	".next": true, ".nuxt": true, ".turbo": true, ".cache": true, ".gradle": true}

// Git is the git access of the store (gitx.Repos).
type Git interface {
	Dirty(ctx context.Context, root string) (head string, files []gitx.DirtyFile, err error)
	Blob(ctx context.Context, root, rev, rel string, limit int, filters bool) ([]byte, bool, error)
}

// Manifest is one snapshot of a repository's uncommitted files.
type Manifest struct {
	Version int       `json:"version"`
	Root    string    `json:"root"`
	Head    string    `json:"head,omitempty"`
	TakenAt time.Time `json:"takenAt"`
	Files   []File    `json:"files"`
	// Truncated is set when the dirty files went over MaxFiles or MaxTotalBytes; later files are missing.
	Truncated bool `json:"truncated,omitempty"`
}

// File is one file of a snapshot.
type File struct {
	// Path is relative to the repository root, with '/' separators.
	Path string `json:"path"`
	// Status is the git status when the file was dirty (api.Git*), "" for a clean file recorded on request.
	Status string `json:"status,omitempty"`
	// Hash is the SHA-256 of the content ("" for a missing file).
	Hash string `json:"hash,omitempty"`
	Size int64  `json:"size"`
	// Missing: the file did not exist.
	Missing bool `json:"missing,omitempty"`
	// Stored: the content is in the blob store (false for a file over MaxFileBytes).
	Stored bool `json:"stored,omitempty"`
	Binary bool `json:"binary,omitempty"`
	// Link: a symbolic link, whose content is its target.
	Link bool `json:"link,omitempty"`
}

// Find returns the entry of path (compared as Key) and whether there is one.
func (m Manifest) Find(p string) (File, bool) {
	k := Key(p)
	for _, f := range m.Files {
		if Key(f.Path) == k {
			return f, true
		}
	}
	return File{}, false
}

// Store keeps snapshots under one folder (config.Paths.Snapshots).
type Store struct {
	dir string
	git Git
	now func() time.Time
}

// New returns a store in dir.
func New(dir string, git Git, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{dir: dir, git: git, now: now}
}

// Git returns the git access of the store.
func (s *Store) Git() Git { return s.git }

// Take snapshots the repository at root: every dirty file, plus the files of extra (relative to root) whatever
// their state, so a file that was cleaned or reverted still shows its end state.
func (s *Store) Take(ctx context.Context, root string, extra []string) (Manifest, error) {
	head, dirty, err := s.git.Dirty(ctx, root)
	if err != nil {
		return Manifest{}, err
	}
	m := Manifest{Version: 1, Root: root, Head: head, TakenAt: s.now().UTC(), Files: []File{}}
	seen := map[string]bool{}
	var total int64
	add := func(rel, status string) error {
		k := Key(rel)
		if seen[k] || skipped(rel) {
			return nil
		}
		seen[k] = true
		if len(m.Files) >= MaxFiles || total > MaxTotalBytes {
			m.Truncated = true
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		f, n, err := s.record(root, rel, status)
		if err != nil {
			return err
		}
		total += n
		if f != nil {
			m.Files = append(m.Files, *f)
		}
		return nil
	}
	for _, d := range dirty {
		if err := add(d.Path, d.Status); err != nil {
			return Manifest{}, err
		}
		if d.OldPath != "" {
			if err := add(d.OldPath, api.GitDeleted); err != nil {
				return Manifest{}, err
			}
		}
	}
	for _, rel := range extra {
		if err := add(filepath.ToSlash(rel), ""); err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, nil
}

// skipped reports a path under a heavy folder.
func skipped(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if heavyDirs[part] {
			return true
		}
	}
	return false
}

// record reads one file and stores its content. It returns nil for something that is not a file (a folder, a
// submodule, a FIFO) and the number of new bytes written.
func (s *Store) record(root, rel, status string) (*File, int64, error) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	f := &File{Path: rel, Status: status}
	st, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Missing = true
		return f, 0, nil
	case err != nil:
		return nil, 0, nil
	case st.IsDir():
		return nil, 0, nil
	}
	f.Link = st.Mode()&os.ModeSymlink != 0
	f.Size = st.Size()
	if !f.Link && !st.Mode().IsRegular() {
		return nil, 0, nil
	}
	if f.Size > MaxFileBytes {
		return f, 0, nil
	}
	data, err := gitx.ReadWorktree(full, MaxFileBytes+1)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			f.Missing = true
			return f, 0, nil
		}
		return nil, 0, nil
	}
	if len(data) > MaxFileBytes {
		return f, 0, nil
	}
	f.Size = int64(len(data))
	f.Binary = gitx.IsBinary(data)
	f.Hash = Hash(data)
	n, err := s.put(f.Hash, data)
	if err != nil {
		return nil, 0, err
	}
	f.Stored = true
	return f, n, nil
}

// Hash is the content hash the store uses.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.dir, "blobs", hash[:2], hash)
}

// put stores data under its hash unless it is there already, and returns the bytes written.
func (s *Store) put(hash string, data []byte) (int64, error) {
	p := s.blobPath(hash)
	if _, err := os.Stat(p); err == nil {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	if err := fsutil.WriteFileAtomic(p, data, 0o600); err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	return int64(len(data)), nil
}

// Put stores content and returns its hash (restore keeps the content it replaces).
func (s *Store) Put(data []byte) (string, error) {
	h := Hash(data)
	_, err := s.put(h, data)
	return h, err
}

// Content returns the stored content of a file of a snapshot.
func (s *Store) Content(f File) ([]byte, error) {
	if !f.Stored || !hashRe.MatchString(f.Hash) {
		return nil, fmt.Errorf("%w: the content of %s was not kept", ErrNotFound, f.Path)
	}
	data, err := fsutil.ReadFile(s.blobPath(f.Hash))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: the content of %s is gone", ErrNotFound, f.Path)
	}
	if err != nil {
		return nil, err
	}
	if Hash(data) != f.Hash {
		return nil, fmt.Errorf("snapshot: the stored content of %s is damaged", f.Path)
	}
	return data, nil
}

// safeID accepts task ids and kinds as file names.
var safeID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

func (s *Store) manifestPath(taskID string, turn int, kind string) (string, error) {
	if !safeID.MatchString(taskID) || !safeID.MatchString(kind) || turn < 1 {
		return "", fmt.Errorf("snapshot: bad name %q/%d/%q", taskID, turn, kind)
	}
	return filepath.Join(s.dir, "tasks", taskID, "t"+strconv.Itoa(turn)+"-"+kind+".json"), nil
}

// Save writes a manifest.
func (s *Store) Save(taskID string, turn int, kind string, m Manifest) error {
	p, err := s.manifestPath(taskID, turn, kind)
	if err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	return fsutil.WriteFileAtomic(p, data, 0o600)
}

// Load reads a manifest (ErrNotFound when there is none). A manifest that cannot be read is an error.
func (s *Store) Load(taskID string, turn int, kind string) (Manifest, error) {
	p, err := s.manifestPath(taskID, turn, kind)
	if err != nil {
		return Manifest{}, err
	}
	data, err := fsutil.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return Manifest{}, ErrNotFound
	}
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("snapshot: %s: %w", p, err)
	}
	return m, nil
}

// Sweep deletes the snapshots of every task keep refuses, then every blob that no remaining manifest names. It
// returns the number of task folders removed.
func (s *Store) Sweep(ctx context.Context, keep func(taskID string) bool) (int, error) {
	tasksDir := filepath.Join(s.dir, "tasks")
	entries, err := os.ReadDir(tasksDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	removed := 0
	used := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(tasksDir, e.Name())
		if !keep(e.Name()) {
			if err := os.RemoveAll(dir); err != nil {
				return removed, err
			}
			removed++
			continue
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			return removed, err
		}
		for _, f := range files {
			data, err := fsutil.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				return removed, err
			}
			var m struct {
				Files []struct {
					Hash string `json:"hash"`
				} `json:"files"`
				Saved []string `json:"saved"`
			}
			if err := json.Unmarshal(data, &m); err != nil {
				// An unreadable manifest keeps every blob: never delete what may still be needed.
				return removed, fmt.Errorf("snapshot: sweep %s: %w", f.Name(), err)
			}
			for _, x := range m.Files {
				used[x.Hash] = true
			}
			for _, h := range m.Saved {
				used[h] = true
			}
		}
	}
	blobs := filepath.Join(s.dir, "blobs")
	err = filepath.WalkDir(blobs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || !hashRe.MatchString(d.Name()) || used[d.Name()] {
			return nil
		}
		return os.Remove(p) //nolint:gosec // a blob of the store's own folder, named by its hash
	})
	return removed, err
}

// Key is how two repository-relative paths are compared: '/' separators, and case-folded where the file system
// usually ignores case (Windows, macOS).
func Key(p string) string {
	p = path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}
