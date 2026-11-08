package task

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/ops"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// claudeLimitMessage is what Claude Code printed when a sign-in account reached its 5-hour limit in the middle of a
// turn (field report 3, B1).
const claudeLimitMessage = "You've hit your 5-hour limit · resets 3:10am (Asia/Ho_Chi_Minh)"

// useClaude gives the harness the claude backend (its binary is the fake CLI) and enables it.
func (h *harness) useClaude() {
	h.t.Helper()
	h.backends = []string{config.BackendClaude}
	cl, err := backend.New(config.BackendClaude, fakecli.Exe(h.t))
	if err != nil {
		h.t.Fatal(err)
	}
	h.s.o.Backends[config.BackendClaude] = cl
	set := h.config()
	set.Backends.Claude.Enabled = true
	h.settings.Store(&set)
}

// claude adds an enabled claude sign-in account; accounts with a higher priority are chosen first.
func (h *harness) claude(label string, priority int) account.Account {
	h.t.Helper()
	a, _, err := h.accounts.Add(h.t.Context(), account.New{Backend: config.BackendClaude, Kind: account.KindIsolated,
		Label: label, Priority: priority, Enabled: true}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.pool.Notify()
	return a
}

func claudeInit(sess string) string {
	return claudeLine(map[string]any{"type": "system", "subtype": "init", "session_id": sess, "model": "claude-opus-5"})
}

// claudeLimitScript does part of the work, then stops on the 5-hour limit, as Claude Code does.
func claudeLimitScript(sess string) string {
	return script(claudeInit(sess), claudeText(sess, "Half of the limiter is written."),
		claudeLine(map[string]any{"type": "result", "subtype": "success", "is_error": true, "result": claudeLimitMessage,
			"session_id": sess, "usage": map[string]any{"input_tokens": 76000, "output_tokens": 1000}}),
		"#!exit 1")
}

func claudeOKScript(sess, response string) string {
	return script(claudeInit(sess), claudeText(sess, response),
		claudeLine(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": response,
			"session_id": sess, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20}}))
}

// checkLimitAttempt checks the first attempt of tk: quota exhausted with the message's reset time, and acc resting
// until that time.
func (h *harness) checkLimitAttempt(tk *Task, acc account.Account) {
	h.t.Helper()
	a := tk.Turns[0].Attempts[0]
	if a.AccountID != acc.ID || a.Status != AttemptQuota || a.Error != claudeLimitMessage || a.Outcome == nil ||
		a.Outcome.ResetAt.IsZero() || a.Usage == nil || a.Usage.InputTokens != 76000 {
		h.t.Fatalf("first attempt = %+v (outcome %+v)", a, a.Outcome)
	}
	states, err := h.pool.States(h.t.Context())
	if err != nil {
		h.t.Fatal(err)
	}
	var rest *pool.Cooldown
	for i := range states {
		if states[i].Account.ID == acc.ID && len(states[i].Cooldowns) == 1 {
			rest = &states[i].Cooldowns[0]
		}
	}
	if rest == nil || rest.Group != "Claude" || rest.Reason != claudeLimitMessage {
		h.t.Fatalf("the account does not rest: %+v", rest)
	}
	// The reset time is read relative to the attempt's real end; the engine's clock is fake. When the reset is after
	// the fake now, the account rests exactly until it.
	if reset := a.Outcome.ResetAt; reset.After(rest.CreatedAt) && !rest.Until.Equal(reset) {
		h.t.Errorf("rest until %v, want the message's reset %v", rest.Until, reset)
	}
	evs := h.events(tk.ID)
	if len(eventsOf(evs, api.EventQuotaExhausted)) != 1 {
		h.t.Errorf("events = %v", types(evs))
	}
}

