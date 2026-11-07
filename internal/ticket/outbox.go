package ticket

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The write-ahead outbox: every action that leaves the machine (or moves the ticket branch) is a row saved as
// running before the action starts, then done or failed. A row still running when the daemon starts is unknown:
// before any retry it is checked against git and GitHub (a push whose commit is on the remote, a pull request that
// exists) and marked done when it already happened.

const (
	scopeRepo   = "repo"
	scopeTicket = "ticket"
)

// entryData is the data column of an outbox row.
type entryData struct {
	Args   map[string]any `json:"args"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func scanEntry(id int64, kind, status, at string, doneAt sql.NullString, raw string) (api.OutboxEntry, error) {
	var d entryData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return api.OutboxEntry{}, fmt.Errorf("outbox entry %d is unreadable: %w", id, err)
	}
	e := api.OutboxEntry{ID: id, Kind: kind, Status: status, Args: d.Args, Result: d.Result, Error: d.Error, At: at}
	if e.Args == nil {
		e.Args = map[string]any{}
	}
	if e.Result == nil {
		e.Result = map[string]any{}
	}
	if doneAt.Valid {
		e.DoneAt = doneAt.String
	}
	return e, nil
}

// listEntries lists the outbox of a repository or a ticket, oldest first.
func listEntries(ctx context.Context, tx *sql.Tx, scope, ref string) ([]api.OutboxEntry, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, status, at, done_at, data FROM github_outbox
		WHERE scope = ? AND ref = ? ORDER BY id`, scope, ref)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []api.OutboxEntry{}
	for rows.Next() {
		var (
			id                    int64
			kind, status, at, raw string
			doneAt                sql.NullString
		)
		if err := rows.Scan(&id, &kind, &status, &at, &doneAt, &raw); err != nil {
			return nil, err
		}
		e, err := scanEntry(id, kind, status, at, doneAt, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func getEntry(ctx context.Context, tx *sql.Tx, id int64) (api.OutboxEntry, error) {
	var (
		kind, status, at, raw string
		doneAt                sql.NullString
	)
	err := tx.QueryRowContext(ctx, `SELECT kind, status, at, done_at, data FROM github_outbox WHERE id = ?`, id).
		Scan(&kind, &status, &at, &doneAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return api.OutboxEntry{}, notFoundf("no outbox entry %d", id)
	}
	if err != nil {
		return api.OutboxEntry{}, err
	}
	return scanEntry(id, kind, status, at, doneAt, raw)
}

// saveEntry writes e (a new row when e.ID is 0) and returns its id.
func saveEntry(ctx context.Context, tx *sql.Tx, scope, ref string, e api.OutboxEntry) (int64, error) {
	raw, err := json.Marshal(entryData{Args: e.Args, Result: e.Result, Error: e.Error})
	if err != nil {
		return 0, err
	}
	var doneAt any
	if e.DoneAt != "" {
		doneAt = e.DoneAt
	}
	if e.ID == 0 {
		res, err := tx.ExecContext(ctx, `INSERT INTO github_outbox (scope, ref, kind, status, at, done_at, data)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, scope, ref, e.Kind, e.Status, e.At, doneAt, string(raw))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err = tx.ExecContext(ctx, `UPDATE github_outbox SET status = ?, at = ?, done_at = ?, data = ? WHERE id = ?`,
		e.Status, e.At, doneAt, string(raw), e.ID)
	return e.ID, err
}

// openEntry returns the last pending or unknown entry of kind (to retry it) or a new pending one. Comments never
// reuse an entry.
func openEntry(ctx context.Context, tx *sql.Tx, scope, ref, kind string, args map[string]any, at string) (
	api.OutboxEntry, error) {
	if kind != api.OutboxComment {
		list, err := listEntries(ctx, tx, scope, ref)
		if err != nil {
			return api.OutboxEntry{}, err
		}
		for i := len(list) - 1; i >= 0; i-- {
			e := list[i]
			if e.Kind == kind && (e.Status == api.OutboxPending || e.Status == api.OutboxUnknown) {
				e.Args = args
				return e, nil
			}
		}
	}
	return api.OutboxEntry{Kind: kind, Status: api.OutboxPending, Args: args, Result: map[string]any{}, At: at}, nil
}

// recoverRunning marks every entry left running by an earlier daemon unknown and returns their refs.
func recoverRunning(ctx context.Context, tx *sql.Tx) (tickets, repos []string, err error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT scope, ref FROM github_outbox WHERE status = ?`, api.OutboxRunning)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var scope, ref string
		if err := rows.Scan(&scope, &ref); err != nil {
			_ = rows.Close()
			return nil, nil, err
		}
		if scope == scopeTicket {
			tickets = append(tickets, ref)
		} else {
			repos = append(repos, ref)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE github_outbox SET status = ? WHERE status = ?`, api.OutboxUnknown,
		api.OutboxRunning)
	return tickets, repos, err
}

// runStep runs one outward action of a ticket write-ahead: the entry is saved running first, then done with the
// action's result or failed with its (redacted) error, which also becomes the ticket's error.
func (s *Service) runStep(ctx context.Context, ticketID, kind string, args map[string]any,
	fn func() (map[string]any, error)) error {
	var id int64
	now := api.FormatTime(s.o.Now())
	if _, err := s.updateTicket(ctx, ticketID, func(tx *sql.Tx, _ *ticketRec) error {
		e, err := openEntry(ctx, tx, scopeTicket, ticketID, kind, args, now)
		if err != nil {
			return err
		}
		e.Status, e.At, e.Error = api.OutboxRunning, now, ""
		if id, err = saveEntry(ctx, tx, scopeTicket, ticketID, e); err != nil {
			return err
		}
		s.logStep(ctx, tx, ticketID, "outbox", map[string]any{"id": id, "kind": kind, "status": api.OutboxRunning})
		return nil
	}); err != nil {
		return err
	}
	result, runErr := fn()
	_, err := s.updateTicket(context.WithoutCancel(ctx), ticketID, func(tx *sql.Tx, t *ticketRec) error {
		e, err := getEntry(ctx, tx, id)
		if err != nil {
			return err
		}
		e.DoneAt = api.FormatTime(s.o.Now())
		if runErr != nil {
			e.Status, e.Error, e.DoneAt = api.OutboxFailed, s.redactErr(runErr, 600), ""
			t.Error = e.Error
		} else {
			e.Status, e.Result = api.OutboxDone, result
		}
		if e.Result == nil {
			e.Result = map[string]any{}
		}
		if _, err := saveEntry(ctx, tx, scopeTicket, ticketID, e); err != nil {
			return err
		}
		data := map[string]any{"id": id, "kind": kind, "status": e.Status}
		if e.Error != "" {
			data["error"] = e.Error
		}
		s.logStep(ctx, tx, ticketID, "outbox", data)
		return nil
	})
	if runErr != nil {
		return runErr
	}
	return err
}

// markRecovered marks an unknown entry done: the action had happened before the daemon stopped.
func markRecovered(ctx context.Context, tx *sql.Tx, scope, ref string, e api.OutboxEntry, result map[string]any,
	at string) error {
	if result == nil {
		result = map[string]any{}
	}
	result["recovered"] = true
	e.Status, e.Result, e.DoneAt = api.OutboxDone, result, at
	_, err := saveEntry(ctx, tx, scope, ref, e)
	return err
}
