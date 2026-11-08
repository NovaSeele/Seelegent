package task

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/member"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

func (h *harness) member(in api.MemberInput) api.Member {
	h.t.Helper()
	m, err := h.members.Create(h.t.Context(), in)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func TestDelegateAndFollowupByMember(t *testing.T) {
	h := newHarness(t)
	h.agy("w", 1)
	h.member(api.MemberInput{ID: "dev", Name: "Dev", Backend: "agy", Rank: api.RankWorker, Tier: "strong"})
	h.member(api.MemberInput{ID: "quick", Name: "Quick", Backend: "agy", Rank: api.RankWorker, Tier: "fast"})
	off := false
	h.member(api.MemberInput{ID: "off", Name: "Off", Backend: "agy", Rank: api.RankWorker, Enabled: &off})

	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Member: "off"}); !errors.Is(err,
		member.ErrDisabled) {
		t.Fatalf("disabled member = %v", err)
	}
	if _, err := h.s.Delegate(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Member: "nobody"}); !errors.Is(err,
		ErrInvalid) {
		t.Fatalf("unknown member = %v", err)
	}
	h.queue.Push(t, okScript("conv-m", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "by member", Member: "dev"})
	tk := h.waitStatus(sum.ID, StatusDone)
	if tk.Member != "dev" || tk.Tier != "strong" || tk.Backend != "agy" {
		t.Fatalf("task = member %q tier %q backend %q", tk.Member, tk.Tier, tk.Backend)
	}
	h.queue.Push(t, okScript("conv-m", "Done again."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "again", Member: "quick"}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if tk.Member != "quick" || tk.LastTurn().Tier != "fast" {
		t.Errorf("after followup: member %q tier %q", tk.Member, tk.LastTurn().Tier)
	}
}

// TestFollowupRefusedOnEndedContestVariant: a variant task takes followups while its contest runs or is ready (a
// followup reopens a ready contest), and none once the contest was applied, cancelled or failed, whose stored copy
// of the variant's changes would go stale (F46).
func TestFollowupRefusedOnEndedContestVariant(t *testing.T) {
	h := newHarness(t)
	h.agy("w", 1)
	h.mu.Lock()
	h.contestStatus = map[string]string{"k1": api.ContestStatusReady}
	h.mu.Unlock()
	h.queue.Push(t, okScript("conv-v", "Variant done."))
	sum, err := h.s.DelegateWith(t.Context(), api.DelegateRequest{Prompt: "x", Cwd: h.cwd, Title: "V1"},
		Spawn{ContestID: "k1", Variant: "V1"})
	if err != nil {
		t.Fatal(err)
	}
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, okScript("conv-v", "Again."))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "once more"}); err != nil {
		t.Fatalf("followup while the contest is ready: %v", err)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	turns := len(tk.Turns)

	for _, st := range []string{api.ContestStatusApplied, api.ContestStatusCancelled, api.ContestStatusFailed} {
		h.mu.Lock()
		h.contestStatus["k1"] = st
		h.mu.Unlock()
		_, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "after the end"})
		var ce *ContestEndedError
		if !errors.Is(err, ErrBadState) || !errors.As(err, &ce) || ce.APIDetails()["reason"] != "contest_ended" ||
			ce.APIDetails()["contest"] != "k1" || ce.APIDetails()["status"] != st {
			t.Fatalf("followup once the contest is %s: %v", st, err)
		}
	}
	if tk = h.waitStatus(sum.ID, StatusDone); len(tk.Turns) != turns {
		t.Fatalf("a refused followup added a turn: %d -> %d", turns, len(tk.Turns))
	}
}

