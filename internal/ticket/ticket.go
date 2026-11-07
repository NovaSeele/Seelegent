// Package ticket turns GitHub issues into pull requests (milestone 15, docs/github.md): connected repositories, an
// issue inbox (GitHub issues and local ones), triage into tickets (one issue worked on in its own git worktree and
// branch), and the explicit pull request flow with a write-ahead outbox, CI check polling and fix_checks.
//
// Safety: the user's checkout is never changed. Seelegent only runs `git worktree add -b seelegent/...`, a plain
// `git worktree remove` of a clean ticket worktree, and status/diff/add/commit/push inside the ticket worktree,
// through the safe runner of gitx. It never force-pushes, merges, closes issues or pushes any branch but the
// ticket's. Issue text and CI output are untrusted: they reach a worker only fenced, with a warning. Before anything
// is committed or pushed, the files are scanned for secrets and junk (old bug D5). Every record is a SQLite row
// (old bug D6); a row that cannot be read is an error.
package ticket

import (
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // an id, not a security hash
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Errors; the HTTP API maps them to not_found, invalid_argument, bad_state and conflict.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrBadState = errors.New("bad state")
	ErrConflict = errors.New("conflict")
	// ErrGit is matched by a git command of the ticket flow that failed (*GitError, redacted).
	ErrGit = errors.New("git failed")

	errUnchanged = errors.New("unchanged")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func badStatef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadState, fmt.Sprintf(format, args...))
}

func notFoundf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, args...))
}

// FailureError refuses a retry "as before" of a ticket whose task could not start because its member is gone or
// turned off (F69): the same choice would fail again at once. The lead retries with another member or backend, or
// names the member again once it is back.
type FailureError struct{ Failure api.TicketFailure }

func (e *FailureError) Error() string {
	why := "no longer exists"
	if e.Failure.Reason == api.FailureMemberDisabled {
		why = "is turned off"
	}
	return fmt.Sprintf("the ticket failed because member %q %s, so a retry as before would fail again; retry with "+
		"another member or backend, or name the member again once it can work", e.Failure.Member, why)
}

// Is makes errors.Is(err, ErrBadState) true.
func (e *FailureError) Is(target error) bool { return target == ErrBadState }

// APIDetails are the details of the API error (reason member_missing or member_disabled).
func (e *FailureError) APIDetails() map[string]any {
	return map[string]any{"reason": e.Failure.Reason, "member": e.Failure.Member}
}

// GitError is a git command of the ticket flow that failed; Message is redacted.
type GitError struct {
	Op      string
	Message string
}

func (e *GitError) Error() string { return "git " + e.Op + ": " + e.Message }

// Is makes errors.Is(err, ErrGit) true.
func (e *GitError) Is(target error) bool { return target == ErrGit }

// Limits.
const (
	maxBody       = 20000
	maxTitle      = 300
	syncLimit     = 100
	maxQuestion   = 8000
	maxFixRuns    = 10
	maxLogEntries = 200
	maxReason     = 300
	maxComment    = 65000
	maxPRBody     = 60000
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Git runs the safe git commands (gitx.Repos).
type Git interface {
	Exec(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) ([]byte, error)
	Lock(ctx context.Context, root string) (func(), error)
	// SnapshotTree writes the tree of the working tree at dir through a temporary index in tmpDir.
	SnapshotTree(ctx context.Context, dir, tmpDir string, exclude ...string) (string, error)
}

// Tasks is the task engine (task.Service).
type Tasks interface {
	DelegateWith(ctx context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error)
	Summary(ctx context.Context, id string) (api.TaskSummary, error)
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	Cancel(ctx context.Context, id string, req api.CancelRequest) (api.TaskSummary, bool, error)
	Bus() *task.Bus
}

// Meetings opens and reads meetings (meeting.Service).
type Meetings interface {
	Open(ctx context.Context, req api.MeetingRequest) (api.Meeting, error)
	Get(ctx context.Context, id string) (api.Meeting, error)
}

// GitHub is the GitHub driver (github.Driver).
type GitHub interface {
	Access(ctx context.Context, force bool) github.Access
	Hosts() github.Hosts
	Client(ctx context.Context, a github.Access, host github.Host, owner, repo string) (*github.Client, error)
	Redact(text string) string
	// HasToken reports whether data holds the stored token.
	HasToken(data []byte) bool
	SaveToken(token string) (string, error)
	RemoveToken() error
}

// Options configure a Service. DB, Git, Tasks and GitHub are required.
type Options struct {
	DB       DB
	Git      Git
	Tasks    Tasks
	Meetings Meetings
	GitHub   GitHub
	// WorktreeBase is the folder of the ticket worktrees (<home>/worktrees/tickets).
	WorktreeBase string
	// RemoteHost is where CreateRemote creates repositories (zero: github.com; tests use a fake server).
	RemoteHost github.Host
	// PollMinutes is the setting githubPollMinutes (0: no polling).
	PollMinutes func() int
	// UserHome is the user's home folder, replaced by "~" in text that leaves the machine.
	UserHome string
	// TicketChanged and IssuesChanged tell the stream (ticket.updated, issues.changed).
	TicketChanged func(api.TicketSummary)
	IssuesChanged func(repoID string)
	Now           func() time.Time
	Logger        *slog.Logger
}

// Service runs the GitHub issue to pull request flow.
type Service struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	locks keyed
	// polling is set while a poll runs.
	pollMu sync.Mutex
}

