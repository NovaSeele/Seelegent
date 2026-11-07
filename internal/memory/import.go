package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ImportVerdict adds a verdict of the old version's history with its original time (`seelegent migrate`). A verdict
// already recorded for the project at the same time, task and verdict is not added again; added says whether this
// one was.
func (s *Store) ImportVerdict(ctx context.Context, v Verdict, at time.Time) (added bool, err error) {
	key, _, err := Project(v.Cwd)
	if err != nil {
		return false, err
	}
	notes := strings.Join(strings.Fields(v.Notes), " ")
	if r := []rune(notes); len(r) > notesChars {
		notes = string(r[:notesChars])
	}
	stamp := at.UTC().Format(timeFormat)
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM review_history WHERE project_key = ? AND at = ? AND task_id = ?
			AND verdict = ?`, key, stamp, v.TaskID, v.Verdict).Scan(&one)
		switch {
		case err == nil:
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO review_history (project_key, at, task_id, title, verdict, notes)
			VALUES (?, ?, ?, ?, ?, ?)`, key, stamp, v.TaskID, v.Title, v.Verdict, notes); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}
