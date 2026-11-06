package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/event"
)

type accountList []account.Account

func (l accountList) List(context.Context) ([]account.Account, error) { return l, nil }

type failingList struct{}

func (failingList) List(context.Context) ([]account.Account, error) {
	return nil, errors.New("db down")
}

func TestSummaryDays(t *testing.T) {
	tests := []struct {
		in, want int
		bad      bool
	}{{0, SummaryDefaultDays, false}, {1, 1, false}, {30, 30, false}, {90, 30, false}, {-1, 0, true}}
	for _, tt := range tests {
		got, err := SummaryDays(tt.in)
		if got != tt.want || (err != nil) != tt.bad || (tt.bad && !errors.Is(err, ErrInvalid)) {
			t.Errorf("SummaryDays(%d) = %d, %v", tt.in, got, err)
		}
	}
}

func TestRecordAttempt(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.st.RecordAttempt(ctx, Attempt{TaskID: "t"}); err == nil {
		t.Fatal("attempt without end time recorded")
	}
	// Total from input + output; no cost; a start after the end is clamped; recording again replaces.
	f.record(Attempt{TaskID: "t", Turn: 1, StartedAt: t0.Add(time.Hour), EndedAt: t0,
		Usage: event.Usage{InputTokens: 10, OutputTokens: 5}})
	f.record(Attempt{TaskID: "t", Turn: 1, StartedAt: t0.Add(-time.Minute), EndedAt: t0,
		Usage: event.Usage{InputTokens: 10, OutputTokens: 5, CachedTokens: 3}})
	var total, cached int64
	var cost sql.NullFloat64
	err := f.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT total_tokens, cached_tokens, cost_usd FROM usage_turns").Scan(&total, &cached, &cost)
	})
	if err != nil || total != 15 || cached != 3 || cost.Valid || f.count("usage_turns") != 1 {
		t.Fatalf("row %d %d %v, %v", total, cached, cost, err)
	}
}

