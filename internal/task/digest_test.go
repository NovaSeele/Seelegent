package task

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// The summaries read from the stored digest are byte for byte the ones built from the loaded turns, and so are
// the lists of rows stored before the digest existed, before and after the backfill.
func TestListDigestMatchesTurns(t *testing.T) {
	h := newHarness(t)
	ids := seedListTasks(t, h)
	ctx := context.Background()
	want := listJSON(t, h, ids)

	for _, id := range ids {
		full, err := h.s.repo.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := json.Marshal(h.s.summary(full))
		got, err := h.s.Summary(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(got)
		if !bytes.Equal(a, b) {
			t.Errorf("summary of %s from the digest:\n%s\nfrom the turns:\n%s", id, b, a)
		}
	}

	// Rows from before migration 0011: no digest, the lists load their turns.
	if err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET turn_count = -1, tokens_used = 0, files_changed = 0,
			gate_turn = 0, gate_state = '', last_account_id = '', last_account_label = ''`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
		t.Errorf("lists without a stored digest differ:\n%s\nwant\n%s", got, want)
	}
	if _, err := h.s.repo.BackfillDigests(ctx, noPause, failSkip(t)); err != nil {
		t.Fatal(err)
	}
	var missing int
	if err := h.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE turn_count < 0`).Scan(&missing)
	}); err != nil || missing != 0 {
		t.Fatalf("%d tasks without a digest after the backfill (%v)", missing, err)
	}
	if got := listJSON(t, h, ids); !bytes.Equal(got, want) {
		t.Errorf("lists after the backfill differ:\n%s\nwant\n%s", got, want)
	}
}

// A save keeps the digest up to date with the turns.
func TestDigestFollowsSaves(t *testing.T) {
	h := newHarness(t)
	ids := seedListTasks(t, h)
	ctx := context.Background()
	tk, err := h.s.repo.Get(ctx, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	tk.Turns = append(tk.Turns, &Turn{N: 3, By: ByLead, Status: TurnRunning, StartedAt: tk.CreatedAt,
		Files: []string{"new.go"}, Attempts: []*Attempt{{N: 1, AccountID: "acc7", AccountLabel: "Seven",
			Status: AttemptRunning, StartedAt: tk.CreatedAt}}})
	if _, err := h.s.repo.Save(ctx, tk, nil, nil); err != nil {
		t.Fatal(err)
	}
	sum, err := h.s.Summary(ctx, ids[2])
	if err != nil {
		t.Fatal(err)
	}
	if sum.TurnCount != 3 || sum.FilesChanged != 4 || sum.Gate != nil || sum.AccountID != "acc7" ||
		sum.AccountLabel != "Seven" {
		t.Errorf("summary after a new turn = turns %d files %d gate %+v account %s/%s", sum.TurnCount,
			sum.FilesChanged, sum.Gate, sum.AccountID, sum.AccountLabel)
	}
}

// The list reads its page through the index that holds kind, in order, without sorting and without reading the
// rows of hidden runs.
func TestListQueryUsesIndex(t *testing.T) {
	h := newHarness(t)
	seedListTasks(t, h)
	ctx := context.Background()
	for name, q := range map[string]Query{"default": {}, "cursor": {Cursor: encodeCursor(
		"2026-11-08T08:05:00.000000000Z", "t05")}, "project": {ProjectKey: "proj-1"}} {
		sqlText, args, _, err := listQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		if err := h.db.Read(ctx, func(tx *sql.Tx) error {
			rows, err := tx.QueryContext(ctx, `EXPLAIN QUERY PLAN `+sqlText, args...)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					return err
				}
				plan = append(plan, detail)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		text := strings.Join(plan, "; ")
		want := "USING INDEX tasks_created"
		if name == "project" {
			want = "USING INDEX tasks_project"
		}
		if !strings.Contains(text, want) || strings.Contains(text, "TEMP B-TREE") {
			t.Errorf("%s: plan %q, want %s without a sort", name, text, want)
		}
	}
}
