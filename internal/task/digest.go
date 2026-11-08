package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// turnDigest is what a task summary needs from the turns and attempts. It is stored in the task's row on every
// save (migration 0011), so the list, the first paint and the summary feed do not load the turns.
type turnDigest struct {
	// known is false for a row stored before the columns existed (turn_count -1): its turns must be loaded.
	known  bool
	turns  int
	tokens int64
	files  int
	// gateTurn is the turn of the newest gate since the lead's latest instruction (0: none) and gateState its state.
	gateTurn  int
	gateState string
	// accountID and accountLabel name the newest attempt of the latest turn that has an account ("" when none).
	accountID, accountLabel string
}

// digestOf computes the digest from the task's turns (all of them must be loaded).
func digestOf(t *Task) turnDigest {
	d := turnDigest{known: true, turns: len(t.Turns), tokens: t.TokensUsed(), files: len(t.FilesChanged())}
	if n := len(t.Turns); n > 0 {
		start := instructionStart(t, t.Turns[n-1].N)
		for i := n - 1; i >= start; i-- {
			if g := t.Turns[i].Gate; g != nil {
				d.gateTurn, d.gateState = t.Turns[i].N, g.State
				break
			}
		}
	}
	if tu := t.LastTurn(); tu != nil {
		for i := len(tu.Attempts) - 1; i >= 0; i-- {
			if a := tu.Attempts[i]; a.AccountID != "" {
				d.accountID, d.accountLabel = a.AccountID, a.AccountLabel
				break
			}
		}
	}
	return d
}

// digestColumns are the digest's columns of the tasks table, in the order of digestArgs and scanListed.
const digestColumns = `turn_count, tokens_used, files_changed, gate_turn, gate_state, last_account_id, last_account_label`

// digestArgs are the values of digestColumns.
func (d turnDigest) args() []any {
	return []any{d.turns, d.tokens, d.files, d.gateTurn, d.gateState, d.accountID, d.accountLabel}
}

// listedColumns are taskColumns with the digest.
const listedColumns = taskColumns + `, ` + digestColumns

// listed is a task read for a list: its record without the turns, and the digest of the turns.
type listed struct {
	t *Task
	d turnDigest
}

// scanListed reads a row of listedColumns.
func scanListed(s scanner) (listed, error) {
	var id, status, data, lastAt, created, updated, gateState, accID, accLabel string
	var seq, tokens int64
	var turns, files, gateTurn int
	if err := s.Scan(&id, &status, &data, &seq, &lastAt, &created, &updated, &turns, &tokens, &files, &gateTurn,
		&gateState, &accID, &accLabel); err != nil {
		return listed{}, err
	}
	t, err := decodeTask(id, status, data, seq, lastAt, created, updated)
	if err != nil {
		return listed{}, err
	}
	d := turnDigest{known: turns >= 0, turns: turns, tokens: tokens, files: files, gateTurn: gateTurn,
		gateState: gateState, accountID: accID, accountLabel: accLabel}
	return listed{t: t, d: d}, nil
}

// queryListed runs a query of listedColumns and loads the turns of the rows whose digest is not stored yet (their
// digest is then computed from them).
func queryListed(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]listed, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	var out []listed
	for rows.Next() {
		l, err := scanListed(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, completeDigests(ctx, tx, out)
}

// completeDigests computes the digest of the tasks that have none stored from their turns.
func completeDigests(ctx context.Context, tx *sql.Tx, ls []listed) error {
	var missing []*Task
	for _, l := range ls {
		if !l.d.known {
			missing = append(missing, l.t)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if err := loadTurns(ctx, tx, missing); err != nil {
		return err
	}
	for i := range ls {
		if !ls[i].d.known {
			ls[i].d = digestOf(ls[i].t)
		}
	}
	return nil
}

// getListed returns task id without its turns, with their digest, or ErrNotFound.
func (r *Repo) getListed(ctx context.Context, id string) (listed, error) {
	var l listed
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		ls, err := queryListed(ctx, tx, `SELECT `+listedColumns+` FROM tasks WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if len(ls) == 0 {
			return fmt.Errorf("%w: task %s", ErrNotFound, id)
		}
		l = ls[0]
		return nil
	})
	return l, err
}

// backfillBatch is how many tasks one transaction of BackfillDigests updates (Repo.backfillBatch; tests lower it).
const backfillBatch = 100

// BackfillDigests stores the digest of the tasks written before the digest columns existed (migration 0011), in
// batches of Repo.backfillBatch in id order, calling pause between batches (it ends the backfill when it returns an
// error). Lists work before and during it: they load the turns of the tasks without a digest. A task that cannot be
// read is passed to skip and keeps no digest (its list entries keep loading its turns, and report the error).
// A task saved meanwhile keeps the digest its save stored. It returns how many digests it stored.
func (r *Repo) BackfillDigests(ctx context.Context, pause func(context.Context) error,
	skip func(id string, err error)) (int, error) {
	stored := 0
	after := ""
	for {
		ids, err := r.undigested(ctx, after)
		if err != nil || len(ids) == 0 {
			return stored, err
		}
		after = ids[len(ids)-1]
		ls := make([]listed, 0, len(ids))
		for _, id := range ids {
			l, err := r.getListed(ctx, id)
			switch {
			case errors.Is(err, ErrNotFound):
			case err != nil && ctx.Err() != nil:
				return stored, ctx.Err()
			case err != nil:
				skip(id, err)
			default:
				ls = append(ls, l)
			}
		}
		n, err := r.storeDigests(ctx, ls)
		stored += n
		if err != nil || len(ids) < r.backfillBatch {
			return stored, err
		}
		if err := pause(ctx); err != nil {
			return stored, err
		}
	}
}

// undigested returns the ids after after (in id order) of up to backfillBatch tasks without a digest.
func (r *Repo) undigested(ctx context.Context, after string) ([]string, error) {
	var ids []string
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE turn_count < 0 AND id > ? ORDER BY id LIMIT ?`,
			after, r.backfillBatch)
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
	})
	return ids, err
}

// storeDigests stores the digests in one transaction, except over a digest a save stored meanwhile.
func (r *Repo) storeDigests(ctx context.Context, ls []listed) (int, error) {
	if len(ls) == 0 {
		return 0, nil
	}
	n := 0
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		n = 0
		for _, l := range ls {
			args := append(l.d.args(), l.t.ID)
			res, err := tx.ExecContext(ctx, `UPDATE tasks SET turn_count = ?, tokens_used = ?, files_changed = ?,
				gate_turn = ?, gate_state = ?, last_account_id = ?, last_account_label = ? WHERE id = ? AND turn_count < 0`,
				args...)
			if err != nil {
				return fmt.Errorf("store the digest of task %s: %w", l.t.ID, err)
			}
			if k, err := res.RowsAffected(); err == nil {
				n += int(k)
			}
		}
		return nil
	})
	return n, err
}

// gateOf is the gate state a task summary shows: the running phase, else the newest gate since the lead's latest
// instruction (nil when none ran).
func gateOf(t *Task, d turnDigest) *api.Gate {
	fixes := 0
	if t.Proof != nil {
		fixes = t.Proof.AutoFixes
	}
	if ph := t.GatePhase; ph != nil {
		g := &api.Gate{State: api.GateRunning, Phase: ph.State, AutoFixes: fixes, Turn: ph.Turn}
		if ph.State == api.GatePhaseQueued {
			g.Wait = ph.Wait
		}
		return g
	}
	if d.gateTurn == 0 {
		return nil
	}
	return &api.Gate{State: d.gateState, AutoFixes: fixes, Turn: d.gateTurn}
}
