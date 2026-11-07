package automation

import (
	"context"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// PoolQuota is the QuotaSource of the account pool.
type PoolQuota struct {
	// States is pool.Pool.States.
	States func(ctx context.Context) ([]pool.AccountState, error)
	// Model resolves the model a run of backend uses ("" asks for the default).
	Model func(backend, model string) string
	// Group is the quota group of model on backend (backend.Backend.QuotaGroup).
	Group func(backend, model string) string
}

// Quota implements QuotaSource: the usable sign-in accounts of backend whose quota was read, with the windows
// that govern model. API-key accounts have no quota windows and are left out.
func (p PoolQuota) Quota(ctx context.Context, backend, model string) ([]AccountQuota, error) {
	states, err := p.States(ctx)
	if err != nil {
		return nil, err
	}
	if p.Model != nil {
		model = p.Model(backend, model)
	}
	group := backend
	if p.Group != nil {
		group = p.Group(backend, model)
	}
	out := []AccountQuota{}
	for _, st := range states {
		a := st.Account
		if a.Backend != backend || !a.Enabled || a.Kind == account.KindAPIKey || !st.Status.Usable() ||
			!st.HasQuota || !st.Quota.Known() {
			continue
		}
		ws, _ := st.Quota.Windows(group, model)
		out = append(out, AccountQuota{AccountID: a.ID, Windows: ws})
	}
	return out, nil
}
