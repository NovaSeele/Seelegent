package task

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// Outside-diff limits (old outside-diff.js).
const (
	maxOutsideFiles     = 20
	maxOutsideFileLines = 400
	maxOutsideLines     = 2000
)

// errNoSnapshots answers the safety endpoints of an engine without snapshots.
func errNoSnapshots() error {
	return fmt.Errorf("%w: the engine keeps no worktree snapshots", ErrUnavailable)
}

// Restore writes back the content files had when turn n of task id started (default: the turn's work-at-risk
// files not restored yet). The current content of each file is kept in a backup snapshot first. It is refused
// while the task is active or taken over, and runs under the repository's git lock.
func (s *Service) Restore(ctx context.Context, id string, n int, req api.RestoreRequest) (api.RestoreResult, error) {
	if s.o.Snapshots == nil {
		return api.RestoreResult{}, errNoSnapshots()
	}
	res := api.RestoreResult{Restored: []string{}, Skipped: []api.RestoreFailed{}}
	_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		if Active(t.Status) || t.Manual {
			return nil, badStatef("task %s is %s; restore after its turn ended", id, t.Status)
		}
		tu := t.TurnN(n)
		if tu == nil {
			return nil, fmt.Errorf("%w: turn %d of task %s", ErrNotFound, n, id)
		}
		if tu.Snap == nil {
			return nil, fmt.Errorf("%w: turn %d of task %s has no snapshot", ErrNotFound, n, id)
		}
		files := make([]string, 0, len(req.Files))
		for _, f := range req.Files {
			rel, err := gitx.CleanPath(tu.Snap.Root, f)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
			files = append(files, rel)
		}
		if len(files) == 0 {
			for _, r := range tu.WorkAtRisk {
				if !r.Restored {
					files = append(files, r.Path)
				}
			}
		}
		if len(files) == 0 {
			return nil, invalidf("turn %d of task %s has no files at risk left to restore; name the files", n, id)
		}
		unlock, err := s.o.Repos.Lock(ctx, tu.Snap.Root)
		if err != nil {
			return nil, err
		}
		out, err := s.o.Snapshots.Restore(id, n, files)
		unlock()
		if err != nil {
			if errors.Is(err, snapshot.ErrNotFound) {
				return nil, fmt.Errorf("%w: the snapshot of turn %d of task %s is gone", ErrNotFound, n, id)
			}
			return nil, err
		}
		done := map[string]bool{}
		for _, r := range out {
			if r.Error != "" {
				res.Skipped = append(res.Skipped, api.RestoreFailed{Path: r.Path, Error: r.Error})
				continue
			}
			res.Restored = append(res.Restored, r.Path)
			done[snapshot.Key(r.Path)] = true
		}
		changed := false
		for i := range tu.WorkAtRisk {
			if r := &tu.WorkAtRisk[i]; !r.Restored && done[snapshot.Key(r.Path)] {
				r.Restored, changed = true, true
			}
		}
		if !changed {
			return nil, errNoChange
		}
		return nil, nil
	})
	if err != nil {
		return api.RestoreResult{}, err
	}
	s.log.Info("task: restored files", "task", id, "turn", n, "restored", len(res.Restored),
		"skipped", len(res.Skipped))
	return res, nil
}

