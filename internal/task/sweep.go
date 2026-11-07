package task

import (
	"context"
	"os"
	"path/filepath"
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

// sweep removes the attempt and launch folders (TasksDir/<id>) and the snapshots of tasks that no longer exist, and
// of finished tasks unchanged for snapshotRetentionDays (bugs.md F5 and D7). A task that is pending, blocked on its
// prerequisites, followed by a run or taken over keeps everything; a store that cannot be read removes nothing.
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
		if !e.IsDir() || keep(e.Name()) {
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
