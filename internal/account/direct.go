package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// DirectNew adds an API-key account in one step: with its provider (an existing one, or one created or reused from
// NewProvider) and, optionally, its key (POST /accounts with newProvider or apiKey).
type DirectNew struct {
	New
	// NewProvider, when set, is the provider to create, or to reuse unchanged when an enabled provider with the same
	// preset, backend, base URL, official flag, wire API, models, allow-list and tiers exists. New.Provider must
	// then be empty, and New.Backend may be (the provider's backend is used). Its ID may be empty: the preset id
	// (else the backend) is used, with "-2", "-3", ... when taken.
	NewProvider *Provider
	// Key is the API key to store as pasted ("" for none); see NormalizeKey.
	Key string
}

// DirectResult is what AddDirect made.
type DirectResult struct {
	Account Account
	// Provider is the account's provider when DirectNew.NewProvider was given (zero otherwise); ProviderCreated
	// tells whether it was created (else it was reused).
	Provider        Provider
	ProviderCreated bool
	// Notices say what was changed on the way (key or base URL cleaned, preset switched, provider reused) and give
	// advice about the key.
	Notices []Notice
	// Warnings list links into the profile that could not be made (as Add).
	Warnings []string
}

// Steps of AddDirect that fault injection can fail (tests only).
const (
	stepProvider = "provider"
	stepAccount  = "account"
	stepProfile  = "profile"
	stepKey      = "key"
	stepCommit   = "commit"
)

// AddDirect creates an API-key account together with its provider and key, all or nothing: the provider, the
// account record and the key hint are written in one transaction, and the profile directory and key file made
// inside it are removed again when any later step (or the commit) fails. Its input errors match ErrInvalid and
// name the request field (FieldError: "apiKey", "newProvider.baseUrl", "provider", "label", ...).
func (m *Manager) AddDirect(ctx context.Context, in DirectNew, links []string) (DirectResult, error) {
	var res DirectResult
	n := in.New
	if n.Kind == "" {
		n.Kind = KindAPIKey
	}
	if n.Kind != KindAPIKey {
		return res, withField("kind", invalidf("a key or a new provider is only for an api-key account"))
	}
	key := ""
	if in.Key != "" {
		k, notices, err := NormalizeKey(in.Key)
		if err != nil {
			return res, err
		}
		key, res.Notices = k, notices
	}
	var np *Provider
	autoID := false
	if in.NewProvider != nil {
		if n.Provider != "" {
			return res, withField("provider", invalidf("give provider or newProvider, not both"))
		}
		p := *in.NewProvider
		if p.Backend == "" {
			p.Backend = n.Backend
		}
		if n.Backend != "" && p.Backend != n.Backend {
			return res, withField("backend", invalidf("the new provider serves %s, not %s", p.Backend, n.Backend))
		}
		if p.ID == "" {
			autoID = true
			p.ID = providerIDBase(p)
		}
		notices, err := p.Check()
		if err != nil {
			return res, PrefixField("newProvider", err)
		}
		n.Backend = p.Backend
		res.Notices = append(res.Notices, notices...)
		np = &p
	} else if n.Provider == "" {
		return res, withField("provider", invalidf("an API-key account needs a provider or a newProvider"))
	}

	accountID := ""
	filesMade := false
	err := m.repo.db.Write(ctx, func(tx *sql.Tx) error {
		var p Provider
		if np != nil {
			var (
				created bool
				err     error
			)
			if p, created, err = m.repo.ensureProviderTx(ctx, tx, *np, autoID); err != nil {
				return PrefixField("newProvider", err)
			}
			res.Provider, res.ProviderCreated = p, created
			if !created {
				res.Notices = append(res.Notices, Notice{Code: NoticeProviderReused, Field: "newProvider",
					Message: fmt.Sprintf("added to the existing provider %s; its settings stay as they are", p.ID)})
			}
			n.Provider = p.ID
		} else {
			var err error
			if p, err = getProvider(ctx, tx, n.Provider); err != nil {
				if errors.Is(err, ErrNotFound) {
					return withField("provider", invalidf("provider %s does not exist", n.Provider))
				}
				return err
			}
			if n.Backend == "" {
				n.Backend = p.Backend
			}
		}
		if err := m.fault(stepProvider); err != nil {
			return err
		}
		if n.Label == "" {
			label, err := defaultLabel(ctx, tx, p)
			if err != nil {
				return err
			}
			n.Label = label
		}
		if err := n.validate(); err != nil {
			return err
		}
		a, err := m.repo.createTx(ctx, tx, n, m.profiles.Exists)
		if err != nil {
			return err
		}
		accountID = a.ID
		if err := m.fault(stepAccount); err != nil {
			return err
		}
		filesMade = true
		if _, res.Warnings, err = m.profiles.Ensure(a, links); err != nil {
			return err
		}
		if err := m.fault(stepProfile); err != nil {
			return err
		}
		if key != "" {
			hint, err := m.keys.Save(a.ID, key)
			if err != nil {
				return err
			}
			if err := m.repo.setKeyHintTx(ctx, tx, a.ID, hint); err != nil {
				return err
			}
			res.Notices = append(res.Notices, KeyNotices(p, key)...)
		}
		if err := m.fault(stepKey); err != nil {
			return err
		}
		if res.Account, err = getAccount(ctx, tx, a.ID); err != nil {
			return err
		}
		return m.fault(stepCommit)
	})
	if err != nil {
		if filesMade {
			if cerr := errors.Join(m.keys.Remove(accountID), m.profiles.Remove(accountID)); cerr != nil {
				err = errors.Join(err, fmt.Errorf("clean up account %s: %w", accountID, cerr))
			}
		}
		return DirectResult{}, wrap("add account", err)
	}
	return res, nil
}

