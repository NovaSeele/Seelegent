// Package automation runs saved delegations on a schedule (milestone 15, docs/automations.md): presets or 5-field
// cron in any IANA zone, a grace window for runs missed while the daemon was off, an overlap guard, a quota rule
// (and "use it before it resets"), and an optional precheck command. One timer, at most 60 s away, drives it on an
// injectable clock.
//
// Trust (old bug D8): every automation is created disabled, and an enable request from the lead (MCP or CLI) only
// records the request. The user turns an automation on in the dashboard, with a one-time token bound to exactly
// what the automation runs; a later change to what it runs turns it off until it is confirmed again. Records and
// the run history are SQLite rows (D6); a row that cannot be read is an error.
package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Errors; the HTTP API maps them to not_found, invalid_argument, bad_state and forbidden.
var (
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid")
	ErrBadState  = errors.New("bad state")
	ErrForbidden = errors.New("forbidden")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Timing and limits.
const (
	// MaxTick is the longest the timer waits: the before-reset rule and the master switch are checked this often.
	MaxTick = 60 * time.Second
	// PrecheckTimeout is the time limit of a precheck; its process tree is killed after it.
	PrecheckTimeout = 60 * time.Second
	// ConfirmTTL is how long a confirmation token is valid.
	ConfirmTTL = 5 * time.Minute
	// BeforeResetWindow and BeforeResetMinLeft: a weekly bucket that resets within 24 h with at least 40 % left
	// starts one extra run (quota.useBeforeReset).
	BeforeResetWindow  = 24 * time.Hour
	BeforeResetMinLeft = 0.4
	// beforeResetRetry is how long a before-reset run that was skipped waits before it is tried again.
	beforeResetRetry = 30 * time.Minute

	defaultGrace    = 60
	defaultMinLeft  = 20
	maxName         = 120
	maxPrompt       = 100000
	maxPrecheck     = 2000
	maxList         = 50
	maxDetail       = 500
	maxRunsKept     = 500
	maxPreviewCount = 20
)

// Tasks is the task engine (task.Service).
type Tasks interface {
	DelegateWith(ctx context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error)
	Summary(ctx context.Context, id string) (api.TaskSummary, error)
	Bus() *task.Bus
}

// Members finds a department member (for the backend an automation's member runs on).
type Members interface {
	Usable(ctx context.Context, id string) (api.Member, error)
}

// AccountQuota is one usable account's quota windows for a run.
type AccountQuota struct {
	AccountID string
	Windows   []quota.Window
}

// QuotaSource returns the usable accounts of a backend whose quota is known, with the windows that govern a run
// of model ("" for the default model).
type QuotaSource interface {
	Quota(ctx context.Context, backend, model string) ([]AccountQuota, error)
}

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Precheck runs a precheck command in dir.
type Precheck func(ctx context.Context, cmdline, dir string, timeout time.Duration) gate.ExecResult

// Options configure a Service. DB and Tasks are required (Import needs only DB).
type Options struct {
	DB      DB
	Tasks   Tasks
	Members Members
	Quota   QuotaSource
	// Precheck defaults to gate.Exec (the user's shell, process tree killed on timeout).
	Precheck Precheck
	Clock    clock.Clock
	// MasterOn is the setting automationsEnabled; off, nothing runs on its schedule (run now still works).
	MasterOn func() bool
	// ConfirmURL is the dashboard page where the user confirms automation id.
	ConfirmURL func(id string) string
	// Changed and Deleted tell the stream (automation.updated, automation.deleted).
	Changed func(api.Automation)
	Deleted func(id string)
	Logger  *slog.Logger
}

// Service keeps the automations and runs them.
type Service struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	wake   chan struct{}
	redact *logx.Redactor

	// edit is held for writing by every change to an automation record (update, Delete) and for reading by a run
	// from its last check to its delegate, so no edit or switch-off lands in between.
	edit    sync.RWMutex
	mu      sync.Mutex
	firing  map[string]bool
	tokens  map[string]confirmEntry
	started bool
}

type confirmEntry struct {
	id      string
	hash    string
	expires time.Time
}

// New returns a Service; Start starts the scheduler.
func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Precheck == nil {
		o.Precheck = func(ctx context.Context, cmdline, dir string, timeout time.Duration) gate.ExecResult {
			return gate.Exec(ctx, cmdline, gate.ExecOptions{Dir: dir, Timeout: timeout, TailBytes: 2000})
		}
	}
	if o.MasterOn == nil {
		o.MasterOn = func() bool { return true }
	}
	if o.ConfirmURL == nil {
		o.ConfirmURL = func(id string) string { return "/automations?confirm=" + id }
	}
	if o.Changed == nil {
		o.Changed = func(api.Automation) {}
	}
	if o.Deleted == nil {
		o.Deleted = func(string) {}
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), redact: logx.NewRedactor(),
		firing: map[string]bool{}, tokens: map[string]confirmEntry{}}
}