func TestConsultPeerObjection(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 1)
	h.member(api.MemberInput{ID: "rev", Name: "Reviewer", Backend: "agy", Rank: api.RankPeer})
	h.member(api.MemberInput{ID: "dev", Name: "Dev", Backend: "agy", Rank: api.RankWorker})
	a := filepath.Join(h.cwd, "a.txt")
	h.queue.Push(t, editScript("conv-p", a, "two\n", "Changed a.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "consulted"})
	h.waitStatus(sum.ID, StatusDone)

	if _, err := h.s.Consult(t.Context(), sum.ID, api.ConsultRequest{Member: "dev"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("worker consult = %v", err)
	}
	h.queue.Push(t, okScript("conv-c", "## Verdict\n**object**\n\n## Findings\n"+
		"- a.txt holds the wrong value [a.txt:1] \"two\"\n- a made-up claim [nope.txt:3]\n\n"+
		"## Required changes\n- set it to three\n"))
	rec, err := h.s.Consult(t.Context(), sum.ID, api.ConsultRequest{Member: "rev", Focus: "the value"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID != "c1" || rec.Status != consultRunning || rec.RunTaskID == "" {
		t.Fatalf("record = %+v", rec)
	}
	h.waitFor("the consult to end", func() bool {
		c := h.task(sum.ID).Consults
		return len(c) == 1 && c[0].Status != consultRunning
	})
	c := h.task(sum.ID).Consults[0]
	if c.Status != consultDone || c.Verdict != api.ConsultObject || len(c.Findings) != 2 ||
		len(c.Required) != 1 || c.Required[0] != "set it to three" {
		t.Fatalf("consult = %+v", c)
	}
	if a := h.task(sum.ID).Attention; a == nil || a.Reason != ReasonPeerObjection || a.Who != WhoLead ||
		a.Detail != "rev objects: set it to three" {
		t.Errorf("attention with an open objection = %+v", a)
	}
	if st := c.Findings[0].Cites[0].State; st != proof.CiteVerified {
		t.Errorf("first cite = %s", st)
	}
	if st := c.Findings[1].Cites[0].State; st != proof.CiteMissingFile {
		t.Errorf("second cite = %s", st)
	}
	run := h.task(rec.RunTaskID)
	if run.Kind != KindConsult || run.Mode != ModePlan || run.ParentID != sum.ID || run.Member != "rev" ||
		run.Review == nil {
		t.Errorf("run = %+v", run)
	}
	prompt, err := h.s.Prompt(t.Context(), run.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "peer reviewer") || !strings.Contains(prompt, "the value") ||
		!strings.Contains(prompt, "a.txt") || !strings.Contains(prompt, "## Verdict") {
		t.Errorf("consult prompt:\n%s", prompt)
	}
	page, err := h.s.List(t.Context(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Errorf("listed %d tasks", len(page.Items))
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Consults) != 1 || !rep.Consults[0].Unanswered || len(rep.Consults[0].Findings) != 2 ||
		!strings.Contains(rep.Consults[0].Findings[0], "(verified)") {
		t.Errorf("report consults = %+v", rep.Consults)
	}

	_, err = h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted})
	var pe *PeerObjectionError
	if !errors.As(err, &pe) || !errors.Is(err, ErrConflict) || len(pe.Consults) != 1 {
		t.Fatalf("accept over an objection = %v", err)
	}
	if _, err := h.s.Review(t.Context(), sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted, Notes: "ok",
		AnswerObjection: "two is right: the spec says so"}); err != nil {
		t.Fatal(err)
	}
	tk := h.task(sum.ID)
	if tk.Consults[0].AnsweredObjection == nil || !strings.Contains(tk.Review.Notes, "Answer to peer objection") {
		t.Errorf("after the answer: %+v / %q", tk.Consults[0].AnsweredObjection, tk.Review.Notes)
	}
	d, err := h.s.Get(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Consults) != 1 || d.Consults[0].Findings == nil {
		t.Errorf("detail consults = %+v", d.Consults)
	}
}

// A hidden run whose record was never stored is stopped and, once its worker has stopped, closed: it never waits
// for the lead's review (a close right after Cancel would find the worker still running).
func TestStopHiddenRunClosesItOnceStopped(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 2)
	h.queue.Push(t, okScript("conv-p", "Done."))
	parent := h.delegate(api.DelegateRequest{Title: "parent"})
	h.waitStatus(parent.ID, StatusDone)
	h.queue.Push(t, hangScript("conv-h"))
	run, err := h.s.DelegateWith(t.Context(), api.DelegateRequest{Prompt: "review", Cwd: h.cwd, Title: "Consult: parent",
		Mode: ModePlan}, Spawn{Kind: KindConsult, ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.runningWorker(run.ID)
	open, err := h.s.OpenHiddenRuns(t.Context(), KindConsult)
	if err != nil || len(open) != 1 || open[0].ID != run.ID || open[0].ParentID != parent.ID {
		t.Fatalf("open hidden runs = %+v %v", open, err)
	}
	if _, err := h.s.OpenHiddenRuns(t.Context(), KindNormal); !errors.Is(err, ErrInvalid) {
		t.Errorf("open runs of a listed kind = %v", err)
	}
	h.s.StopHiddenRun(run.ID, "consult not stored")
	h.waitFor("the hidden run to be closed", func() bool {
		r := h.task(run.ID)
		return r.Status == StatusCancelled && r.Review != nil
	})
	if r := h.task(run.ID); r.Review.Verdict != api.VerdictAccepted || r.Review.Notes != "consult not stored" {
		t.Errorf("review = %+v", r.Review)
	}
	if open, err := h.s.OpenHiddenRuns(t.Context(), KindConsult); err != nil || len(open) != 0 {
		t.Errorf("open hidden runs after the close = %+v %v", open, err)
	}
}

// A daemon that stopped between creating a consult run and storing its record leaves a run nobody reads: the next
// start stops and closes it, and leaves alone a consult run its parent records as running. A run whose consult
// already ended (the daemon stopped between storing the outcome and closing the run) is closed too, whether it
// finished or still runs.
func TestStartStopsOrphanConsultRuns(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 3)
	h.agy("w2", 2) // two turns at a time per account: three runs hang at once
	h.queue.Push(t, okScript("conv-p", "Done."))
	parent := h.delegate(api.DelegateRequest{Title: "parent"})
	h.waitStatus(parent.ID, StatusDone)
	consult := func(conv string) string {
		h.queue.Push(t, hangScript(conv))
		run, err := h.s.DelegateWith(t.Context(), api.DelegateRequest{Prompt: "review", Cwd: h.cwd,
			Title: "Consult: parent", Mode: ModePlan}, Spawn{Kind: KindConsult, ParentID: parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		h.runningWorker(run.ID)
		return run.ID
	}
	orphan, recorded, endedRunning := consult("conv-o"), consult("conv-r"), consult("conv-e")
	h.queue.Push(t, okScript("conv-d", "Approve."))
	finished, err := h.s.DelegateWith(t.Context(), api.DelegateRequest{Prompt: "review", Cwd: h.cwd,
		Title: "Consult: parent", Mode: ModePlan}, Spawn{Kind: KindConsult, ParentID: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.waitStatus(finished.ID, StatusDone)
	record := func(id, run, status string) api.Consult {
		return api.Consult{ID: id, Member: "rev", Rank: api.RankPeer, RunTaskID: run, Status: status,
			Findings: []api.Claim{}, Required: []string{}, At: api.FormatTime(h.clk.Now())}
	}
	if _, err := h.s.mutate(t.Context(), parent.ID, func(tk *Task) ([]NewEvent, error) {
		tk.Consults = append(tk.Consults, record("c1", recorded, consultRunning), record("c2", finished.ID, consultDone),
			record("c3", endedRunning, consultFailed))
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
	if open, err := h.s.OpenHiddenRuns(t.Context(), KindConsult); err != nil || len(open) != 4 {
		t.Fatalf("open hidden runs before the restart = %+v %v", open, err)
	}
	h.stop()
	h.start()
	h.waitFor("the ended consults' runs to be closed", func() bool {
		d, e := h.task(finished.ID), h.task(endedRunning)
		return d.Review != nil && e.Status == StatusCancelled && e.Review != nil
	})
	if r := h.task(finished.ID); r.Status != StatusDone || r.Review.Notes != "consult "+consultDone {
		t.Errorf("finished run of a stored consult = %s, review %+v", r.Status, r.Review)
	}
	if r := h.task(endedRunning); r.Review.Notes != "consult "+consultFailed {
		t.Errorf("running run of an ended consult: review %+v", r.Review)
	}
	h.waitFor("the orphan consult run to be closed", func() bool {
		r := h.task(orphan)
		return r.Status == StatusCancelled && r.Review != nil
	})
	if r := h.task(orphan); r.Review.Notes != "consult not stored" {
		t.Errorf("orphan review = %+v", r.Review)
	}
	if r := h.task(recorded); r.Status != StatusRunning || r.Review != nil {
		t.Errorf("recorded consult run = %s, review %+v", r.Status, r.Review)
	}
	if _, _, err := h.s.Cancel(t.Context(), recorded, api.CancelRequest{WaitSeconds: 10}); err != nil {
		t.Fatal(err)
	}
}

// TestConsultDuringFollowup creates a consult run while a followup on the parent is saved: the child task is
// created outside the parent's lock, so neither call waits on the other (lock order: claimMu, then the task).
func TestConsultDuringFollowup(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 2)
	h.member(api.MemberInput{ID: "rev", Name: "Reviewer", Backend: "agy", Rank: api.RankPeer})
	h.queue.Push(t, okScript("conv-p", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "parent"})
	h.waitStatus(sum.ID, StatusDone)
	h.queue.Push(t, okScript("conv-x", "## Verdict\napprove\n"))
	h.queue.Push(t, okScript("conv-y", "## Verdict\napprove\n"))
	errs := make(chan error, 2)
	go func() {
		_, err := h.s.Consult(t.Context(), sum.ID, api.ConsultRequest{Member: "rev"})
		errs <- err
	}()
	go func() {
		_, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "Once more."})
		errs <- err
	}()
	timeout := time.After(20 * time.Second)
	for range 2 {
		select {
		case err := <-errs:
			// The followup may find the consult's turn-ended check racing it; only a hang is a failure here.
			if err != nil && !errors.Is(err, ErrBadState) {
				t.Errorf("concurrent call: %v", err)
			}
		case <-timeout:
			t.Fatal("consult and followup on the same task deadlocked")
		}
	}
}

func TestSeniorObjectionWarns(t *testing.T) {
	cs := []api.Consult{
		{Member: "sam", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultObject,
			Findings: []api.Claim{{Text: "the total can go negative"}}},
		{Member: "pia", Rank: api.RankPeer, Status: consultDone, Verdict: api.ConsultObject},
		{Member: "ted", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultConcerns},
		{Member: "una", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultObject,
			Required: []string{"add a test"}},
	}
	got := seniorObjections(cs)
	if want := []api.SeniorObjection{{Member: "sam", Text: "the total can go negative"},
		{Member: "una", Text: "add a test"}}; !slices.Equal(got, want) {
		t.Errorf("objections = %+v", got)
	}
	if w := objectionWarnings(got); !slices.Equal(w, []string{"senior sam objected: the total can go negative",
		"senior una objected: add a test"}) {
		t.Errorf("warnings = %q", w)
	}
}

// Only a senior's latest finished consult counts: an objection approved later no longer warns, an approval
// followed by an objection does, and a consult still running or failed leaves the earlier verdict in place.
func TestSeniorObjectionLatestConsult(t *testing.T) {
	cs := []api.Consult{
		{Member: "sam", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultObject,
			Findings: []api.Claim{{Text: "old finding"}}},
		{Member: "una", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultApprove},
		{Member: "sam", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultApprove},
		{Member: "una", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultObject,
			Findings: []api.Claim{{Text: "new finding"}}},
		{Member: "ted", Rank: api.RankSenior, Status: consultDone, Verdict: api.ConsultObject,
			Required: []string{"keep the check"}},
		{Member: "ted", Rank: api.RankSenior, Status: consultFailed},
		{Member: "una", Rank: api.RankSenior, Status: consultRunning},
	}
	got := objectionWarnings(seniorObjections(cs))
	want := []string{"senior una objected: new finding", "senior ted objected: keep the check"}
	if !slices.Equal(got, want) {
		t.Errorf("warnings = %q, want %q", got, want)
	}
}

// A hidden run that continues on another account (a fresh conversation) gets its first brief again; the same
// conversation gets only the turn's message.
func TestHiddenRunFreshConversation(t *testing.T) {
	tk := &Task{Kind: KindMeeting, Turns: []*Turn{{N: 1, Message: "You are P1, a peer."}, {N: 2, Message: "Round 2 of 3."}}}
	s := &Service{}
	if got := s.prompt(t.Context(), tk, tk.Turns[1], true); got != "Round 2 of 3." {
		t.Errorf("continuing = %q", got)
	}
	got := s.prompt(t.Context(), tk, tk.Turns[1], false)
	if !strings.Contains(got, "You are P1, a peer.") || !strings.HasSuffix(got, "Round 2 of 3.") {
		t.Errorf("fresh conversation = %q", got)
	}
	if got := s.prompt(t.Context(), tk, tk.Turns[0], false); got != "You are P1, a peer." {
		t.Errorf("first turn = %q", got)
	}
}

func TestParseConsultDefaults(t *testing.T) {
	v, f, r := parseConsult("no headings at all", t.TempDir())
	if v != api.ConsultConcerns || len(f) != 0 || len(r) != 0 || f == nil || r == nil {
		t.Errorf("parse = %q %v %v", v, f, r)
	}
	v, _, r = parseConsult("## Verdict\nApprove.\n\n## Required changes\n- none\n", t.TempDir())
	if v != api.ConsultApprove || len(r) != 0 {
		t.Errorf("parse = %q %v", v, r)
	}
}