// New returns a Service; Start recovers the outbox and starts the task follower and the poller.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.TicketChanged == nil {
		o.TicketChanged = func(api.TicketSummary) {}
	}
	if o.IssuesChanged == nil {
		o.IssuesChanged = func(string) {}
	}
	if o.PollMinutes == nil {
		o.PollMinutes = func() int { return 0 }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, ctx: ctx, cancel: cancel, locks: keyed{m: map[string]*keyEntry{}}}
}

// keyed is a set of named locks.
type keyed struct {
	mu sync.Mutex
	m  map[string]*keyEntry
}

type keyEntry struct {
	ch   chan struct{}
	refs int
}

// lock takes the named lock (or gives up when ctx ends) and returns its unlock.
func (k *keyed) lock(ctx context.Context, name string) (func(), error) {
	k.mu.Lock()
	e := k.m[name]
	if e == nil {
		e = &keyEntry{ch: make(chan struct{}, 1)}
		k.m[name] = e
	}
	e.refs++
	k.mu.Unlock()
	release := func() {
		k.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(k.m, name)
		}
		k.mu.Unlock()
	}
	select {
	case e.ch <- struct{}{}:
		return func() { <-e.ch; release() }, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

// ---- records ----------------------------------------------------------------------------------------------------

// repoRec is a connected repository as stored. Whether it is on GitHub is worked out from RemoteURL and the host
// list each time (the settings can change).
type repoRec struct {
	ID            string    `json:"id"`
	Path          string    `json:"path"`
	Name          string    `json:"name"`
	HasOrigin     bool      `json:"hasOrigin"`
	RemoteURL     string    `json:"remoteUrl,omitempty"`
	DefaultBranch string    `json:"defaultBranch"`
	SharedDirs    []string  `json:"sharedDirs"`
	CreatedAt     time.Time `json:"createdAt"`
	LastSyncAt    time.Time `json:"lastSyncAt,omitzero"`
	SyncError     string    `json:"syncError,omitempty"`
	NextLocalID   int       `json:"nextLocalId"`
}

// ghRepo is the GitHub repository of a connected repository.
type ghRepo struct {
	host  github.Host
	owner string
	name  string
	// why says why the repository is not on GitHub.
	why string
}

func (g ghRepo) ok() bool { return g.owner != "" }

// gh works out the GitHub repository of r.
func (s *Service) gh(r *repoRec) ghRepo {
	if !r.HasOrigin || r.RemoteURL == "" {
		return ghRepo{why: "no origin remote"}
	}
	rem, ok := github.ParseRemote(r.RemoteURL)
	if !ok {
		return ghRepo{why: "origin is not an owner/repository URL"}
	}
	host, ok := s.o.GitHub.Hosts().Lookup(rem)
	if !ok {
		return ghRepo{why: "origin's host " + rem.Host + " is not github.com or a listed enterprise host"}
	}
	return ghRepo{host: host, owner: rem.Owner, name: rem.Repo}
}

// ticketRec is a ticket as stored.
type ticketRec struct {
	ID       string          `json:"id"`
	RepoID   string          `json:"repoId"`
	IssueKey string          `json:"issueKey"`
	Issue    api.TicketIssue `json:"issue"`
	Slug     string          `json:"slug"`
	Branch   string          `json:"branch"`
	Base     string          `json:"base"`
	BaseSha  string          `json:"baseSha,omitempty"`
	// PushedSha is the commit Seelegent last pushed to the ticket branch (its own commit, never the worker's
	// history) and PushedBase the base commit it built on.
	PushedSha  string             `json:"pushedSha,omitempty"`
	PushedBase string             `json:"pushedBase,omitempty"`
	Worktree   string             `json:"worktree"`
	Status     string             `json:"status"`
	TaskIDs    []string           `json:"taskIds"`
	MeetingID  string             `json:"meetingId,omitempty"`
	PR         *api.TicketPR      `json:"pr,omitempty"`
	Checks     *api.TicketChecks  `json:"checks,omitempty"`
	ChecksFix  *api.ChecksFix     `json:"checksFix,omitempty"`
	Env        *api.WorktreeEnv   `json:"env,omitempty"`
	Error      string             `json:"error,omitempty"`
	Failure    *api.TicketFailure `json:"failure,omitempty"`
	Asked      *api.TicketAsk     `json:"asked,omitempty"`
	Refusal    *api.TicketRefusal `json:"refusal,omitempty"`
	// Exposed is what the branch the previous version pushed already held on GitHub when Seelegent adopted it.
	Exposed         []api.ScanFinding `json:"exposed,omitempty"`
	DropReason      string            `json:"dropReason,omitempty"`
	WorktreeRemoved bool              `json:"worktreeRemoved,omitempty"`
	CleanedAt       time.Time         `json:"cleanedAt,omitzero"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

// derived are the statuses that follow the ticket's tasks.
func derived(st string) bool {
	return st == api.TicketWorking || st == api.TicketReview || st == api.TicketReady
}

// finished are the statuses of a ticket that is over.
func finished(st string) bool {
	return st == api.TicketDone || st == api.TicketDropped || st == api.TicketFailed
}

// needsAttention is what the lead has to do next on a ticket whose pull request is in its hands: a refused push
// first, then failed checks, then a pushed branch whose pull request is still to be opened by hand.
func (t *ticketRec) needsAttention() string {
	if t.Status != api.TicketReady && t.Status != api.TicketPROpen {
		return ""
	}
	switch {
	case t.Refusal != nil:
		return api.TicketPushRefused
	case t.Status != api.TicketPROpen:
		return ""
	case t.Checks != nil && t.Checks.State == api.ChecksFailure:
		return api.TicketChecksFailed
	case t.PR != nil && t.PR.Number == 0 && t.PR.ManualURL != "":
		return api.TicketOpenPR
	}
	return ""
}

// dropRefusal forgets a refused pull request: its refusal and the message of a refusal kept as the error. It no
// longer says what the next pr will find once new work changes the worktree, nor anything once the ticket is
// dropped; the ticket's log keeps it.
func (t *ticketRec) dropRefusal() {
	t.Refusal = nil
	if t.Status != api.TicketFailed {
		t.Error = ""
	}
}

func (t *ticketRec) summary() api.TicketSummary {
	sum := api.TicketSummary{ID: t.ID, RepoID: t.RepoID, IssueKey: t.IssueKey, Issue: t.Issue, Branch: t.Branch,
		Worktree: t.Worktree, Status: t.Status, TaskIDs: api.NonNil(t.TaskIDs), MeetingID: t.MeetingID, PR: t.PR,
		NeedsAttention: t.needsAttention(), Error: t.Error, Asked: t.Asked, CreatedAt: api.FormatTime(t.CreatedAt),
		UpdatedAt: api.FormatTime(t.UpdatedAt)}
	if t.Refusal != nil && (t.Status == api.TicketReady || t.Status == api.TicketPROpen) {
		rf := *t.Refusal
		rf.Findings = api.NonNil(rf.Findings)
		sum.Refusal = &rf
	}
	if t.Checks != nil {
		c := *t.Checks
		c.Runs = api.NonNil(c.Runs)
		sum.Checks = &c
	}
	if t.Status == api.TicketFailed {
		sum.Failure = t.Failure
	}
	return sum
}

// ---- storage ----------------------------------------------------------------------------------------------------

func scanRepo(raw string) (*repoRec, error) {
	var r repoRec
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, err
	}
	if r.SharedDirs == nil {
		r.SharedDirs = []string{}
	}
	return &r, nil
}

func loadRepo(ctx context.Context, tx *sql.Tx, id string) (*repoRec, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT data FROM github_repos WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFoundf("no connected repository %s", id)
	}
	if err != nil {
		return nil, err
	}
	r, err := scanRepo(raw)
	if err != nil {
		return nil, fmt.Errorf("repository %s is unreadable: %w", id, err)
	}
	return r, nil
}

func listRepoRecs(ctx context.Context, tx *sql.Tx) ([]*repoRec, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, data FROM github_repos ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*repoRec
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		r, err := scanRepo(raw)
		if err != nil {
			return nil, fmt.Errorf("repository %s is unreadable: %w", id, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func saveRepo(ctx context.Context, tx *sql.Tx, r *repoRec, pathKey string) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_repos (id, path_key, created_at, data) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, r.ID, pathKey, api.FormatTime(r.CreatedAt), string(data))
	return err
}

func scanIssue(repoID, key, raw string) (api.Issue, error) {
	var it api.Issue
	if err := json.Unmarshal([]byte(raw), &it); err != nil {
		return api.Issue{}, fmt.Errorf("issue %s of %s is unreadable: %w", key, repoID, err)
	}
	it.Labels = api.NonNil(it.Labels)
	it.Ticket = nil
	return it, nil
}

func loadIssue(ctx context.Context, tx *sql.Tx, repoID, key string) (api.Issue, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT data FROM github_issues WHERE repo_id = ? AND key = ?`, repoID, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Issue{}, notFoundf("no issue %s in repository %s", key, repoID)
	}
	if err != nil {
		return api.Issue{}, err
	}
	return scanIssue(repoID, key, raw)
}

