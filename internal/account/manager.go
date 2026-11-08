package account

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Manager is the account service: the records in SQLite together with the profile directories and keys on disk.
// It does not know whether an account is busy; the caller (the pool) must make sure no turn runs on an account it
// removes or whose key it changes.
type Manager struct {
	repo     *Repo
	profiles Profiles
	keys     Keys
	// faults, set by tests only, fails a step of AddDirect.
	faults func(step string) error
}

// NewManager returns the account service on db, with profiles under profiles. now is the clock (time.Now when nil).
func NewManager(db DB, profiles Profiles, now func() time.Time) *Manager {
	return &Manager{repo: NewRepo(db, now), profiles: profiles, keys: NewKeys(profiles)}
}

// Repo returns the underlying repository (records only).
func (m *Manager) Repo() *Repo { return m.repo }

// Profiles returns the profile directories.
func (m *Manager) Profiles() Profiles { return m.profiles }

// Add creates an account and its profile, linking the given entries of the real home into it. Warnings list links
// that could not be made. If the profile cannot be created the record is removed again.
func (m *Manager) Add(ctx context.Context, n New, links []string) (a Account, warnings []string, err error) {
	a, err = m.repo.Create(ctx, n, m.profiles.Exists)
	if err != nil {
		return Account{}, nil, err
	}
	_, warnings, err = m.profiles.Ensure(a, links)
	if err != nil {
		if derr := m.repo.Delete(ctx, a.ID); derr != nil {
			err = errors.Join(err, derr)
		}
		return Account{}, nil, err
	}
	return a, warnings, nil
}

// Get returns one account.
func (m *Manager) Get(ctx context.Context, id string) (Account, error) { return m.repo.Get(ctx, id) }

// List returns every account, highest priority first.
func (m *Manager) List(ctx context.Context) ([]Account, error) { return m.repo.List(ctx) }

// Update changes label, enabled or priority.
func (m *Manager) Update(ctx context.Context, id string, p Patch) (Account, error) {
	return m.repo.Update(ctx, id, p)
}

// Remove deletes the account: its key, its profile directory (never following links into the real home) and its
// record. Removing the system account deletes only the record: the real home is the user's. If the files cannot be
// removed (a CLI still holds them open) the record stays so the removal can be retried.
func (m *Manager) Remove(ctx context.Context, id string) error {
	a, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if a.Kind != KindSystem {
		if err := m.keys.Remove(a.ID); err != nil {
			return err
		}
		if err := m.profiles.Remove(a.ID); err != nil {
			return err
		}
	}
	return m.repo.Delete(ctx, a.ID)
}

// SetKey stores the API key of an API-key account and returns the updated account (with the new key hint).
func (m *Manager) SetKey(ctx context.Context, id, key string) (Account, error) {
	a, err := m.repo.Get(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if a.Kind != KindAPIKey {
		return Account{}, invalidf("account %s is not an API-key account", id)
	}
	hint, err := m.keys.Save(a.ID, key)
	if err != nil {
		return Account{}, err
	}
	if err := m.repo.SetKeyHint(ctx, a.ID, hint); err != nil {
		return Account{}, err
	}
	return m.repo.Get(ctx, a.ID)
}

// MarkRunOK records that a turn that started at started ended well on the account; see Repo.MarkRunOK.
func (m *Manager) MarkRunOK(ctx context.Context, id string, started time.Time) (bool, error) {
	return m.repo.MarkRunOK(ctx, id, started)
}

// RecordTest stores the result of a key test of the account; see Repo.RecordTest.
func (m *Manager) RecordTest(ctx context.Context, id, result string, started time.Time) (bool, error) {
	return m.repo.RecordTest(ctx, id, result, started)
}

// StoredKey returns the account and its stored API key, for a key test. The key must never be logged or shown.
func (m *Manager) StoredKey(ctx context.Context, id string) (Account, string, error) {
	a, err := m.repo.Get(ctx, id)
	if err != nil {
		return Account{}, "", err
	}
	if a.Kind != KindAPIKey {
		return Account{}, "", withField("accountId", invalidf("account %s is not an API-key account", id))
	}
	key, err := m.keys.Load(a.ID)
	if err != nil {
		return Account{}, "", err
	}
	return a, key, nil
}

// ClearKey removes the stored API key.
func (m *Manager) ClearKey(ctx context.Context, id string) (Account, error) {
	a, err := m.repo.Get(ctx, id)
	if err != nil {
		return Account{}, err
	}
	if err := m.keys.Remove(a.ID); err != nil {
		return Account{}, err
	}
	if err := m.repo.SetKeyHint(ctx, a.ID, ""); err != nil {
		return Account{}, err
	}
	return m.repo.Get(ctx, a.ID)
}

// Profile returns where the account's CLI keeps its state, without touching the disk.
func (m *Manager) Profile(a Account) (Profile, error) { return m.profiles.For(a) }

// EnsureProfile creates the profile (if missing) and adds links that are missing; see Profiles.Ensure.
func (m *Manager) EnsureProfile(a Account, links []string) (Profile, []string, error) {
	return m.profiles.Ensure(a, links)
}

// Credentials returns the provider and key of an API-key account, checking that the turn can use them. A disabled
// or missing provider gives ErrProviderUnavailable and a missing key ErrNoKey: both mean "sign-in required" and are
// known before any slot or process is taken.
func (m *Manager) Credentials(ctx context.Context, a Account) (Provider, string, error) {
	if a.Kind != KindAPIKey {
		return Provider{}, "", invalidf("account %s is not an API-key account", a.ID)
	}
	p, err := m.repo.GetProvider(ctx, a.Provider)
	if errors.Is(err, ErrNotFound) {
		return Provider{}, "", fmt.Errorf("account %s: provider %s is gone: %w", a.ID, a.Provider, ErrProviderUnavailable)
	}
	if err != nil {
		return Provider{}, "", err
	}
	// A provider saved before the base URL rules ran with its URL cleaned (and as the official preset when it
	// points at api.anthropic.com).
	if p.Official || validateBaseURL(p.BaseURL) == nil {
		p.normalizeEndpoint()
	}
	if !p.Enabled {
		return Provider{}, "", fmt.Errorf("account %s: provider %s is disabled: %w", a.ID, p.ID, ErrProviderUnavailable)
	}
	if p.Backend != a.Backend {
		return Provider{}, "", fmt.Errorf("account %s: provider %s serves %s: %w", a.ID, p.ID, p.Backend,
			ErrProviderUnavailable)
	}
	key, err := m.keys.Load(a.ID)
	if err != nil {
		return Provider{}, "", err
	}
	return p, key, nil
}

// CreateProvider adds a provider.
func (m *Manager) CreateProvider(ctx context.Context, p Provider) (Provider, error) {
	return m.repo.CreateProvider(ctx, p)
}

// UpdateProvider replaces a provider.
func (m *Manager) UpdateProvider(ctx context.Context, p Provider) (Provider, error) {
	return m.repo.UpdateProvider(ctx, p)
}

// GetProvider returns one provider.
func (m *Manager) GetProvider(ctx context.Context, id string) (Provider, error) {
	return m.repo.GetProvider(ctx, id)
}

// ListProviders returns every provider.
func (m *Manager) ListProviders(ctx context.Context) ([]Provider, error) {
	return m.repo.ListProviders(ctx)
}

// DeleteProvider removes a provider no account uses.
func (m *Manager) DeleteProvider(ctx context.Context, id string) error {
	return m.repo.DeleteProvider(ctx, id)
}
