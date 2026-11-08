package pool

import (
	"errors"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// TestKeyProvidersNamed (field report F14): a request without provider waits while its sign-in accounts rest; the
// free API-key accounts of the backend are named, never taken.
func TestKeyProvidersNamed(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.mu.Lock()
	f.noReader = map[string]bool{"codex": true}
	f.mu.Unlock()
	signIn := f.add(account.New{Backend: "codex", Kind: account.KindIsolated})
	if _, err := f.p.MarkExhausted(ctx, signIn.ID, Exhaustion{Group: "G", Reason: "limit"}); err != nil {
		t.Fatal(err)
	}
	req := Request{TaskID: "t", Backend: "codex", Group: "G", Model: "gpt-x"}
	unavailable := func() *Unavailable {
		t.Helper()
		_, err := f.p.Select(ctx, req)
		var u *Unavailable
		if !errors.As(err, &u) || u.Reason != ReasonResting {
			t.Fatalf("select = %v", err)
		}
		return u
	}
	// No API-key account: the detail is the plain resting one.
	if u := unavailable(); len(u.KeyProviders) != 0 || strings.Contains(u.Error(), "API-key") {
		t.Fatalf("without API-key accounts: %+v %q", u.KeyProviders, u.Error())
	}

	f.apiKeyAccount("openrouter", "sk-test-0123456789abcdef")
	f.apiKeyAccount("openrouter", "sk-test-0123456789abcdeg") // the same provider is named once
	f.apiKeyAccount("nokey", "")                              // no key: not selectable
	u := unavailable()
	if len(u.KeyProviders) != 1 || u.KeyProviders[0].ID != "openrouter" {
		t.Fatalf("key providers = %+v", u.KeyProviders)
	}
	if got := u.Error(); !strings.HasPrefix(got, "all 1 codex accounts are resting until ") ||
		!strings.HasSuffix(got, " (quota group G); free API-key accounts: pass provider: openrouter") {
		t.Errorf("detail = %q", got)
	}
	u.KeyProviders[0].NeedsModel = true
	if got := u.Error(); !strings.HasSuffix(got, "pass provider: openrouter and a model") {
		t.Errorf("detail with a model = %q", got)
	}

	// A request that pins an account, or names a provider, gets no such hint.
	pinned := req
	pinned.Account = signIn.ID
	_, err := f.p.Select(ctx, pinned)
	var nu *Unavailable
	if !errors.As(err, &nu) || len(nu.KeyProviders) != 0 {
		t.Errorf("pinned: %v", err)
	}
	req.Provider = "nokey"
	_, err = f.p.Select(ctx, req)
	if !errors.As(err, &nu) || len(nu.KeyProviders) != 0 {
		t.Errorf("with provider: %v", err)
	}
}

func TestKeyProvidersShownAtMostThree(t *testing.T) {
	u := &Unavailable{Reason: ReasonBusy, Backend: "claude", Busy: 1, KeyProviders: []KeyProvider{{ID: "a"},
		{ID: "b", NeedsModel: true}, {ID: "c"}, {ID: "d"}, {ID: "e"}}}
	want := "all claude accounts are busy (1 busy, 0 resting); free API-key accounts: pass provider: a, or " +
		"provider: b and a model, or provider: c, or 2 more"
	if got := u.Error(); got != want {
		t.Errorf("detail = %q\nwant      %q", got, want)
	}
}
