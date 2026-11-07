package ticket

import (
	"errors"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestRetryFailedTicket: a ticket whose task could not start (an unknown member) is failed; retry starts its task
// again with another member in the same worktree, and refuses a ticket that did not fail.
func TestRetryFailedTicket(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	r, err := e.s.Connect(ctx, api.RepoConnectRequest{Path: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	it, err := e.s.AddIssue(ctx, r.ID, api.IssueInput{Title: "Search"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Triage(ctx, r.ID, it.Key, api.TriageRequest{Action: api.TriageAssign,
		TicketTaskOptions: api.TicketTaskOptions{Member: unknownMember}}); err == nil {
		t.Fatal("triage with an unknown member started a task")
	}
	list, err := e.s.Tickets(ctx, r.ID, nil)
	if err != nil || len(list) != 1 || list[0].Status != api.TicketFailed || list[0].Error == "" {
		t.Fatalf("tickets = %+v, %v", list, err)
	}
	// The cause has a code the dashboard explains in its own words.
	if f := list[0].Failure; f == nil || f.Reason != api.FailureMemberMissing || f.Member != unknownMember {
		t.Fatalf("failure = %+v", list[0].Failure)
	}
	if a := list[0].Asked; a == nil || a.Member != unknownMember {
		t.Fatalf("asked = %+v", list[0].Asked)
	}
	id := list[0].ID

	// "As before" (no member, no backend) is the same unknown member: failed again.
	if _, err := e.s.Retry(ctx, id, api.TicketTaskRequest{}); err == nil {
		t.Fatal("retry as before started a task with the unknown member")
	}
	// Again with the same unknown member: failed again, with the new error.
	if _, err := e.s.Retry(ctx, id, api.TicketTaskRequest{TicketTaskOptions: api.TicketTaskOptions{
		Member: unknownMember}}); err == nil {
		t.Fatal("retry with the unknown member started a task")
	}
	if got, _ := e.s.Ticket(ctx, id); got.Status != api.TicketFailed {
		t.Fatalf("status after a failed retry = %s", got.Status)
	}
	res, err := e.s.Retry(ctx, id, api.TicketTaskRequest{TicketTaskOptions: api.TicketTaskOptions{Member: "ada"}})
	if err != nil || res.TaskID == "" {
		t.Fatalf("retry = %+v, %v", res, err)
	}
	got, err := e.s.Ticket(ctx, id)
	if err != nil || got.Status != api.TicketWorking || got.Error != "" || got.Failure != nil || len(got.Tasks) != 1 ||
		got.Tasks[0].Member != "ada" || got.Asked == nil || got.Asked.Member != "ada" {
		t.Fatalf("after retry = %+v, %v", got.TicketSummary, err)
	}
	if _, err := e.s.Retry(ctx, id, api.TicketTaskRequest{}); !errors.Is(err, ErrBadState) {
		t.Fatalf("retry of a working ticket: %v", err)
	}
}

// TestNeedsAttention: what the lead has to do next, in order: a refused push, failed checks, a pull request still
// to be opened by hand.
func TestNeedsAttention(t *testing.T) {
	manual := &api.TicketPR{ManualURL: "https://github.com/o/r/compare/main...b"}
	failed := &api.TicketChecks{State: api.ChecksFailure}
	refusal := &api.TicketRefusal{Reason: api.RefusalUnsafeFiles}
	for _, c := range []struct {
		t    ticketRec
		want string
	}{
		{ticketRec{Status: api.TicketReady}, ""},
		{ticketRec{Status: api.TicketReady, Refusal: refusal}, api.TicketPushRefused},
		{ticketRec{Status: api.TicketWorking, Refusal: refusal}, ""},
		{ticketRec{Status: api.TicketPROpen, PR: manual}, api.TicketOpenPR},
		{ticketRec{Status: api.TicketPROpen, PR: &api.TicketPR{Number: 4, ManualURL: manual.ManualURL}}, ""},
		{ticketRec{Status: api.TicketPROpen, PR: &api.TicketPR{Number: 4}, Checks: failed}, api.TicketChecksFailed},
		{ticketRec{Status: api.TicketPROpen, PR: &api.TicketPR{Number: 4}, Checks: failed, Refusal: refusal},
			api.TicketPushRefused},
		{ticketRec{Status: api.TicketDone, PR: manual}, ""},
	} {
		if got := c.t.needsAttention(); got != c.want {
			t.Errorf("%s %+v: %q, want %q", c.t.Status, c.t.PR, got, c.want)
		}
		if sum := c.t.summary(); (sum.Refusal != nil) != (c.want == api.TicketPushRefused) {
			t.Errorf("%s: refusal shown = %v", c.t.Status, sum.Refusal != nil)
		}
	}
}