func listIssueRecs(ctx context.Context, tx *sql.Tx, repoID string) ([]api.Issue, error) {
	rows, err := tx.QueryContext(ctx, `SELECT key, data FROM github_issues WHERE repo_id = ? ORDER BY updated_at DESC, key`,
		repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []api.Issue
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		it, err := scanIssue(repoID, key, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func saveIssue(ctx context.Context, tx *sql.Tx, repoID string, it api.Issue) error {
	it.Ticket = nil
	data, err := json.Marshal(it)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO github_issues (repo_id, key, state, updated_at, data) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repo_id, key) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at,
		data = excluded.data`, repoID, it.Key, it.State, it.UpdatedAt, string(data))
	return err
}

func scanTicket(id, raw string) (*ticketRec, error) {
	var t ticketRec
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("ticket %s is unreadable: %w", id, err)
	}
	if t.TaskIDs == nil {
		t.TaskIDs = []string{}
	}
	return &t, nil
}

func loadTicket(ctx context.Context, tx *sql.Tx, id string) (*ticketRec, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT data FROM tickets WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFoundf("no ticket %s", id)
	}
	if err != nil {
		return nil, err
	}
	return scanTicket(id, raw)
}

// listTicketRecs lists tickets, newest change first; repoID and issueKey filter when not empty.
func listTicketRecs(ctx context.Context, tx *sql.Tx, repoID, issueKey string) ([]*ticketRec, error) {
	q := `SELECT id, data FROM tickets`
	var args []any
	var where []string
	if repoID != "" {
		where, args = append(where, "repo_id = ?"), append(args, repoID)
	}
	if issueKey != "" {
		where, args = append(where, "issue_key = ?"), append(args, issueKey)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // fixed clauses; the values are parameters
	}
	q += " ORDER BY updated_at DESC, id DESC"
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*ticketRec
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		t, err := scanTicket(id, raw)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func saveTicket(ctx context.Context, tx *sql.Tx, t *ticketRec) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO tickets (id, repo_id, issue_key, status, created_at, updated_at, data)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET status = excluded.status,
		updated_at = excluded.updated_at, data = excluded.data`,
		t.ID, t.RepoID, t.IssueKey, t.Status, api.FormatTime(t.CreatedAt), api.FormatTime(t.UpdatedAt), string(data))
	return err
}

// ---- helpers ------------------------------------------------------------------------------------------------------

// repoID is "r" and 8 hex digits of the repository root's path key.
func repoID(pathKey string) string {
	h := sha1.Sum([]byte(pathKey)) //nolint:gosec // an id
	return "r" + hex.EncodeToString(h[:])[:8]
}

// newTicketID is "k", the time to the second and four random hex digits.
func newTicketID(now time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "k" + now.UTC().Format("060102150405") + "-" + hex.EncodeToString(b[:])
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit])
	}
	return s
}

