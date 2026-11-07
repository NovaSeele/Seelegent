// Package member keeps the department members: named worker personas bound to a backend (or an API provider), a
// model or tier, a role and a rank (worker, senior, peer). Members live in seelegent.db (migration 0009, table
// members); the old members.json is imported by `seelegent migrate`.
//
// A member is used by name: delegate and followup take `member` (its backend, model, role and preferred accounts
// apply unless the request names its own), meetings and consults run members, contests may run variants and the
// judge as members. A turned-off member gets no new work (ErrDisabled). The rank decides what a member's word
// weighs in meetings and consults (api.Rank).
package member

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// Errors. Test with errors.Is.
var (
	// ErrNotFound: no member with that id.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: a field is missing or wrong.
	ErrInvalid = errors.New("invalid")
	// ErrConflict: a member with that id exists already.
	ErrConflict = errors.New("conflict")
	// ErrDisabled: the member is turned off and gets no new work (API code conflict, reason member_disabled).
	ErrDisabled = errors.New("member disabled")
)

// DisabledError is a turned-off member named for work.
type DisabledError struct{ ID string }

func (e *DisabledError) Error() string {
	return fmt.Sprintf("member %q is turned off; turn it on in Members or pick another member", e.ID)
}

// Is makes errors.Is(err, ErrDisabled) true.
func (e *DisabledError) Is(target error) bool { return target == ErrDisabled }

// APIDetails are the details of the API error.
func (e *DisabledError) APIDetails() map[string]any {
	return map[string]any{"reason": "member_disabled", "member": e.ID}
}

// MissingError is a member named for new work that does not exist (deleted, or never made). It is a bad argument
// of the request (ErrInvalid) with details, so a ticket or an automation run can say which member it was.
type MissingError struct{ ID string }

func (e *MissingError) Error() string {
	return fmt.Sprintf("no member %q; pick another member or a backend", e.ID)
}

// Is makes errors.Is(err, ErrInvalid) true.
func (e *MissingError) Is(target error) bool { return target == ErrInvalid }

