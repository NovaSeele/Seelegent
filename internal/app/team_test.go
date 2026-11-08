package app

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// TestContestAccountsFollowThePool: the contest's quota plan counts an account usable exactly when the pool would
// run work on it. Accounts without a quota reader are in the error state (no numbers yet) and still run tasks, so a
// contest must not be refused on them.
func TestContestAccountsFollowThePool(t *testing.T) {
	d := newDaemon(t)
	d.account("one", 0)
	ctx := t.Context()
	if _, _, err := d.a.Accounts.Add(ctx, account.New{Backend: config.BackendAgy, Kind: account.KindIsolated,
		Label: "two", Priority: 1}, nil); err != nil {
		t.Fatal(err)
	}
	// After a turn the pool tries to read the account's quota; without a reader it lands in the error state.
	d.queue.Push(t, ok("conv-1", "Done."))
	sum, err := d.a.Tasks.Delegate(ctx, api.DelegateRequest{Prompt: "Do it.", Cwd: d.cwd})
	if err != nil {
		t.Fatal(err)
	}
	d.wait(sum.ID)
	// The quota read after the turn runs on its own: wait until account "one" lands in the error state, so that
	// States and contestAccounts below read the same pool.
	var states []pool.AccountState
	waitFor(t, func() bool {
		var err error
		if states, err = d.a.Pool.States(ctx); err != nil {
			t.Fatal(err)
		}
		for _, st := range states {
			if st.Account.Label == "one" {
				return st.Status == pool.StatusError
			}
		}
		return false
	})
	got, err := d.a.contestAccounts(ctx)
	if err != nil || len(got) != len(states) {
		t.Fatalf("accounts = %+v, %v", got, err)
	}
	for i, st := range states {
		want := st.Account.Enabled && (st.Status.Usable() || st.Status == pool.StatusNew)
		if got[i].Usable != want {
			t.Errorf("%s (%s, enabled %t): usable = %t", st.Account.Label, st.Status, st.Account.Enabled, got[i].Usable)
		}
		if st.Account.Label == "one" && (st.Status != pool.StatusError || !got[i].Usable) {
			t.Errorf("an account without quota numbers: status %s, usable %t", st.Status, got[i].Usable)
		}
		if st.Account.Label == "two" && got[i].Usable {
			t.Error("a turned-off account is usable")
		}
	}
}
