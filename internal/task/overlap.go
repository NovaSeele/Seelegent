package task

import (
	"context"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// Contention limits (old contention.js).
const (
	// overlapTTL is how long the tasks of a repository are reused between contention views.
	overlapTTL = 3 * time.Second
	// maxContentionTasks caps the tasks one view looks at (active first, then the newest).
	maxContentionTasks = 200
	// maxOverlaps caps the overlaps of one task.
	maxOverlaps = 200
)

// overlapCache is the recent tasks of one repository, read at a time.
type overlapCache struct {
	at    time.Time
	root  string
	tasks []*Task
}

// fileSpans is one file a task touched (relative to the repository root) and the turns in which it did.
type fileSpans struct {
	path  string
	spans [][2]time.Time
}

// rootKey returns the comparison key of a repository root (the root itself when it cannot be resolved).
func rootKey(root string) string {
	if k, err := fsutil.Key(root); err == nil {
		return k
	}
	return root
}

// repoTasks returns the tasks of the repository at root that are active or changed within the contention window
// (contentionWindowHours). The answer is reused for overlapTTL; the root of a task is the one recorded when its
// first turn started, never a cached guess from its folder (bugs.md D14).
func (s *Service) repoTasks(ctx context.Context, root string) ([]*Task, error) {
	key := rootKey(root)
	now := s.clk.Now()
	s.overlapMu.Lock()
	c, ok := s.overlaps[key]
	s.overlapMu.Unlock()
	if ok && now.Sub(c.at) < overlapTTL {
		return c.tasks, nil
	}
	hours := s.settings().ContentionWindowHours
	if hours <= 0 {
		hours = 24
	}
	all, err := s.repo.Recent(ctx, now.Add(-time.Duration(hours)*time.Hour), maxContentionTasks)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	var out []*Task
	for _, t := range all {
		if t.RepoRoot == "" || (t.Kind != KindNormal && t.Kind != "") {
			continue
		}
		k, seen := keys[t.RepoRoot]
		if !seen {
			k = rootKey(t.RepoRoot)
			keys[t.RepoRoot] = k
		}
		if k == key {
			out = append(out, t)
		}
	}
	s.overlapMu.Lock()
	for k, old := range s.overlaps {
		if now.Sub(old.at) >= overlapTTL {
			delete(s.overlaps, k)
		}
	}
	s.overlaps[key] = overlapCache{at: now, root: root, tasks: out}
	s.overlapMu.Unlock()
	return out, nil
}

// forgetOverlaps drops the cached contention views (a turn ended: its files changed).
func (s *Service) forgetOverlaps() {
	s.overlapMu.Lock()
	clear(s.overlaps)
	s.overlapMu.Unlock()
}

// touched returns the files task t touched with its edit tools, by snapshot.Key, with the turns in which it did.
// A turn still running counts until now, with the files its worker reported so far.
func (s *Service) touched(t *Task, now time.Time) map[string]*fileSpans {
	out := map[string]*fileSpans{}
	add := func(rel string, from, to time.Time) {
		k := snapshot.Key(rel)
		f := out[k]
		if f == nil {
			f = &fileSpans{path: rel}
			out[k] = f
		}
		f.spans = append(f.spans, [2]time.Time{from, to})
	}
	var live liveState
	if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
		live = s.live(t.ID)
	}
	for _, tu := range t.Turns {
		end := tu.EndedAt
		if end.IsZero() {
			if tu.Status != TurnRunning {
				continue
			}
			end = now
		}
		for _, rel := range turnFiles(t, tu, live) {
			add(rel, tu.StartedAt, end)
		}
	}
	return out
}

// turnFiles lists the files (relative to the repository root) a turn's edit tools changed.
func turnFiles(t *Task, tu *Turn, live liveState) []string {
	if tu.Edited != nil || tu.Snap != nil && tu.Snap.Checked {
		return tu.Edited
	}
	var abs []string
	for _, a := range tu.Attempts {
		if a.Outcome != nil {
			abs = append(abs, a.Outcome.FilesEdited...)
		}
	}
	if tu.Status == TurnRunning {
		abs = append(abs, live.edited...)
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range abs {
		rel, ok := repoRel(t.RepoRoot, t.Cwd, p) // canonical: a CLI may report a short (8.3) or other spelling
		if !ok {
			continue
		}
		if k := snapshot.Key(rel); !seen[k] {
			seen[k] = true
			out = append(out, rel)
		}
	}
	return out
}

// intersects reports whether two sets of intervals overlap; intervals that only touch do not.
func intersects(a, b [][2]time.Time) bool {
	for _, x := range a {
		for _, y := range b {
			if x[0].Before(y[1]) && y[0].Before(x[1]) {
				return true
			}
		}
	}
	return false
}

// related reports a parent and its child (their work is one job, never contention).
func related(a, b *Task) bool { return a.ParentID == b.ID || b.ParentID == a.ID }

// fileOverlaps lists the files task t and another task of the same repository changed in overlapping time.
func (s *Service) fileOverlaps(ctx context.Context, t *Task) []api.FileOverlap {
	if t.RepoRoot == "" {
		return nil
	}
	tasks, err := s.repoTasks(ctx, t.RepoRoot)
	if err != nil {
		s.log.Warn("task: contention", "task", t.ID, "err", err)
		return nil
	}
	now := s.clk.Now()
	mine := s.touched(t, now)
	if len(mine) == 0 {
		return nil
	}
	var out []api.FileOverlap
	for _, o := range tasks {
		if o.ID == t.ID || related(t, o) {
			continue
		}
		theirs := s.touched(o, now)
		for k, f := range mine {
			if g, ok := theirs[k]; ok && intersects(f.spans, g.spans) {
				sev := api.OverlapRecent
				if Active(t.Status) && Active(o.Status) {
					sev = api.OverlapActive
				}
				out = append(out, api.FileOverlap{File: f.path, TaskID: o.ID, Title: o.Title, Status: o.Status,
					Severity: sev})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].TaskID < out[j].TaskID
	})
	if len(out) > maxOverlaps {
		out = out[:maxOverlaps]
	}
	return out
}

// othersDuring returns the files (by snapshot.Key) other tasks of the repository changed while turn tu ran.
func (s *Service) othersDuring(ctx context.Context, t *Task, tu *Turn, end time.Time) map[string]bool {
	out := map[string]bool{}
	tasks, err := s.repoTasks(ctx, tu.Snap.Root)
	if err != nil {
		s.log.Warn("task: contention", "task", t.ID, "err", err)
		return out
	}
	window := [][2]time.Time{{tu.StartedAt, end}}
	for _, o := range tasks {
		if o.ID == t.ID {
			continue
		}
		for k, f := range s.touched(o, end) {
			if intersects(window, f.spans) {
				out[k] = true
			}
		}
	}
	return out
}
