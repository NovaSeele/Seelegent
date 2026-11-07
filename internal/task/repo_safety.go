package task

import (
	"context"
	"database/sql"
	"time"
)

// Recent returns up to limit tasks (with turns) that are pending (active or waiting for dependencies, whatever their
// age: they hold their lane claims) or were changed at or after since: pending ones first, then the most recently
// changed (the contention view, old contention.js).
func (r *Repo) Recent(ctx context.Context, since time.Time, limit int) ([]*Task, error) {
	var out []*Task
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks
			WHERE status IN (?, ?, ?, ?) OR updated_at >= ?
			ORDER BY CASE WHEN status IN (?, ?, ?, ?) THEN 0 ELSE 1 END, updated_at DESC, id
			LIMIT ?`,
			string(StatusQueued), string(StatusRunning), string(StatusWaitingQuota), string(StatusWaitingDeps),
			formatTime(since),
			string(StatusQueued), string(StatusRunning), string(StatusWaitingQuota), string(StatusWaitingDeps), limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			t, err := scanTask(rows)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return loadTurns(ctx, tx, out)
	})
	return out, err
}

// Age is the status and last change of a task (the retention sweep).
type Age struct {
	Status    Status
	UpdatedAt time.Time
}

// Ages returns the status and last change of every task, by id.
func (r *Repo) Ages(ctx context.Context) (map[string]Age, error) {
	out := map[string]Age{}
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, status, updated_at FROM tasks`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, status, updated string
			if err := rows.Scan(&id, &status, &updated); err != nil {
				return err
			}
			at, err := parseTime(updated)
			if err != nil {
				return err
			}
			out[id] = Age{Status: Status(status), UpdatedAt: at}
		}
		return rows.Err()
	})
	return out, err
}
