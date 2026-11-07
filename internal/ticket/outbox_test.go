package ticket

import (
	"context"
	"database/sql"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestRunStepFinishesAfterCancel is the review's N1: the request's context ends right after the action happened;
// the outbox entry is still marked done, never left running.
func TestRunStepFinishesAfterCancel(t *testing.T) {
	e := setupFlow(t)
	tk, _ := readyTicket(t, e, "Cancel")
	ctx, cancel := context.WithCancel(t.Context())
	err := e.s.runStep(ctx, tk.ID, api.OutboxComment, map[string]any{"issue": 1}, func() (map[string]any, error) {
		cancel()
		return map[string]any{"url": "u"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var status string
	if err := e.db.Read(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT status FROM github_outbox WHERE kind = 'comment'`).Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != api.OutboxDone {
		t.Fatalf("entry is %s", status)
	}
}
