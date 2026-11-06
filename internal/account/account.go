// Package account owns the worker accounts: their records (SQLite), their isolated CLI profiles under
// profiles/<id>/, the API keys of API-key accounts, the API-key providers those accounts talk to, and the
// environment a worker turn runs with.
//
// What an account can do right now (quota, cooldowns, sign-in state, busy slots) belongs to the pool; this package
// only knows what the account is.
package account

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// Kind says how an account signs in and where its CLI keeps its state.
type Kind string

// Account kinds.
const (
	// KindIsolated accounts sign in through the CLI's own login, into an isolated profile under profiles/<id>/home.
	KindIsolated Kind = "isolated"
	// KindSystem uses the CLI's sign-in in the user's real home directory (agy only).
	KindSystem Kind = "system"
	// KindAPIKey accounts run with an API key against a provider, in an isolated profile (claude and codex).
	KindAPIKey Kind = "api-key"
)

// SystemID is the id of the system account; there is at most one.
const SystemID = "system"

// Limits of account fields.
const (
	maxLabel    = 80
	minPriority = -1000
	maxPriority = 1000
)

// Errors returned by the account functions; match them with errors.Is.
var (
	// ErrNotFound: no account or provider with that id.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: the input breaks a rule; the message says which.
	ErrInvalid = errors.New("invalid")
	// ErrConflict: the change clashes with existing data (an id already taken, a provider still in use).
	ErrConflict = errors.New("conflict")
	// ErrNoKey: an API-key account has no key stored. A turn cannot start; treat it as "sign-in required".
	ErrNoKey = errors.New("no API key stored")
	// ErrProviderUnavailable: the provider of an API-key account is disabled or misconfigured. A turn cannot start;
	// treat it as "sign-in required".
	ErrProviderUnavailable = errors.New("provider unavailable")
)

// invalidf returns an error matching ErrInvalid.
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Account is one worker account.
type Account struct {
	// ID never changes and is never given to another account, even after this one is removed.
	ID string `json:"id"`
	// Backend is the worker CLI: agy, claude or codex.
	Backend string `json:"backend"`
	// Kind says how the account signs in.
	Kind Kind `json:"kind"`
	// Label is the user's name for the account.
	Label string `json:"label"`
	// Enabled accounts may receive work.
	Enabled bool `json:"enabled"`
	// Priority orders accounts: higher first.
	Priority int `json:"priority"`
	// Provider is the provider id of an API-key account (empty otherwise).
	Provider string `json:"provider,omitempty"`
	// KeyHint shows the last four characters of the stored API key ("…abcd"); empty when no key is stored. The key
	// itself is never part of an Account.
	KeyHint string `json:"keyHint,omitempty"`
	// KeySetAt is when the API key was last stored or removed (zero when never); other edits do not change it.
	KeySetAt time.Time `json:"keySetAt,omitzero"`
	// CreatedAt and UpdatedAt are UTC times.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// New describes an account to create.
type New struct {
	Backend  string
	Kind     Kind
	Label    string
	Enabled  bool
	Priority int
	// Provider is required for KindAPIKey and must be empty otherwise.
	Provider string
}

// Patch changes an account; nil fields stay as they are.
type Patch struct {
	Label    *string
	Enabled  *bool
	Priority *int
}

// backends lists the valid backend ids.
func backends() []string {
	return []string{config.BackendAgy, config.BackendClaude, config.BackendCodex}
}

// validate checks a new account (without the provider lookup).
func (n New) validate() error {
	switch n.Backend {
	case config.BackendAgy, config.BackendClaude, config.BackendCodex:
	default:
		return invalidf("backend %q (want one of %s)", n.Backend, strings.Join(backends(), ", "))
	}
	switch n.Kind {
	case KindIsolated:
	case KindSystem:
		if n.Backend != config.BackendAgy {
			return invalidf("a system account is only possible for agy, not %s", n.Backend)
		}
	case KindAPIKey:
		if n.Backend == config.BackendAgy {
			return invalidf("agy has no API-key accounts")
		}
		if n.Provider == "" {
			return invalidf("an API-key account needs a provider")
		}
	default:
		return invalidf("kind %q (want isolated, system or api-key)", n.Kind)
	}
	if n.Kind != KindAPIKey && n.Provider != "" {
		return invalidf("only API-key accounts have a provider")
	}
	if err := validateLabel(n.Label); err != nil {
		return err
	}
	return validatePriority(n.Priority)
}

func validateLabel(l string) error {
	if !utf8.ValidString(l) || utf8.RuneCountInString(l) > maxLabel {
		return invalidf("label must be valid text of at most %d characters", maxLabel)
	}
	if strings.IndexFunc(l, unicode.IsControl) >= 0 {
		return invalidf("label must not contain control characters")
	}
	return nil
}

func validatePriority(p int) error {
	if p < minPriority || p > maxPriority {
		return invalidf("priority %d out of range %d..%d", p, minPriority, maxPriority)
	}
	return nil
}

// validID reports whether id can name an account (and its profile directory).
func validID(id string) bool {
	if id == "" || len(id) > 64 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}
