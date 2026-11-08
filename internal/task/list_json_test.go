package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// seedListTasks stores tasks that cover what a list summary shows from the turns: no turns, tokens with and
// without a total, files edited in several turns, gates before and after the lead's latest instruction, a running
// gate phase, attempts with and without an account, hidden runs, and every status. It returns their ids.
func seedListTasks(t *testing.T, h *harness) []string {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC)
	usage := func(in, out, total int64) *event.Usage {
		return &event.Usage{InputTokens: in, OutputTokens: out, TotalTokens: total}
	}
	att := func(n int, acc, label string, u *event.Usage) *Attempt {
		return &Attempt{N: n, AccountID: acc, AccountLabel: label, Backend: "agy", Status: AttemptOK,
			StartedAt: base, EndedAt: base.Add(time.Minute), Usage: u, Prompt: "brief"}
	}
	turn := func(n int, by string, files []string, gate *api.GateResult, atts ...*Attempt) *Turn {
		return &Turn{N: n, By: by, Message: fmt.Sprintf("message %d", n), Status: TurnOK, StartedAt: base,
			EndedAt: base.Add(time.Minute), Files: files, Gate: gate, Attempts: atts, Response: "a long answer"}
	}
	green := &api.GateResult{State: api.GateGreen, Steps: []api.GateStep{}, NotRun: []string{}, Warnings: []string{}}
	red := &api.GateResult{State: api.GateRed, Steps: []api.GateStep{}, NotRun: []string{}, Warnings: []string{}}
	tasks := []*Task{
		{Status: StatusQueued},
		{Status: StatusRunning, Turns: []*Turn{turn(1, ByLead, []string{"a.go"}, nil,
			att(1, "acc1", "One", usage(100, 20, 0)), att(2, "", "", usage(5, 5, 50)))}},
		{Status: StatusDone, Review: &api.Review{Verdict: "accepted"}, Turns: []*Turn{
			turn(1, ByLead, []string{"a.go", "b.go"}, red, att(1, "acc1", "One", usage(1, 1, 1000))),
			turn(2, ByGate, []string{"b.go", "c.go"}, green, att(1, "acc2", "", nil))}},
		{Status: StatusDone, Turns: []*Turn{
			turn(1, ByLead, nil, green, att(1, "acc1", "One", nil)),
			turn(2, ByLead, []string{"z.go"}, nil, att(1, "acc3", "Three", usage(0, 0, 7)), att(2, "", "", nil))}},
		{Status: StatusRunning, Proof: &Proof{AutoFixes: 2}, GatePhase: &GatePhase{State: api.GatePhaseQueued,
			Turn: 1, Since: base, Wait: api.GateWaitSlot}, Turns: []*Turn{turn(1, ByLead, []string{"q.go"}, nil,
			att(1, "acc1", "One", usage(3, 4, 0)))}},
		{Status: StatusFailed, Proof: &Proof{AutoFixes: 1}, Error: "boom", Turns: []*Turn{
			turn(1, ByUser, nil, red), turn(2, ByVerifier, nil, nil, att(1, "acc2", "Two", nil))}},
		{Status: StatusWaitingQuota, Waiting: api.WaitingMemory, WaitingUntil: base.Add(time.Hour)},
		{Status: StatusCancelled, Attention: &api.Attention{Reason: "stalled"}, Turns: []*Turn{
			turn(1, "", []string{"x.go", "x.go"}, nil)}},
		{Status: StatusDone, Kind: KindVerify, Turns: []*Turn{turn(1, ByLead, nil, nil, att(1, "acc1", "", nil))}},
		{Status: StatusDone, Kind: KindConsult},
		{Status: StatusWaitingDeps, After: []string{"t00"}, Consults: []api.Consult{{ID: "c1", Status: "running"}}},
		{Status: StatusBlockedDeps, Kind: "", Turns: []*Turn{turn(1, ByLead, nil, nil, att(1, "acc9", "Nine",
			usage(10, 10, 0)))}},
	}
	var ids []string
	for i, tk := range tasks {
		tk.ID = fmt.Sprintf("t%02d", i)
		tk.Title = "Task " + tk.ID
		if tk.Kind == "" && i%5 != 4 {
			tk.Kind = KindNormal
		}
		tk.Mode, tk.Cwd, tk.ProjectKey, tk.ProjectName, tk.Backend = ModeEdit, `D:\proj`, "proj-1", "proj", "agy"
		tk.Prompt = "a prompt"
		tk.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		tk.UpdatedAt = tk.CreatedAt.Add(time.Second)
		if _, err := h.s.repo.Create(ctx, tk, []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
			Data: api.LeadMessageData{Message: "go", By: ByLead}, TS: tk.CreatedAt}}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, tk.ID)
	}
	return ids
}

// listJSON is what the dashboard reads of the seeded tasks: the list (all, by status, by text, a page with its
// cursor), the first paint and each task's summary.
func listJSON(t *testing.T, h *harness, ids []string) []byte {
	t.Helper()
	ctx := context.Background()
	out := map[string]any{}
	for name, q := range map[string]Query{"all": {}, "running": {Statuses: []Status{StatusRunning}},
		"text": {Text: "t0"}, "page": {Limit: 3}, "project": {ProjectKey: "proj-1", Limit: 4}} {
		page, err := h.s.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		out["list-"+name] = page
	}
	boot, err := h.s.Boot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out["boot"] = boot
	for _, id := range ids {
		sum, err := h.s.Summary(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		out["summary-"+id] = sum
	}
	data, err := json.MarshalIndent(out, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestListJSONDump writes listJSON of the seeded tasks to $SEELEGENT_LIST_DUMP, to compare builds (the list must
// not change when its query does).
func TestListJSONDump(t *testing.T) {
	path := os.Getenv("SEELEGENT_LIST_DUMP")
	if path == "" {
		t.Skip("SEELEGENT_LIST_DUMP is not set")
	}
	h := newHarness(t)
	if err := os.WriteFile(path, listJSON(t, h, seedListTasks(t, h)), 0o600); err != nil {
		t.Fatal(err)
	}
}
