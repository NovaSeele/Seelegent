package task

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// Retention sweep timing.
const (
	// sweepFirst is the delay before the first sweep after the engine starts.
	sweepFirst = 2 * time.Minute
	// sweepEvery is the time between sweeps.
	sweepEvery = 6 * time.Hour
)

// watchSweep runs the retention sweep a little after the start and then every sweepEvery.
func (s *Service) watchSweep() {
	wait := sweepFirst
	for {
		t := s.clk.NewTimer(wait)
		select {
		case <-s.root.Done():
			t.Stop()
			return
		case <-t.C():
		}
		wait = sweepEvery
		s.sweep(s.root)
	}
}

// ownFolder and ownFile match what this version writes directly in a task folder TasksDir/<id>: attempt folders
// t<turn>-a<n> (attemptDir), console folders console-<ms> (takeover) and gate step logs gate-t<turn>-<step>.log
// (gatephase).
var (
	ownFolder = regexp.MustCompile(`^(t\d+-a\d+|console-\d+)$`)
	ownFile   = regexp.MustCompile(`^gate-t\d+-\d+\.log$`)
)

// sweep removes the attempt and launch folders (TasksDir/<id>) and the snapshots of tasks that no longer exist, and
// of finished tasks unchanged for snapshotRetentionDays (bugs.md F5 and D7). A task that is pending, blocked on its
// prerequisites, followed by a run or taken over keeps everything; a store that cannot be read removes nothing.
//
// TasksDir is shared with the old Node version, whose tasks the store never knew (migrate does not import them):
// <id>.json and <id>.jsonl next to a folder <id>/ of stream and stderr files. A folder is removed only when it is
// provably this version's (removableTaskFolder), never on the store's silence alone (final verification N1).
func (s *Service) sweep(ctx context.Context) {
	ages, err := s.repo.Ages(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("task: retention sweep", "err", err)
		}
		return
	}
	days := s.settings().SnapshotRetentionDays
	if days < 1 {
		days = 14
	}
	cutoff := s.clk.Now().Add(-time.Duration(days) * 24 * time.Hour)
	keep := func(id string) bool {
		s.mu.Lock()
		busy := s.runs[id] != nil || s.watches[id] != nil
		s.mu.Unlock()
		if busy {
			return true
		}
		a, ok := ages[id]
		if !ok {
			return false
		}
		return !Finished(a.Status) || a.UpdatedAt.After(cutoff)
	}
	if s.o.Snapshots != nil {
		if n, err := s.o.Snapshots.Sweep(ctx, keep); err != nil {
			s.log.Warn("task: sweep snapshots", "err", err)
		} else if n > 0 {
			s.log.Info("task: swept snapshots", "tasks", n)
		}
	}
	entries, err := os.ReadDir(s.o.TasksDir)
	if err != nil {
		if !os.IsNotExist(err) {
			s.log.Warn("task: sweep task folders", "err", err)
		}
		return
	}
	removed := 0
	for _, e := range entries {
		if ctx.Err() != nil {
			return
		}
		if !e.IsDir() || keep(e.Name()) || !s.removableTaskFolder(e.Name(), ages) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.o.TasksDir, e.Name())); err != nil {
			s.log.Warn("task: remove a task folder", "task", e.Name(), "err", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.log.Info("task: swept task folders", "folders", removed)
	}
}

// removableTaskFolder reports whether the task folder TasksDir/<id> that the retention rule gave up may be removed:
//   - never while the old version's task file <id>.json or <id>.jsonl sits next to it, or when that cannot be told;
//   - a task the store knows is this version's: its folder goes by the retention rule;
//   - a folder the store does not know goes only when everything in it is what this version writes there
//     (ownFolder, ownFile), so a folder of the old version, or anything else put there, stays.
func (s *Service) removableTaskFolder(id string, ages map[string]Age) bool {
	for _, ext := range []string{".json", ".jsonl"} {
		if _, err := os.Lstat(filepath.Join(s.o.TasksDir, id+ext)); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	if _, known := ages[id]; known {
		return true
	}
	entries, err := os.ReadDir(filepath.Join(s.o.TasksDir, id))
	if err != nil {
		return false
	}
	for _, e := range entries {
		own := e.IsDir() && ownFolder.MatchString(e.Name()) || e.Type().IsRegular() && ownFile.MatchString(e.Name())
		if !own {
			return false
		}
	}
	return true
}
