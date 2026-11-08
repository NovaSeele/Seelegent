package task

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// The post-turn safety checks (milestone 12, docs/safety.md): before a turn's first attempt the engine snapshots
// the repository's uncommitted files (snapshot.Store, outside the user's .git); when the turn ends it snapshots
// them again and compares: files whose uncommitted work was lost (work at risk), the hygiene findings and the
// scope warnings. The checks never fail a task: an error is logged and the turn simply has no findings.

// Time limits of the checks.
const (
	snapTimeout  = 2 * time.Minute
	checkTimeout = 3 * time.Minute
	// slowCheck is how long the checks before a turn's end may take before the log warns.
	slowCheck = 10 * time.Second
)

// takeStartSnapshot records the repository before the first attempt of turn tu.
func (s *Service) takeStartSnapshot(ctx context.Context, t *Task, tu *Turn) {
	sctx, cancel := context.WithTimeout(ctx, snapTimeout)
	defer cancel()
	m, err := s.o.Snapshots.Take(sctx, t.RepoRoot, nil)
	if err == nil {
		err = s.o.Snapshots.Save(t.ID, tu.N, snapshot.KindStart, m)
	}
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("task: start snapshot", "task", t.ID, "turn", tu.N, "err", err)
		}
		return
	}
	_, err = s.mutate(ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		x := cur.TurnN(tu.N)
		if x == nil || x.Snap != nil || len(x.Attempts) > 0 || x.Status != TurnRunning {
			return nil, errNoChange
		}
		x.Snap = &TurnSnap{Root: m.Root, Head: m.Head, Truncated: m.Truncated}
		return nil, nil
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("task: start snapshot", "task", t.ID, "err", err)
	}
}

// turnCheck is the result of the post-turn checks.
type turnCheck struct {
	edited, changed, preexisting []string
	risks                        []api.RiskFile
	hygiene, scope               []api.Finding
	// files are the turn's files by its snapshots, as display paths (relative to the task's Cwd, sorted): the
	// changed ones that exist at the end and the tracked ones the turn deleted; deleted marks the latter.
	files   []string
	deleted map[string]bool
	// truncated: the end snapshot hit its limits (snapshot.Store.Take).
	truncated bool
}