// APIDetails are the details of the API error.
func (e *MissingError) APIDetails() map[string]any {
	return map[string]any{"reason": "member_missing", "member": e.ID}
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// idRe is the form of member and role ids.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Limits.
const (
	maxName  = 60
	maxModel = 200
	maxPref  = 20
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Providers looks up API providers (account.Manager).
type Providers interface {
	GetProvider(ctx context.Context, id string) (account.Provider, error)
}

// Store reads and writes members.
type Store struct {
	db        DB
	providers Providers
	now       func() time.Time
	// changed is called after every change (the composition root publishes members.changed).
	changed func()
}

// Options configure a Store. DB is required; Providers may be nil (provider members are then refused).
type Options struct {
	DB        DB
	Providers Providers
	Now       func() time.Time
	Changed   func()
}

// New returns a store.
func New(o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Changed == nil {
		o.Changed = func() {}
	}
	return &Store{db: o.DB, providers: o.Providers, now: o.Now, changed: o.Changed}
}

// timeFormat is how times are stored: UTC, fixed width.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Validate checks a member input and returns the member it describes (without times). id is the id to use when
// the input has none.
func (s *Store) Validate(ctx context.Context, in api.MemberInput, id string) (api.Member, error) {
	m := api.Member{ID: strings.TrimSpace(in.ID), Name: strings.TrimSpace(in.Name), Backend: in.Backend,
		Provider: strings.TrimSpace(in.Provider), Rank: in.Rank, Tier: in.Tier, Model: strings.TrimSpace(in.Model),
		Role: strings.TrimSpace(in.Role), Enabled: true}
	if m.ID == "" {
		m.ID = id
	}
	if !idRe.MatchString(m.ID) {
		return api.Member{}, invalidf("member id %q must be 1-40 lower-case letters, digits or '-', starting with a "+
			"letter or digit", m.ID)
	}
	if n := len([]rune(m.Name)); n < 1 || n > maxName {
		return api.Member{}, invalidf("name must be 1-%d characters", maxName)
	}
	if m.Provider != "" {
		if s.providers == nil {
			return api.Member{}, invalidf("providers are not available")
		}
		p, err := s.providers.GetProvider(ctx, m.Provider)
		if err != nil {
			return api.Member{}, invalidf("provider %q: %v", m.Provider, err)
		}
		if m.Backend == "" {
			m.Backend = p.Backend
		}
		if m.Backend != p.Backend {
			return api.Member{}, invalidf("backend %q does not match provider %q, which runs on %s", m.Backend,
				m.Provider, p.Backend)
		}
	}
	if m.Backend == "" {
		return api.Member{}, invalidf("backend is required (agy, codex or claude)")
	}
	if !slices.Contains(backends(), m.Backend) {
		return api.Member{}, invalidf("backend %q must be one of %s", m.Backend, strings.Join(backends(), ", "))
	}
	if !slices.Contains(api.Ranks(), m.Rank) {
		return api.Member{}, invalidf("rank %q must be one of %s", m.Rank, strings.Join(api.Ranks(), ", "))
	}
	if m.Tier != "" && !slices.Contains(config.Tiers(), m.Tier) {
		return api.Member{}, invalidf("tier %q must be one of %s", m.Tier, strings.Join(config.Tiers(), ", "))
	}
	if len(m.Model) > maxModel {
		return api.Member{}, invalidf("model must be at most %d characters", maxModel)
	}
	if m.Role != "" && !idRe.MatchString(m.Role) {
		return api.Member{}, invalidf("role %q is not a role id", m.Role)
	}
	m.PreferredAccounts = []string{}
	for _, a := range in.PreferredAccounts {
		a = strings.TrimSpace(a)
		if a == "" {
			return api.Member{}, invalidf("preferredAccounts must not contain empty ids")
		}
		if !slices.Contains(m.PreferredAccounts, a) {
			m.PreferredAccounts = append(m.PreferredAccounts, a)
		}
	}
	if len(m.PreferredAccounts) > maxPref {
		return api.Member{}, invalidf("at most %d preferred accounts", maxPref)
	}
	m.FallbackBackends = []string{}
	for _, b := range in.FallbackBackends {
		if !slices.Contains(backends(), b) {
			return api.Member{}, invalidf("fallback backend %q must be one of %s", b, strings.Join(backends(), ", "))
		}
		if b == m.Backend {
			return api.Member{}, invalidf("fallbackBackends must not name the member's own backend %q", b)
		}
		if !slices.Contains(m.FallbackBackends, b) {
			m.FallbackBackends = append(m.FallbackBackends, b)
		}
	}
	if m.Provider != "" && len(m.FallbackBackends) > 0 {
		// A provider member runs on its provider's keys; another backend would leave the provider.
		return api.Member{}, invalidf("a member bound to a provider has no fallback backends")
	}
	if in.Enabled != nil {
		m.Enabled = *in.Enabled
	}
	return m, nil
}

func backends() []string {
	return []string{config.BackendAgy, config.BackendCodex, config.BackendClaude}
}

// Input returns the editable fields of m (the base a PATCH is merged onto).
func Input(m api.Member) api.MemberInput {
	enabled := m.Enabled
	return api.MemberInput{ID: m.ID, Name: m.Name, Backend: m.Backend, Provider: m.Provider, Rank: m.Rank,
		Tier: m.Tier, Model: m.Model, Role: m.Role, PreferredAccounts: slices.Clone(m.PreferredAccounts),
		FallbackBackends: slices.Clone(m.FallbackBackends), Enabled: &enabled}
}

// Slug makes a member id from a name: lower-case ASCII letters and digits joined by '-'. Latin letters with
// accents (Vietnamese included) lose their marks; other letters are dropped.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		r = fold(r)
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case unicode.IsLetter(r) || unicode.Is(unicode.Mn, r):
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 32 {
		s = strings.Trim(s[:32], "-")
	}
	if s == "" {
		s = "member"
	}
	return s
}

// fold maps an accented Latin letter to its base letter; other runes are returned as they are.
func fold(r rune) rune {
	if r < 0x80 {
		return r
	}
	for _, g := range []struct {
		base rune
		list string
	}{
		{'a', "àáạảãâầấậẩẫăằắặẳẵäåā"}, {'e', "èéẹẻẽêềếệểễëē"}, {'i', "ìíịỉĩïî"},
		{'o', "òóọỏõôồốộổỗơờớợởỡöøō"}, {'u', "ùúụủũưừứựửữüûū"}, {'y', "ỳýỵỷỹÿ"},
		{'d', "đ"}, {'c', "ç"}, {'n', "ñ"},
	} {
		if strings.ContainsRune(g.list, r) {
			return g.base
		}
	}
	return r
}

// Create adds a member. An input without id gets one made from its name (made unique with -2, -3, ...).
func (s *Store) Create(ctx context.Context, in api.MemberInput) (api.Member, error) {
	base := Slug(in.Name)
	m, err := s.Validate(ctx, in, base)
	if err != nil {
		return api.Member{}, err
	}
	explicit := strings.TrimSpace(in.ID) != ""
	now := s.now().UTC()
	m.CreatedAt, m.UpdatedAt = api.FormatTime(now), api.FormatTime(now)
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		id := m.ID
		for n := 2; ; n++ {
			taken, err := exists(ctx, tx, id)
			if err != nil {
				return err
			}
			if !taken {
				break
			}
			if explicit {
				return fmt.Errorf("%w: a member with id %q exists already", ErrConflict, id)
			}
			id = base + "-" + strconv.Itoa(n)
		}
		m.ID = id
		return put(ctx, tx, m, now, now)
	})
	if err != nil {
		return api.Member{}, err
	}
	s.changed()
	return s.withState(ctx, m), nil
}

