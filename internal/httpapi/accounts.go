package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

func (s *Server) accountRoutes() {
	s.handle("GET /accounts", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := s.accounts(r.Context())
		return http.StatusOK, list, err
	})
	s.handle("POST /accounts", s.addAccount)
	s.handle("PATCH /accounts/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.AccountPatch
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		if _, err := s.o.Accounts.Update(r.Context(), id, account.Patch{Label: req.Label, Enabled: req.Enabled,
			Priority: req.Priority}); err != nil {
			return 0, nil, err
		}
		if req.Enabled != nil && *req.Enabled {
			s.o.Quota.Request(id)
		}
		return s.accountChanged(r.Context(), id, http.StatusOK)
	})
	s.handle("DELETE /accounts/{id}", s.removeAccount)
	s.handle("POST /accounts/{id}/refresh", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		if _, err := s.o.Quota.Refresh(r.Context(), id); err != nil {
			if errors.Is(err, account.ErrNotFound) {
				return 0, nil, err
			}
			// A failed read is recorded on the account (quota.error); ErrPassive: nothing to read.
			if !errors.Is(err, quota.ErrPassive) {
				s.log.Debug("quota refresh", "account", id, "err", err)
			}
		}
		return s.accountChanged(r.Context(), id, http.StatusOK)
	})
	s.handle("POST /quota/refresh", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		s.o.Quota.RefreshAll(r.Context())
		s.o.Pool.Notify()
		list, err := s.accounts(r.Context())
		return http.StatusOK, list, err
	})
	s.handle("POST /accounts/{id}/login", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		l, err := s.o.SignIn.Start(r.Context(), r.PathValue("id"))
		s.kickAccounts()
		return http.StatusOK, l, err
	})
	s.handle("GET /accounts/{id}/login", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		l, ok := s.o.SignIn.Get(r.PathValue("id"))
		if !ok {
			return 0, nil, newError(api.CodeNotFound, "no sign-in of account %s", r.PathValue("id"))
		}
		return http.StatusOK, l, nil
	})
	s.handle("POST /accounts/{id}/login/code", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.LoginCodeRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		l, err := s.o.SignIn.SubmitCode(r.PathValue("id"), req.Code)
		return http.StatusOK, l, err
	})
	s.handle("POST /accounts/{id}/logout", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		if _, err := s.o.SignIn.Logout(r.Context(), id); err != nil {
			return 0, nil, err
		}
		s.o.Quota.Request(id)
		return s.accountChanged(r.Context(), id, http.StatusOK)
	})
	s.handle("PUT /accounts/{id}/key", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.KeyRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		if strings.TrimSpace(req.Key) == "" {
			return 0, nil, invalid("key is required").withField("key")
		}
		key, notices, err := account.NormalizeKey(req.Key)
		if err != nil {
			return 0, nil, account.KeyFieldAs("key", err)
		}
		a, err := s.o.Accounts.SetKey(r.Context(), id, key)
		if err != nil {
			return 0, nil, keyError(err)
		}
		if p, err := s.o.Accounts.GetProvider(r.Context(), a.Provider); err == nil {
			notices = append(notices, account.KeyNotices(p, key)...)
		}
		code, body, err := s.accountChanged(r.Context(), id, http.StatusOK)
		if out, ok := body.(api.Account); ok && err == nil {
			out.Notices = noticeDTOs(account.RenameNotices(notices, "apiKey", "key"))
			body = out
		}
		return code, body, err
	})
	s.handle("DELETE /accounts/{id}/key", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		id := r.PathValue("id")
		if _, err := s.o.Accounts.ClearKey(r.Context(), id); err != nil {
			return 0, nil, err
		}
		return s.accountChanged(r.Context(), id, http.StatusOK)
	})
	s.providerRoutes()
	s.handle("GET /usage/summary", s.usageSummary)
	s.handle("GET /usage/history", s.usageHistory)
}