// clip cuts s to n runes, ending with "…" when it was cut.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// redact removes the GitHub token and anything token-like.
func (s *Service) redact(text string) string { return s.o.GitHub.Redact(text) }

// redactErr is a one-line, redacted error text.
func (s *Service) redactErr(err error, limit int) string {
	if err == nil {
		return ""
	}
	return oneLine(s.redact(err.Error()), limit)
}

// git runs a safe git command and returns its trimmed output; a failure is a redacted *GitError (never the raw
// gitx.Error, whose stderr could name a remote with credentials).
func (s *Service) git(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) (string, error) {
	out, err := s.o.Git.Exec(ctx, dir, opt, args...)
	if err != nil {
		op := "git"
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && !strings.Contains(a, "=") {
				op = a
				break
			}
		}
		if errors.Is(err, gitx.ErrRefused) {
			return "", &GitError{Op: op, Message: oneLine(s.redact(err.Error()), 600)}
		}
		var ge *gitx.Error
		msg := err.Error()
		if errors.As(err, &ge) {
			msg = ge.Error()
		}
		return "", &GitError{Op: op, Message: oneLine(s.redact(msg), 600)}
	}
	return strings.TrimSpace(string(out)), nil
}

// gitOK runs a git read and reports only whether it succeeded, with its output.
func (s *Service) gitOK(ctx context.Context, dir string, args ...string) (string, bool) {
	out, err := s.git(ctx, dir, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, args...)
	return out, err == nil
}

