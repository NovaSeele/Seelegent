package httpapi

import (
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// accountView is what an account DTO is built from besides the pool state.
type accountView struct {
	settings  config.Settings
	providers map[string]account.Provider
	backends  map[string]backend.Backend
	signingIn func(id string) bool
	now       time.Time
}

// account maps a pool state to the API's Account. The email is never known to the daemon (only a hash of it), so
// Account.Email stays empty.
func (v *accountView) account(st *pool.AccountState) api.Account {
	a := st.Account
	out := api.Account{
		ID: a.ID, Backend: a.Backend, Kind: string(a.Kind), Label: a.Label, Enabled: a.Enabled,
		Priority: a.Priority, Provider: a.Provider, KeyHint: a.KeyHint, Status: api.AccountStatus(st.Status),
		TestResult: a.TestState(), TestedAt: api.FormatTime(a.TestedAt),
		TwinOf: st.TwinOf, Busy: st.Busy, MaxConcurrent: v.settings.MaxConcurrentPerAccount,
		SigningIn: v.signingIn(a.ID), CreatedAt: api.FormatTime(a.CreatedAt), UpdatedAt: api.FormatTime(a.UpdatedAt),
	}
	out.Quota = v.quota(st)
	until, resting := v.resting(st)
	out.Resting, out.RestingUntil = resting, api.FormatTime(until)
	out.Models = v.models(st)
	if a.Kind == account.KindAPIKey {
		source := account.SpendNone
		if p, ok := v.providers[a.Provider]; ok {
			out.ModelPerTask = p.ModelPerTask()
			source = p.SpendSource()
		}
		spendDTO(&out, &a, source)
	}
	if st.RestReason != "" {
		// Rests until the user acts (pool.RestCredits, pool.RestBudget): a reason, never a date.
		out.Resting, out.RestingUntil, out.RestReason = true, "", st.RestReason
	}
	return out
}

// spendDTO fills an API-key account's spend and budget fields.
func spendDTO(out *api.Account, a *account.Account, source string) {
	stop := a.BudgetStop
	out.SpentUSD, out.SpentTokens = a.Spent.USD, a.Spent.Tokens
	out.SpentInputTokens, out.SpentOutputTokens = a.Spent.Input, a.Spent.Output
	out.SpentSince, out.SpendSource, out.BudgetStop = api.FormatTime(a.KeySetAt), source, &stop
	out.BudgetUSD, out.BudgetState = a.BudgetUSD, account.BudgetLevel(source, a.Spent.USD, a.BudgetUSD)
	if pk := a.PreviousKey; pk != nil {
		out.PreviousKey = &api.PreviousKey{KeyHint: pk.KeyHint, Since: api.FormatTime(pk.Since),
			Until: api.FormatTime(pk.Until), SpentUSD: pk.Spent.USD, SpentTokens: pk.Spent.Tokens,
			SpentInputTokens: pk.Spent.Input, SpentOutputTokens: pk.Spent.Output, BudgetUSD: pk.BudgetUSD}
	}
}

// quota maps the account's quota snapshot; an account never read gets an empty one of the right source.
func (v *accountView) quota(st *pool.AccountState) api.QuotaSnapshot {
	if !st.HasQuota {
		src := quota.SourceActive
		if st.Account.Kind == account.KindAPIKey {
			src = quota.SourcePassive
		} else if b, ok := v.backends[st.Account.Backend]; !ok || b.QuotaReader() == nil {
			src = quota.SourcePassive
		}
		return api.QuotaSnapshot{Source: string(src), Groups: []api.Group{}}
	}
	return quotaDTO(st.Quota)
}

// quotaDTO maps a quota snapshot 1:1 (times as API strings; the account id, backend, identity and model catalog
// are not part of it).
func quotaDTO(s quota.Snapshot) api.QuotaSnapshot {
	out := api.QuotaSnapshot{
		Source: string(s.Source), FetchedAt: api.FormatTime(s.FetchedAt), Plan: s.Plan, NoPlan: s.NoPlan,
		NoPlanReason: s.NoPlanReason, ExhaustedUntil: api.FormatTime(s.ExhaustedUntil),
		ExhaustedReason: s.ExhaustedReason, Groups: make([]api.Group, 0, len(s.Groups)),
	}
	if out.Source == "" {
		out.Source = string(quota.SourceActive)
	}
	for _, g := range s.Groups {
		ag := api.Group{Name: g.Name, Windows: make([]api.QuotaWindow, 0, len(g.Windows))}
		for _, w := range g.Windows {
			ag.Windows = append(ag.Windows, api.QuotaWindow{Window: w.Window, Label: w.Label, Minutes: w.Minutes,
				Remaining: w.Remaining, ResetAt: api.FormatTime(w.ResetAt)})
		}
		out.Groups = append(out.Groups, ag)
	}
	if s.Error != nil {
		out.Error = &api.QuotaError{Kind: string(s.Error.Kind), Message: s.Error.Message, At: api.FormatTime(s.Error.At)}
	}
	return out
}

// resting reports whether the account rests now: a cooldown in force, a rate limit, or a quota window at or below
// minRemaining. until is the latest end among them (zero when none says when).
func (v *accountView) resting(st *pool.AccountState) (until time.Time, resting bool) {
	later := func(t time.Time) {
		if t.After(until) {
			until = t
		}
	}
	for _, c := range st.Cooldowns {
		if c.Until.After(v.now) {
			resting = true
			later(c.Until)
		}
	}
	if !st.HasQuota {
		return until, resting
	}
	if st.Quota.ExhaustedUntil.After(v.now) {
		resting = true
		later(st.Quota.ExhaustedUntil)
	}
	if st.Quota.Known() {
		for _, g := range st.Quota.Groups {
			for _, w := range g.Windows {
				if w.RemainingAt(v.now) <= v.settings.MinRemaining {
					resting = true
					later(w.ResetAt)
				}
			}
		}
	}
	return until, resting
}

// models lists the models the account may run: its catalog (codex) filtered by the policy, else the tier models
// followed by its provider's allow-list (API-key accounts).
func (v *accountView) models(st *pool.AccountState) []string {
	var prov *account.Provider
	if st.Account.Kind == account.KindAPIKey {
		if p, ok := v.providers[st.Account.Provider]; ok {
			prov = &p
		}
	}
	pol, err := backend.PolicyFor(st.Account.Backend, v.settings, prov)
	if err != nil {
		return []string{}
	}
	if st.HasQuota && st.Quota.Models != nil {
		out := []string{}
		for _, m := range st.Quota.Models {
			if pol.Allowed(m) == nil && !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
		return out
	}
	out := tierModels(pol.Tiers)
	if prov != nil {
		for _, m := range prov.Models {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// tierModels returns the distinct models of a tier mapping, in tier order.
func tierModels(t config.TierModels) []string {
	out := []string{}
	for _, tier := range config.Tiers() {
		if m, ok := t.Model(tier); ok && m != "" && !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// providerDTO maps a provider; accounts is the number of accounts that use it.
func providerDTO(p account.Provider, accounts int) api.Provider {
	return api.Provider{
		ID: p.ID, Name: p.Name, Backend: p.Backend, BaseURL: p.BaseURL, Enabled: p.Enabled, Models: modelList(p.Models),
		Accounts: accounts, Preset: p.Preset, WireAPI: p.WireAPI, Official: p.Official, AllowedModels: p.AllowedModels,
		Tiers: p.Tiers, ModelPerTask: p.ModelPerTask(), SpendSource: p.SpendSource(), CreatedAt: api.FormatTime(p.CreatedAt),
		UpdatedAt: api.FormatTime(p.UpdatedAt),
	}
}

// noticeDTOs maps notices (nil for none, so the field is left out).
func noticeDTOs(in []account.Notice) []api.Notice {
	if len(in) == 0 {
		return nil
	}
	out := make([]api.Notice, 0, len(in))
	for _, n := range in {
		out = append(out, api.Notice{Code: n.Code, Field: n.Field, Message: n.Message})
	}
	return out
}

// modelList is a copy of an allow-list, never nil.
func modelList(models []string) []string {
	return append(make([]string, 0, len(models)), models...)
}

// presetDTOs lists the provider presets by id. "anthropic" suggests the claude backend's tier models from the
// settings, when they are set; the other presets suggest none.
func presetDTOs(set config.Settings) []api.ProviderPreset {
	var claudeTiers config.TierModels
	if b, ok := set.Backends.Backend(config.BackendClaude); ok {
		claudeTiers = b.Tiers
	}
	presets := account.Presets()
	ids := make([]string, 0, len(presets))
	for id := range presets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]api.ProviderPreset, 0, len(ids))
	for _, id := range ids {
		p := presets[id]
		pr := api.ProviderPreset{ID: id, Name: p.Name, Backend: p.Backend, BaseURL: p.BaseURL,
			Enabled: true, Models: []string{}, WireAPI: p.WireAPI, Official: p.Official}
		if id == "anthropic" {
			pr.Tiers = claudeTiers
		}
		out = append(out, pr)
	}
	return out
}

// applyProvider applies the fields of in to p (the stored provider, or the new one). The allow-list follows these
// rules (api.ProviderInput):
//   - a non-empty models replaces the list; its pattern replaces allowedModels (account.Provider.Validate builds it);
//   - otherwise a given allowedModels is set as the pattern, and models becomes the list it was built from, or []
//     for a hand-written pattern; an allowedModels that only repeats the pattern of the stored list, sent together
//     with models: [], is that list's echo and does not keep it;
//   - otherwise models: [] clears both: any model.
//
// The tiers never change through models or allowedModels.
func applyProvider(p *account.Provider, in *api.ProviderInput) {
	if in.Name != nil {
		p.Name = *in.Name
	}
	if in.BaseURL != nil {
		p.BaseURL = *in.BaseURL
	}
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	if in.WireAPI != nil {
		p.WireAPI = *in.WireAPI
	}
	if in.Tiers != nil {
		p.Tiers = *in.Tiers
	}
	listEcho := in.Models != nil && in.AllowedModels != nil && len(p.Models) > 0 &&
		*in.AllowedModels == p.AllowedModels
	switch {
	case in.Models != nil && account.ModelsPattern(*in.Models) != "":
		p.Models, p.AllowedModels = slices.Clone(*in.Models), ""
	case in.AllowedModels != nil && !listEcho:
		p.AllowedModels = strings.TrimSpace(*in.AllowedModels)
		p.Models = account.ModelsOf(p.AllowedModels)
	case in.Models != nil:
		p.Models, p.AllowedModels = []string{}, ""
	}
}

// usageSummaryDTO maps a usage summary 1:1.
func usageSummaryDTO(s usage.Summary) api.Summary {
	out := api.Summary{
		Days: s.Days, Since: api.FormatTime(s.Since), Until: api.FormatTime(s.Until),
		PreviousSince: api.FormatTime(s.PreviousSince), DataSince: api.FormatTime(s.DataSince),
		Totals: api.Totals(s.Totals), Previous: api.Totals(s.Previous), Daily: make([]api.DayTotal, 0, len(s.Daily)),
		Projects: make([]api.ProjectUsage, 0, len(s.Projects)), MoreProjects: s.MoreProjects,
		Accounts: make([]api.AccountUsage, 0, len(s.Accounts)), MoreAccounts: s.MoreAccounts,
	}
	for _, d := range s.Daily {
		out.Daily = append(out.Daily, api.DayTotal(d))
	}
	for _, p := range s.Projects {
		ap := api.ProjectUsage{Key: p.Key, Name: p.Name, Tokens: p.Tokens, CostUSD: p.CostUSD, DurationMS: p.DurationMS,
			Tasks: p.Tasks, Turns: p.Turns, Items: make([]api.TaskUsage, 0, len(p.Items)), More: p.More}
		for i := range p.Items {
			t := &p.Items[i]
			at := api.TaskUsage{ID: t.ID, Title: t.Title, Status: t.Status, Backend: t.Backend, Tokens: t.Tokens,
				CostUSD: t.CostUSD, DurationMS: t.DurationMS, LastAt: api.FormatTime(t.LastAt), AccountID: t.AccountID,
				Account: t.Account, Turns: t.Turns, Items: make([]api.TurnUsage, 0, len(t.Items)), More: t.More}
			for _, u := range t.Items {
				at.Items = append(at.Items, api.TurnUsage{N: u.N, Tokens: u.Tokens, CostUSD: u.CostUSD,
					DurationMS: u.DurationMS, Attempts: u.Attempts, At: api.FormatTime(u.At), AccountID: u.AccountID,
					Account: u.Account, Model: u.Model})
			}
			ap.Items = append(ap.Items, at)
		}
		out.Projects = append(out.Projects, ap)
	}
	for _, a := range s.Accounts {
		aa := api.AccountUsage{ID: a.ID, Label: a.Label, Backend: a.Backend, Tokens: a.Tokens, CostUSD: a.CostUSD,
			Attempts: a.Attempts, DurationMS: a.DurationMS, Models: make([]api.ModelUsage, 0, len(a.Models)),
			More: a.More}
		for _, m := range a.Models {
			aa.Models = append(aa.Models, api.ModelUsage(m))
		}
		out.Accounts = append(out.Accounts, aa)
	}
	return out
}

// usageHistoryDTO maps a usage history 1:1.
func usageHistoryDTO(h usage.History) api.History {
	out := api.History{Bucket: string(h.Bucket), Since: api.FormatTime(h.Since), Until: api.FormatTime(h.Until),
		FirstAt: api.FormatTime(h.FirstAt)}
	if h.Rows != nil {
		out.Rows = make([]api.UsageSnapshot, 0, len(h.Rows))
		for i := range h.Rows {
			r := &h.Rows[i]
			out.Rows = append(out.Rows, api.UsageSnapshot{At: api.FormatTime(r.At), AccountID: r.AccountID,
				Backend: r.Backend, Label: r.Label, Enabled: r.Enabled, Status: r.Status, Plan: r.Plan,
				Quota: usageWindows(r.Quota), TokensToday: r.TokensToday, CostToday: r.CostToday})
		}
	}
	if h.Days != nil {
		out.Days = make([]api.Day, 0, len(h.Days))
		for i := range h.Days {
			d := &h.Days[i]
			out.Days = append(out.Days, api.Day{Date: d.Date, AccountID: d.AccountID, Backend: d.Backend,
				Label: d.Label, Enabled: d.Enabled, Status: d.Status, Plan: d.Plan, Samples: d.Samples,
				FirstAt: api.FormatTime(d.FirstAt), LastAt: api.FormatTime(d.LastAt), Quota: usageWindows(d.Quota),
				TokensToday: d.TokensToday, CostToday: d.CostToday})
		}
	}
	return out
}

func usageWindows(ws []usage.Window) []api.UsageWindow {
	out := make([]api.UsageWindow, 0, len(ws))
	for _, w := range ws {
		out = append(out, api.UsageWindow{Group: w.Group, Window: w.Window, Label: w.Label, Remaining: w.Remaining,
			ResetAt: api.FormatTime(w.ResetAt), MinRemaining: w.MinRemaining})
	}
	return out
}
