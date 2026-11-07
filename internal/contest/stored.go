package contest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// A variant's changes outlive its worktree in Seelegent's own data folder, never in the user's repository: the
// commits a pick or a cleanup leaves there are referenced by nothing (no branch, no tag, no ref of ours), so
// `git gc` may prune them, and nothing reads them once the worktree is gone. Before a worktree is removed its diff
// against the base (cut at maxDiff, like every contest diff) and its line counts are written to
// <data>/contests/<contest id>/<label>.json; the scoreboard and the variant diff read that file from then on. The
// winner's applied patch is kept beside it (<label>.patch).

// recordVersion is the layout of a stored variant.
const recordVersion = 1

// variantRecord is what the views need of a variant whose worktree is gone.
type variantRecord struct {
	Version   int      `json:"version"`
	Contest   string   `json:"contest"`
	Label     string   `json:"label"`
	Base      string   `json:"base"`
	Diff      string   `json:"diff"`
	Truncated bool     `json:"truncated"`
	Files     []string `json:"files"`
	Added     int      `json:"added"`
	Removed   int      `json:"removed"`
	// SHA256 is the hash of Diff, so a damaged file is told apart from a variant without changes.
	SHA256 string `json:"sha256"`
	At     string `json:"at"`
}

// Why a variant's stored changes cannot be shown (DataError.Reason, the API's details.reason).
const (
	// DataGone: the worktree is gone and nothing was stored (a contest cleaned up before the store existed, or a
	// worktree folder deleted by hand).
	DataGone = "variant_data_gone"
	// DataDamaged: the stored file cannot be read back as it was written.
	DataDamaged = "variant_data_damaged"
)

// DataError says that a removed variant's changes cannot be shown. A gone record is not found; a damaged one is a
// bad state. Never an empty diff that would read as "no changes".
type DataError struct {
	Contest string
	Label   string
	Reason  string
	Detail  string
}

func (e *DataError) Error() string {
	if e.Reason == DataGone {
		return fmt.Sprintf("the changes of %s in contest %s are no longer available: its worktree was removed "+
			"without a stored copy", e.Label, e.Contest)
	}
	return fmt.Sprintf("the stored changes of %s in contest %s are damaged: %s", e.Label, e.Contest, e.Detail)
}

// Is maps the error to the package's kinds.
func (e *DataError) Is(target error) bool {
	if e.Reason == DataGone {
		return target == ErrNotFound
	}
	return target == ErrBadState
}

// APIDetails are the details of the error for the dashboard (reason, label).
func (e *DataError) APIDetails() map[string]any {
	return map[string]any{"reason": e.Reason, "label": e.Label}
}

func (s *Service) contestDir(id string) string { return filepath.Join(s.o.Dir, id) }

func (s *Service) recordPath(id, label string) string {
	return filepath.Join(s.contestDir(id), label+".json")
}

// storeVariant writes the variant's changes against the base, from its worktree as it is now, to the data folder,
// once: a valid stored copy is never replaced. Cleanup only runs once the variant's task has stopped, so the first
// copy is the final content; a retry after a `git worktree remove` that stopped partway would only see a
// half-deleted tree. A worktree git no longer knows as one (its .git went first) is never read.
func (s *Service) storeVariant(ctx context.Context, c *api.Contest, v *api.ContestVariant) error {
	if _, err := s.storedVariant(c.ID, v.Label); err == nil {
		return nil
	}
	if !s.trees.Working(ctx, v.Worktree) {
		return errors.New("the worktree is no longer a git worktree and no copy of its changes was stored")
	}
	tmp := filepath.Join(s.o.Dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	tree, err := s.o.Git.SnapshotTree(ctx, v.Worktree, tmp, linked(v.Env)...)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	text, truncated, err := s.gitDiff(ctx, c, tree, maxDiff)
	if err != nil {
		return fmt.Errorf("diff: %w", err)
	}
	stat, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff", "--numstat",
		"--no-renames", c.BaseSha, tree)
	if err != nil {
		return fmt.Errorf("numstat: %w", err)
	}
	files, added, removed := parseNumstat(string(stat))
	sum := sha256.Sum256([]byte(text))
	rec := variantRecord{Version: recordVersion, Contest: c.ID, Label: v.Label, Base: c.BaseSha, Diff: text,
		Truncated: truncated, Files: files[:min(len(files), maxFileList)], Added: added, Removed: removed,
		SHA256: hex.EncodeToString(sum[:]), At: s.now()}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.contestDir(c.ID), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.recordPath(c.ID, v.Label), data, 0o600)
}

// storedVariant reads a removed variant's changes back; a missing or damaged file is a DataError.
func (s *Service) storedVariant(id, label string) (variantRecord, error) {
	data, err := os.ReadFile(s.recordPath(id, label))
	if errors.Is(err, os.ErrNotExist) {
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataGone}
	}
	if err != nil {
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataDamaged, Detail: err.Error()}
	}
	var rec variantRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataDamaged, Detail: err.Error()}
	}
	sum := sha256.Sum256([]byte(rec.Diff))
	switch {
	case rec.Version != recordVersion:
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataDamaged,
			Detail: fmt.Sprintf("unknown layout %d", rec.Version)}
	case rec.Contest != id || rec.Label != label:
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataDamaged,
			Detail: "the file belongs to " + rec.Contest + " " + rec.Label}
	case rec.SHA256 != hex.EncodeToString(sum[:]):
		return variantRecord{}, &DataError{Contest: id, Label: label, Reason: DataDamaged,
			Detail: "the diff does not match its checksum"}
	}
	if rec.Files == nil {
		rec.Files = []string{}
	}
	return rec, nil
}

// stored reads the variant's stored copy for the views. has is false when there is none; err is set when there is
// one that cannot be read back (DataDamaged). A stored copy always wins over the worktree: once it exists the
// worktree may be half removed.
func (s *Service) stored(c *api.Contest, v *api.ContestVariant) (rec variantRecord, has bool, err error) {
	rec, err = s.storedVariant(c.ID, v.Label)
	var de *DataError
	if errors.As(err, &de) && de.Reason == DataGone {
		return variantRecord{}, false, nil
	}
	return rec, true, err
}

// cutDiff cuts a diff to at most limit bytes, at the end of a line when one fits (else at a character boundary),
// so a cut never splits a UTF-8 character or leaves half a line.
func cutDiff(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := text[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i >= 0 {
		return cut[:i+1]
	}
	i := limit
	for i > 0 && !utf8.RuneStart(text[i]) {
		i--
	}
	return text[:i]
}

// live reports whether the variant's worktree is still there to be read with git.
func live(v *api.ContestVariant) bool {
	if v.Removed || v.Worktree == "" {
		return false
	}
	_, err := os.Stat(v.Worktree)
	return err == nil
}
