package task

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// The verifier (docs/gates.md): after a green gate a task with verify gets an LLM review before the lead sees its
// work. The review runs as a hidden child task (KindVerify, plan mode, the verifierTier model, another account
// when there is one); the parent waits in GatePhase verifying. A rejection opens a fix turn by the verifier while
// the automatic fix budget lasts.

// Verifier limits.
const (
	verifyPoll    = 2 * time.Second
	verifyDiffMax = 60 << 10
	verifyFileMax = 100
)

var (
	verdictRe = regexp.MustCompile(`(?is)##\s*Verdict\s*\n+[\s*_>` + "`" + `]*(accept|reject)`)
	reasonsRe = regexp.MustCompile(`(?im)^##\s*Reasons\s*$`)
)

// verifierPrompt is the brief of a verify task.
func (s *Service) verifierPrompt(ctx context.Context, t *Task, turn int, res *api.GateResult, files []string) string {
	var b strings.Builder
	b.WriteString("You are the pre-lead verifier. A worker just changed the files below; review the change before the " +
		"lead sees it. Do not edit, create or delete any file and do not run commands that change anything: read the " +
		"files with your tools.\n\n")
	fmt.Fprintf(&b, "Repository: %s\n\n## The task\n%s\n\n", claimRoot(t), t.Title)
	if i := instructionStart(t, turn); i < len(t.Turns) {
		fmt.Fprintf(&b, "### The lead's latest instruction\n%s\n\n", cut(t.Turns[i].Message, 6000))
	}
	if t.Proof != nil && t.Proof.FeatureDir != "" && t.Spec != nil && len(t.Spec.TaskIDs) > 0 {
		spec, tasks, err := proof.ReadFeature(t.Proof.FeatureDir)
		if err == nil {
			var covers []string
			b.WriteString("### Spec tasks\n")
			for _, id := range t.Spec.TaskIDs {
				if tl, ok := tasks.Find(id); ok {
					b.WriteString(strings.TrimSpace(tl.Line) + "\n")
					covers = append(covers, tl.Covers...)
				}
			}
			b.WriteString("\n")
			if len(covers) > 0 {
				b.WriteString("### Requirements these tasks must meet\n")
				for _, r := range spec.Requirements {
					if slices.Contains(covers, r.ID) {
						fmt.Fprintf(&b, "- %s: %s\n", r.ID, r.Text)
					}
				}
				b.WriteString("\n")
			}
			if ents, err := filepath.Glob(filepath.Join(t.Proof.FeatureDir, "contracts", "*")); err == nil && len(ents) > 0 {
				b.WriteString("### Contracts (read them)\n")
				for _, e := range ents {
					b.WriteString("- " + filepath.ToSlash(e) + "\n")
				}
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("## Changed files\n")
	for i, f := range files {
		if i == verifyFileMax {
			fmt.Fprintf(&b, "- … %d more\n", len(files)-i)
			break
		}
		b.WriteString("- " + f + "\n")
	}
	if diff := s.turnDiff(ctx, t, files); diff != "" {
		b.WriteString("\n## Diff against HEAD\n```diff\n" + diff + "\n```\n")
	}
	if res != nil {
		var cmds []string
		for i := range res.Steps {
			st := &res.Steps[i]
			if st.Cmd != "" && st.State == api.StepGreen {
				cmds = append(cmds, st.Cmd)
			}
		}
		if len(cmds) > 0 {
			b.WriteString("\n## Gate\nThese commands passed: " + strings.Join(cmds, "; ") + ". Do not run them again.\n")
		}
	}
	b.WriteString("\n## Answer in exactly this format\n## Verdict\naccept or reject\n\n## Reasons\n" +
		"- path/to/file:line - why (one line per problem; cite real lines)\n\n" +
		"Reject only for real defects: a bug, a requirement or contract not met, a broken or missing test. Style " +
		"preferences are not a reason to reject.")
	return b.String()
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// turnDiff is the diff of the changed files against HEAD, capped.
func (s *Service) turnDiff(ctx context.Context, t *Task, files []string) string {
	if t.RepoRoot == "" || len(files) == 0 {
		return ""
	}
	var out []byte
	args := append([]string{"--literal-pathspecs", "diff", "--no-color", "--no-ext-diff", "HEAD", "--"}, files...)
	if err := s.o.Git.Run(func(git string) error {
		var err error
		out, err = runGit(ctx, git, t.RepoRoot, args...)
		return err
	}); err != nil {
		return ""
	}
	d := string(out)
	if len(d) > verifyDiffMax {
		d = d[:verifyDiffMax] + "\n… (diff cut)"
	}
	return strings.TrimSpace(d)
}

// startVerifier creates the verify task for the gate of turn and moves the parent to the verifying phase.
func (s *Service) startVerifier(t *Task, turn int, res *api.GateResult, files, barChanged []string) (
	bool, error) {
	ctx := s.background()
	set := s.settings()
	var prov *account.Provider
	if t.Provider != "" {
		if p, err := s.o.Accounts.GetProvider(ctx, t.Provider); err == nil {
			prov = &p
		}
	}
	model, tier, _, err := s.resolveModel(t.Backend, prov, "", set.VerifierTier, "", nil)
	if err != nil {
		s.log.Warn("task: verifier model", "task", t.ID, "err", err)
		model, tier = t.Model, t.Tier
	}
	var prefer []string
	if accs, err := s.o.Accounts.List(ctx); err == nil {
		for i := range accs {
			a := &accs[i]
			if a.Backend == t.Backend && a.ID != t.AccountID {
				prefer = append(prefer, a.ID)
			}
		}
	}
	prompt := s.verifierPrompt(ctx, t, turn, res, files)
	now := s.now()
	child := &Task{Title: "Verify: " + t.Title, Status: StatusQueued, Kind: KindVerify, Mode: ModePlan, Cwd: t.Cwd,
		ProjectKey: t.ProjectKey, ProjectName: t.ProjectName, Prompt: prompt, Backend: t.Backend, Provider: t.Provider,
		Model: model, Tier: tier, Session: t.Session, Prefer: prefer, ParentID: t.ID, BaselineTaken: true,
		AfterMode: AfterAccepted, CreatedAt: now, UpdatedAt: now}
	child.Turns = []*Turn{{N: 1, By: ByLead, Message: prompt, Status: TurnRunning, Model: model, Tier: tier,
		StartedAt: now}}
	evs := []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
		Data: api.LeadMessageData{Message: prompt, By: ByLead, Model: model, Tier: tier}}}
	var out []api.TaskEvent
	for try := 0; ; try++ {
		child.ID = s.newID()
		out, err = s.repo.Create(ctx, child, evs)
		if err == nil {
			break
		}
		if try >= maxCreateTries {
			s.log.Warn("task: create the verify task", "task", t.ID, "err", err)
			v := &api.VerifierResult{Verdict: api.VerdictError, Reasons: []string{}, Cites: []api.CiteCheck{},
				Detail: "the verify task could not be created"}
			return false, s.endVerify(t.ID, turn, v, barChanged)
		}
	}
	s.publishNotices(child.ID, true, out)
	_, err = s.mutate(ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		if cur.GatePhase == nil || cur.GatePhase.Turn != turn {
			return nil, errNoChange
		}
		if tu := cur.TurnN(turn); tu != nil {
			tu.Gate = res
			tu.Verifier = &api.VerifierResult{Verdict: api.GateRunning, ChildID: child.ID, Reasons: []string{},
				Cites: []api.CiteCheck{}}
		}
		if cur.Proof != nil && len(barChanged) > 0 {
			cur.Proof.BarChanged = mergeSorted(cur.Proof.BarChanged, barChanged)
		}
		cur.GatePhase.State, cur.GatePhase.ChildID = api.GatePhaseVerifying, child.ID
		var evs []NewEvent
		if res != nil {
			evs = append(evs, gateResultEvent(turn, res, barChanged))
		}
		return append(evs, NewEvent{Type: api.EventVerify, Turn: turn,
			Data: api.VerifyEventData{Phase: "start", ChildID: child.ID}}), nil
	})
	if errors.Is(err, errNoChange) {
		// The phase ended meanwhile (a cancel, or a lead followup that superseded the gate): no verdict is needed.
		if _, _, cerr := s.Cancel(ctx, child.ID, api.CancelRequest{}); cerr != nil {
			s.log.Warn("task: cancel the verify task", "task", child.ID, "err", cerr)
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.startRun(child.ID)
	return true, nil
}

// awaitVerifier waits for the verify task of the parent's gate phase, then applies its verdict.
func (s *Service) awaitVerifier(r *run, t *Task) (bool, error) {
	ph := t.GatePhase
	for {
		child, err := s.childWhenChanged(r, ph.ChildID)
		if r.ctx.Err() != nil {
			cur, gerr := s.repo.Get(s.background(), t.ID)
			if gerr == nil && cur.CancelRequested {
				s.finishCancel(t.ID) // cancels the verify task too
			}
			return false, nil
		}
		if errors.Is(err, ErrNotFound) {
			v := &api.VerifierResult{Verdict: api.VerdictError, ChildID: ph.ChildID, Reasons: []string{},
				Cites: []api.CiteCheck{}, Detail: "the verify task is gone"}
			return s.applyVerdict(t.ID, ph.Turn, v)
		}
		if err != nil {
			return false, err
		}
		if child != nil {
			return s.applyVerdict(t.ID, ph.Turn, s.verdictOf(t, child))
		}
	}
}

// childWhenChanged returns the verify task when it has finished, else waits for its next change (or a slow poll,
// in case a notice was missed) and returns nil.
func (s *Service) childWhenChanged(r *run, id string) (*Task, error) {
	changed, release := s.bus.TasksChanged([]string{id})
	defer release()
	child, err := s.repo.Get(r.ctx, id)
	if err != nil || Finished(child.Status) {
		return child, err
	}
	timer := time.NewTimer(verifyPoll)
	defer timer.Stop()
	select {
	case <-changed:
	case <-r.ctx.Done():
	case <-timer.C:
	}
	return nil, nil
}

// verdictOf reads the verdict of a finished verify task.
func (s *Service) verdictOf(parent, child *Task) *api.VerifierResult {
	v := &api.VerifierResult{Verdict: api.VerdictError, ChildID: child.ID, Reasons: []string{},
		Cites: []api.CiteCheck{}, Tokens: child.TokensUsed()}
	tu := child.LastTurn()
	switch {
	case child.Status != StatusDone || tu == nil:
		v.Detail = fmt.Sprintf("the verify task ended %s", child.Status)
		if child.Error != "" {
			v.Detail += ": " + child.Error
		}
		return v
	case len(lastEdits(child, tu)) > 0:
		v.Detail = "the verifier changed files: " + strings.Join(lastEdits(child, tu), ", ")
		return v
	}
	m := verdictRe.FindStringSubmatch(tu.Response)
	if m == nil {
		v.Detail = "the verifier's answer has no verdict"
		return v
	}
	v.Verdict = strings.ToLower(m[1])
	if loc := reasonsRe.FindStringIndex(tu.Response); loc != nil {
		section := tu.Response[loc[1]:]
		if next := nextHeading.FindStringIndex(section); next != nil {
			section = section[:next[0]]
		}
		for _, line := range strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n") {
			if m := listItem.FindStringSubmatch(line); m != nil {
				v.Reasons = append(v.Reasons, strings.TrimSpace(m[1]))
			}
		}
	}
	root := claimRoot(parent)
	for _, c := range proof.FindCites(strings.Join(v.Reasons, "\n")) {
		r := proof.CheckCite(root, c)
		v.Cites = append(v.Cites, api.CiteCheck{Path: r.Path, Line: r.Line, EndLine: r.EndLine, State: r.State,
			Detail: r.Detail})
	}
	return v
}

// verifierNoDelete keeps a rejected worker from deleting its own work to answer the review (field report 4, F4).
const verifierNoDelete = "Do not delete, revert or move files to answer this review unless a reason above names " +
	"that exact file and change: the lead decides about files outside your lane."

// applyVerdict records the verdict on the parent and the verify task, then opens a fix turn (reject, budget left)
// or ends the phase.
func (s *Service) applyVerdict(id string, turn int, v *api.VerifierResult) (bool, error) {
	ctx := s.background()
	if v.ChildID != "" {
		_, err := s.mutate(ctx, v.ChildID, func(c *Task) ([]NewEvent, error) {
			if c.Verdict != nil {
				return nil, errNoChange
			}
			cp := *v
			c.Verdict = &cp
			if Finished(c.Status) && c.Review == nil {
				// The engine accepts its own review task; the lead's review history is not touched.
				c.Review = &api.Review{Verdict: api.VerdictAccepted, Notes: "verifier: " + v.Verdict,
					At: api.FormatTime(s.now())}
			}
			return nil, nil
		})
		if err != nil && !errors.Is(err, ErrNotFound) {
			s.log.Warn("task: record the verdict on the verify task", "task", v.ChildID, "err", err)
		}
	}
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return false, err
	}
	p := t.Proof
	if v.Verdict == api.VerdictReject && p != nil && p.AutoFixes < p.Budget {
		if err := s.saveVerdict(id, turn, v); err != nil {
			return false, err
		}
		var reasons strings.Builder
		for _, r := range v.Reasons {
			reasons.WriteString("- " + r + "\n")
		}
		msg := "The pre-lead verifier reviewed your changes and rejected them.\n\n## Reasons\n" + reasons.String() +
			"\nPlease fix the reported issues and verify. " + verifierNoDelete
		return s.fixTurn(id, turn, api.GateResult{}, nil, msg, ByVerifier)
	}
	return false, s.endVerify(id, turn, v, nil)
}

// saveVerdict stores the verdict on the parent's turn with its event.
func (s *Service) saveVerdict(id string, turn int, v *api.VerifierResult) error {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		tu := t.TurnN(turn)
		if tu == nil {
			return nil, errNoChange
		}
		cp := *v
		tu.Verifier = &cp
		return []NewEvent{{Type: api.EventVerify, Turn: turn, Data: api.VerifyEventData{Phase: "result",
			ChildID: v.ChildID, Result: &cp}}}, nil
	})
	return err
}

// endVerify stores the verdict and ends the gate phase (the task is done).
func (s *Service) endVerify(id string, turn int, v *api.VerifierResult, barChanged []string) error {
	if err := s.saveVerdict(id, turn, v); err != nil {
		return err
	}
	t, err := s.repo.Get(s.background(), id)
	if err != nil {
		return err
	}
	var res *api.GateResult
	if tu := t.TurnN(turn); tu != nil {
		res = tu.Gate
	}
	return s.endGateKeep(id, turn, res, barChanged)
}

// endGateKeep ends the phase like endGate without announcing the gate's result again.
func (s *Service) endGateKeep(id string, turn int, res *api.GateResult, barChanged []string) error {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if t.GatePhase == nil || t.GatePhase.Turn != turn {
			return nil, errNoChange
		}
		if tu := t.TurnN(turn); tu != nil {
			tu.Gate = res
		}
		if t.Proof != nil && len(barChanged) > 0 {
			t.Proof.BarChanged = mergeSorted(t.Proof.BarChanged, barChanged)
		}
		t.GatePhase = nil
		if !t.CancelRequested {
			t.Status, t.Error = StatusDone, ""
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	s.checkDeps(s.background())
	return nil
}