// checkTurn runs the post-turn checks of turn tu of t, whose last attempt ended with outcome last (it may not be
// in tu yet). ok says the turn ended well (its last attempt succeeded): only then is its response a report whose
// silence about a new file means something (unreported_file). It returns nil when the turn has no start snapshot
// or the checks cannot run.
func (s *Service) checkTurn(ctx context.Context, t *Task, tu *Turn, last *backend.Outcome, ok bool) *turnCheck {
	if s.o.Snapshots == nil || tu == nil || tu.Snap == nil || tu.Snap.Checked {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	root := tu.Snap.Root
	start, err := s.o.Snapshots.Load(t.ID, tu.N, snapshot.KindStart)
	if err != nil {
		s.log.Warn("task: load the start snapshot", "task", t.ID, "turn", tu.N, "err", err)
		return nil
	}
	var abs, commands []string
	response := tu.Response
	for _, a := range tu.Attempts {
		if a.Outcome != nil { // the attempt that just ended has none yet: its outcome is last
			abs = append(abs, a.Outcome.FilesEdited...)
			commands = append(commands, a.Outcome.Commands...)
		}
	}
	if last != nil {
		abs = append(abs, last.FilesEdited...)
		commands = append(commands, last.Commands...)
		response = last.Response
	}
	c := &turnCheck{}
	editedKeys := map[string]bool{}
	var editedAbs []string
	for _, p := range abs {
		if !filepath.IsAbs(p) {
			p = filepath.Join(t.Cwd, p)
		}
		if slices.Contains(editedAbs, p) {
			continue
		}
		editedAbs = append(editedAbs, p)
		if rel, ok := relInside(root, p); ok {
			rel = filepath.ToSlash(rel)
			if k := snapshot.Key(rel); !editedKeys[k] {
				editedKeys[k] = true
				c.edited = append(c.edited, rel)
			}
		}
	}
	extra := slices.Clone(c.edited)
	for _, f := range start.Files {
		extra = append(extra, f.Path)
	}
	end, err := s.o.Snapshots.Take(ctx, root, extra)
	if err == nil {
		err = s.o.Snapshots.Save(t.ID, tu.N, snapshot.KindEnd, end)
	}
	if err != nil {
		s.log.Warn("task: end snapshot", "task", t.ID, "turn", tu.N, "err", err)
		return nil
	}
	c.truncated = end.Truncated
	s.forgetOverlaps()
	// Files other tasks changed meanwhile are theirs: not lost work, not this turn's change. The task's own
	// earlier lines are not someone else's work either: they count as committed, so only the lines someone else
	// added to those files can be reported lost.
	others := s.othersDuring(ctx, t, tu, s.clk.Now())
	prior := s.priorWorkOf(ctx, t, tu)
	skip := maps.Clone(others)
	maps.Copy(skip, prior.skip)
	head := func(rel string) ([]byte, bool) {
		data, ok, err := s.o.Repos.Blob(ctx, root, start.Head, rel, snapshot.MaxFileBytes, false)
		ok = ok && err == nil
		if own := prior.own[snapshot.Key(rel)]; len(own) > 0 {
			if !ok {
				data = nil
			}
			return headWithOwn(data, own), true
		}
		return data, ok
	}
	for _, r := range snapshot.Analyze(snapshot.Input{Start: start, End: end, Edited: editedKeys,
		Head: head, Content: s.o.Snapshots.Content, Others: skip}) {
		if prior.mine(r.Path, r.Lost, r.Binary) {
			continue
		}
		c.risks = append(c.risks, api.RiskFile{Path: r.Path, Why: r.Why, LostLines: r.Lost, Deleted: r.Deleted,
			Binary: r.Binary})
	}
	c.changed, c.preexisting = changedFiles(start, end, editedKeys, others)
	c.files, c.deleted = snapshotFiles(t.Cwd, root, end, c.changed)
	if s.settings().Hygiene {
		reported := ok && strings.TrimSpace(response) != ""
		c.hygiene = s.hygiene(ctx, root, start, end, c.changed, response, reported)
	}
	c.scope = s.scope(ctx, t, root, editedAbs, commands, response)
	return c
}

// changedFiles lists the files whose content differs between the snapshots, plus the edited ones that exist at the
// end, and those of them that were already dirty at the start.
func changedFiles(start, end snapshot.Manifest, edited, others map[string]bool) (changed, preexisting []string) {
	for _, f := range end.Files {
		b, ok := start.Find(f.Path)
		k := snapshot.Key(f.Path)
		switch {
		case others[k] && !edited[k]:
			continue
		case ok && (b.Hash != f.Hash || b.Missing != f.Missing || b.Size != f.Size):
		case !ok && f.Status != "":
		case edited[k] && !f.Missing:
		default:
			continue
		}
		changed = append(changed, f.Path)
		if ok && b.Status != "" {
			preexisting = append(preexisting, f.Path)
		}
	}
	sort.Strings(changed)
	sort.Strings(preexisting)
	return changed, preexisting
}

// snapshotFiles turns the changed files (relative to root) into a turn's file list: those that exist at the end,
// and those deleted that git tracks (a file made and removed within the turn is not in changed; an untracked file
// that was already there and is gone is not the turn's change to show). The paths are display paths, as
// displayPaths makes them, sorted.
func snapshotFiles(cwd, root string, end snapshot.Manifest, changed []string) (files []string,
	deleted map[string]bool) {
	show := displayUnder(cwd, root)
	files = make([]string, 0, len(changed))
	deleted = map[string]bool{}
	for _, rel := range changed {
		f, ok := end.Find(rel)
		if !ok {
			continue
		}
		if f.Missing {
			if f.Status == "" || f.Status == api.GitUntracked || f.Status == api.GitAdded {
				continue // not in HEAD: nothing a commit would delete
			}
			deleted[show(rel)] = true
		}
		files = append(files, show(rel))
	}
	slices.Sort(files)
	return slices.Compact(files), deleted
}

// displayUnder returns a function that turns a path relative to root ('/' separators) into the display path
// displayPaths would give its absolute form, comparing root and cwd once rather than per file.
func displayUnder(cwd, root string) func(rel string) string {
	if under, ok := relInside(root, cwd); ok {
		under = filepath.ToSlash(under)
		if under == "." {
			return func(rel string) string { return rel }
		}
		return func(rel string) string {
			if strings.HasPrefix(rel, under+"/") {
				return rel[len(under)+1:]
			}
			return filepath.ToSlash(filepath.Join(root, filepath.FromSlash(rel)))
		}
	}
	return func(rel string) string { return filepath.ToSlash(filepath.Join(root, filepath.FromSlash(rel))) }
}

// checkedFiles is a checked turn's Files: the files its snapshots show it changed. When a snapshot was truncated
// those may miss some, so the edit-tool list (fallback, already without the files that no longer exist) is added,
// and the deleted files are left out like the fallback's.
func checkedFiles(snap *TurnSnap, c *turnCheck, fallback []string) []string {
	if !snap.Truncated && !c.truncated {
		return c.files
	}
	out := slices.Clone(fallback)
	for _, f := range c.files {
		if !c.deleted[f] {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// applyCheck stores a check's results on the turn and returns its snapshot_truncated and work_at_risk events, if
// any.
func applyCheck(t *Task, tu *Turn, c *turnCheck) []NewEvent {
	if c == nil || tu.Snap == nil || tu.Snap.Checked {
		return nil
	}
	tu.Files = checkedFiles(tu.Snap, c, tu.Files)
	tu.Snap.Checked = true
	tu.Snap.EndTruncated = c.truncated
	tu.Edited = api.NonNil(c.edited)
	tu.Changed, tu.Preexisting = c.changed, c.preexisting
	tu.WorkAtRisk, tu.Hygiene, tu.Scope = c.risks, c.hygiene, c.scope
	var out []NewEvent
	if tu.Snap.Truncated || tu.Snap.EndTruncated {
		var which []string
		if tu.Snap.Truncated {
			which = append(which, snapshot.KindStart)
		}
		if tu.Snap.EndTruncated {
			which = append(which, snapshot.KindEnd)
		}
		out = append(out, NewEvent{Type: api.EventSnapshotTruncated, Turn: tu.N,
			Data: api.SnapshotTruncatedData{Snapshots: which, MaxFiles: snapshot.MaxFiles,
				MaxBytes: snapshot.MaxTotalBytes}})
	}
	if len(c.risks) == 0 {
		return out
	}
	files := make([]string, 0, len(c.risks))
	for _, r := range c.risks {
		files = append(files, r.Path)
	}
	return append(out, NewEvent{Type: api.EventWorkAtRisk, Turn: tu.N,
		Data: api.WorkAtRiskData{Files: files, Snapshot: snapshotRef(t.ID, tu.N)}})
}

// snapshotRef names the start snapshot of a turn in events and reports.
func snapshotRef(taskID string, turn int) string { return fmt.Sprintf("%s/t%d", taskID, turn) }

// preCheck runs the post-turn checks before an attempt's end is recorded, when the attempt may end its turn, so the
// findings are saved together with the turn's end (a lead woken by the end sees them). It returns nil otherwise.
func (s *Service) preCheck(ctx context.Context, id string, turn int, status string, o *backend.Outcome,
	n, maxAttempts int) *turnCheck {
	if s.o.Snapshots == nil {
		return nil
	}
	switch status {
	case AttemptQuota, AttemptAuth, AttemptTransient:
		if n < maxAttempts {
			return nil
		}
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil
	}
	// The turn's end is announced only after this: a slow check delays the lead.
	began := s.clk.Now()
	c := s.checkTurn(ctx, t, t.TurnN(turn), o, status == AttemptOK || status == AttemptBackgroundWait)
	took := s.clk.Now().Sub(began)
	if took > slowCheck {
		s.log.Warn("task: post-turn checks were slow", "task", id, "turn", turn, "took", took)
	} else {
		s.log.Debug("task: post-turn checks", "task", id, "turn", turn, "took", took)
	}
	return c
}

// lateCheck runs the post-turn checks of a turn that ended without them (a cancel, a failure) and saves them.
func (s *Service) lateCheck(ctx context.Context, id string, turn int) {
	if s.o.Snapshots == nil {
		return
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return
	}
	tu := t.TurnN(turn)
	if tu == nil || tu.Status == TurnRunning || tu.Snap == nil || tu.Snap.Checked {
		return
	}
	c := s.checkTurn(ctx, t, tu, nil, tu.Status == TurnOK)
	if c == nil {
		return
	}
	_, err = s.mutate(ctx, id, func(cur *Task) ([]NewEvent, error) {
		x := cur.TurnN(turn)
		if x == nil || x.Status == TurnRunning || x.Snap == nil || x.Snap.Checked {
			return nil, errNoChange
		}
		return applyCheck(cur, x, c), nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Warn("task: save the post-turn checks", "task", id, "err", err)
	}
}
