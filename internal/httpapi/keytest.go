package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// testProviderID names a newProvider without an id while it is tested (it is never stored).
const testProviderID = "key-test"

// testKey is POST /providers/test: one cheap call to the provider with the key (backend.ProbeKey), nothing saved
// except, with accountId, the result on that account. It answers 200 whenever the call could be made, the key
// refused included. The key is never logged or returned; it is not on the worker allow-list.
func (s *Server) testKey(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var req api.KeyTestRequest
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	ctx := r.Context()
	switch {
	case (req.APIKey == "") == (req.AccountID == ""):
		return 0, nil, invalid("give apiKey or accountId (one of them)").withField("apiKey")
	case req.Provider != "" && req.NewProvider != nil:
		return 0, nil, invalid("give provider or newProvider, not both").withField("provider")
	}
	var (
		key     string
		notices []account.Notice
		acc     *account.Account
	)
	if req.AccountID != "" {
		a, k, err := s.o.Accounts.StoredKey(ctx, req.AccountID)
		switch {
		case errors.Is(err, account.ErrNoKey):
			return 0, nil, invalid("account %s has no key stored", req.AccountID).withField("accountId")
		case errors.Is(err, account.ErrNotFound):
			return 0, nil, newError(api.CodeNotFound, "account %s not found", req.AccountID).withField("accountId")
		case err != nil:
			return 0, nil, err
		}
		// A stored key goes only to the account's own provider: never to an address the caller chose.
		switch {
		case req.NewProvider != nil:
			return 0, nil, invalid("a stored key is tested only against its account's own provider: give apiKey " +
				"to test another provider").withField("newProvider")
		case req.Provider != "" && req.Provider != a.Provider:
			return 0, nil, invalid("account %s uses provider %s: a stored key is tested only against it",
				a.ID, a.Provider).withField("provider")
		}
		key, acc = k, &a
		req.Provider = a.Provider
	} else {
		k, ns, err := account.NormalizeKey(req.APIKey)
		if err != nil {
			return 0, nil, err
		}
		key, notices = k, ns
	}
	p, ns, err := s.providerToTest(ctx, &req)
	if err != nil {
		return 0, nil, err
	}
	notices = append(notices, ns...)
	notices = append(notices, account.KeyNotices(p, key)...)
	started := s.clock()
	res := backend.ProbeKey(ctx, p, key, s.o.Probe)
	out := api.KeyTestResult{OK: res.OK, Status: res.Status, Reason: res.Reason, Message: res.Message,
		Models: api.NonNil(res.Models), LatencyMs: res.Latency.Milliseconds(), Notices: noticeDTOs(notices)}
	s.log.Info("provider key test", "provider", p.ID, "reason", res.Reason, "status", res.Status)
	// With accountId the account's own provider was tested (checked above), so the result is the account's.
	if acc != nil && ctx.Err() == nil {
		saved, err := s.o.Accounts.RecordTest(ctx, acc.ID, res.Reason, started)
		if err != nil {
			return 0, nil, err
		}
		out.Saved = saved
		if saved {
			s.o.Pool.Notify()
			s.kickAccounts()
		}
	}
	return http.StatusOK, out, nil
}

// providerToTest returns the provider of a key test, validated and with its base URL cleaned (the notices say
// what was cleaned for a newProvider).
func (s *Server) providerToTest(ctx context.Context, req *api.KeyTestRequest) (account.Provider, []account.Notice,
	error) {
	if req.NewProvider != nil {
		p, err := providerFromInput(req.NewProvider, false, "newProvider")
		if err != nil {
			return account.Provider{}, nil, err
		}
		if p.ID == "" {
			p.ID = testProviderID
		}
		notices, err := p.Check()
		if err != nil {
			return account.Provider{}, nil, account.PrefixField("newProvider", err)
		}
		return p, notices, nil
	}
	if req.Provider == "" {
		return account.Provider{}, nil, invalid("provider or newProvider is required").withField("provider")
	}
	p, err := s.o.Accounts.GetProvider(ctx, req.Provider)
	if errors.Is(err, account.ErrNotFound) {
		return account.Provider{}, nil, invalid("provider %s does not exist", req.Provider).withField("provider")
	}
	if err != nil {
		return account.Provider{}, nil, err
	}
	// A stored provider is cleaned the way a turn would use it (Credentials); its notices were given at save time.
	if _, err := p.Check(); err != nil {
		return account.Provider{}, nil, account.PrefixField("provider", err)
	}
	return p, nil, nil
}