// rec is a stored automation.
type rec struct {
	api.Automation
	// ConfirmedHash is the hash of what the automation runs, as the user confirmed it.
	ConfirmedHash string `json:"confirmedHash,omitempty"`
	// BeforeResetFired are the buckets (account@reset) a before-reset run started for; BeforeResetTried is the
	// last one tried that did not start, and when.
	BeforeResetFired []string    `json:"beforeResetFired,omitempty"`
	BeforeResetTried *resetTried `json:"beforeResetTried,omitempty"`
}

type resetTried struct {
	Key string    `json:"key"`
	At  time.Time `json:"at"`
}

// contentHash is the hash of everything that decides what a run does and when: a change needs a new confirmation.
// That includes the rules that add runs or move them: useBeforeReset (a run outside the schedule) and graceMinutes
// (how late a missed run may still start). quota.minRemaining is left out on purpose: it can only skip a run the
// confirmed schedule already allows, never add or move one, so tuning it keeps the automation on.
func (r *rec) contentHash() string {
	b, _ := json.Marshal([]any{r.Prompt, r.Cwd, r.Member, r.Backend, r.Model, r.Gate, r.GateCommands, r.Lane,
		r.NoLane, r.Precheck, r.Schedule, r.Timezone, r.Quota.UseBeforeReset, r.GraceMinutes})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// confirmed reports whether what a run does is what the user last confirmed.
func (r *rec) confirmed() bool {
	return r.ConfirmedHash != "" && r.ConfirmedHash == r.contentHash()
}

// revision is the short form of contentHash the API shows (Automation.Revision).
func (r *rec) revision() string {
	return r.contentHash()[:16]
}

func newID() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return "a" + hex.EncodeToString(b)
}

// ---- store -------------------------------------------------------------------------------------------------------

func scanRec(id, raw string) (*rec, error) {
	var r rec
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("automation %s is unreadable: %w", id, err)
	}
	r.ID = id
	return &r, nil
}

func loadRec(ctx context.Context, tx *sql.Tx, id string) (*rec, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT data FROM automations WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: no automation %s", ErrNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	return scanRec(id, raw)
}