// logStep appends a step to a ticket's log (redacted).
func (s *Service) logStep(ctx context.Context, tx *sql.Tx, ticketID, typ string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	text := s.redact(string(raw))
	if !json.Valid([]byte(text)) {
		text = `{}`
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ticket_log (ticket_id, at, type, data) VALUES (?, ?, ?, ?)`,
		ticketID, api.FormatTime(s.o.Now()), typ, text); err != nil {
		s.log.Warn("ticket: log step", "ticket", ticketID, "err", err)
	}
}

// updateTicket reads the ticket, applies fn and saves it in one transaction; errUnchanged from fn skips the save.
// The stream hears about a saved change.
func (s *Service) updateTicket(ctx context.Context, id string, fn func(tx *sql.Tx, t *ticketRec) error) (*ticketRec,
	error) {
	var out *ticketRec
	saved := false
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		saved = false
		t, err := loadTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := fn(tx, t); err != nil {
			if errors.Is(err, errUnchanged) {
				out = t
				return nil
			}
			return err
		}
		t.UpdatedAt = s.o.Now()
		if err := saveTicket(ctx, tx, t); err != nil {
			return err
		}
		out, saved = t, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if saved {
		s.o.TicketChanged(out.summary())
	}
	return out, nil
}

// getTicket reads one ticket.
func (s *Service) getTicket(ctx context.Context, id string) (*ticketRec, error) {
	var t *ticketRec
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		t, err = loadTicket(ctx, tx, id)
		return err
	})
	return t, err
}

// getRepo reads one repository.
func (s *Service) getRepo(ctx context.Context, id string) (*repoRec, error) {
	var r *repoRec
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = loadRepo(ctx, tx, id)
		return err
	})
	return r, err
}
