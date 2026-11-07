package task

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// errChangedSinceTurn is the message of a file a restore skipped because it changed after the turn ended.
const errChangedSinceTurn = "changed since the turn ended"

// restoreFiles runs one restore of turn tu under the repository's git lock. A file whose content differs from the
// turn's end state (someone changed it after the turn) is skipped unless force. The current content of the other
// files is kept in a backup snapshot (t<turn>-restore-<time>, returned as backup), then the snapshot store writes
// them back from the turn's start snapshot.
func (s *Service) restoreFiles(ctx context.Context, t *Task, tu *Turn, files []string, force bool) (
	out []snapshot.Restored, skipped []api.RestoreFailed, backup string, err error) {
	root := tu.Snap.Root
	unlock, err := s.o.Repos.Lock(ctx, root)
	if err != nil {
		return nil, nil, "", err
	}
	defer unlock()
	end, hasEnd, err := s.turnEndState(t, tu)
	if err != nil {
		return nil, nil, "", err
	}
	keep := snapshot.Manifest{Version: 1, Root: root, Head: end.Head, TakenAt: s.clk.Now().UTC(),
		Files: []snapshot.File{}}
	write := make([]string, 0, len(files))
	for _, rel := range files {
		cur, data := currentFile(root, rel)
		if hasEnd && !force {
			same, err := s.sameAsEnd(ctx, end, cur)
			if err != nil {
				return nil, nil, "", err
			}
			if !same {
				skipped = append(skipped, api.RestoreFailed{Path: rel, Error: errChangedSinceTurn,
					Code: api.RestoreChangedSinceTurn})
				continue
			}
		}
		if data != nil {
			if _, err := s.o.Snapshots.Put(data); err != nil {
				return nil, nil, "", err
			}
		}
		keep.Files = append(keep.Files, cur)
		write = append(write, rel)
	}
	if len(write) == 0 {
		return nil, skipped, "", nil
	}
	kind := "restore-" + strconv.FormatInt(s.clk.Now().UnixNano(), 10)
	if err := s.o.Snapshots.Save(t.ID, tu.N, kind, keep); err != nil {
		return nil, nil, "", err
	}
	out, err = s.o.Snapshots.Restore(t.ID, tu.N, write)
	return out, skipped, fmt.Sprintf("t%d-%s", tu.N, kind), err
}

// turnEndState returns the worktree as the turn left it: its end snapshot, or the next turn's start snapshot when
// the turn has none. ok is false when neither exists (nothing to compare with).
func (s *Service) turnEndState(t *Task, tu *Turn) (m snapshot.Manifest, ok bool, err error) {
	switch next := t.TurnN(tu.N + 1); {
	case tu.Snap.Checked:
		m, err = s.o.Snapshots.Load(t.ID, tu.N, snapshot.KindEnd)
	case next != nil && next.Snap != nil:
		m, err = s.o.Snapshots.Load(t.ID, tu.N+1, snapshot.KindStart)
	default:
		return snapshot.Manifest{}, false, nil
	}
	if errors.Is(err, snapshot.ErrNotFound) {
		return snapshot.Manifest{}, false, nil
	}
	return m, err == nil, err
}

// sameAsEnd reports whether a file still holds what it held when the turn ended. A file the end snapshot does
// not list was clean then, so it is compared with HEAD at that time.
func (s *Service) sameAsEnd(ctx context.Context, end snapshot.Manifest, cur snapshot.File) (bool, error) {
	if r, ok := end.Find(cur.Path); ok {
		switch {
		case r.Missing || cur.Missing:
			return r.Missing == cur.Missing, nil
		case r.Hash == "" || cur.Hash == "":
			// Too large to hash (or not a regular file now): the size is all there is to compare.
			return r.Hash == cur.Hash && r.Size == cur.Size, nil
		}
		return r.Hash == cur.Hash, nil
	}
	if end.Head == "" {
		return cur.Missing, nil
	}
	data, ok, err := s.o.Repos.Blob(ctx, end.Root, end.Head, cur.Path, snapshot.MaxFileBytes+1, true)
	if err != nil {
		return false, err
	}
	if !ok {
		return cur.Missing, nil
	}
	return !cur.Missing && cur.Hash == snapshot.Hash(data), nil
}

// currentFile describes a working-tree file as a snapshot entry, with its content when the store can keep it (nil
// for a missing, too large or special file).
func currentFile(root, rel string) (f snapshot.File, data []byte) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	f = snapshot.File{Path: rel}
	st, err := os.Lstat(full)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Missing = true
		return f, nil
	case err != nil:
		return f, nil
	}
	f.Link = st.Mode()&os.ModeSymlink != 0
	f.Size = st.Size()
	if !f.Link && !st.Mode().IsRegular() || f.Size > snapshot.MaxFileBytes {
		return f, nil
	}
	data, err = gitx.ReadWorktree(full, snapshot.MaxFileBytes+1)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		f.Missing = true
		return f, nil
	case err != nil || len(data) > snapshot.MaxFileBytes:
		return f, nil
	}
	f.Size = int64(len(data))
	f.Binary = gitx.IsBinary(data)
	f.Hash = snapshot.Hash(data)
	f.Stored = true
	return f, data
}
