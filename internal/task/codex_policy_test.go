package task

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// codexPolicyLine is what codex 0.167 printed on standard error for each shell command its Windows sandbox refused.
const codexPolicyLine = `2026-11-08T09:00:01.000000Z ERROR codex_core::tools::router: error=exec_command failed: ` +
	`CreateProcess { message: "Rejected(\"` + "`" + `\"C:\\Program Files\\PowerShell\\7\\pwsh.exe\" -Command 'rg --files'` +
	"`" + ` rejected: blocked by policy\")" }`

// TestCodexPolicyRefusedFails: a codex turn whose every shell command codex's policy refused fails, so the lead
// must look at it, instead of ending done (and going on to the gate and review) on the worker's own "commands were
// blocked" answer. The refused command is in the turn's and the report's denied actions.
func TestCodexPolicyRefusedFails(t *testing.T) {
	h := newHarness(t)
	h.backends = []string{config.BackendCodex}
	cx, err := backend.New(config.BackendCodex, fakecli.Exe(t))
	if err != nil {
		t.Fatal(err)
	}
	h.s.o.Backends[config.BackendCodex] = cx
	set := h.config()
	set.Backends.Codex.Enabled = true
	h.settings.Store(&set)
	if _, _, err := h.accounts.Add(t.Context(), account.New{Backend: config.BackendCodex, Kind: account.KindIsolated,
		Label: "cx", Enabled: true}, nil); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.pool.Notify()

	const answer = "Every command was blocked and the workspace looks read-only, so I could not make the change."
	h.queue.Push(t, script(
		jsonLine(map[string]any{"type": "thread.started", "thread_id": "th-policy"}),
		jsonLine(map[string]any{"type": "turn.started"}),
		"#!stderr "+codexPolicyLine,
		jsonLine(map[string]any{"type": "item.completed", "item": map[string]any{"id": "m", "type": "agent_message",
			"text": answer}}),
		jsonLine(map[string]any{"type": "turn.completed", "usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendCodex, Model: "gpt-6-luna"})
	tk := h.waitStatus(sum.ID, StatusFailed)

	refused := `"C:\Program Files\PowerShell\7\pwsh.exe" -Command 'rg --files'`
	if !strings.Contains(tk.Error, "refused every shell command") || !strings.Contains(tk.Error, refused) {
		t.Errorf("task error = %q", tk.Error)
	}
	if a := tk.Attention; a == nil || !api.NeedsLead(a.State) || a.Reason != ReasonFailed {
		t.Errorf("attention = %+v", tk.Attention)
	}
	tu := tk.LastTurn()
	if tu.Status != TurnFailed || tu.Response != answer || !slices.Equal(tu.DeniedActions, []string{refused}) {
		t.Errorf("turn = %s, response %q, denied %q", tu.Status, tu.Response, tu.DeniedActions)
	}
	if tk.GatePhase != nil || len(eventsOf(h.events(sum.ID), api.EventGate)) != 0 {
		t.Errorf("the gate ran on a turn that could not run commands: %+v", tk.GatePhase)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != string(StatusFailed) || !slices.Equal(rep.DeniedActions, []string{refused}) {
		t.Errorf("report status %q denied %q", rep.Status, rep.DeniedActions)
	}

	recs := h.queue.Records(t)
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	override := slices.Contains(recs[0].Args, "windows.sandbox=unelevated")
	if override != (runtime.GOOS == "windows") {
		t.Errorf("codex argv on %s = %q", runtime.GOOS, recs[0].Args)
	}
}