func listRecs(ctx context.Context, tx *sql.Tx) ([]*rec, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM automations ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*rec{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		r, err := scanRec(id, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func saveRec(ctx context.Context, tx *sql.Tx, r *rec) error {
	r.Running = false
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO automations (id, enabled, created_at, updated_at, data)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET enabled = excluded.enabled,
		updated_at = excluded.updated_at, data = excluded.data`, r.ID, enabled, r.CreatedAt, r.UpdatedAt, string(raw))
	return err
}

func (s *Service) now() time.Time { return s.o.Clock.Now() }

// update reads, changes and saves one automation, then tells the stream.
func (s *Service) update(ctx context.Context, id string, fn func(r *rec) error) (api.Automation, error) {
	var out *rec
	s.edit.Lock()
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadRec(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
		out = r
		return saveRec(ctx, tx, r)
	})
	s.edit.Unlock()
	if err != nil {
		return api.Automation{}, err
	}
	v := s.view(ctx, out)
	s.o.Changed(v)
	s.poke()
	return v, nil
}

// poke re-arms the timer.
func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// running reports whether a run of r is starting or its task is still active.
func (s *Service) running(ctx context.Context, r *rec) (busy bool, why *api.AutomationRunInfo) {
	s.mu.Lock()
	f := s.firing[r.ID]
	s.mu.Unlock()
	if f {
		return true, &api.AutomationRunInfo{Reason: api.RunStarting}
	}
	if r.ActiveTaskID == "" {
		return false, nil
	}
	sum, err := s.o.Tasks.Summary(ctx, r.ActiveTaskID)
	if err != nil || !task.Pending(sum.Status) {
		return false, nil
	}
	return true, &api.AutomationRunInfo{Reason: api.RunTaskRunning, TaskID: sum.ID, TaskStatus: string(sum.Status)}
}

func (s *Service) view(ctx context.Context, r *rec) api.Automation {
	v := r.Automation
	v.Revision = r.revision()
	v.Confirmed = r.confirmed()
	v.Running, _ = s.running(ctx, r)
	v.GateCommands = api.NonNil(v.GateCommands)
	v.Lane = api.NonNil(v.Lane)
	return v
}

// ---- validation --------------------------------------------------------------------------------------------------

var backends = []string{"agy", "claude", "codex"}

func cleanList(list []string, what string) ([]string, error) {
	out := []string{}
	if len(list) > maxList {
		return nil, invalidf("%s: at most %d entries", what, maxList)
	}
	for _, v := range list {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if len(v) > 1000 || strings.ContainsAny(v, "\x00\r\n") {
			return nil, invalidf("%s: bad entry %q", what, clip(v, 60))
		}
		out = append(out, v)
	}
	return out, nil
}

// normSchedule checks a schedule and fills in a preset's defaults.
func normSchedule(sc api.Schedule) (api.Schedule, error) {
	sc.Cron = strings.TrimSpace(sc.Cron)
	if sc.Cron != "" {
		if _, err := ParseCron(sc.Cron); err != nil {
			return api.Schedule{}, err
		}
		return api.Schedule{Cron: sc.Cron}, nil
	}
	if sc.Every != api.EveryWeekly {
		sc.Weekday = nil
	} else if sc.Weekday == nil {
		wd := 1
		sc.Weekday = &wd
	}
	if sc.At == "" {
		sc.At = "09:00"
		if sc.Every == api.EveryHourly {
			sc.At = "00:00"
		}
	}
	expr, err := ScheduleCron(sc)
	if err != nil {
		return api.Schedule{}, err
	}
	if _, err := ParseCron(expr); err != nil {
		return api.Schedule{}, err
	}
	return sc, nil
}

func checkCwd(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !filepath.IsAbs(p) {
		return "", invalidf("cwd must be an absolute folder")
	}
	p = filepath.Clean(p)
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return "", invalidf("cwd does not exist: %s", p)
	}
	return p, nil
}

func checkQuota(q api.AutomationQuota) error {
	if q.MinRemaining < 0 || q.MinRemaining > 100 || q.MinRemaining != q.MinRemaining {
		return invalidf("quota.minRemaining must be 0-100")
	}
	return nil
}

// validate checks every field of r.
func (s *Service) validate(r *rec) error {
	if strings.TrimSpace(r.Prompt) == "" {
		return invalidf("prompt is required")
	}
	if len(r.Prompt) > maxPrompt {
		return invalidf("prompt is longer than %d bytes", maxPrompt)
	}
	cwd, err := checkCwd(r.Cwd)
	if err != nil {
		return err
	}
	r.Cwd = cwd
	r.Name = oneLine(r.Name, maxName)
	if r.Name == "" {
		r.Name = oneLine(strings.SplitN(strings.TrimSpace(r.Prompt), "\n", 2)[0], maxName)
	}
	if r.Name == "" {
		r.Name = "Automation"
	}
	if r.Schedule, err = normSchedule(r.Schedule); err != nil {
		return err
	}
	if r.Timezone == "" {
		r.Timezone = "local"
	}
	if _, ok, err := NextRun(r.Schedule, r.Timezone, s.now()); err != nil {
		return err
	} else if !ok {
		return invalidf("the schedule never runs: no date in the next %d years matches it", searchYears)
	}
	r.Member, r.Model = strings.TrimSpace(r.Member), strings.TrimSpace(r.Model)
	if len(r.Member) > 100 || len(r.Model) > 200 {
		return invalidf("member or model is too long")
	}
	if r.Backend != "" && !slices.Contains(backends, r.Backend) {
		return invalidf("backend must be agy, claude or codex")
	}
	if r.GateCommands, err = cleanList(r.GateCommands, "gateCommands"); err != nil {
		return err
	}
	if r.Lane, err = cleanList(r.Lane, "lane"); err != nil {
		return err
	}
	r.Precheck = strings.TrimSpace(r.Precheck)
	if len(r.Precheck) > maxPrecheck || strings.ContainsRune(r.Precheck, 0) {
		return invalidf("precheck is longer than %d bytes", maxPrecheck)
	}
	if err := checkQuota(r.Quota); err != nil {
		return err
	}
	if r.GraceMinutes < 0 || r.GraceMinutes > 1440 {
		return invalidf("graceMinutes must be 0-1440")
	}
	return nil
}

// nextRun is the next scheduled run of an enabled automation after from ("" when disabled or never).
func nextRun(r *rec, from time.Time) string {
	if !r.Enabled {
		return ""
	}
	t, ok, err := NextRun(r.Schedule, r.Timezone, from)
	if err != nil || !ok {
		return ""
	}
	return api.FormatTime(t)
}

// ---- CRUD --------------------------------------------------------------------------------------------------------

// List returns every automation.
func (s *Service) List(ctx context.Context) ([]api.Automation, error) {
	var recs []*rec
	if err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		recs, err = listRecs(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}
	out := make([]api.Automation, 0, len(recs))
	for _, r := range recs {
		out = append(out, s.view(ctx, r))
	}
	return out, nil
}

// Get returns one automation.
func (s *Service) Get(ctx context.Context, id string) (api.Automation, error) {
	r, err := s.get(ctx, id)
	if err != nil {
		return api.Automation{}, err
	}
	return s.view(ctx, r), nil
}

func (s *Service) get(ctx context.Context, id string) (*rec, error) {
	var r *rec
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = loadRec(ctx, tx, id)
		return err
	})
	return r, err
}

// Create saves a new automation, always disabled; in.Enabled only records an enable request.
func (s *Service) Create(ctx context.Context, in api.AutomationInput) (api.Automation, error) {
	now := api.FormatTime(s.now())
	r := &rec{Automation: api.Automation{ID: newID(), Name: in.Name, Prompt: in.Prompt, Cwd: in.Cwd,
		Schedule: in.Schedule, Timezone: in.Timezone, Member: in.Member, Backend: in.Backend, Model: in.Model,
		Gate: in.Gate, GateCommands: in.GateCommands, Lane: in.Lane, NoLane: in.NoLane, Precheck: in.Precheck,
		Quota: api.AutomationQuota{MinRemaining: defaultMinLeft}, GraceMinutes: defaultGrace, CreatedAt: now,
		UpdatedAt: now}}
	if in.Quota != nil {
		r.Quota = *in.Quota
	}
	if in.GraceMinutes != nil {
		r.GraceMinutes = *in.GraceMinutes
	}
	if err := s.validate(r); err != nil {
		return api.Automation{}, err
	}
	if in.Enabled {
		r.EnableRequested, r.EnableRequestedAt = true, now
	}
	if err := s.o.DB.Write(ctx, func(tx *sql.Tx) error { return saveRec(ctx, tx, r) }); err != nil {
		return api.Automation{}, err
	}
	v := s.view(ctx, r)
	s.o.Changed(v)
	return v, nil
}

// Update changes an automation. Enabled false turns it off; Enabled true only records an enable request. A change
// of what it runs turns an enabled automation off until the user confirms it again.
func (s *Service) Update(ctx context.Context, id string, p api.AutomationPatch) (api.Automation, error) {
	return s.update(ctx, id, func(r *rec) error {
		set := func(dst *string, v *string) {
			if v != nil {
				*dst = *v
			}
		}
		set(&r.Name, p.Name)
		set(&r.Prompt, p.Prompt)
		set(&r.Cwd, p.Cwd)
		set(&r.Timezone, p.Timezone)
		set(&r.Member, p.Member)
		set(&r.Backend, p.Backend)
		set(&r.Model, p.Model)
		set(&r.Precheck, p.Precheck)
		if p.Schedule != nil {
			r.Schedule = *p.Schedule
		}
		if p.Gate != nil {
			g := *p.Gate
			r.Gate = &g
		}
		if p.GateCommands != nil {
			r.GateCommands = *p.GateCommands
		}
		if p.Lane != nil {
			r.Lane = *p.Lane
		}
		if p.NoLane != nil {
			r.NoLane = *p.NoLane
		}
		if p.Quota != nil {
			r.Quota = *p.Quota
		}
		if p.GraceMinutes != nil {
			r.GraceMinutes = *p.GraceMinutes
		}
		if err := s.validate(r); err != nil {
			return err
		}
		now := s.now()
		if p.Enabled != nil && !*p.Enabled {
			r.Enabled, r.EnableRequested, r.EnableRequestedAt = false, false, ""
		}
		if r.Enabled && r.contentHash() != r.ConfirmedHash {
			r.Enabled = false
			r.EnableRequested, r.EnableRequestedAt = true, api.FormatTime(now)
		}
		if p.Enabled != nil && *p.Enabled && !r.Enabled {
			r.EnableRequested, r.EnableRequestedAt = true, api.FormatTime(now)
		}
		r.UpdatedAt = api.FormatTime(now)
		r.NextRun = nextRun(r, now)
		return nil
	})
}

// Delete removes an automation and its run history (its tasks stay).
func (s *Service) Delete(ctx context.Context, id string) error {
	s.edit.Lock()
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM automations WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: no automation %s", ErrNotFound, id)
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM automation_runs WHERE automation_id = ?`, id)
		return err
	})
	s.edit.Unlock()
	if err != nil {
		return err
	}
	s.mu.Lock()
	for k, e := range s.tokens {
		if e.id == id {
			delete(s.tokens, k)
		}
	}
	s.mu.Unlock()
	s.o.Deleted(id)
	s.poke()
	return nil
}

