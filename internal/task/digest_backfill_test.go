package task

import (
	"bytes"
	"context"
	"database/sql"
	"slices"
	"testing"
)

// noPause lets a test's backfill run its batches back to back.
func noPause(context.Context) error { return nil }

// failSkip fails the test for any task the backfill could not read.
func failSkip(t *testing.T) func(string, error) {
	return func(id string, err error) { t.Errorf("task %s skipped: %v", id, err) }
}

// clearDigests makes every task look stored before migration 0011.
func clearDigests(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET turn_count = -1, tokens_used = 0, files_changed = 0,
			gate_turn = 0, gate_state = '', last_account_id = '', last_account_label = ''`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// undigestedIDs returns the tasks without a stored digest.
func undigestedIDs(t *testing.T, h *harness) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	if err := h.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE turn_count < 0 ORDER BY id`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

// The lists are the same before, between every two batches of and after the backfill.
func TestListRightDuringBackfill(t *testing.T) {
	h := newHarness(t)
	ids := seedListTasks(t, h)
	want := listJSON(t, h, ids)
	clearDigests(t, h)
	h.s.repo.backfillBatch = 3
	pauses := 0
	pause := func(context.Context) error {
		pauses++
		if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
			t.Errorf("lists after %d batches differ:\n%s\nwant\n%s", pauses, got, want)
		}
		return nil
	}
	n, err := h.s.repo.BackfillDigests(context.Background(), pause, failSkip(t))
	if err != nil || n != len(ids) {
		t.Fatalf("backfill = %d, %v; want %d", n, err, len(ids))
	}
	if pauses != len(ids)/3 {
		t.Errorf("%d pauses for %d tasks in batches of 3", pauses, len(ids))
	}
	if left := undigestedIDs(t, h); len(left) != 0 {
		t.Fatalf("without a digest after the backfill: %v", left)
	}
	if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
		t.Errorf("lists after the backfill differ:\n%s\nwant\n%s", got, want)
	}
}

// A task whose turns cannot be read is passed over (it keeps no digest) and stops neither the backfill nor a
// daemon start.
func TestBackfillPassesOverUnreadableTask(t *testing.T) {
	h := newHarness(t)
	ids := seedListTasks(t, h)
	clearDigests(t, h)
	broken := ids[3]
	ctx := context.Background()
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE turns SET data = 'nope' WHERE task_id = ?`, broken)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h.s.repo.backfillBatch = 4
	var skipped []string
	n, err := h.s.repo.BackfillDigests(ctx, noPause, func(id string, err error) {
		if err == nil {
			t.Errorf("task %s skipped without an error", id)
		}
		skipped = append(skipped, id)
	})
	if err != nil || n != len(ids)-1 {
		t.Fatalf("backfill = %d, %v; want %d", n, err, len(ids)-1)
	}
	if !slices.Equal(skipped, []string{broken}) {
		t.Fatalf("skipped = %v, want [%s]", skipped, broken)
	}
	if left := undigestedIDs(t, h); !slices.Equal(left, []string{broken}) {
		t.Fatalf("without a digest = %v, want [%s]", left, broken)
	}
	// A restart with the unreadable task still undigested starts, and its background backfill ends by itself.
	h.stop()
	h.start()
	h.s.BackfillDigests()
	h.stop()
	if left := undigestedIDs(t, h); !slices.Equal(left, []string{broken}) {
		t.Fatalf("without a digest after the restart = %v, want [%s]", left, broken)
	}
}

// The daemon's backfill rests between batches on the engine clock and stops cleanly when the engine closes; the
// next backfill goes on where it stopped.
func TestBackfillStopsOnClose(t *testing.T) {
	h := newHarness(t)
	ids := seedListTasks(t, h)
	want := listJSON(t, h, ids)
	clearDigests(t, h)
	h.s.repo.backfillBatch = 5
	h.s.BackfillDigests()
	// The fake clock never advances: the backfill stores its first batch and waits for its pause.
	h.waitFor("the first batch", func() bool { return len(undigestedIDs(t, h)) == len(ids)-5 })
	h.stop() // fails the test if the backfill does not stop
	if left := undigestedIDs(t, h); len(left) != len(ids)-5 {
		t.Fatalf("%d tasks without a digest after the close, want %d", len(left), len(ids)-5)
	}
	// The lists read the half-done digests right; the rest is stored by the next backfill.
	if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
		t.Errorf("lists after a stopped backfill differ:\n%s\nwant\n%s", got, want)
	}
	if _, err := h.s.repo.BackfillDigests(context.Background(), noPause, failSkip(t)); err != nil {
		t.Fatal(err)
	}
	if left := undigestedIDs(t, h); len(left) != 0 {
		t.Fatalf("without a digest after the next backfill: %v", left)
	}
	if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
		t.Errorf("lists after the backfill differ:\n%s\nwant\n%s", got, want)
	}
}
