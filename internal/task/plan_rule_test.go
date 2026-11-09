package task

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// withCodex registers the codex backend (the fake CLI), enables it and adds a codex sign-in account.
func (h *harness) withCodex() {
	h.t.Helper()
	h.backends = append(h.backends, config.BackendCodex)
	cx, err := backend.New(config.BackendCodex, fakecli.Exe(h.t))
	if err != nil {
		h.t.Fatal(err)
	}
	h.s.o.Backends[config.BackendCodex] = cx
	set := h.config()
	set.Backends.Codex.Enabled = true
	set.Backends.Codex.Tiers = config.TierModels{Fast: "gpt-6-luna", Standard: "gpt-6-luna", Strong: "gpt-6-luna",
		Max: "gpt-6-luna"}
	h.settings.Store(&set)
	if _, _, err := h.accounts.Add(h.t.Context(), account.New{Backend: config.BackendCodex,
		Kind: account.KindIsolated, Label: "cx", Enabled: true}, nil); err != nil {
		h.t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.pool.Notify()
}

// codexOK is a whole successful codex turn.
func codexOK(thread, text string) string {
	return script(
		jsonLine(map[string]any{"type": "thread.started", "thread_id": thread}),
		jsonLine(map[string]any{"type": "turn.started"}),
		jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "m", "type": "agent_message",
			"text": text}}),
		jsonLine(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 10,
			"output_tokens": 5}}))
}

// TestPlanRuleFollowsAttemptBackend: a plan task that starts on agy and moves to codex (its member's fallback
// backend, since no agy account exists) is briefed for codex's read-only sandbox, not for agy's file tools (R4).
func TestPlanRuleFollowsAttemptBackend(t *testing.T) {
	h := newHarness(t)
	h.withCodex()
	h.member(api.MemberInput{ID: "dual", Name: "Dual", Backend: config.BackendAgy, Rank: api.RankWorker,
		FallbackBackends: []string{config.BackendCodex}})
	h.queue.Push(t, codexOK("th-plan", "The pool retries twice."))
	sum := h.delegate(api.DelegateRequest{Title: "explain", Member: "dual", Mode: ModePlan,
		Prompt: "Explain the pool."})
	tk := h.waitStatus(sum.ID, StatusDone)
	// The task starts on agy and moves to codex (no agy account). The summary delegate returns may already show
	// codex on a busy machine, so the move is read from the handoff event.
	moved := false
	for _, e := range eventsOf(h.events(sum.ID), api.EventHandoff) {
		data, err := json.Marshal(e.Data)
		if err != nil {
			t.Fatal(err)
		}
		var d api.HandoffData
		if json.Unmarshal(data, &d) == nil && d.Kind == api.HandoffBackend && d.From == config.BackendAgy &&
			d.To == config.BackendCodex {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("no handoff from agy to codex: %+v", h.events(sum.ID))
	}
	a := tk.LastTurn().LastAttempt()
	if tk.Backend != config.BackendCodex || a == nil || a.Backend != config.BackendCodex {
		t.Fatalf("task backend %q, attempt %+v", tk.Backend, a)
	}
	prompt, err := h.s.Prompt(t.Context(), sum.ID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, brief.PlanRule(true)) || strings.Contains(prompt, brief.PlanRule(false)) {
		t.Errorf("codex attempt prompt:\n%s", prompt)
	}
}

// TestConsultCodexMemberPrompt: a consult by a codex member is told that its read-only sandbox runs shell
// commands that read and to take citation lines from rg -n; the same consult by an agy member keeps the file
// tools.
func TestConsultCodexMemberPrompt(t *testing.T) {
	h := newHarness(t)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	h.agy("w", 1)
	h.withCodex()
	h.member(api.MemberInput{ID: "cx", Name: "Codex reviewer", Backend: config.BackendCodex, Rank: api.RankPeer})
	h.member(api.MemberInput{ID: "ag", Name: "Agy reviewer", Backend: config.BackendAgy, Rank: api.RankSenior})
	h.queue.Push(t, editScript("conv-p", h.cwd+"/a.txt", "two\n", "Changed a.txt."))
	sum := h.delegate(api.DelegateRequest{Title: "consulted", Backend: config.BackendAgy})
	h.waitStatus(sum.ID, StatusDone)

	verdict := "## Verdict\napprove\n\n## Findings\n- fine [a.txt:1] \"two\"\n\n## Required changes\n- none\n"
	for i, c := range []struct {
		member, script string
		codex          bool
	}{{"cx", codexOK("th-c", verdict), true}, {"ag", okScript("conv-c", verdict), false}} {
		h.queue.Push(t, c.script)
		rec, err := h.s.Consult(t.Context(), sum.ID, api.ConsultRequest{Member: c.member})
		if err != nil {
			t.Fatal(err)
		}
		h.waitFor("the consult to end", func() bool {
			cs := h.task(sum.ID).Consults
			return len(cs) == i+1 && cs[i].Status != consultRunning
		})
		prompt, err := h.s.Prompt(t.Context(), rec.RunTaskID, 1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(prompt, brief.PlanRule(c.codex)) || !strings.Contains(prompt, "## Verdict") ||
			!strings.Contains(prompt, "\nDo not explore ~/.seelegent or Seelegent's processes") {
			t.Errorf("%s consult prompt:\n%s", c.member, prompt)
		}
		if strings.Contains(prompt, "`rg -n`") != c.codex || strings.Contains(prompt, "file tools") == c.codex {
			t.Errorf("%s consult prompt has the wrong plan rule:\n%s", c.member, prompt)
		}
		if run := h.task(rec.RunTaskID); run.Backend != map[bool]string{true: config.BackendCodex,
			false: config.BackendAgy}[c.codex] {
			t.Errorf("%s consult ran on %q", c.member, run.Backend)
		}
	}
}