// TestClaudeLimitWaitsAndResumes: the only claude account reaches its 5-hour limit in the middle of a turn. The
// task waits for the reset instead of failing, then resumes the same conversation on the same account.
func TestClaudeLimitWaitsAndResumes(t *testing.T) {
	h := newHarness(t)
	h.useClaude()
	acc := h.claude("solo", 0)
	const sess = "sess-limit"
	h.queue.Push(t, claudeLimitScript(sess))
	h.queue.Push(t, claudeOKScript(sess, "Finished the limiter after the reset."))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendClaude, Model: "claude-opus-5"})

	h.waitFor("the task to wait for the reset", func() bool {
		tk := h.task(sum.ID)
		return tk.Status == StatusWaitingQuota && tk.Waiting == string(pool.ReasonResting)
	})
	tk := h.task(sum.ID)
	if tk.AccountID != acc.ID || tk.ConversationID != sess {
		t.Fatalf("owner = %s %s", tk.AccountID, tk.ConversationID)
	}
	h.checkLimitAttempt(tk, acc)

	// The rest ends: the same conversation goes on.
	if until := tk.WaitingUntil; until.After(h.clk.Now()) {
		h.clk.Advance(until.Sub(h.clk.Now()))
	}
	h.waitFor("the task to resume and finish", func() bool {
		if h.task(sum.ID).Status == StatusWaitingQuota {
			h.clk.Advance(time.Minute)
		}
		return h.task(sum.ID).Status == StatusDone && !h.s.hasRun(sum.ID)
	})
	tk = h.task(sum.ID)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[1].AccountID != acc.ID || at[1].Status != AttemptOK || !at[1].Continuing ||
		tk.ConversationID != sess || tk.Error != "" {
		t.Fatalf("task %s (%q), attempts %+v", tk.Status, tk.Error, at)
	}
	recs := h.queue.Records(t)
	if len(recs) != 2 {
		t.Fatalf("runs = %d", len(recs))
	}
	if i := slices.Index(recs[1].Args, "--resume"); i < 0 || i+1 >= len(recs[1].Args) || recs[1].Args[i+1] != sess {
		t.Errorf("the second run did not resume the conversation: %q", recs[1].Args)
	}
	if slices.Contains(recs[0].Args, "--resume") {
		t.Errorf("the first run resumed something: %q", recs[0].Args)
	}
	// The tokens of the work done before the limit stay counted.
	if u := tk.LastTurn().Usage; u == nil || u.InputTokens != 76100 {
		t.Errorf("turn usage = %+v", u)
	}
}

// TestClaudeLimitRepeatedRests: the only claude account hits its limit five times in a row in one turn (Claude resets
// late, the resumed run hits the limit again). Quota attempts do not use up the attempt budget (one account plus
// extraAttempts = 4), so the task rests each time and ends done.
func TestClaudeLimitRepeatedRests(t *testing.T) {
	h := newHarness(t)
	h.useClaude()
	acc := h.claude("solo", 0)
	const sess, hits = "sess-again", 5
	for range hits {
		h.queue.Push(t, claudeLimitScript(sess))
	}
	h.queue.Push(t, claudeOKScript(sess, "Finished the limiter after five rests."))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendClaude, Model: "claude-opus-5"})

	h.waitFor("the task to finish after the rests", func() bool {
		tk := h.task(sum.ID)
		if tk.Status == StatusWaitingQuota {
			if until := tk.WaitingUntil; until.After(h.clk.Now()) {
				h.clk.Advance(until.Sub(h.clk.Now()))
			} else {
				h.clk.Advance(time.Minute)
			}
		}
		return (tk.Status == StatusDone || tk.Status == StatusFailed) && !h.s.hasRun(sum.ID)
	})
	tk := h.task(sum.ID)
	at := tk.LastTurn().Attempts
	if tk.Status != StatusDone || tk.Error != "" || len(at) != hits+1 {
		t.Fatalf("task %s (%q), %d attempts", tk.Status, tk.Error, len(at))
	}
	for i, a := range at {
		want := AttemptQuota
		if i == hits {
			want = AttemptOK
		}
		if a.Status != want || a.AccountID != acc.ID {
			t.Errorf("attempt %d = %s on %s", i+1, a.Status, a.AccountID)
		}
	}
	if n := len(eventsOf(h.events(sum.ID), api.EventQuotaExhausted)); n != hits {
		t.Errorf("quota events = %d", n)
	}
}