// keyError keeps the submitted key out of an error message: only the error's kind is passed on.
func keyError(err error) error {
	switch {
	case errors.Is(err, account.ErrNotFound):
		return newError(api.CodeNotFound, "account not found")
	case errors.Is(err, account.ErrBadKey):
		// The message says what is wrong with the key (a URL, spaces, ...) and never quotes it.
		return account.KeyFieldAs("key", err)
	case errors.Is(err, account.ErrInvalid):
		return invalid("the key cannot be stored for this account (only API-key accounts take a key)").
			withField("key")
	}
	return fmt.Errorf("storing the API key failed: %w", err) // answered as internal; logs are redacted
}

// accountChanged wakes the pool and the account feed, sends account.updated and answers with the account.
func (s *Server) accountChanged(ctx context.Context, id string, status int) (code int, body any, err error) {
	s.o.Pool.Notify()
	s.kickAccounts()
	a, err := s.account(ctx, id)
	if err != nil {
		return 0, nil, err
	}
	s.accountSent(&a)
	return status, a, nil
}

// addAccount is POST /accounts: a sign-in account, or an API-key account with its provider and key in one
// transaction (addDirect).
func (s *Server) addAccount(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var req api.NewAccount
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	if req.NewProvider != nil || req.APIKey != "" || req.Kind == string(account.KindAPIKey) ||
		(req.Kind == "" && req.Provider != "") {
		return s.addDirect(r.Context(), &req)
	}
	if err := oneOf("backend", req.Backend, false, backend.Names()...); err != nil {
		return 0, nil, err
	}
	if err := oneOf("kind", req.Kind, false, string(account.KindIsolated), string(account.KindSystem),
		string(account.KindAPIKey)); err != nil {
		return 0, nil, err
	}
	n := account.New{Backend: req.Backend, Kind: account.Kind(req.Kind), Label: req.Label, Enabled: true,
		Provider: req.Provider}
	if req.Enabled != nil {
		n.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		n.Priority = *req.Priority
	}
	a, warnings, err := s.o.Accounts.Add(r.Context(), n, s.o.Settings.Settings().LinkIntoProfiles)
	if err != nil {
		return 0, nil, err
	}
	for _, wmsg := range warnings {
		s.log.Warn("account: profile link", "account", a.ID, "warning", wmsg)
	}
	if a.Enabled && a.Kind != account.KindAPIKey {
		s.o.Quota.Request(a.ID)
	}
	return s.accountChanged(r.Context(), a.ID, http.StatusCreated)
}

// removeAccount is DELETE /accounts/{id}: refused while the account runs a turn.
func (s *Server) removeAccount(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	id := r.PathValue("id")
	states, err := s.o.Pool.States(r.Context())
	if err != nil {
		return 0, nil, err
	}
	provider := ""
	for i := range states {
		if states[i].Account.ID != id {
			continue
		}
		provider = states[i].Account.Provider
		if states[i].Busy > 0 {
			return 0, nil, newError(api.CodeBadState, "account %s runs %d turn(s); remove it when they end", id,
				states[i].Busy).with("busy", states[i].Busy)
		}
	}
	s.o.SignIn.Cancel(id)
	if err := s.o.Accounts.Remove(r.Context(), id); err != nil {
		return 0, nil, err
	}
	s.accountGone(id, provider)
	s.o.Pool.Notify()
	s.kickAccounts()
	return http.StatusNoContent, nil, nil
}

