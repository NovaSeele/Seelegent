package meeting

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// The dissent memory: for 30 days after a decision, a later task that changed a file an open dissent cites and
// was rejected, or ended with a red gate, is recorded as a signal on the dissent (it may vindicate it). Regression
// runs are not a signal (old bug D19: that signal could never fire; it is dropped).

// watchEntry is a meeting with open dissents that watch files.
type watchEntry struct {
	id      string
	rootKey string
	decided time.Time
}

// memoryBuffer is the room of the dissent memory's task subscription.
const memoryBuffer = 1024

// memory follows the task bus and records dissent signals.
func (s *Service) memory() {
	bus := s.o.Tasks.Bus()
	sub := bus.Subscribe(memoryBuffer)
	defer func() { sub.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case n, open := <-sub.C():
			if !open {
				if s.ctx.Err() != nil {
					return
				}
				sub = bus.Subscribe(memoryBuffer) // fell behind: a missed task is checked at its next change
				continue
			}
			if n.Kind == task.NoticeTask && !n.Created {
				s.signal(s.ctx, n.TaskID)
			}
		}
	}
}

// watchList returns the meetings whose dissents watch files, rebuilt after a meeting changed.
func (s *Service) watchList(ctx context.Context) []watchEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return s.watched
	}
	var out []watchEntry
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = nil
		rows, err := tx.QueryContext(ctx, `SELECT data FROM meetings WHERE status = ?
			AND json_array_length(data, '$.dissents') > 0`, api.MeetingStatusClosed)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			m, err := decode(data)
			if err != nil || m.Decision == nil {
				continue //nolint:nilerr // an unreadable meeting watches nothing; Get reports it
			}
			open := false
			for di := range m.Dissents {
				if d := &m.Dissents[di]; d.Kind == "dissent" && d.Outcome == "" && len(d.WatchPaths) > 0 {
					open = true
				}
			}
			decided, err := time.Parse(time.RFC3339Nano, m.Decision.At)
			if !open || err != nil {
				continue
			}
			key, err := fsutil.Key(m.RepoRoot)
			if err != nil {
				continue
			}
			out = append(out, watchEntry{id: m.ID, rootKey: key, decided: decided})
		}
		return rows.Err()
	})
	if err != nil {
		s.log.Warn("meeting: dissent watch list", "err", err)
		return s.watched
	}
	s.watched, s.dirty = out, false
	return out
}

// signal checks a changed task against the open dissents.
func (s *Service) signal(ctx context.Context, taskID string) {
	list := s.watchList(ctx)
	if len(list) == 0 {
		return
	}
	now := s.o.Now()
	var live []watchEntry
	for _, w := range list {
		if now.Sub(w.decided) <= watchDays*24*time.Hour && !now.Before(w.decided) {
			live = append(live, w)
		}
	}
	if len(live) == 0 {
		return
	}
	d, err := s.o.Tasks.Get(ctx, taskID)
	if err != nil || task.Hidden(d.Kind) || task.Pending(d.Status) {
		return
	}
	var reasons []string
	if d.Review != nil && d.Review.Verdict == api.VerdictRejected {
		reasons = append(reasons, "rejected")
	}
	if n := len(d.Turns); n > 0 && d.Turns[n-1].Gate != nil && d.Turns[n-1].Gate.State == api.GateRed {
		reasons = append(reasons, "gate_red")
	}
	if len(reasons) == 0 {
		return
	}
	root := d.RepoRoot
	if root == "" {
		root = s.rootOf(ctx, d.Cwd)
	}
	if c, err := fsutil.Canonical(root); err == nil {
		root = c // files are canonical below; a short or differently cased root would not contain them
	}
	rootKey, err := fsutil.Key(root)
	if err != nil {
		return
	}
	files := map[string]bool{}
	for i := range d.Turns {
		for _, f := range d.Turns[i].Files {
			p := filepath.FromSlash(f)
			if !filepath.IsAbs(p) {
				p = filepath.Join(d.Cwd, p)
			}
			if c, err := fsutil.Canonical(p); err == nil {
				p = c
			}
			if rel, err := filepath.Rel(root, p); err == nil && !filepath.IsAbs(rel) && rel != ".." &&
				!startsUp(rel) {
				files[relKey(rel)] = true
			}
		}
	}
	if len(files) == 0 {
		return
	}
	updated, _ := time.Parse(time.RFC3339Nano, d.UpdatedAt)
	for _, w := range live {
		if w.rootKey != rootKey || updated.Before(w.decided) {
			continue
		}
		_, err := s.update(ctx, w.id, func(m *api.Meeting) error {
			changed := false
			for i := range m.Dissents {
				ds := &m.Dissents[i]
				if ds.Kind != "dissent" || ds.Outcome != "" || !watches(ds.WatchPaths, files) {
					continue
				}
				for _, r := range reasons {
					if hasSignal(ds.Signals, taskID, r) {
						continue
					}
					ds.Signals = append(ds.Signals, api.DissentSignal{TaskID: taskID, Reason: r, At: s.now()})
					s.logStep(ctx, m.ID, "dissent_signal", map[string]any{"dissentId": ds.ID, "taskId": taskID,
						"reason": r})
					changed = true
				}
			}
			if !changed {
				return errUnchanged
			}
			return nil
		})
		if err != nil && err != errUnchanged { //nolint:errorlint // a sentinel of this file
			s.log.Warn("meeting: dissent signal", "meeting", w.id, "err", err)
		}
	}
}

func startsUp(rel string) bool {
	return len(rel) >= 3 && rel[:2] == ".." && (rel[2] == '/' || rel[2] == '\\')
}

func watches(paths []string, files map[string]bool) bool {
	for _, p := range paths {
		if files[relKey(p)] {
			return true
		}
	}
	return false
}

func hasSignal(list []api.DissentSignal, taskID, reason string) bool {
	for _, x := range list {
		if x.TaskID == taskID && x.Reason == reason {
			return true
		}
	}
	return false
}