func TestSummary(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	now := f.clk.Now()
	day := 24 * time.Hour
	at := now.Add
	attempts := []Attempt{
		// Project alpha, task a1: turn 1 failed on acc1 and was retried on acc2; turn 2 on acc2.
		{TaskID: "a1", Turn: 1, Attempt: 1, AccountID: "acc1", Backend: "agy", Model: "gemini-flash", ProjectKey: "alpha",
			ProjectName: "Alpha", StartedAt: at(-3*time.Hour - time.Minute), EndedAt: at(-3 * time.Hour),
			Usage: event.Usage{TotalTokens: 100}},
		{TaskID: "a1", Turn: 1, Attempt: 2, AccountID: "acc2", Backend: "agy", Model: "gemini-pro", ProjectKey: "alpha",
			ProjectName: "Alpha", StartedAt: at(-2*time.Hour - 2*time.Minute), EndedAt: at(-2 * time.Hour),
			Usage: event.Usage{TotalTokens: 300, CostUSD: 0.1}},
		{TaskID: "a1", Turn: 2, Attempt: 1, AccountID: "acc2", Backend: "agy", Model: "gemini-pro", ProjectKey: "alpha",
			ProjectName: "Alpha", StartedAt: at(-time.Hour - time.Minute), EndedAt: at(-time.Hour),
			Usage: event.Usage{TotalTokens: 50, CostUSD: 0.2}},
		// Project beta, task b1 two days ago on a removed account.
		{TaskID: "b1", Turn: 1, AccountID: "gone", Backend: "codex", Model: "gpt-x", ProjectKey: "beta", ProjectName: "Beta",
			StartedAt: at(-2*day - time.Minute), EndedAt: at(-2 * day), Usage: event.Usage{TotalTokens: 1000}},
		// The previous period (days 8..14 ago) and older.
		{TaskID: "p1", Turn: 1, AccountID: "acc1", Backend: "agy", EndedAt: at(-8 * day), Usage: event.Usage{TotalTokens: 7, CostUSD: 0.5}},
		{TaskID: "p1", Turn: 2, AccountID: "acc1", Backend: "agy", EndedAt: at(-9 * day), Usage: event.Usage{TotalTokens: 3}},
		{TaskID: "old", Turn: 1, AccountID: "acc1", Backend: "agy", EndedAt: at(-20 * day), Usage: event.Usage{TotalTokens: 9}},
		// In the future: not counted.
		{TaskID: "future", Turn: 1, EndedAt: at(time.Hour), Usage: event.Usage{TotalTokens: 5}},
	}
	for _, a := range attempts {
		f.record(a)
	}
	look := Lookups{
		Accounts: accountList{
			{ID: "acc1", Backend: "agy", Label: "First"},
			{ID: "acc2", Backend: "agy", Label: "me@example.com"},
		},
		Tasks: func(context.Context, []string) (map[string]TaskInfo, error) {
			return map[string]TaskInfo{"a1": {Title: "Build it", Status: "done"}}, nil
		},
	}
	s, err := f.st.Summary(ctx, 7, look)
	if err != nil {
		t.Fatal(err)
	}
	wantSince := time.Date(2026, 10, 31, 0, 0, 0, 0, zone)
	if s.Days != 7 || !s.Since.Equal(wantSince) || !s.PreviousSince.Equal(wantSince.AddDate(0, 0, -7)) ||
		!s.DataSince.Equal(at(-20*day).UTC()) || !s.Until.Equal(now) {
		t.Fatalf("period %+v", s)
	}
	if s.Totals != (Totals{Tokens: 1450, CostUSD: 0.3, HasCost: true, Tasks: 2, Turns: 3, Attempts: 4}) {
		t.Fatalf("totals %+v", s.Totals)
	}
	if s.Previous != (Totals{Tokens: 10, CostUSD: 0.5, HasCost: true, Tasks: 1, Turns: 2, Attempts: 2}) {
		t.Fatalf("previous %+v", s.Previous)
	}
	if len(s.Daily) != 7 || s.Daily[6].Date != "2026-11-06" || s.Daily[6].Tokens != 450 || s.Daily[6].Tasks != 1 ||
		s.Daily[4].Tokens != 1000 || s.Daily[6].CostUSD != 0.3 {
		t.Fatalf("daily %+v", s.Daily)
	}
	if len(s.Projects) != 2 || s.Projects[0].Key != "beta" || s.Projects[1].Name != "Alpha" || s.Projects[1].Turns != 2 {
		t.Fatalf("projects %+v", s.Projects)
	}
	task := s.Projects[1].Items[0]
	if task.Title != "Build it" || task.Status != "done" || task.Tokens != 450 || task.Turns != 2 ||
		task.AccountID != "acc2" || task.Account != "acc2" || task.DurationMS != 4*60*1000 ||
		!task.LastAt.Equal(at(-time.Hour).UTC()) {
		t.Fatalf("task %+v", task)
	}
	if t1 := task.Items[0]; t1.N != 1 || t1.Attempts != 2 || t1.Tokens != 400 || t1.Model != "gemini-pro" || t1.AccountID != "acc2" {
		t.Fatalf("turn 1 %+v", t1)
	}
	if b1 := s.Projects[0].Items[0]; b1.Title != "b1" || b1.Account != "gone" || b1.Backend != "codex" {
		t.Fatalf("task without info %+v", b1)
	}
	if len(s.Accounts) != 3 || s.Accounts[0].ID != "gone" || s.Accounts[0].Backend != "codex" ||
		s.Accounts[1].ID != "acc2" || s.Accounts[1].Label != "acc2" || s.Accounts[2].Label != "First" ||
		len(s.Accounts[1].Models) != 1 || s.Accounts[1].Models[0].Attempts != 2 || s.Accounts[1].CostUSD != 0.3 {
		t.Fatalf("accounts %+v", s.Accounts)
	}

	// One day: only today; the previous period is yesterday.
	s, err = f.st.Summary(ctx, 1, Lookups{})
	if err != nil || s.Totals.Tokens != 450 || s.Previous.Tokens != 0 || len(s.Daily) != 1 {
		t.Fatalf("one day %+v, %v", s.Totals, err)
	}
	if _, err := f.st.Summary(ctx, -2, Lookups{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad days: %v", err)
	}
	if _, err := f.st.Summary(ctx, 7, Lookups{Accounts: failingList{}}); err == nil {
		t.Fatal("account lookup error ignored")
	}
	failTasks := Lookups{Tasks: func(context.Context, []string) (map[string]TaskInfo, error) { return nil, errors.New("x") }}
	if _, err := f.st.Summary(ctx, 7, failTasks); err == nil {
		t.Fatal("task lookup error ignored")
	}
}

func TestSummaryEmptyAndCaps(t *testing.T) {
	f := newFixture(t)
	s, err := f.st.Summary(t.Context(), 0, Lookups{})
	if err != nil || s.Days != SummaryDefaultDays || s.Projects == nil || s.Accounts == nil || !s.DataSince.IsZero() ||
		s.Totals.HasCost {
		t.Fatalf("empty summary %+v, %v", s, err)
	}
	// The caps, through the builder (no database needed).
	start := time.Date(2026, 11, 6, 0, 0, 0, 0, zone)
	b := newSummaryBuilder(1, start, zone, nil)
	end := start.Add(time.Hour)
	for p := range CapProjects + 2 {
		r := attemptRow{taskID: fmt.Sprintf("p%d", p), projectKey: fmt.Sprintf("p%02d", p), accountID: fmt.Sprintf("a%02d", p),
			started: end, ended: end, tokens: 1}
		b.add(&r)
	}
	for i := range CapTasksPerProject + 3 {
		r := attemptRow{taskID: fmt.Sprintf("t%03d", i), projectKey: "big", accountID: "a00", started: end, ended: end,
			tokens: 10, model: fmt.Sprintf("m%02d", i%(CapModelsPerAccount+4))}
		b.add(&r)
	}
	for n := range CapTurnsPerTask + 5 {
		r := attemptRow{taskID: "t000", turn: n + 1, projectKey: "big", accountID: "a00", started: end, ended: end}
		b.add(&r)
	}
	var out Summary
	if err := b.finish(t.Context(), &out, Lookups{}); err != nil {
		t.Fatal(err)
	}
	if len(out.Projects) != CapProjects || out.MoreProjects != 3 || out.Projects[0].Key != "big" {
		t.Fatalf("projects %d, more %d", len(out.Projects), out.MoreProjects)
	}
	big := out.Projects[0]
	if len(big.Items) != CapTasksPerProject || big.More != 3 || big.Items[0].ID != "t000" {
		t.Fatalf("tasks %d, more %d", len(big.Items), big.More)
	}
	if turns := big.Items[0]; len(turns.Items) != CapTurnsPerTask || turns.More != 6 || turns.Turns != CapTurnsPerTask+6 {
		t.Fatalf("turns %d, more %d", len(turns.Items), turns.More)
	}
	if len(out.Accounts) != CapAccounts || out.MoreAccounts != 2 || out.Accounts[0].More != 5 || // 34 models plus ""

		len(out.Accounts[0].Models) != CapModelsPerAccount {
		t.Fatalf("accounts %d, more %d", len(out.Accounts), out.MoreAccounts)
	}
}
