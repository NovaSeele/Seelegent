package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
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

// A turn that has not started an attempt has no prompt yet: a clear not-found, distinct from a missing turn.
func TestRepoPromptBeforeAttempt(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), func() time.Time { return testEpoch })
	if _, err := r.Create(ctx, newTestTask("t1", testEpoch), nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		turn, n int
		want    string
	}{
		{1, 0, "turn 1 of task t1 has not started an attempt yet"},
		{1, 2, "turn 1 of task t1 has no attempt 2"},
		{2, 0, "task t1 has no turn 2"},
	} {
		_, err := r.Prompt(ctx, "t1", c.turn, c.n)
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Prompt(%d, %d) = %v, want %q", c.turn, c.n, err, c.want)
		}
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
	tu.Attempts[0].Usage = &event.Usage{InputTokens: 5, OutputTokens: 6}
	if _, err := r.Save(ctx, tk, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Append(ctx, "t1", []NewEvent{{Type: api.EventWorker, Turn: 1, Attempt: 1,
		Data: event.Event{Kind: event.KindMessage, Text: "hi"}}}, &Offsets{Turn: 1, Attempt: 1, Stream: 42, Stderr: 7}); err != nil {
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
	if a.StreamOffset != 42 || a.StderrOffset != 7 || a.Prompt != "" || a.LaunchDir != `C:\l` || got.TokensUsed() != 11 {
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
	if _, err := r.Append(ctx, "nope", []NewEvent{{Type: api.EventCancel}}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Append(nope) = %v", err)
	}
	if _, err := r.Append(ctx, "t1", nil, &Offsets{Turn: 9, Attempt: 1}); !errors.Is(err, ErrNotFound) {
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
		if _, err := r.Append(ctx, tk.ID, []NewEvent{{Type: api.EventError,
			Data: api.ErrorData{Message: fmt.Sprint(i)}, TS: testEpoch.Add(time.Duration(i) * time.Second)}}, nil); err != nil {
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
			for range 10 {
				if _, err := r.Append(ctx, "t1", []NewEvent{{Type: api.EventCancel}, {Type: api.EventCancel}}, nil); err != nil {
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
		page, next, err := r.listPage(ctx, Query{Limit: 5, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range page {
			seen = append(seen, l.t.ID)
			// A page carries the digest of the turns, not the turns.
			if len(l.t.Turns) != 0 || !l.d.known || l.d.turns != 1 {
				t.Fatalf("%s: %d turns loaded, digest %+v", l.t.ID, len(l.t.Turns), l.d)
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
	// A page reads the turns only of a task without a digest (stored before the digest columns existed).
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET turn_count = -1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.List(ctx, Query{}); err == nil {
		t.Fatal("unreadable turn accepted by List")
	}
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET data = 'nope', turn_count = 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.List(ctx, Query{}); err == nil {
		t.Fatal("unreadable task accepted by List")
	}
}

func TestFoldText(t *testing.T) {
	if got := foldText("Sửa lỗi ĐĂNG NHẬP – Tiếng Việt, Ångström Çà"); got != "sua loi dang nhap – tieng viet, angstrom ca" {
		t.Fatalf("foldText = %q", got)
	}
	// Decomposed (NFD) text folds like the precomposed form.
	if got := foldText("Tie\u0302\u0301ng Vie\u0323\u0302t I\u0307stanbul İzmir"); got != "tieng viet istanbul izmir" {
		t.Fatalf("foldText NFD = %q", got)
	}
}

// TestRepoSearchFolds finds Vietnamese titles whatever the case and accents of the search, also for rows written
// before the folded column existed (BackfillFold).
func TestRepoSearchFolds(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	r := NewRepo(st, nil)
	a := newTestTask("t1", testEpoch)
	a.Title = "Sửa lỗi Đăng nhập"
	b := newTestTask("t2", testEpoch.Add(time.Second))
	b.Title = "Thêm trang cài đặt"
	c := newTestTask("t3", testEpoch.Add(2*time.Second))
	c.Title = "Sửa bản Tie\u0302\u0301ng Vie\u0323\u0302t" // decomposed (NFD), as Unikey's "Unicode tổ hợp" types it
	for _, tk := range []*Task{a, b, c} {
		if _, err := r.Create(ctx, tk, nil); err != nil {
			t.Fatal(err)
		}
	}
	search := func(text string) []string {
		t.Helper()
		got, _, err := r.List(ctx, Query{Text: text})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, tk := range got {
			ids = append(ids, tk.ID)
		}
		return ids
	}
	for text, want := range map[string]string{"dang nhap": "t1", "ĐĂNG": "t1", "SỬA LỖI": "t1", "cai dat": "t2",
		"tiếng việt": "t3", "Đa\u0306ng nha\u0323\u0302p": "t1"} {
		if got := search(text); len(got) != 1 || got[0] != want {
			t.Errorf("search %q = %v", text, got)
		}
	}
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE tasks SET title_fold = ''`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := search("dang nhap"); len(got) != 0 {
		t.Fatalf("search before the backfill = %v", got)
	}
	if err := r.BackfillFold(ctx); err != nil {
		t.Fatal(err)
	}
	if got := search("dang nhap"); len(got) != 1 {
		t.Fatalf("search after the backfill = %v", got)
	}
}

func TestRepoAttentionIDs(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), nil)
	for i, att := range []*api.Attention{nil, {State: api.AttentionInfo, Reason: ReasonReview}} {
		tk := newTestTask(fmt.Sprintf("t%d", i), testEpoch.Add(time.Duration(i)*time.Second))
		tk.Status, tk.Attention = StatusDone, att
		if _, err := r.Create(ctx, tk, nil); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := r.AttentionIDs(ctx, StatusDone, StatusFailed)
	if err != nil || len(ids) != 1 || ids[0] != "t1" {
		t.Fatalf("AttentionIDs = %v, %v", ids, err)
	}
}

// The lane's run windows read only the fields of the shell tool events: SQLite filters and extracts them, so a test
// run's megabytes of output never reach Go (review N2), and blocked calls, other tools and other turns stay out.
func TestRepoCommandEventsLeaveOutput(t *testing.T) {
	ctx := context.Background()
	r := NewRepo(openStore(t), func() time.Time { return testEpoch })
	tk := newTestTask("t1", testEpoch)
	tk.Turns = append(tk.Turns, &Turn{N: 2, By: ByLead, Message: "more", Status: TurnRunning, StartedAt: testEpoch})
	if _, err := r.Create(ctx, tk, nil); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 4<<20)
	ev := func(turn int, at time.Duration, e event.Event) NewEvent {
		return NewEvent{Type: api.EventWorker, Turn: turn, Attempt: 1, TS: testEpoch.Add(at), Data: e}
	}
	evs := []NewEvent{
		ev(1, 0, event.Event{Kind: event.KindMessage, Text: "running the tests"}),
		ev(1, time.Second, event.Event{Kind: event.KindToolCall, Step: 2,
			Tool: &event.Tool{ID: "c1", Name: "run", State: event.ToolCalling, Command: "go test ./..."}}),
		ev(1, 2*time.Second, event.Event{Kind: event.KindToolCall, Step: 3,
			Tool: &event.Tool{ID: "e1", Name: "edit", State: event.ToolCalling}}), // no command
		ev(1, 3*time.Second, event.Event{Kind: event.KindToolCall, Step: 4,
			Tool: &event.Tool{ID: "b1", Name: "run", State: event.ToolFailed, Command: "rm -rf /", Blocked: true}}),
	}
	for i := range 5 {
		evs = append(evs, ev(1, time.Duration(4+i)*time.Second, event.Event{Kind: event.KindToolResult, Step: 5 + i,
			Tool: &event.Tool{ID: fmt.Sprint("r", i), Name: "run", State: event.ToolDone, Command: "npm test",
				Output: big, DurationMs: 1500}}))
	}
	evs = append(evs, ev(2, 10*time.Second, event.Event{Kind: event.KindToolCall, Step: 1,
		Tool: &event.Tool{ID: "c9", Name: "run", State: event.ToolCalling, Command: "make"}}))
	if _, err := r.Append(ctx, "t1", evs, nil); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got, err := r.CommandEvents(ctx, "t1", 1, 1, 100)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	want := []CommandEvent{{TS: testEpoch.Add(time.Second), Turn: 1, Attempt: 1, Step: 2, Kind: event.KindToolCall,
		ToolID: "c1", Command: "go test ./..."}}
	for i := range 5 {
		want = append(want, CommandEvent{TS: testEpoch.Add(time.Duration(4+i) * time.Second), Turn: 1, Attempt: 1,
			Step: 5 + i, Kind: event.KindToolResult, ToolID: fmt.Sprint("r", i), Command: "npm test",
			DurationMs: 1500})
	}
	if len(got) != len(want) {
		t.Fatalf("CommandEvents = %+v, want %+v", got, want)
	}
	for i := range want {
		if !got[i].TS.Equal(want[i].TS) {
			t.Errorf("event %d at %v, want %v", i, got[i].TS, want[i].TS)
		}
		got[i].TS = want[i].TS
		if got[i] != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	// 20 MB of output sit in the rows: reading them whole allocates about twice that (42 MB measured), the
	// extracted fields about 2 MB (SQLite's own pages), whatever the output's size.
	if n := after.TotalAlloc - before.TotalAlloc; n > 8<<20 {
		t.Errorf("CommandEvents allocated %d bytes: the outputs were read", n)
	}
	cols, _, _ := strings.Cut(commandEventsQuery, "FROM")
	if c := strings.ReplaceAll(cols, "json_extract(data,", ""); strings.Contains(c, "data") ||
		strings.Contains(c, "output") {
		t.Errorf("the query selects more than the command fields: %s", cols)
	}
}
