package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Restored is the outcome of one file of a restore.
type Restored struct {
	Path string `json:"path"`
	// Error says why the file was not restored ("" when it was).
	Error string `json:"error,omitempty"`
}

// backup records what a restore replaced, so it can be undone by hand and the sweep keeps the content.
type backup struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Files   []File `json:"files"`
	// Saved names every blob the backup keeps.
	Saved []string `json:"saved"`
}

// Restore writes back the start content of the given files of a turn (relative to the snapshot's root). The
// current content of each file is kept first in a backup manifest (t<turn>-restore-<unix>.json). A file that did
// not exist at the start, or whose content was not kept, is skipped with a reason.
func (s *Store) Restore(taskID string, turn int, files []string) ([]Restored, error) {
	start, err := s.Load(taskID, turn, KindStart)
	if err != nil {
		return nil, err
	}
	b := backup{Version: 1, Root: start.Root, Files: []File{}, Saved: []string{}}
	out := make([]Restored, 0, len(files))
	for _, rel := range files {
		f, ok := start.Find(rel)
		switch {
		case !ok:
			out = append(out, Restored{Path: rel, Error: "not in the snapshot"})
			continue
		case f.Missing:
			out = append(out, Restored{Path: f.Path, Error: "the file did not exist when the turn started"})
			continue
		}
		data, err := s.Content(f)
		if err != nil {
			out = append(out, Restored{Path: f.Path, Error: err.Error()})
			continue
		}
		full := filepath.Join(start.Root, filepath.FromSlash(f.Path))
		if ok, err := fsutil.Within(start.Root, full); err != nil || !ok {
			out = append(out, Restored{Path: f.Path, Error: "the path leaves the repository"})
			continue
		}
		cur, _, err := s.record(start.Root, f.Path, "")
		if err != nil {
			return out, err
		}
		if cur != nil {
			b.Files = append(b.Files, *cur)
			if cur.Stored {
				b.Saved = append(b.Saved, cur.Hash)
			}
			if !cur.Missing && !cur.Stored {
				out = append(out, Restored{Path: f.Path, Error: "the current file is too large to keep a backup of"})
				continue
			}
		}
		if err := writeBack(full, data, f.Link); err != nil {
			out = append(out, Restored{Path: f.Path, Error: err.Error()})
			continue
		}
		out = append(out, Restored{Path: f.Path})
	}
	if len(b.Files) > 0 {
		kind := "restore-" + strconv.FormatInt(s.now().UnixNano(), 10)
		p, err := s.manifestPath(taskID, turn, kind)
		if err != nil {
			return out, err
		}
		data, err := json.Marshal(b)
		if err != nil {
			return out, err
		}
		if err := fsutil.WriteFileAtomic(p, data, 0o600); err != nil {
			return out, fmt.Errorf("snapshot: restore backup: %w", err)
		}
	}
	return out, nil
}

// writeBack writes data to full (a symbolic link's target when link), keeping the mode of an existing file.
func writeBack(full string, data []byte, link bool) error {
	//nolint:gosec // a folder of the user's repository, made as git would make it
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if link {
		if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Symlink(string(data), full)
	}
	perm := fs.FileMode(0o644)
	if st, err := os.Lstat(full); err == nil {
		if st.Mode()&os.ModeSymlink != 0 || st.IsDir() {
			return fmt.Errorf("%s is now a link or a folder; restore it by hand", filepath.Base(full))
		}
		perm = st.Mode().Perm()
	}
	return fsutil.WriteFileAtomic(full, data, perm)
}