// ---- enabling (D8) -----------------------------------------------------------------------------------------------

// EnableResult is the answer to an enable request: the automation stays off until the user confirms it.
func (s *Service) EnableResult(a api.Automation) api.AutomationEnableResult {
	u := s.o.ConfirmURL(a.ID)
	msg := fmt.Sprintf("Automation %q stays off until the user turns it on in the dashboard: %s", a.Name, u)
	if a.Enabled {
		msg = fmt.Sprintf("Automation %q is already on.", a.Name)
	}
	return api.AutomationEnableResult{Automation: a, Confirm: !a.Enabled, ConfirmURL: u, Message: msg}
}

// RequestEnable records the lead's request to turn an automation on (MCP, CLI, PATCH enabled true).
func (s *Service) RequestEnable(ctx context.Context, id string) (api.AutomationEnableResult, error) {
	a, err := s.update(ctx, id, func(r *rec) error {
		if r.Enabled {
			return nil
		}
		r.EnableRequested, r.EnableRequestedAt = true, api.FormatTime(s.now())
		return nil
	})
	if err != nil {
		return api.AutomationEnableResult{}, err
	}
	return s.EnableResult(a), nil
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ConfirmToken issues a one-time token that turns on automation id as the dashboard shows it: revision is the
// Revision it shows, and a token is refused when the automation has changed since (the dashboard asks for the
// token right when the user confirms; the HTTP layer only serves it to the dashboard).
func (s *Service) ConfirmToken(ctx context.Context, id, revision string) (api.AutomationConfirmToken, error) {
	r, err := s.get(ctx, id)
	if err != nil {
		return api.AutomationConfirmToken{}, err
	}
	if strings.TrimSpace(revision) == "" {
		return api.AutomationConfirmToken{}, invalidf("revision is required: the revision of the automation as shown")
	}
	if revision != r.revision() {
		return api.AutomationConfirmToken{}, fmt.Errorf("%w: the automation changed since it was shown; read it again",
			ErrBadState)
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return api.AutomationConfirmToken{}, err
	}
	tok := hex.EncodeToString(b)
	now := s.now()
	exp := now.Add(ConfirmTTL)
	s.mu.Lock()
	for k, e := range s.tokens {
		if !now.Before(e.expires) || e.id == id {
			delete(s.tokens, k)
		}
	}
	s.tokens[tokenKey(tok)] = confirmEntry{id: id, hash: r.contentHash(), expires: exp}
	s.mu.Unlock()
	return api.AutomationConfirmToken{Token: tok, ExpiresAt: api.FormatTime(exp)}, nil
}

// Confirm turns automation id on with a token from ConfirmToken. The token works once, for a few minutes, and only
// while the automation is unchanged since it was issued.
func (s *Service) Confirm(ctx context.Context, id, token string) (api.Automation, error) {
	key := tokenKey(token)
	s.mu.Lock()
	e, ok := s.tokens[key]
	delete(s.tokens, key)
	s.mu.Unlock()
	if !ok || e.id != id || !s.now().Before(e.expires) || strings.TrimSpace(token) == "" {
		return api.Automation{}, fmt.Errorf("%w: the confirmation token is not valid; confirm again", ErrForbidden)
	}
	return s.update(ctx, id, func(r *rec) error {
		h := r.contentHash()
		if h != e.hash {
			return fmt.Errorf("%w: the automation changed since the confirmation was shown; confirm again",
				ErrBadState)
		}
		now := s.now()
		r.Enabled, r.EnableRequested, r.EnableRequestedAt = true, false, ""
		r.ConfirmedAt, r.ConfirmedHash = api.FormatTime(now), h
		r.UpdatedAt = api.FormatTime(now)
		r.NextRun = nextRun(r, now)
		return nil
	})
}

// ---- runs --------------------------------------------------------------------------------------------------------

// Runs returns the newest runs of an automation: limit defaults to 20 and is capped at the runs kept (the HTTP API
// takes 1-200).
func (s *Service) Runs(ctx context.Context, id string, limit int) ([]api.AutomationRun, error) {
	if limit <= 0 {
		limit = 20
	}
	limit = min(limit, maxRunsKept)
	out := []api.AutomationRun{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		if _, err := loadRec(ctx, tx, id); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id, data FROM automation_runs WHERE automation_id = ?
			ORDER BY id DESC LIMIT ?`, id, limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var rid int64
			var raw string
			if err := rows.Scan(&rid, &raw); err != nil {
				return err
			}
			var run api.AutomationRun
			if err := json.Unmarshal([]byte(raw), &run); err != nil {
				return fmt.Errorf("automation run %d is unreadable: %w", rid, err)
			}
			run.ID = rid
			out = append(out, run)
		}
		return rows.Err()
	})
	return out, err
}

// record stores a run and makes it the automation's last run. A run with an ID resolves that row (the pending
// row the scheduler wrote); without one, or when that row is gone, it is a new row.
func (s *Service) record(ctx context.Context, id string, run api.AutomationRun) (api.AutomationRun, error) {
	run.At = api.FormatTime(s.now())
	if run.Detail == "" {
		run.Detail = runDetail(run)
	}
	run.Detail = clip(s.redact.String(run.Detail), maxDetail)
	if run.Info != nil {
		info := *run.Info
		info.Output = s.redact.String(info.Output)
		info.Message = clip(s.redact.String(info.Message), maxDetail)
		run.Info = &info
	}
	var r *rec
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if r, err = loadRec(ctx, tx, id); err != nil {
			return err
		}
		if run, err = saveRun(ctx, tx, id, run); err != nil {
			return err
		}
		last := run
		r.LastRun = &last
		if run.TaskID != "" {
			r.ActiveTaskID = run.TaskID
		}
		return saveRec(ctx, tx, r)
	})
	if err != nil {
		return run, err
	}
	s.o.Changed(s.view(ctx, r))
	return run, nil
}

// saveRun writes run: over its row when it has an ID and the row is still there, else as a new row (the oldest
// beyond maxRunsKept go).
func saveRun(ctx context.Context, tx *sql.Tx, id string, run api.AutomationRun) (api.AutomationRun, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return run, err
	}
	if run.ID != 0 {
		res, err := tx.ExecContext(ctx, `UPDATE automation_runs SET at = ?, data = ? WHERE id = ? AND automation_id = ?`,
			run.At, string(raw), run.ID, id)
		if err != nil {
			return run, err
		}
		if n, err := res.RowsAffected(); err != nil || n == 1 {
			return run, err
		}
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO automation_runs (automation_id, at, data) VALUES (?, ?, ?)`,
		id, run.At, string(raw))
	if err != nil {
		return run, err
	}
	if run.ID, err = res.LastInsertId(); err != nil {
		return run, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM automation_runs WHERE automation_id = ? AND id <= ?`, id,
		run.ID-maxRunsKept)
	return run, err
}

// targetBackend is the backend a run of r uses.
func (s *Service) targetBackend(ctx context.Context, r *rec) string {
	if r.Backend != "" {
		return r.Backend
	}
	if r.Member != "" && s.o.Members != nil {
		if m, err := s.o.Members.Usable(ctx, r.Member); err == nil && m.Backend != "" {
			return m.Backend
		}
	}
	return "agy"
}

// quotaRows reads the quota of r's backend (nil when unknown).
func (s *Service) quotaRows(ctx context.Context, r *rec) (string, []AccountQuota) {
	b := s.targetBackend(ctx, r)
	if s.o.Quota == nil {
		return b, nil
	}
	rows, err := s.o.Quota.Quota(ctx, b, r.Model)
	if err != nil {
		s.log.Warn("automation: read quota", "automation", r.ID, "err", err)
		return b, nil
	}
	return b, rows
}

// bestRemaining is the most any account has left in the window that limits it (false when no data).
func bestRemaining(rows []AccountQuota, now time.Time) (float64, bool) {
	best, known := 0.0, false
	for _, a := range rows {
		left := 1.0
		for _, w := range a.Windows {
			left = min(left, w.RemainingAt(now))
		}
		best, known = max(best, left), true
	}
	return best, known
}

// resetCandidate is a weekly bucket that resets within 24 h with at least 40 % left (the most left wins).
type resetCandidate struct {
	key       string
	accountID string
	remaining float64
	resetAt   time.Time
}

func beforeResetCandidate(rows []AccountQuota, now time.Time) *resetCandidate {
	var best *resetCandidate
	for _, a := range rows {
		for _, w := range a.Windows {
			if w.Window != "weekly" || w.ResetAt.IsZero() || !w.ResetAt.After(now) ||
				w.ResetAt.Sub(now) > BeforeResetWindow || w.Remaining < BeforeResetMinLeft {
				continue
			}
			if best == nil || w.Remaining > best.remaining {
				best = &resetCandidate{key: a.AccountID + "@" + api.FormatTime(w.ResetAt), accountID: a.AccountID,
					remaining: w.Remaining, resetAt: w.ResetAt}
			}
		}
	}
	return best
}

// fire runs r once: overlap guard, quota, precheck, then the task. The outcome is always recorded, over the
// scheduler's pending row when row is not 0. reset is the bucket of a before-reset run: its own remaining share is
// judged against minRemaining (old bug D9).
func (s *Service) fire(ctx context.Context, id, trigger string, reset *resetCandidate, row int64) (api.AutomationRun,
	error) {
	record := func(ctx context.Context, id string, run api.AutomationRun) (api.AutomationRun, error) {
		run.ID = row
		return s.record(ctx, id, run)
	}
	r, err := s.get(ctx, id)
	if err != nil {
		return api.AutomationRun{}, err
	}
	if busy, why := s.running(ctx, r); busy {
		return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedOverlap, Info: why})
	}
	s.mu.Lock()
	if s.firing[id] {
		s.mu.Unlock()
		return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedOverlap,
			Info: &api.AutomationRunInfo{Reason: api.RunStarting}})
	}
	s.firing[id] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.firing, id)
		s.mu.Unlock()
	}()

	// What runs is this version: the one the checks below judge. A change while they run (the precheck can take a
	// minute) is not run before the user confirms it.
	h := r.contentHash()
	if why := s.mayStart(r, h, trigger); why != "" {
		return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedChanged,
			Info: &api.AutomationRunInfo{Reason: why}})
	}
	minLeft := r.Quota.MinRemaining
	if reset != nil {
		if reset.remaining*100 < minLeft {
			left := reset.remaining * 100
			return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedQuota,
				Info: &api.AutomationRunInfo{Account: reset.accountID, Remaining: &left, MinRemaining: minLeft}})
		}
	} else {
		b, rows := s.quotaRows(ctx, r)
		if best, ok := bestRemaining(rows, s.now()); ok && best*100 < minLeft {
			left := best * 100
			return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedQuota,
				Info: &api.AutomationRunInfo{Backend: b, Remaining: &left, MinRemaining: minLeft}})
		}
	}
	if r.Precheck != "" {
		res := s.o.Precheck(ctx, r.Precheck, r.Cwd, PrecheckTimeout)
		if !res.OK() {
			return record(ctx, id, precheckRun(trigger, res))
		}
	}
	// The last check and the delegate hold the edit lock: an edit, a switch-off or a delete waits for the task to
	// exist, or comes first and is seen here.
	s.edit.RLock()
	now, err := s.get(ctx, id)
	if err != nil { // deleted during the precheck
		s.edit.RUnlock()
		return api.AutomationRun{}, err
	}
	if why := s.mayStart(now, h, trigger); why != "" {
		s.edit.RUnlock()
		return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoSkippedChanged,
			Info: &api.AutomationRunInfo{Reason: why}})
	}
	req := api.DelegateRequest{Prompt: r.Prompt, Cwd: r.Cwd, Title: r.Name, Member: r.Member, Backend: r.Backend,
		Model: r.Model, Gate: r.Gate, GateCommands: r.GateCommands, Lane: r.Lane, NoLane: r.NoLane}
	sum, err := s.o.Tasks.DelegateWith(ctx, req, task.Spawn{AutomationID: id})
	s.edit.RUnlock()
	if err != nil {
		return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoError, Info: errorInfo(err),
			Detail: oneLine(err.Error(), maxDetail)})
	}
	return record(ctx, id, api.AutomationRun{Trigger: trigger, Outcome: api.AutoStarted, TaskID: sum.ID})
}

// mayStart says why r cannot start now ("" when it can): it must still be the version h whose checks ran and that
// the user confirmed, and a run of its own (schedule, missed, before reset) also needs it and the master switch on.
func (s *Service) mayStart(r *rec, h, trigger string) string {
	switch {
	case r.contentHash() != h:
		return api.RunChanged
	case !r.confirmed():
		return api.RunUnconfirmed
	case trigger == api.TriggerManual:
		return ""
	case !r.Enabled:
		return api.RunDisabled
	case !s.o.MasterOn():
		return api.RunMasterOff
	}
	return ""
}

// precheckRun is the run of a precheck that did not say go. Exit code 1 is the precheck's answer "skip" (grep -q,
// test, git diff --quiet); anything else means the precheck itself broke: another exit code (2 for a usage error,
// 126/127 for a missing command, 128 for a git error), killed, timed out or not started.
func precheckRun(trigger string, res gate.ExecResult) api.AutomationRun {
	run := api.AutomationRun{Trigger: trigger, Outcome: api.AutoPrecheckFailed, Info: &api.AutomationRunInfo{}}
	if tail := strings.TrimSpace(res.Tail); tail != "" && tail != res.Err {
		run.Info.Output = tailOf(tail, 400)
	}
	switch {
	case res.TimedOut:
		run.Info.TimedOut, run.Info.TimeoutSeconds = true, int(PrecheckTimeout/time.Second)
	case res.Exit != nil:
		code := *res.Exit
		run.Info.ExitCode = &code
		if code == 1 {
			run.Outcome = api.AutoSkippedPrecheck
		}
	default:
		run.Info.NotStarted = true
		if run.Info.Output == "" {
			run.Info.Output = tailOf(strings.TrimSpace(res.Err), 400)
		}
	}
	return run
}

// runDetail is a run's Info said in English, for the lead's tools (the dashboard says it in the user's language).
func runDetail(run api.AutomationRun) string {
	in := run.Info
	if in == nil {
		return ""
	}
	pct := func(p *float64) float64 {
		if p == nil {
			return 0
		}
		return *p
	}
	var why string
	switch run.Outcome {
	case api.AutoSkippedOverlap:
		why = "a run is already starting"
		if in.Reason == api.RunTaskRunning {
			why = fmt.Sprintf("task %s is still %s", in.TaskID, in.TaskStatus)
		}
	case api.AutoSkippedQuota:
		if in.Account != "" {
			why = fmt.Sprintf("the weekly bucket of %s has %.0f%% left (< %g%%)", in.Account, pct(in.Remaining),
				in.MinRemaining)
		} else {
			why = fmt.Sprintf("the best %s account has %.0f%% left (< %g%%)", in.Backend, pct(in.Remaining),
				in.MinRemaining)
		}
	case api.AutoSkippedPrecheck, api.AutoPrecheckFailed:
		switch {
		case in.TimedOut:
			why = fmt.Sprintf("timed out after %d s", in.TimeoutSeconds)
		case in.ExitCode != nil:
			why = fmt.Sprintf("exit code %d", *in.ExitCode)
		default:
			why = "did not run"
		}
		if in.Output != "" {
			why += ": " + in.Output
		}
	case api.AutoSkippedLate:
		why = fmt.Sprintf("missed by %s (grace %d min)", time.Duration(in.LateMinutes)*time.Minute, in.GraceMinutes)
	case api.AutoSkippedChanged:
		why = map[string]string{
			api.RunChanged:     "the automation changed while its precheck ran; nothing was started",
			api.RunUnconfirmed: "the automation is not confirmed as it is now; nothing was started",
			api.RunDisabled:    "the automation was turned off while its precheck ran; nothing was started",
			api.RunMasterOff:   "automations were turned off while the precheck ran; nothing was started",
		}[in.Reason]
	case api.AutoError:
		why = in.Message
	}
	return why
}

// errorInfo is what is known of a run that could not start: a member that is gone or turned off has a reason; any
// other cause is only its message.
func errorInfo(err error) *api.AutomationRunInfo {
	var detailed interface{ APIDetails() map[string]any }
	if errors.As(err, &detailed) {
		d := detailed.APIDetails()
		reason, _ := d["reason"].(string)
		member, _ := d["member"].(string)
		if reason == api.RunMemberMissing || reason == api.RunMemberDisabled {
			return &api.AutomationRunInfo{Reason: reason, Member: member}
		}
	}
	return &api.AutomationRunInfo{Message: oneLine(err.Error(), maxDetail)}
}

// NotConfirmedError refuses run now for an automation whose content the user has not confirmed: never confirmed,
// or changed since. The lead's tools can create and edit automations, so running one at once would run a prompt the
// user never saw (the safeguard of old bug D8).
type NotConfirmedError struct {
	ID, Name, ConfirmURL string
}

func (e *NotConfirmedError) Error() string {
	return fmt.Sprintf("automation %q has not been confirmed in the dashboard as it is now, so it cannot run; the "+
		"user reviews and turns it on at %s", e.Name, e.ConfirmURL)
}

// Is makes errors.Is(err, ErrBadState) true.
func (e *NotConfirmedError) Is(target error) bool { return target == ErrBadState }

// APIDetails are the error details of the HTTP API.
func (e *NotConfirmedError) APIDetails() map[string]any {
	return map[string]any{"reason": "not_confirmed", "confirmUrl": e.ConfirmURL}
}

// RunNow runs an automation at once, with the same rules as a scheduled run, also when it is disabled or the
// master switch is off. Only what the user confirmed runs: an automation never confirmed, or changed since, is
// refused (NotConfirmedError).
func (s *Service) RunNow(ctx context.Context, id string) (api.AutomationRun, error) {
	r, err := s.get(ctx, id)
	if err != nil {
		return api.AutomationRun{}, err
	}
	if !r.confirmed() {
		return api.AutomationRun{}, &NotConfirmedError{ID: r.ID, Name: r.Name, ConfirmURL: s.o.ConfirmURL(r.ID)}
	}
	return s.fire(ctx, id, api.TriggerManual, nil, 0)
}

// Preview returns the next run times of a schedule.
func (s *Service) Preview(req api.SchedulePreviewRequest) (api.SchedulePreview, error) {
	sc, err := normSchedule(req.Schedule)
	if err != nil {
		return api.SchedulePreview{}, err
	}
	tz := req.Timezone
	if tz == "" {
		tz = "local"
	}
	loc, err := Zone(tz)
	if err != nil {
		return api.SchedulePreview{}, err
	}
	n := req.Count
	if n <= 0 {
		n = 5
	}
	n = min(n, maxPreviewCount)
	out := api.SchedulePreview{Runs: []string{}, Local: []string{}, Timezone: tz}
	if tz == "local" {
		out.Timezone = "local (" + time.Local.String() + ")"
	}
	from := s.now()
	for len(out.Runs) < n {
		t, ok, err := NextRun(sc, tz, from)
		if err != nil {
			return api.SchedulePreview{}, err
		}
		if !ok {
			break
		}
		out.Runs = append(out.Runs, api.FormatTime(t))
		out.Local = append(out.Local, t.In(loc).Format("2006-01-02 15:04 Mon MST"))
		from = t
	}
	return out, nil
}

func oneLine(s string, n int) string {
	return clip(strings.Join(strings.Fields(s), " "), n)
}

// clip cuts s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

// tailOf keeps the last n bytes of s on a rune boundary.
func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && !utf8Start(s[start]) {
		start++
	}
	return "…" + s[start:]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
