package task

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
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
}

// checkTurn runs the post-turn checks of turn tu of t, whose last attempt ended with outcome last (it may not be
// in tu yet). It returns nil when the turn has no start snapshot or the checks cannot run.
func (s *Service) checkTurn(ctx context.Context, t *Task, tu *Turn, last *backend.Outcome) *turnCheck {
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
	s.forgetOverlaps()
	// Files other tasks changed meanwhile are theirs: not lost work, not this turn's change. The task's own
	// earlier changes are not someone else's work either.
	others := s.othersDuring(ctx, t, tu, s.clk.Now())
	skip := priorFiles(t, tu)
	maps.Copy(skip, others)
	head := func(rel string) ([]byte, bool) {
		data, ok, err := s.o.Repos.Blob(ctx, root, start.Head, rel, snapshot.MaxFileBytes, false)
		return data, ok && err == nil
	}
	for _, r := range snapshot.Analyze(snapshot.Input{Start: start, End: end, Edited: editedKeys,
		Head: head, Content: s.o.Snapshots.Content, Others: skip}) {
		c.risks = append(c.risks, api.RiskFile{Path: r.Path, Why: r.Why, LostLines: r.Lost, Deleted: r.Deleted,
			Binary: r.Binary})
	}
	c.changed, c.preexisting = changedFiles(start, end, editedKeys, others)
	if s.settings().Hygiene {
		c.hygiene = s.hygiene(ctx, root, start, end, c.changed, response)
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

// applyCheck stores a check's results on the turn and returns its work_at_risk event, if any.
func applyCheck(t *Task, tu *Turn, c *turnCheck) []NewEvent {
	if c == nil || tu.Snap == nil || tu.Snap.Checked {
		return nil
	}
	tu.Snap.Checked = true
	tu.Edited = api.NonNil(c.edited)
	tu.Changed, tu.Preexisting = c.changed, c.preexisting
	tu.WorkAtRisk, tu.Hygiene, tu.Scope = c.risks, c.hygiene, c.scope
	if len(c.risks) == 0 {
		return nil
	}
	files := make([]string, 0, len(c.risks))
	for _, r := range c.risks {
		files = append(files, r.Path)
	}
	return []NewEvent{{Type: api.EventWorkAtRisk, Turn: tu.N,
		Data: api.WorkAtRiskData{Files: files, Snapshot: snapshotRef(t.ID, tu.N)}}}
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
	return s.checkTurn(ctx, t, t.TurnN(turn), o)
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
	c := s.checkTurn(ctx, t, tu, nil)
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
