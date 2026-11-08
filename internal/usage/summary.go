package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// Caps on the nested summary lists; what is cut is counted in More.
const (
	CapProjects         = 50
	CapTasksPerProject  = 100
	CapTurnsPerTask     = 60
	CapAccounts         = 50
	CapModelsPerAccount = 30
)

// Attempt is the usage of one worker attempt, recorded when it ends.
type Attempt struct {
	TaskID  string
	Turn    int
	Attempt int
	// AccountID is the account the attempt ran on ("" when none was chosen).
	AccountID string
	Backend   string
	Model     string
	// ProjectKey and ProjectName group tasks by project (from the task's working folder).
	ProjectKey  string
	ProjectName string
	StartedAt   time.Time
	EndedAt     time.Time
	// Usage is what the CLI reported; a zero CostUSD counts as "no cost reported".
	Usage event.Usage
}

// RecordAttempt stores the usage of an attempt, replacing an earlier record of the same attempt.
func (s *Store) RecordAttempt(ctx context.Context, a Attempt) error {
	if a.TaskID == "" || a.EndedAt.IsZero() {
		return errors.New("record usage: task id and end time are required")
	}
	started := a.StartedAt
	if started.IsZero() || started.After(a.EndedAt) {
		started = a.EndedAt
	}
	u := a.Usage
	total := u.TotalTokens
	if total <= 0 {
		total = max(0, u.InputTokens) + max(0, u.OutputTokens)
	}
	var cost any
	if u.CostUSD > 0 {
		cost = u.CostUSD
	}
	next := account.SpendRow{AccountID: a.AccountID, StartedAt: formatTime(started), CostUSD: max(0, u.CostUSD),
		Input: int64(max(0, u.InputTokens)), Output: int64(max(0, u.OutputTokens)), Total: int64(total)}
	var crossings []account.BudgetCrossing
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		// The attempt's earlier record, if any: its spend is taken back before the new one is added.
		var prev *account.SpendRow
		var old account.SpendRow
		var oldCost sql.NullFloat64
		err := tx.QueryRowContext(ctx, `SELECT account_id, started_at, cost_usd, input_tokens, output_tokens, total_tokens
			FROM usage_turns WHERE task_id = ? AND turn = ? AND attempt = ?`, a.TaskID, a.Turn, a.Attempt).Scan(
			&old.AccountID, &old.StartedAt, &oldCost, &old.Input, &old.Output, &old.Total)
		switch {
		case err == nil:
			old.CostUSD = max(0, oldCost.Float64)
			prev = &old
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO usage_turns (task_id, turn, attempt, account_id, backend, model,
			project_key, project_name, started_at, ended_at, input_tokens, output_tokens, cached_tokens, total_tokens, cost_usd)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (task_id, turn, attempt) DO UPDATE SET account_id = excluded.account_id,
				backend = excluded.backend, model = excluded.model, project_key = excluded.project_key,
				project_name = excluded.project_name, started_at = excluded.started_at, ended_at = excluded.ended_at,
				input_tokens = excluded.input_tokens, output_tokens = excluded.output_tokens,
				cached_tokens = excluded.cached_tokens, total_tokens = excluded.total_tokens, cost_usd = excluded.cost_usd`,
			a.TaskID, a.Turn, a.Attempt, a.AccountID, a.Backend, a.Model, a.ProjectKey, a.ProjectName,
			formatTime(started), formatTime(a.EndedAt), max(0, u.InputTokens), max(0, u.OutputTokens),
			max(0, u.CachedTokens), total, cost)
		if err != nil {
			return err
		}
		// The running spend of API-key accounts: kept here, in the same transaction, never summed from this table
		// (it is pruned), and whatever the usageHistory setting says.
		crossings, err = account.ApplySpendTx(ctx, tx, prev, next)
		return err
	})
	if err != nil {
		return fmt.Errorf("record usage of %s: %w", a.TaskID, err)
	}
	if s.budget != nil {
		for _, c := range crossings {
			s.budget(ctx, c)
		}
	}
	return nil
}

// TaskInfo is what the summary shows of a task besides its usage.
type TaskInfo struct {
	Title  string
	Status string
}

// Lookups give the summary its labels; both are optional.
type Lookups struct {
	// Accounts lists the accounts (labels and backends); *account.Manager satisfies it.
	Accounts interface {
		List(ctx context.Context) ([]account.Account, error)
	}
	// Tasks returns title and status of the given tasks (missing ones show their id).
	Tasks func(ctx context.Context, ids []string) (map[string]TaskInfo, error)
}

// Totals add up a period.
type Totals struct {
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
	// HasCost is true when some attempt reported a cost.
	HasCost  bool `json:"hasCost"`
	Tasks    int  `json:"tasks"`
	Turns    int  `json:"turns"`
	Attempts int  `json:"attempts"`
}

// DayTotal is one local day of the period.
type DayTotal struct {
	Date    string  `json:"date"`
	Tokens  int64   `json:"tokens"`
	CostUSD float64 `json:"costUsd"`
	Tasks   int     `json:"tasks"`
}

// TurnUsage is one turn of a task. Account and model are those of its last attempt.
type TurnUsage struct {
	N          int       `json:"n"`
	Tokens     int64     `json:"tokens"`
	CostUSD    float64   `json:"costUsd"`
	DurationMS int64     `json:"durationMs"`
	Attempts   int       `json:"attempts"`
	At         time.Time `json:"at"`
	AccountID  string    `json:"accountId"`
	Account    string    `json:"account"`
	Model      string    `json:"model"`
}

// TaskUsage is one task of a project.
type TaskUsage struct {
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Status     string      `json:"status"`
	Backend    string      `json:"backend"`
	Tokens     int64       `json:"tokens"`
	CostUSD    float64     `json:"costUsd"`
	DurationMS int64       `json:"durationMs"`
	LastAt     time.Time   `json:"lastAt"`
	AccountID  string      `json:"accountId"`
	Account    string      `json:"account"`
	Turns      int         `json:"turns"`
	Items      []TurnUsage `json:"items"`
	More       int         `json:"more"`
}

// ProjectUsage is one project.
type ProjectUsage struct {
	Key        string      `json:"key"`
	Name       string      `json:"name"`
	Tokens     int64       `json:"tokens"`
	CostUSD    float64     `json:"costUsd"`
	DurationMS int64       `json:"durationMs"`
	Tasks      int         `json:"tasks"`
	Turns      int         `json:"turns"`
	Items      []TaskUsage `json:"items"`
	More       int         `json:"more"`
}

// ModelUsage is one model of an account.
type ModelUsage struct {
	Model      string  `json:"model"`
	Tokens     int64   `json:"tokens"`
	CostUSD    float64 `json:"costUsd"`
	Attempts   int     `json:"attempts"`
	DurationMS int64   `json:"durationMs"`
}

// AccountUsage is one account.
type AccountUsage struct {
	ID         string       `json:"id"`
	Label      string       `json:"label"`
	Backend    string       `json:"backend"`
	Tokens     int64        `json:"tokens"`
	CostUSD    float64      `json:"costUsd"`
	Attempts   int          `json:"attempts"`
	DurationMS int64        `json:"durationMs"`
	Models     []ModelUsage `json:"models"`
	More       int          `json:"more"`
}

// Summary is the usage of the last Days local days (today included) and of the Days before.
type Summary struct {
	Days          int       `json:"days"`
	Since         time.Time `json:"since"`
	Until         time.Time `json:"until"`
	PreviousSince time.Time `json:"previousSince"`
	// DataSince is the oldest attempt kept (zero when none).
	DataSince    time.Time      `json:"dataSince,omitzero"`
	Totals       Totals         `json:"totals"`
	Previous     Totals         `json:"previous"`
	Daily        []DayTotal     `json:"daily"`
	Projects     []ProjectUsage `json:"projects"`
	MoreProjects int            `json:"moreProjects"`
	Accounts     []AccountUsage `json:"accounts"`
	MoreAccounts int            `json:"moreAccounts"`
}

// SummaryDays checks a requested period: 0 gives SummaryDefaultDays, more than MaxDays gives MaxDays, a negative
// value gives an error matching ErrInvalid.
func SummaryDays(days int) (int, error) {
	switch {
	case days == 0:
		return SummaryDefaultDays, nil
	case days < 0:
		return 0, fmt.Errorf("%w: days must be a number from 1 to %d", ErrInvalid, MaxDays)
	}
	return min(days, MaxDays), nil
}

// attemptRow is one usage_turns row.
type attemptRow struct {
	taskID, accountID, backend, model, projectKey, projectName string
	turn                                                       int
	started, ended                                             time.Time
	tokens                                                     int64
	cost                                                       sql.NullFloat64
}

func (r *attemptRow) duration() int64 {
	if r.ended.Before(r.started) {
		return 0
	}
	return r.ended.Sub(r.started).Milliseconds()
}

// Summary adds up the attempts that ended in the last days local days (see SummaryDays) and in the days before.
// Attempts are attributed to the local day they ended.
func (s *Store) Summary(ctx context.Context, days int, look Lookups) (Summary, error) {
	n, err := SummaryDays(days)
	if err != nil {
		return Summary{}, err
	}
	now := s.clock.Now()
	start := addDays(startOfDay(now, s.loc), -(n - 1))
	prevStart := addDays(start, -n)
	out := Summary{Days: n, Since: start.UTC(), Until: now.UTC(), PreviousSince: prevStart.UTC()}

	rows, dataSince, err := s.attempts(ctx, prevStart, now)
	if err != nil {
		return Summary{}, err
	}
	out.DataSince = dataSince
	labels, err := accountLabels(ctx, look)
	if err != nil {
		return Summary{}, err
	}
	b := newSummaryBuilder(n, start, s.loc, labels)
	prevTasks := map[string]bool{}
	prevTurns := map[string]bool{}
	for i := range rows {
		r := &rows[i]
		if r.ended.Before(start) {
			b.previous.Tokens += r.tokens
			if r.cost.Valid {
				b.previous.CostUSD += r.cost.Float64
				b.previous.HasCost = true
			}
			b.previous.Attempts++
			prevTasks[r.taskID] = true
			prevTurns[fmt.Sprintf("%s\x00%d", r.taskID, r.turn)] = true
			continue
		}
		b.add(r)
	}
	b.previous.Tasks, b.previous.Turns = len(prevTasks), len(prevTurns)
	if err := b.finish(ctx, &out, look); err != nil {
		return Summary{}, err
	}
	return out, nil
}

// attempts reads the attempts that ended in [from, to], oldest first, and the end of the oldest attempt kept.
func (s *Store) attempts(ctx context.Context, from, to time.Time) (rows []attemptRow, dataSince time.Time, err error) {
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		var first sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT min(ended_at) FROM usage_turns").Scan(&first); err != nil {
			return err
		}
		dataSince, _ = parseTime(first.String)
		q, err := tx.QueryContext(ctx, `SELECT task_id, turn, account_id, backend, model, project_key, project_name,
			started_at, ended_at, total_tokens, cost_usd FROM usage_turns WHERE ended_at >= ? AND ended_at <= ?
			ORDER BY ended_at, task_id, turn, attempt`, formatTime(from), formatTime(to))
		if err != nil {
			return err
		}
		defer func() { _ = q.Close() }()
		for q.Next() {
			var r attemptRow
			var started, ended string
			if err := q.Scan(&r.taskID, &r.turn, &r.accountID, &r.backend, &r.model, &r.projectKey, &r.projectName,
				&started, &ended, &r.tokens, &r.cost); err != nil {
				return err
			}
			var ok bool
			if r.ended, ok = parseTime(ended); !ok {
				continue
			}
			if r.started, ok = parseTime(started); !ok {
				r.started = r.ended
			}
			r.tokens = max(0, r.tokens)
			if r.cost.Valid && r.cost.Float64 < 0 {
				r.cost.Float64 = 0
			}
			rows = append(rows, r)
		}
		return q.Err()
	})
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("read usage: %w", err)
	}
	return rows, dataSince, nil
}

type accountLabel struct{ label, backend string }

func accountLabels(ctx context.Context, look Lookups) (map[string]accountLabel, error) {
	out := map[string]accountLabel{}
	if look.Accounts == nil {
		return out, nil
	}
	list, err := look.Accounts.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}
	for i := range list {
		out[list[i].ID] = accountLabel{SafeLabel(list[i].Label, list[i].ID), list[i].Backend}
	}
	return out, nil
}

// summaryBuilder accumulates the attempts of the period.
type summaryBuilder struct {
	loc      *time.Location
	labels   map[string]accountLabel
	totals   Totals
	previous Totals
	daily    []DayTotal
	dayIndex map[string]int
	dayTasks []map[string]bool
	projects map[string]*projectAcc
	tasks    map[string]*taskAcc
	accounts map[string]*accountAcc
}

type projectAcc struct {
	ProjectUsage
	tasks []*taskAcc
}

type taskAcc struct {
	TaskUsage
	project *projectAcc // the project the task was first counted in
	turns   map[int]*TurnUsage
}

type accountAcc struct {
	AccountUsage
	models map[string]*ModelUsage
}

func newSummaryBuilder(days int, start time.Time, loc *time.Location, labels map[string]accountLabel) *summaryBuilder {
	b := &summaryBuilder{loc: loc, labels: labels, dayIndex: map[string]int{}, projects: map[string]*projectAcc{},
		tasks: map[string]*taskAcc{}, accounts: map[string]*accountAcc{}}
	for i := range days {
		date := addDays(start, i).Format(time.DateOnly)
		b.dayIndex[date] = i
		b.daily = append(b.daily, DayTotal{Date: date})
		b.dayTasks = append(b.dayTasks, map[string]bool{})
	}
	return b
}

func (b *summaryBuilder) label(id string) accountLabel {
	if l, ok := b.labels[id]; ok {
		return l
	}
	return accountLabel{label: id}
}

// add counts one attempt of the period (rows come oldest first).
func (b *summaryBuilder) add(r *attemptRow) {
	cost := 0.0
	if r.cost.Valid {
		cost = r.cost.Float64
		b.totals.HasCost = true
	}
	dur := r.duration()
	b.totals.Tokens += r.tokens
	b.totals.CostUSD += cost
	b.totals.Attempts++
	if i, ok := b.dayIndex[dateOf(r.ended, b.loc)]; ok {
		b.daily[i].Tokens += r.tokens
		b.daily[i].CostUSD += cost
		b.dayTasks[i][r.taskID] = true
	}
	acct := b.label(r.accountID)

	t, ok := b.tasks[r.taskID]
	if !ok {
		p, ok := b.projects[r.projectKey]
		if !ok {
			p = &projectAcc{ProjectUsage: ProjectUsage{Key: r.projectKey, Name: r.projectName}}
			b.projects[r.projectKey] = p
		}
		t = &taskAcc{TaskUsage: TaskUsage{ID: r.taskID, Title: r.taskID}, project: p, turns: map[int]*TurnUsage{}}
		b.tasks[r.taskID] = t
		p.tasks = append(p.tasks, t)
		p.Tasks++
		b.totals.Tasks++
	}
	p := t.project
	turn, ok := t.turns[r.turn]
	if !ok {
		turn = &TurnUsage{N: r.turn}
		t.turns[r.turn] = turn
		p.Turns++
		b.totals.Turns++
	}
	turn.Tokens += r.tokens
	turn.CostUSD += cost
	turn.DurationMS += dur
	turn.Attempts++
	turn.At, turn.AccountID, turn.Account, turn.Model = r.ended.UTC(), r.accountID, acct.label, r.model
	t.Tokens += r.tokens
	t.CostUSD += cost
	t.DurationMS += dur
	t.LastAt, t.AccountID, t.Account, t.Backend = r.ended.UTC(), r.accountID, acct.label, r.backend
	p.Tokens += r.tokens
	p.CostUSD += cost
	p.DurationMS += dur

	a, ok := b.accounts[r.accountID]
	if !ok {
		backend := acct.backend
		if backend == "" {
			backend = r.backend
		}
		a = &accountAcc{AccountUsage: AccountUsage{ID: r.accountID, Label: acct.label, Backend: backend},
			models: map[string]*ModelUsage{}}
		b.accounts[r.accountID] = a
	}
	a.Tokens += r.tokens
	a.CostUSD += cost
	a.Attempts++
	a.DurationMS += dur
	m, ok := a.models[r.model]
	if !ok {
		m = &ModelUsage{Model: r.model}
		a.models[r.model] = m
	}
	m.Tokens += r.tokens
	m.CostUSD += cost
	m.Attempts++
	m.DurationMS += dur
}

// byUsage orders by tokens, then duration (both most first), then key.
func byUsage(tokA, tokB, durA, durB int64, keyA, keyB string) bool {
	switch {
	case tokA != tokB:
		return tokA > tokB
	case durA != durB:
		return durA > durB
	}
	return keyA < keyB
}

// finish sorts, caps and rounds into out.
func (b *summaryBuilder) finish(ctx context.Context, out *Summary, look Lookups) error {
	if look.Tasks != nil && len(b.tasks) > 0 {
		ids := make([]string, 0, len(b.tasks))
		for id := range b.tasks {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		info, err := look.Tasks(ctx, ids)
		if err != nil {
			return fmt.Errorf("usage summary: %w", err)
		}
		for id, t := range b.tasks {
			if in, ok := info[id]; ok {
				if in.Title != "" {
					t.Title = in.Title
				}
				t.Status = in.Status
			}
		}
	}
	b.totals.CostUSD = roundCost(b.totals.CostUSD)
	b.previous.CostUSD = roundCost(b.previous.CostUSD)
	out.Totals, out.Previous = b.totals, b.previous
	out.Daily = make([]DayTotal, len(b.daily))
	for i, d := range b.daily {
		d.CostUSD = roundCost(d.CostUSD)
		d.Tasks = len(b.dayTasks[i])
		out.Daily[i] = d
	}

	projects := make([]*projectAcc, 0, len(b.projects))
	for _, p := range b.projects {
		projects = append(projects, p)
	}
	sort.Slice(projects, func(i, j int) bool {
		x, y := projects[i], projects[j]
		return byUsage(x.Tokens, y.Tokens, x.DurationMS, y.DurationMS, x.Key, y.Key)
	})
	out.Projects = []ProjectUsage{}
	for i, p := range projects {
		if i >= CapProjects {
			out.MoreProjects = len(projects) - CapProjects
			break
		}
		out.Projects = append(out.Projects, p.finish())
	}

	accounts := make([]*accountAcc, 0, len(b.accounts))
	for _, a := range b.accounts {
		accounts = append(accounts, a)
	}
	sort.Slice(accounts, func(i, j int) bool {
		x, y := accounts[i], accounts[j]
		return byUsage(x.Tokens, y.Tokens, x.DurationMS, y.DurationMS, x.ID, y.ID)
	})
	out.Accounts = []AccountUsage{}
	for i, a := range accounts {
		if i >= CapAccounts {
			out.MoreAccounts = len(accounts) - CapAccounts
			break
		}
		out.Accounts = append(out.Accounts, a.finish())
	}
	return nil
}

func (p *projectAcc) finish() ProjectUsage {
	out := p.ProjectUsage
	out.CostUSD = roundCost(out.CostUSD)
	tasks := slices.Clone(p.tasks)
	sort.Slice(tasks, func(i, j int) bool {
		x, y := tasks[i], tasks[j]
		if x.Tokens != y.Tokens || x.DurationMS != y.DurationMS {
			return byUsage(x.Tokens, y.Tokens, x.DurationMS, y.DurationMS, "", "")
		}
		if !x.LastAt.Equal(y.LastAt) {
			return x.LastAt.After(y.LastAt)
		}
		return x.ID < y.ID
	})
	out.Items = []TaskUsage{}
	for i, t := range tasks {
		if i >= CapTasksPerProject {
			out.More = len(tasks) - CapTasksPerProject
			break
		}
		out.Items = append(out.Items, t.finish())
	}
	return out
}

func (t *taskAcc) finish() TaskUsage {
	out := t.TaskUsage
	out.CostUSD = roundCost(out.CostUSD)
	out.Turns = len(t.turns)
	ns := make([]int, 0, len(t.turns))
	for n := range t.turns {
		ns = append(ns, n)
	}
	sort.Ints(ns)
	out.Items = []TurnUsage{}
	for i, n := range ns {
		if i >= CapTurnsPerTask {
			out.More = len(ns) - CapTurnsPerTask
			break
		}
		turn := *t.turns[n]
		turn.CostUSD = roundCost(turn.CostUSD)
		out.Items = append(out.Items, turn)
	}
	return out
}

func (a *accountAcc) finish() AccountUsage {
	out := a.AccountUsage
	out.CostUSD = roundCost(out.CostUSD)
	models := make([]*ModelUsage, 0, len(a.models))
	for _, m := range a.models {
		models = append(models, m)
	}
	sort.Slice(models, func(i, j int) bool {
		x, y := models[i], models[j]
		return byUsage(x.Tokens, y.Tokens, x.DurationMS, y.DurationMS, x.Model, y.Model)
	})
	out.Models = []ModelUsage{}
	for i, m := range models {
		if i >= CapModelsPerAccount {
			out.More = len(models) - CapModelsPerAccount
			break
		}
		mu := *m
		mu.CostUSD = roundCost(mu.CostUSD)
		out.Models = append(out.Models, mu)
	}
	return out
}
