package pool

import (
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// An API-key account that was never tested is selected like any other (its first turn is the check); a good turn
// marks it working, and a new key makes it not tested again, still selectable.
func TestNotTestedAccountIsSelected(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	keyed := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	if keyed.TestState() != account.TestNotTested {
		t.Fatalf("new key: %q", keyed.TestState())
	}
	req := Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openai"}
	c, err := f.p.Select(ctx, req)
	if err != nil || c.Account.ID != keyed.ID {
		t.Fatalf("not tested account: %+v, %v", c, err)
	}
	changed := f.p.Changed()
	if err := f.p.MarkSucceeded(ctx, keyed.ID, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Error("MarkSucceeded did not wake the account feed")
	}
	if a, err := f.m.Get(ctx, keyed.ID); err != nil || a.TestState() != account.TestOK {
		t.Fatalf("after a good turn: %+v, %v", a, err)
	}
	f.clk.Advance(time.Second)
	if _, err := f.m.SetKey(ctx, keyed.ID, "sk-test-fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	if c, err = f.p.Select(ctx, req); err != nil || c.Account.ID != keyed.ID || c.Account.TestState() != account.TestNotTested {
		t.Fatalf("after a new key: %+v, %v", c, err)
	}
	// Unknown accounts are no error: the account may be gone by the time its turn ends.
	if err := f.p.MarkSucceeded(ctx, "acc99", f.clk.Now()); err != nil {
		t.Errorf("unknown account: %v", err)
	}
}
