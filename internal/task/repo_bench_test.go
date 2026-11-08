package task

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/store"
)

var benchEpoch = time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)

// benchRepo is a repository on a fresh database file.
func benchRepo(b *testing.B) *Repo {
	b.Helper()
	st, err := store.Open(context.Background(), filepath.Join(b.TempDir(), "seelegent.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return NewRepo(st, nil)
}

// benchTask creates a running task with one turn and one attempt, as the engine has while a worker streams.
func benchTask(b *testing.B, r *Repo, id string, created time.Time) {
	b.Helper()
	t := &Task{ID: id, Title: "bench " + id, Status: StatusQueued, Kind: KindNormal, Mode: ModeEdit, Cwd: `D:\proj`,
		ProjectKey: "proj-1234", ProjectName: "proj", Prompt: "do it", Backend: "agy", Model: "m",
		CreatedAt: created, UpdatedAt: created,
		Turns: []*Turn{{N: 1, By: ByLead, Message: "do it", Status: TurnRunning, StartedAt: created}}}
	ctx := context.Background()
	if _, err := r.Create(ctx, t, []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
		Data: api.LeadMessageData{Message: "do it", By: ByLead}}}); err != nil {
		b.Fatal(err)
	}
	t.Turns[0].Attempts = []*Attempt{{N: 1, AccountID: "acc1", Backend: "agy", Status: AttemptRunning,
		StartedAt: created, Prompt: strings.Repeat("brief ", 500)}}
	t.Status = StatusRunning
	if _, err := r.Save(ctx, t, nil, nil); err != nil {
		b.Fatal(err)
	}
}

// workerEvent is a tool event of about the size a worker's stream line gives.
func workerEvent(i int) NewEvent {
	return NewEvent{Type: api.EventWorker, Turn: 1, Attempt: 1, Data: event.Event{Kind: event.KindToolResult,
		Step: i, Text: fmt.Sprintf("view_file /work/demo/pkg/file%d.go", i), Tool: &event.Tool{Name: "view_file",
			State: event.ToolDone, Output: strings.Repeat("// a line of the file\n", 12)}}}
}

// BenchmarkRepoAppend appends worker events with the stream offsets, the write task/follow.go does per stream
// line: one event per transaction (today) against ten.
func BenchmarkRepoAppend(b *testing.B) {
	for _, per := range []int{1, 10} {
		b.Run(fmt.Sprintf("events=%d", per), func(b *testing.B) {
			r := benchRepo(b)
			benchTask(b, r, "t1", benchEpoch)
			ctx := context.Background()
			evs := make([]NewEvent, per)
			b.ReportAllocs()
			n := 0
			for b.Loop() {
				for j := range evs {
					evs[j] = workerEvent(n + j)
				}
				n += per
				if _, err := r.Append(ctx, "t1", evs, &Offsets{Turn: 1, Attempt: 1, Stream: int64(n * 400)}); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(n), "ns/event")
		})
	}
}

// benchTasks creates n tasks with a few events each.
func benchTasks(b *testing.B, n int) *Repo {
	b.Helper()
	r := benchRepo(b)
	ctx := context.Background()
	for i := range n {
		id := fmt.Sprintf("t%04d", i)
		benchTask(b, r, id, benchEpoch.Add(time.Duration(i)*time.Second))
		if _, err := r.Append(ctx, id, []NewEvent{workerEvent(1), workerEvent(2), workerEvent(3)},
			&Offsets{Turn: 1, Attempt: 1, Stream: 1200}); err != nil {
			b.Fatal(err)
		}
	}
	return r
}

// BenchmarkRepoList reads the first page of the task list over 1000 tasks (GET /tasks, the dashboard's list).
func BenchmarkRepoList(b *testing.B) {
	r := benchTasks(b, 1000)
	ctx := context.Background()
	for _, q := range []struct {
		name string
		q    Query
	}{
		{"page50", Query{Limit: 50}},
		{"running", Query{Limit: 50, Statuses: []Status{StatusRunning}}},
		{"text", Query{Limit: 50, Text: "t09"}},
	} {
		b.Run(q.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ts, _, err := r.List(ctx, q.q)
				if err != nil || len(ts) == 0 {
					b.Fatalf("List = %d tasks, %v", len(ts), err)
				}
			}
		})
	}
}

// BenchmarkRepoGet reads one task with its turns and attempts (every summary and detail does).
func BenchmarkRepoGet(b *testing.B) {
	r := benchTasks(b, 1000)
	ctx := context.Background()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if _, err := r.Get(ctx, fmt.Sprintf("t%04d", i%1000)); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