// fault returns the injected error of a step (tests only; nil otherwise).
func (m *Manager) fault(step string) error {
	if m.faults == nil {
		return nil
	}
	return m.faults(step)
}

// providerIDBase is the id a new provider gets when none is given: its preset, else its backend.
func providerIDBase(p Provider) string {
	if p.Preset != "" {
		return p.Preset
	}
	return p.Backend
}

// ensureProviderTx returns the enabled provider that matches p exactly (same preset, backend, base URL, official
// flag, wire API, models, allow-list and tiers; with the same id unless autoID), unchanged, or inserts p
// (validated) under its id, or under the first free "<id>-<n>" when autoID.
func (r *Repo) ensureProviderTx(ctx context.Context, tx *sql.Tx, p Provider, autoID bool) (Provider, bool, error) {
	list, err := listProvidersTx(ctx, tx)
	if err != nil {
		return Provider{}, false, err
	}
	for i := range list {
		e := &list[i]
		// A reused provider is never changed (its other accounts rely on it as it is), so it must already be
		// exactly what was asked for: endpoint, wire, models, allow-list and tiers (a model-per-task provider stays
		// one). Anything else creates a new provider ("anthropic-2").
		if !e.Enabled || e.Preset != p.Preset || e.Backend != p.Backend || e.BaseURL != p.BaseURL ||
			e.Official != p.Official || e.WireAPI != p.WireAPI || e.AllowedModels != p.AllowedModels ||
			!slices.Equal(e.Models, p.Models) || e.Tiers != p.Tiers || (!autoID && e.ID != p.ID) {
			continue
		}
		return *e, false, nil
	}
	if autoID {
		taken := map[string]bool{}
		for i := range list {
			taken[list[i].ID] = true
		}
		base := p.ID
		for k := 2; taken[p.ID]; k++ {
			suffix := "-" + strconv.Itoa(k)
			p.ID = base[:min(len(base), 32-len(suffix))] + suffix
		}
	}
	out, err := r.insertProviderTx(ctx, tx, p)
	return out, err == nil, err
}

// listProvidersTx returns every provider ordered by id.
func listProvidersTx(ctx context.Context, tx *sql.Tx) ([]Provider, error) {
	rows, err := tx.QueryContext(ctx, "SELECT "+providerColumns+" FROM providers ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// defaultLabel names a new account of the provider "<provider name> <n>", n one more than its accounts.
func defaultLabel(ctx context.Context, tx *sql.Tx, p Provider) (string, error) {
	n, err := providerUsers(ctx, tx, p.ID)
	if err != nil {
		return "", err
	}
	suffix := " " + strconv.Itoa(n+1)
	name := p.Name
	for utf8.RuneCountInString(name)+len(suffix) > maxLabel {
		r := []rune(name)
		name = string(r[:len(r)-1])
	}
	return name + suffix, nil
}