// addDirect adds an API-key account with an existing provider or a new one (created or reused) and, optionally,
// its key: all or nothing (account.Manager.AddDirect). Errors name the request field (apiKey,
// newProvider.baseUrl, ...).
func (s *Server) addDirect(ctx context.Context, req *api.NewAccount) (code int, body any, err error) {
	if err := oneOf("backend", req.Backend, true, config.BackendClaude, config.BackendCodex); err != nil {
		return 0, nil, err
	}
	in := account.DirectNew{New: account.New{Backend: req.Backend, Kind: account.Kind(req.Kind), Label: req.Label,
		Enabled: true, Provider: req.Provider}, Key: req.APIKey}
	if req.Enabled != nil {
		in.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		in.Priority = *req.Priority
	}
	if req.NewProvider != nil {
		p, err := providerFromInput(req.NewProvider, false, "newProvider")
		if err != nil {
			return 0, nil, err
		}
		in.NewProvider = &p
	}
	res, err := s.o.Accounts.AddDirect(ctx, in, s.o.Settings.Settings().LinkIntoProfiles)
	if err != nil {
		return 0, nil, err
	}
	for _, wmsg := range res.Warnings {
		s.log.Warn("account: profile link", "account", res.Account.ID, "warning", wmsg)
	}
	if req.NewProvider != nil {
		s.providersChanged()
	}
	status, body, err := s.accountChanged(ctx, res.Account.ID, http.StatusCreated)
	if err != nil {
		return 0, nil, err
	}
	out := api.AccountCreated{Account: body.(api.Account), ProviderCreated: res.ProviderCreated}
	out.Notices = noticeDTOs(res.Notices)
	if req.NewProvider != nil {
		p, err := s.provider(ctx, res.Provider.ID)
		if err != nil {
			return 0, nil, err
		}
		out.NewProvider = &p
	}
	return status, out, nil
}

// providerFromInput builds a provider from a POST /providers body (or the newProvider of POST /accounts and POST
// /providers/test): from the preset when one is named, then the given fields. requireID makes id mandatory;
// prefix is put before the field names of errors ("newProvider").
func providerFromInput(in *api.ProviderInput, requireID bool, prefix string) (account.Provider, error) {
	field := func(f string) string {
		if prefix == "" {
			return f
		}
		return prefix + "." + f
	}
	p := account.Provider{Enabled: true}
	if in.ID != nil {
		p.ID = strings.TrimSpace(*in.ID)
	}
	if requireID && p.ID == "" {
		return account.Provider{}, invalid("id is required").withField(field("id"))
	}
	if in.Preset != nil && *in.Preset != "" {
		fp, err := account.FromPreset(p.ID, *in.Preset)
		if err != nil {
			return account.Provider{}, account.PrefixField(field("preset"), err)
		}
		p = fp
	}
	if in.Backend != nil {
		if p.Backend != "" && p.Backend != *in.Backend {
			return account.Provider{}, invalid("preset %s serves %s, not %s", p.Preset, p.Backend, *in.Backend).
				withField(field("backend"))
		}
		p.Backend = *in.Backend
	}
	if p.Backend == "" {
		return account.Provider{}, invalid("backend is required").withField(field("backend"))
	}
	applyProvider(&p, in)
	return p, nil
}

func (s *Server) providerRoutes() {
	s.handle("GET /providers", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := s.providers(r.Context())
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, api.ProviderList{Items: list, Presets: presetDTOs(s.o.Settings.Settings())}, nil
	})
	s.handle("POST /providers", s.createProvider)
	s.handle("POST /providers/test", s.testKey)
	s.handle("PATCH /providers/{id}", s.updateProvider)
	s.handle("DELETE /providers/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.o.Accounts.DeleteProvider(r.Context(), r.PathValue("id")); err != nil {
			return 0, nil, err
		}
		s.providersChanged()
		return http.StatusNoContent, nil, nil
	})
}

func (s *Server) providersChanged() {
	s.changed(api.StreamProvidersChanged)
	s.o.Pool.Notify()
	s.kickAccounts()
}

// providers lists the providers with their account counts.
func (s *Server) providers(ctx context.Context) ([]api.Provider, error) {
	list, err := s.o.Accounts.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.o.Accounts.List(ctx)
	if err != nil {
		return nil, err
	}
	count := map[string]int{}
	for i := range accounts {
		a := &accounts[i]
		if a.Provider != "" {
			count[a.Provider]++
		}
	}
	out := make([]api.Provider, 0, len(list))
	for i := range list {
		p := &list[i]
		out = append(out, providerDTO(*p, count[p.ID]))
	}
	return out, nil
}

