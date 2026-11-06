package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

var testEpoch = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)

func newTestTask(id string, created time.Time) *Task {
	return &Task{
		ID: id, Title: "title " + id, Status: StatusQueued, Kind: KindNormal, Mode: ModeEdit,
		Cwd: `D:\proj`, ProjectKey: "proj-1234", ProjectName: "proj", Prompt: "do it", Backend: "agy",
		Model: "m", CreatedAt: created, UpdatedAt: created,
		Turns: []*Turn{{N: 1, By: ByLead, Message: "do it", Status: TurnRunning, StartedAt: created}},
	}
}

func TestRepoCreateGetSave(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), func() time.Time { return testEpoch })
	tk := newTestTask("t1", testEpoch)
	tk.After = []string{"t0"}
	evs, err := r.Create(ctx, tk, []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
		Data: api.LeadMessageData{Message: "do it", By: ByLead}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Seq != 1 || tk.LastEventSeq != 1 {
		t.Fatalf("events %+v, last %d", evs, tk.LastEventSeq)
	}
	if _, err := r.Create(ctx, tk, nil); err == nil {
		t.Fatal("duplicate id accepted")
	}

	tu := tk.Turns[0]
	tu.Attempts = append(tu.Attempts, &Attempt{N: 1, AccountID: "acc1", Backend: "agy", Status: AttemptRunning,
		StartedAt: testEpoch, Prompt: "the brief", LaunchDir: `C:\l`})
	tk.Status = StatusRunning
	if _, err := r.Save(ctx, tk, nil, []NewEvent{{Type: api.EventRoute, Turn: 1, Attempt: 1,
		Data: api.RouteData{AccountID: "acc1"}}}); err != nil {
		t.Fatal(err)
	}
	// The prompt is written once: a later save with another prompt does not change it.
	tu.Attempts[0].Prompt = "changed"
	tu.Attempts[0].StreamOffset = 42
	tu.Attempts[0].Usage = &event.Usage{InputTokens: 5, OutputTokens: 6}
	if _, err := r.Append(ctx, tk, tu, tu.Attempts[0], []NewEvent{{Type: api.EventWorker, Turn: 1, Attempt: 1,
		Data: event.Event{Kind: event.KindMessage, Text: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusRunning || got.LastEventSeq != 3 || len(got.Turns) != 1 ||
		len(got.Turns[0].Attempts) != 1 || got.After[0] != "t0" || !got.CreatedAt.Equal(testEpoch) {
		t.Fatalf("got %+v", got)
	}
	a := got.Turns[0].Attempts[0]
	if a.StreamOffset != 42 || a.Prompt != "" || a.LaunchDir != `C:\l` || got.TokensUsed() != 11 {
		t.Fatalf("attempt %+v tokens %d", a, got.TokensUsed())
	}
	if p, err := r.Prompt(ctx, "t1", 1, 0); err != nil || p != "the brief" {
		t.Fatalf("Prompt = %q, %v", p, err)
	}
	if p, err := r.Prompt(ctx, "t1", 1, 1); err != nil || p != "the brief" {
		t.Fatalf("Prompt(1) = %q, %v", p, err)
	}
	if _, err := r.Prompt(ctx, "t1", 2, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Prompt(turn 2) = %v", err)
	}
	if _, err := r.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(nope) = %v", err)
	}
	if _, err := r.Save(ctx, &Task{ID: "nope"}, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Save(nope) = %v", err)
	}
	if _, err := r.Append(ctx, &Task{ID: "nope"}, nil, nil, []NewEvent{{Type: api.EventCancel}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Append(nope) = %v", err)
	}
}

func TestRepoEventsAndTail(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), nil)
	tk := newTestTask("t1", testEpoch)
	if _, err := r.Create(ctx, tk, nil); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		if _, err := r.Append(ctx, tk, nil, nil, []NewEvent{{Type: api.EventError,
			Data: api.ErrorData{Message: fmt.Sprint(i)}, TS: testEpoch.Add(time.Duration(i) * time.Second)}}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := r.Events(ctx, "t1", 3, 4)
	if err != nil || len(evs) != 4 || evs[0].Seq != 4 || evs[3].Seq != 7 {
		t.Fatalf("Events = %+v, %v", evs, err)
	}
	var d api.ErrorData
	if err := json.Unmarshal(evs[0].Data.(json.RawMessage), &d); err != nil || d.Message != "3" {
		t.Fatalf("data %+v %v", d, err)
	}
	if evs[0].TS != "2026-11-07T09:00:03.000Z" || evs[0].TaskID != "t1" || evs[0].Type != api.EventError {
		t.Fatalf("event %+v", evs[0])
	}
	tail, err := r.Tail(ctx, "t1", 3)
	if err != nil || len(tail) != 3 || tail[0].Seq != 8 || tail[2].Seq != 10 {
		t.Fatalf("Tail = %+v, %v", tail, err)
	}
	got, _ := r.Get(ctx, "t1")
	if got.LastEventSeq != 10 || !got.LastEventAt.Equal(testEpoch.Add(9*time.Second)) {
		t.Fatalf("last %d at %v", got.LastEventSeq, got.LastEventAt)
	}
}

// Concurrent appends never share or skip a seq.
func TestRepoSeqIsMonotonic(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), nil)
	if _, err := r.Create(ctx, newTestTask("t1", testEpoch), nil); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			tk := &Task{ID: "t1"}
			for range 10 {
				if _, err := r.Append(ctx, tk, nil, nil, []NewEvent{{Type: api.EventCancel}, {Type: api.EventCancel}}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	evs, err := r.Events(ctx, "t1", 0, 1000)
	if err != nil || len(evs) != 160 {
		t.Fatalf("%d events, %v", len(evs), err)
	}
	for i, e := range evs {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d", i, e.Seq)
		}
	}
}

func TestRepoListPaging(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), nil)
	// Five tasks share one timestamp: the cursor must neither repeat nor drop them.
	for i := range 12 {
		created := testEpoch.Add(time.Duration(i/5) * time.Minute)
		tk := newTestTask(fmt.Sprintf("t%02d", i), created)
		if i%3 == 0 {
			tk.Status = StatusDone
		}
		if i == 7 {
			tk.Title = "Fix the 100% bug"
			tk.ProjectKey = "other-1"
		}
		if _, err := r.Create(ctx, tk, nil); err != nil {
			t.Fatal(err)
		}
	}
	var seen []string
	cursor := ""
	for range 10 {
		page, next, err := r.List(ctx, Query{Limit: 5, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range page {
			seen = append(seen, tk.ID)
			if len(tk.Turns) != 1 {
				t.Fatalf("%s has %d turns", tk.ID, len(tk.Turns))
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	want := []string{"t11", "t10", "t09", "t08", "t07", "t06", "t05", "t04", "t03", "t02", "t01", "t00"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("pages = %v", seen)
	}
	done, _, err := r.List(ctx, Query{Statuses: []Status{StatusDone}})
	if err != nil || len(done) != 4 {
		t.Fatalf("done = %d, %v", len(done), err)
	}
	if got, _, _ := r.List(ctx, Query{Text: "100%"}); len(got) != 1 || got[0].ID != "t07" {
		t.Fatalf("text search = %v", got)
	}
	if got, _, _ := r.List(ctx, Query{Text: "T03"}); len(got) != 1 {
		t.Fatalf("id search = %v", got)
	}
	if got, _, _ := r.List(ctx, Query{ProjectKey: "other-1"}); len(got) != 1 {
		t.Fatalf("project filter = %v", got)
	}
	if _, _, err := r.List(ctx, Query{Cursor: "%%%"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad cursor = %v", err)
	}
	if _, _, err := r.List(ctx, Query{Statuses: []Status{"bogus"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad status = %v", err)
	}
	if _, _, err := r.List(ctx, Query{Limit: 201}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("big limit = %v", err)
	}
	ids, err := r.IDs(ctx, StatusDone)
	if err != nil || fmt.Sprint(ids) != "[t00 t03 t06 t09]" {
		t.Fatalf("IDs = %v, %v", ids, err)
	}
}

// A task whose stored record cannot be read is an error, never silently skipped.
func TestRepoUnreadableIsError(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	r := NewRepo(st, nil)
	if _, err := r.Create(ctx, newTestTask("t1", testEpoch), nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE turns SET data = 'nope'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "t1"); err == nil {
		t.Fatal("unreadable turn accepted")
	}
	if _, _, err := r.List(ctx, Query{}); err == nil {
		t.Fatal("unreadable turn accepted by List")
	}
}
