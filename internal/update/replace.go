package update

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// File names next to the installed executable.
const (
	// PreviousSuffix marks the executable an update replaced, kept for --rollback: seelegent.exe.previous on
	// Windows, seelegent.previous elsewhere. PATHEXT does not list it, so Windows never runs it by its short name.
	PreviousSuffix = ".previous"
	// newSuffix is the incoming executable while it is put in place.
	newSuffix = ".new"
	// oldInfix marks executables that are no longer used but could not be deleted yet (Windows keeps a running
	// executable, for example an MCP server a lead host started, from being deleted; renaming it is allowed).
	oldInfix = ".old-"
)

// PreviousPath is where the executable an update replaced is kept.
func PreviousPath(exe string) string { return exe + PreviousSuffix }

// swap is one replacement of the installed executable that can be undone.
type swap struct {
	exe string
	// src is the file that was put in place (it is moved back there on undo).
	src string
	// aside is where the replaced executable went (the previous file, or a trash name).
	aside string
	// keep is the previous file whose earlier content went to keptTrash ("" when there was none).
	keep, keptTrash string
}

// replaceExe puts src in place of exe. The replaced executable is moved to keep (its earlier content, if any, goes
// to the trash first), or to the trash when keep is "". Every step is a rename in exe's folder, so a running
// executable can be replaced on Windows. On failure everything is put back as it was and exe is untouched.
func replaceExe(exe, src, keep string) (*swap, error) {
	s := &swap{exe: exe, src: src, keep: keep}
	if keep != "" {
		if _, err := os.Lstat(keep); err == nil {
			s.keptTrash = trashName(exe)
			if err := fsutil.Rename(keep, s.keptTrash); err != nil {
				return nil, fmt.Errorf("move the older kept executable aside: %w", err)
			}
		}
		s.aside = keep
	} else {
		s.aside = trashName(exe)
	}
	if err := fsutil.Rename(exe, s.aside); err != nil {
		s.restoreKept()
		return nil, fmt.Errorf("move %s aside: %w; nothing was changed", filepath.Base(exe), err)
	}
	if err := fsutil.Rename(src, exe); err != nil {
		if rerr := fsutil.Rename(s.aside, exe); rerr != nil {
			return nil, fmt.Errorf("put the new executable in place: %w; and putting %s back failed too: %w "+
				"(rename %s to %s by hand)", err, filepath.Base(exe), rerr, s.aside, exe)
		}
		s.restoreKept()
		return nil, fmt.Errorf("put the new executable in place: %w; nothing was changed", err)
	}
	return s, nil
}

// undo puts the replaced executable back (the one that was put in place goes back to src).
func (s *swap) undo() error {
	if err := fsutil.Rename(s.exe, s.src); err != nil {
		return fmt.Errorf("move %s aside again: %w", filepath.Base(s.exe), err)
	}
	if err := fsutil.Rename(s.aside, s.exe); err != nil {
		return fmt.Errorf("put %s back: %w (rename %s to %s by hand)", filepath.Base(s.exe), err, s.aside, s.exe)
	}
	s.restoreKept()
	return nil
}

func (s *swap) restoreKept() {
	if s.keptTrash != "" {
		_ = fsutil.Rename(s.keptTrash, s.keep)
		s.keptTrash = ""
	}
}

// trashName is a free name next to exe for a file that is to be deleted.
func trashName(exe string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return exe + oldInfix + hex.EncodeToString(b[:])
}

// sweep deletes leftovers next to exe: trash files that are no longer running and an incoming file of an update
// that was interrupted. Files that cannot be deleted yet stay for the next run.
func sweep(exe string) {
	dir, base := filepath.Split(exe)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, base+oldInfix) || n == base+newSuffix {
			if err := os.Remove(filepath.Join(dir, n)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				continue // still running (Windows): try again next time
			}
		}
	}
}