func (s *Server) provider(ctx context.Context, id string) (api.Provider, error) {
	list, err := s.providers(ctx)
	if err != nil {
		return api.Provider{}, err
	}
	for i := range list {
		p := &list[i]
		if p.ID == id {
			return *p, nil
		}
	}
	return api.Provider{}, newError(api.CodeNotFound, "provider %s not found", id)
}

// createProvider is POST /providers: from a preset when one is named, then the given fields.
func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var in api.ProviderInput
	if err := decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	p, err := providerFromInput(&in, true, "")
	if err != nil {
		return 0, nil, err
	}
	notices, err := p.Check()
	if err != nil {
		return 0, nil, err
	}
	created, err := s.o.Accounts.CreateProvider(r.Context(), p)
	if err != nil {
		return 0, nil, err
	}
	s.providersChanged()
	out, err := s.provider(r.Context(), created.ID)
	out.Notices = noticeDTOs(notices)
	return http.StatusCreated, out, err
}

// updateProvider is PATCH /providers/{id}; id, backend and preset cannot change.
func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var in api.ProviderInput
	if err := decode(w, r, &in); err != nil {
		return 0, nil, err
	}
	p, err := s.o.Accounts.GetProvider(r.Context(), r.PathValue("id"))
	if err != nil {
		return 0, nil, err
	}
	switch {
	case in.ID != nil && *in.ID != p.ID:
		return 0, nil, invalid("a provider's id cannot change").withField("id")
	case in.Backend != nil && *in.Backend != p.Backend:
		return 0, nil, invalid("a provider's backend cannot change").withField("backend")
	case in.Preset != nil && *in.Preset != p.Preset:
		return 0, nil, invalid("a provider's preset cannot change").withField("preset")
	}
	applyProvider(&p, &in)
	notices, err := p.Check()
	if err != nil {
		return 0, nil, err
	}
	if _, err := s.o.Accounts.UpdateProvider(r.Context(), p); err != nil {
		return 0, nil, err
	}
	s.providersChanged()
	out, err := s.provider(r.Context(), p.ID)
	out.Notices = noticeDTOs(notices)
	return http.StatusOK, out, err
}

// usageSummary is GET /usage/summary?days=1..30 (7).
func (s *Server) usageSummary(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	days, err := queryInt(r, "days", 7, 30)
	if err != nil {
		return 0, nil, err
	}
	look := usage.Lookups{Accounts: s.o.Accounts, Tasks: func(ctx context.Context, ids []string) (map[string]usage.TaskInfo,
		error) {
		out := make(map[string]usage.TaskInfo, len(ids))
		for _, id := range ids {
			if sum, err := s.o.Tasks.Summary(ctx, id); err == nil {
				out[id] = usage.TaskInfo{Title: sum.Title, Status: string(sum.Status)}
			}
		}
		return out, nil
	}}
	sum, err := s.o.Usage.Summary(r.Context(), days, look)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, usageSummaryDTO(sum), nil
}

// usageHistory is GET /usage/history?since=&accountId=&bucket=hour|day.
func (s *Server) usageHistory(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	q := r.URL.Query()
	hq := usage.HistoryQuery{AccountID: q.Get("accountId"), Bucket: usage.Bucket(q.Get("bucket"))}
	if hq.Bucket == "" {
		hq.Bucket = usage.BucketHour
	}
	if err := oneOf("bucket", string(hq.Bucket), false, string(usage.BucketHour), string(usage.BucketDay)); err != nil {
		return 0, nil, err
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return 0, nil, invalid("since must be an RFC 3339 time").withField("since")
		}
		hq.Since = t
	}
	h, err := s.o.Usage.History(r.Context(), hq)
	if err != nil {
		return 0, nil, err
	}
	return http.StatusOK, usageHistoryDTO(h), nil
}
