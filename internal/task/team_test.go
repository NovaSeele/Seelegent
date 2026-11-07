package task

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
		member.ErrNotFound) {
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