// OutsideDiff returns the changes of turn n of task id that its edit tools did not make (a script, a shell
// redirect, a git command), without the files other tasks changed meanwhile: the start snapshot against the end
// one, the next turn's start, or (for a turn without either) the worktree now.
func (s *Service) OutsideDiff(ctx context.Context, id string, n int) ([]api.FileDiff, error) {
	if s.o.Snapshots == nil {
		return nil, errNoSnapshots()
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	tu := t.TurnN(n)
	if tu == nil {
		return nil, fmt.Errorf("%w: turn %d of task %s", ErrNotFound, n, id)
	}
	if tu.Snap == nil {
		return nil, fmt.Errorf("%w: turn %d of task %s has no snapshot", ErrNotFound, n, id)
	}
	root := tu.Snap.Root
	start, err := s.o.Snapshots.Load(id, n, snapshot.KindStart)
	if err != nil {
		return nil, s.snapErr(err, id, n)
	}
	var after snapshot.Manifest
	next := t.TurnN(n + 1)
	switch {
	case tu.Snap.Checked:
		after, err = s.o.Snapshots.Load(id, n, snapshot.KindEnd)
	case next != nil && next.Snap != nil:
		after, err = s.o.Snapshots.Load(id, n+1, snapshot.KindStart)
	default:
		extra := turnFiles(t, tu, s.live(id))
		for _, f := range start.Files {
			extra = append(extra, f.Path)
		}
		after, err = s.o.Snapshots.Take(ctx, root, extra)
	}
	if err != nil {
		return nil, s.snapErr(err, id, n)
	}
	skip := map[string]bool{}
	for _, rel := range turnFiles(t, tu, s.live(id)) {
		skip[snapshot.Key(rel)] = true
	}
	end := tu.EndedAt
	if end.IsZero() {
		end = s.clk.Now()
	}
	for k := range s.othersDuring(ctx, t, tu, end) {
		skip[k] = true
	}
	out := []api.FileDiff{}
	total := 0
	for _, a := range after.Files {
		if len(out) >= maxOutsideFiles || total >= maxOutsideLines {
			break
		}
		if skip[snapshot.Key(a.Path)] || snapshot.Scratch(a.Path) {
			continue
		}
		d, ok, err := s.outsideFile(ctx, start, a)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		total += capDiff(&d, min(maxOutsideFileLines, maxOutsideLines-total))
		out = append(out, d)
	}
	return out, nil
}

// snapErr maps a missing snapshot to not_found.
func (s *Service) snapErr(err error, id string, n int) error {
	if errors.Is(err, snapshot.ErrNotFound) {
		return fmt.Errorf("%w: the snapshot of turn %d of task %s is gone", ErrNotFound, n, id)
	}
	return err
}

// outsideFile diffs one file of the later snapshot against its start state (its start snapshot entry, or HEAD at
// the start when it was clean then); ok is false when it did not change.
func (s *Service) outsideFile(ctx context.Context, start snapshot.Manifest, a snapshot.File) (api.FileDiff, bool,
	error) {
	b, inStart := start.Find(a.Path)
	var before []byte
	beforeMissing := false
	switch {
	case inStart:
		if b.Hash == a.Hash && b.Missing == a.Missing && b.Size == a.Size {
			return api.FileDiff{}, false, nil
		}
		beforeMissing = b.Missing
		if !b.Missing {
			if !b.Stored {
				return api.FileDiff{Path: a.Path, Binary: b.Binary || a.Binary, Truncated: true,
					Hunks: []api.DiffHunk{}}, true, nil
			}
			data, err := s.o.Snapshots.Content(b)
			if err != nil {
				return api.FileDiff{}, false, err
			}
			before = data
		}
	case a.Status == "":
		return api.FileDiff{}, false, nil // clean at both ends
	default:
		data, ok, err := s.o.Repos.Blob(ctx, start.Root, start.Head, a.Path, snapshot.MaxFileBytes, false)
		if err != nil {
			return api.FileDiff{}, false, err
		}
		before, beforeMissing = data, !ok
		if ok && !a.Missing && a.Stored && snapshot.Hash(data) == a.Hash {
			return api.FileDiff{}, false, nil
		}
	}
	var after []byte
	if !a.Missing {
		if !a.Stored {
			return api.FileDiff{Path: a.Path, Binary: a.Binary, Truncated: true, Hunks: []api.DiffHunk{}}, true, nil
		}
		data, err := s.o.Snapshots.Content(a)
		if err != nil {
			return api.FileDiff{}, false, err
		}
		after = data
	}
	if beforeMissing && a.Missing {
		return api.FileDiff{}, false, nil
	}
	d, err := s.o.Repos.DiffContent(ctx, a.Path, before, after, beforeMissing, a.Missing)
	if err != nil {
		return api.FileDiff{}, false, err
	}
	if len(d.Hunks) == 0 && !d.Binary && bytes.Equal(normEOL(before), normEOL(after)) && !beforeMissing &&
		!a.Missing {
		return api.FileDiff{}, false, nil // only line endings changed, and git saw nothing
	}
	return d, true, nil
}

func normEOL(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }

// capDiff keeps at most limit diff lines of d (marking it truncated) and returns how many it kept.
func capDiff(d *api.FileDiff, limit int) int {
	kept := 0
	for i := range d.Hunks {
		h := &d.Hunks[i]
		if kept+len(h.Lines) > limit {
			h.Lines = h.Lines[:max(0, limit-kept)]
			d.Hunks = d.Hunks[:i+1]
			d.Truncated = true
			return kept + len(h.Lines)
		}
		kept += len(h.Lines)
	}
	return kept
}

// shellQuote quotes a word for a POSIX shell: a word of plain characters stays as it is, anything else goes in
// single quotes with each embedded quote written as '\”.
func shellQuote(s string) string {
	plain := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:@+,", r)
	}
	if s != "" && strings.IndexFunc(s, func(r rune) bool { return !plain(r) }) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// restoreCommand returns the CLI command that restores files of a turn, as an argument list and as a line to
// show (bugs.md D15: every path is one argument; the shown line quotes each word for a POSIX shell, and a path
// is always passed as --file=<path>, so a leading '-' is never read as an option).
func restoreCommand(id string, turn int, files []string) (argv []string, line string) {
	argv = []string{"seelegent", "restore", id, "--turn", strconv.Itoa(turn)}
	for _, f := range files {
		argv = append(argv, "--file="+f)
	}
	words := make([]string, len(argv))
	for i, a := range argv {
		words[i] = shellQuote(a)
	}
	return argv, strings.Join(words, " ")
}

// riskReport is the work_at_risk field of a report: the latest turn with files not restored yet.
func riskReport(t *Task) *api.ReportRisk {
	for i := len(t.Turns) - 1; i >= 0; i-- {
		tu := t.Turns[i]
		var files []string
		var details []api.ReportRiskFile
		for _, r := range tu.WorkAtRisk {
			if r.Restored {
				continue
			}
			files = append(files, r.Path)
			details = append(details, api.ReportRiskFile{Path: r.Path, Why: r.Why, LostLines: r.LostLines,
				Deleted: r.Deleted})
		}
		if len(files) == 0 {
			continue
		}
		sort.Strings(files)
		argv, line := restoreCommand(t.ID, tu.N, files)
		return &api.ReportRisk{Turn: tu.N, Snapshot: snapshotRef(t.ID, tu.N), Files: files, Details: details,
			Restore: line, RestoreArgv: argv}
	}
	return nil
}

// findingsReport converts findings for a report.
func findingsReport(fs []api.Finding) []api.ReportFinding {
	var out []api.ReportFinding
	for _, f := range fs {
		out = append(out, api.ReportFinding(f))
	}
	return out
}
