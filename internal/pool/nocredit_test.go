package pool

import (
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// TestNoCreditRestsUntilTheUserActs (review N1): an account whose credit ran out is left out with no rest time, so a
// request with nothing else fails at once (no_account, naming the key) instead of waiting. A new key or an edit of
// the account lifts it; time does not.
func TestNoCreditRestsUntilTheUserActs(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.mu.Lock()
	f.noReader = map[string]bool{"codex": true}
	f.mu.Unlock()
	broke := f.apiKeyAccount("openrouter", "sk-test-0123456789abcdef")
	req := Request{TaskID: "t", Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openrouter"}
	if err := f.p.MarkNoCredit(ctx, broke.ID, "Credit balance is too low"); err != nil {
		t.Fatal(err)
	}
	if err := f.p.MarkNoCredit(ctx, "missing", "x"); !errors.Is(err, account.ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	noCredit := func(what string) {
		t.Helper()
		_, err := f.p.Select(ctx, req)
		var u *Unavailable
		if !errors.As(err, &u) || !errors.Is(err, ErrNoAccount) || len(u.NoCredit) != 1 ||
			u.Error() != "the openrouter key has no credit left; top up or change the key" {
			t.Fatalf("%s: select = %v", what, err)
		}
	}
	noCredit("after the mark")
	f.clk.Advance(400 * 24 * time.Hour) // over a year later: still no credit
	noCredit("a year later")
	states, err := f.p.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cd := states[0].Cooldowns; len(cd) != 1 || cd[0].Group != "" || cd[0].Reason != "Credit balance is too low" ||
		!cd[0].Passive {
		t.Errorf("cooldowns = %+v", cd)
	}

	// A second key of the provider takes the work.
	funded := f.apiKeyAccount("openrouter", "sk-test-0123456789abcdeg")
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != funded.ID {
		t.Fatalf("select with a funded key = %v, %v", c.Account.ID, err)
	}
	f.disable(funded.ID)

	// A new key lifts the rest.
	if _, err := f.m.SetKey(ctx, broke.ID, "sk-test-0123456789abcdeh"); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Second)
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != broke.ID {
		t.Fatalf("after a new key: %v, %v", c.Account.ID, err)
	}
	// So does an edit of the account (switched off and on after a top-up).
	if err := f.p.MarkNoCredit(ctx, broke.ID, "Credit balance is too low"); err != nil {
		t.Fatal(err)
	}
	noCredit("marked again")
	f.clk.Advance(time.Second)
	f.disable(broke.ID)
	on := true
	if _, err := f.m.Update(ctx, broke.ID, account.Patch{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != broke.ID {
		t.Fatalf("after an edit: %v, %v", c.Account.ID, err)
	}
}

// TestNoCreditNamedWhileWaiting: with other accounts resting, the request waits for them and the detail names the
// key without credit, each provider once.
func TestNoCreditNamedWhileWaiting(t *testing.T) {
	u := &Unavailable{Reason: ReasonResting, Backend: "claude", Resting: 1,
		NoCredit: []NoCredit{{AccountID: "a", Provider: "anthropic"}, {AccountID: "b", Provider: "anthropic"},
			{AccountID: "c", Label: "work"}}}
	want := "all 1 claude accounts are resting; the anthropic key, the claude account work have no credit left; " +
		"top up or change the keys"
	if got := u.Error(); got != want {
		t.Errorf("detail = %q\nwant      %q", got, want)
	}
}