// TestWaitNamesFreeKeyProviders (field report F14): a task without provider waits while the only claude sign-in
// account rests. A free API-key account of a provider without tiers is named in the wait detail, the report and its
// summary, with "and a model"; the task never moves to it. Without the API-key account the detail is the plain one.
func TestWaitNamesFreeKeyProviders(t *testing.T) {
	for _, withKey := range []bool{false, true} {
		t.Run(map[bool]string{false: "sign-in only", true: "free API-key account"}[withKey], func(t *testing.T) {
			h := newHarness(t)
			h.useClaude()
			h.claude("solo", 0)
			if withKey {
				ctx := t.Context()
				if _, err := h.accounts.CreateProvider(ctx, account.Provider{ID: "anthropic",
					Backend: config.BackendClaude, Official: true, Enabled: true}); err != nil {
					t.Fatal(err)
				}
				key, _, err := h.accounts.Add(ctx, account.New{Backend: config.BackendClaude, Kind: account.KindAPIKey,
					Label: "key", Provider: "anthropic", Enabled: true}, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := h.accounts.SetKey(ctx, key.ID, "qz7Kp2mWx9Rt4Lb8Nc3Vd6Fh1Jg5Ty0Ue"); err != nil {
					t.Fatal(err)
				}
				h.pool.Notify()
			}
			h.queue.Push(t, claudeLimitScript("sess-f14"))
			sum := h.delegate(api.DelegateRequest{Backend: config.BackendClaude, Model: "claude-opus-5"})
			h.waitFor("the task to wait for the reset", func() bool {
				tk := h.task(sum.ID)
				return tk.Status == StatusWaitingQuota && tk.Waiting == string(pool.ReasonResting)
			})
			tk := h.task(sum.ID)
			const hint = "; free API-key accounts: pass provider: anthropic and a model"
			if !strings.HasPrefix(tk.WaitDetail, "all 1 claude accounts are resting") ||
				strings.HasSuffix(tk.WaitDetail, hint) != withKey || strings.Count(tk.WaitDetail, "anthropic") > 1 {
				t.Fatalf("wait detail = %q", tk.WaitDetail)
			}
			rep, err := h.s.Report(t.Context(), sum.ID, "")
			if err != nil {
				t.Fatal(err)
			}
			if rep.WaitDetail != tk.WaitDetail || !strings.Contains(ops.ReportSummary(rep), "waiting: "+tk.WaitDetail) {
				t.Errorf("report wait detail = %q, summary %q", rep.WaitDetail, ops.ReportSummary(rep))
			}
			if at := tk.LastTurn().Attempts; len(at) != 1 || len(h.queue.Records(t)) != 1 {
				t.Errorf("the task moved on: %d attempts", len(at))
			}
		})
	}
}

// TestAttemptBudget: quota attempts count only toward the quota cap, every other attempt toward maxAttempts.
func TestAttemptBudget(t *testing.T) {
	tu := &Turn{Attempts: []*Attempt{{Status: AttemptQuota}, {Status: AttemptTransient}, {Status: AttemptQuota},
		{Status: AttemptRunning}}}
	if counted, rests := attemptBudget(tu, 4, AttemptQuota); counted != 1 || rests != 3 {
		t.Errorf("quota end = %d counted, %d rests", counted, rests)
	}
	if counted, rests := attemptBudget(tu, 4, AttemptError); counted != 2 || rests != 2 {
		t.Errorf("error end = %d counted, %d rests", counted, rests)
	}
}

// TestClaudeLimitHandsOver: with a second claude account free, the turn moves on to it at once.
func TestClaudeLimitHandsOver(t *testing.T) {
	h := newHarness(t)
	h.useClaude()
	first := h.claude("first", 10)
	second := h.claude("second", 0)
	h.queue.Push(t, claudeLimitScript("sess-a"))
	h.queue.Push(t, claudeOKScript("sess-b", "Finished the limiter on the second account."))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendClaude, Model: "claude-opus-5"})
	tk := h.waitStatus(sum.ID, StatusDone)
	h.checkLimitAttempt(tk, first)
	at := tk.LastTurn().Attempts
	if len(at) != 2 || at[1].AccountID != second.ID || at[1].Status != AttemptOK || at[1].Continuing ||
		tk.AccountID != second.ID || tk.ConversationID != "sess-b" {
		t.Fatalf("attempts = %+v", at)
	}
	if len(eventsOf(h.events(sum.ID), api.EventHandoff)) != 1 {
		t.Errorf("events = %v", types(h.events(sum.ID)))
	}
}