// Update replaces the editable fields of a member (the id stays).
func (s *Store) Update(ctx context.Context, id string, in api.MemberInput) (api.Member, error) {
	in.ID = id
	m, err := s.Validate(ctx, in, id)
	if err != nil {
		return api.Member{}, err
	}
	now := s.now().UTC()
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		old, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		created, err := time.Parse(time.RFC3339Nano, old.CreatedAt)
		if err != nil {
			created = now
		}
		m.CreatedAt, m.UpdatedAt = old.CreatedAt, api.FormatTime(now)
		return put(ctx, tx, m, created, now)
	})
	if err != nil {
		return api.Member{}, err
	}
	s.changed()
	return s.withState(ctx, m), nil
}

// Delete removes a member. Tasks, meetings and contests keep its id as text.
func (s *Store) Delete(ctx context.Context, id string) error {
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM members WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: no member %q", ErrNotFound, id)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.changed()
	return nil
}

// Get returns one member, with Invalid set when it cannot be used now.
func (s *Store) Get(ctx context.Context, id string) (api.Member, error) {
	var m api.Member
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		m, err = get(ctx, tx, id)
		return err
	})
	if err != nil {
		return api.Member{}, err
	}
	return s.withState(ctx, m), nil
}

// List returns every member, by id.
func (s *Store) List(ctx context.Context) ([]api.Member, error) {
	var out []api.Member
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT data FROM members ORDER BY id`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			m, err := decode(data)
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i] = s.withState(ctx, out[i])
	}
	if out == nil {
		out = []api.Member{}
	}
	return out, nil
}

// Usable returns the member with id for new work: it must exist, be valid and be turned on.
func (s *Store) Usable(ctx context.Context, id string) (api.Member, error) {
	m, err := s.Get(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return api.Member{}, &MissingError{ID: id}
	}
	if err != nil {
		return api.Member{}, err
	}
	if m.Invalid != "" {
		return api.Member{}, invalidf("member %q cannot be used: %s", id, m.Invalid)
	}
	if !m.Enabled {
		return api.Member{}, &DisabledError{ID: id}
	}
	return m, nil
}

// Import adds a member of the old version as it is (`seelegent migrate`); a member with the same id is kept and
// added is false.
func (s *Store) Import(ctx context.Context, in api.MemberInput) (added bool, err error) {
	m, err := s.Validate(ctx, in, "")
	if err != nil {
		return false, err
	}
	now := s.now().UTC()
	m.CreatedAt, m.UpdatedAt = api.FormatTime(now), api.FormatTime(now)
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		taken, err := exists(ctx, tx, m.ID)
		if err != nil || taken {
			return err
		}
		added = true
		return put(ctx, tx, m, now, now)
	})
	if err == nil && added {
		s.changed()
	}
	return added, err
}

// withState sets Invalid: a provider that is gone, or a role id that no longer parses.
func (s *Store) withState(ctx context.Context, m api.Member) api.Member {
	m.Invalid = ""
	if m.Provider != "" {
		if s.providers == nil {
			m.Invalid = "providers are not available"
		} else if p, err := s.providers.GetProvider(ctx, m.Provider); err != nil {
			m.Invalid = fmt.Sprintf("provider %s is gone", m.Provider)
		} else if !p.Enabled {
			m.Invalid = fmt.Sprintf("provider %s is turned off", m.Provider)
		}
	}
	if m.PreferredAccounts == nil {
		m.PreferredAccounts = []string{}
	}
	if m.FallbackBackends == nil {
		m.FallbackBackends = []string{}
	}
	return m
}

func exists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM members WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func get(ctx context.Context, tx *sql.Tx, id string) (api.Member, error) {
	var data string
	err := tx.QueryRowContext(ctx, `SELECT data FROM members WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Member{}, fmt.Errorf("%w: no member %q", ErrNotFound, id)
	}
	if err != nil {
		return api.Member{}, err
	}
	return decode(data)
}

func decode(data string) (api.Member, error) {
	var m api.Member
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return api.Member{}, fmt.Errorf("member record: %w", err)
	}
	return m, nil
}

func put(ctx context.Context, tx *sql.Tx, m api.Member, created, updated time.Time) error {
	m.Invalid = ""
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO members (id, data, created_at, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		m.ID, string(data), created.UTC().Format(timeFormat), updated.UTC().Format(timeFormat))
	return err
}
