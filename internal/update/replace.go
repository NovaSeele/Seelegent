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

// swap is one replacement of a file next to the installed executable (the executable or its companion launcher)
// that can be undone.
type swap struct {
	exe string
	// src is the file that was put in place (it is moved back there on undo); "" when exe was only moved away.
	src string
	// aside is where the replaced file went (the previous file, or a trash name); "" when there was none.
	aside string
	// keep is the previous file whose earlier content went to keptTrash ("" when there was none).
	keep, keptTrash string
}

// replaceExe puts src in place of exe. The replaced file is moved to keep (its earlier content, if any, goes to the
// trash first), or to the trash when keep is "". Every step is a rename in exe's folder, so a running executable can
// be replaced on Windows. On failure everything is put back as it was and exe is untouched.
//
// For the companion launcher either side may be missing: when exe does not exist, src is only renamed into place
// (and an earlier keep still goes to the trash: it belonged to a build that is no longer the previous one); when
// src is "", exe is only moved away (to keep, or the trash), so no companion of another build stays next to the
// executable.
func replaceExe(exe, src, keep string) (*swap, error) {
	s := &swap{exe: exe, src: src, keep: keep}
	if keep != "" {
		if _, err := os.Lstat(keep); err == nil {
			s.keptTrash = trashName(exe)
			if err := fsutil.Rename(keep, s.keptTrash); err != nil {
				return nil, fmt.Errorf("move the older kept %s aside: %w", filepath.Base(exe), err)
			}
		}
	}
	if _, err := os.Lstat(exe); err == nil {
		s.aside = keep
		if s.aside == "" {
			s.aside = trashName(exe)
		}
		if err := fsutil.Rename(exe, s.aside); err != nil {
			s.restoreKept()
			return nil, fmt.Errorf("move %s aside: %w; nothing was changed", filepath.Base(exe), err)
		}
	}
	if src == "" {
		return s, nil
	}
	if err := fsutil.Rename(src, exe); err != nil {
		if s.aside != "" {
			if rerr := fsutil.Rename(s.aside, exe); rerr != nil {
				return nil, fmt.Errorf("put the new %s in place: %w; and putting the old one back failed too: %w "+
					"(rename %s to %s by hand)", filepath.Base(exe), err, rerr, s.aside, exe)
			}
		}
		s.restoreKept()
		return nil, fmt.Errorf("put the new %s in place: %w; nothing was changed", filepath.Base(exe), err)
	}
	return s, nil
}

// undo puts the replaced file back (the one that was put in place goes back to src).
func (s *swap) undo() error {
	if s.src != "" {
		if err := fsutil.Rename(s.exe, s.src); err != nil {
			return fmt.Errorf("move %s aside again: %w", filepath.Base(s.exe), err)
		}
	}
	if s.aside != "" {
		if err := fsutil.Rename(s.aside, s.exe); err != nil {
			return fmt.Errorf("put %s back: %w (rename %s to %s by hand)", filepath.Base(s.exe), err, s.aside, s.exe)
		}
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

// pair is the replacement of the installed executable (the embedded swap) and of its companion launcher, done and
// undone together, so the companion next to the executable is always the one of its release (or none).
type pair struct {
	*swap
	companion *swap
	// companionErr is what went wrong putting the companion back in an undo (the executable is back then; the
	// daemon runs launches as `seelegent __launch` while the companion is not of its build).
	companionErr error
}

// replacePair puts exeSrc in place of exe and companionSrc ("" for none) in place of the companion, keeping the
// replaced ones as their previous files when keep is true (else they go to the trash). The companion goes first:
// when the executable cannot be replaced, the companion is put back and nothing was changed.
func replacePair(exe, exeSrc, companion, companionSrc string, keep bool) (*pair, error) {
	keepExe, keepCompanion := "", ""
	if keep {
		keepExe, keepCompanion = PreviousPath(exe), PreviousPath(companion)
	}
	c, err := replaceExe(companion, companionSrc, keepCompanion)
	if err != nil {
		return nil, fmt.Errorf("the companion launcher: %w", err)
	}
	m, err := replaceExe(exe, exeSrc, keepExe)
	if err != nil {
		if uerr := c.undo(); uerr != nil {
			return nil, fmt.Errorf("%w; the companion launcher could not be put back: %w", err, uerr)
		}
		return nil, err
	}
	return &pair{swap: m, companion: c}, nil
}

// undo puts the replaced executable back, then the replaced companion. An error is about the executable; the
// companion's goes to companionErr (it only costs memory: launches run as `seelegent __launch` meanwhile).
func (p *pair) undo() error {
	if err := p.swap.undo(); err != nil {
		return err
	}
	p.companionErr = p.companion.undo()
	return nil
}

// trashName is a free name next to exe for a file that is to be deleted.
func trashName(exe string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return exe + oldInfix + hex.EncodeToString(b[:])
}

// sweep deletes leftovers next to exe (the executable or its companion launcher): trash files that are no longer
// running and an incoming file of an update that was interrupted. Files that cannot be deleted yet (a launcher that
// still runs a task's worker keeps its file on Windows) stay for the next update or rollback.
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
