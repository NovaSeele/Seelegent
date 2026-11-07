// Package contest runs contests (docs/team.md): one brief, 2..5 variants, each a normal task in its own detached
// git worktree made from a snapshot of the user's working tree (HEAD, uncommitted and untracked files). When every
// variant ended, the machine scores them, an optional read-only judge compares the best three as anonymous letters,
// and a pick applies the winner's diff to the user's working tree with `git apply` (never the index). Worktrees are
// removed without --force after their shared-folder links (package worktree). Contests live in SQLite (old bug D6),
// their step log in team_log.
package contest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/worktree"
)

// Errors; the HTTP API maps them to not_found, invalid_argument, bad_state and conflict.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrBadState = errors.New("bad state")
	ErrConflict = errors.New("conflict")

	errUnchanged = errors.New("unchanged")
)

// Timing and sizes.
const (
	// MaxWait caps a wait on a contest.
	MaxWait     = 1500 * time.Second
	poll        = 5 * time.Second
	maxDiff     = 400_000
	maxPatch    = 64 << 20
	maxTitle    = 200
	notifyBuf   = 1024
	maxFileList = 500
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Tasks is the task engine (task.Service).
type Tasks interface {
	DelegateWith(ctx context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error)
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	Cancel(ctx context.Context, id string, req api.CancelRequest) (api.TaskSummary, bool, error)
	ReviewWith(ctx context.Context, id string, req api.ReviewRequest, o task.ReviewOptions) (api.TaskSummary, error)
	CloseHiddenRun(id, notes string)
	Bus() *task.Bus
}

// Members are the department members (member.Store).
type Members interface {
	Usable(ctx context.Context, id string) (api.Member, error)
	List(ctx context.Context) ([]api.Member, error)
}

// Git is what a contest runs git for (gitx.Repos).
type Git interface {
	worktree.Git
	Root(ctx context.Context, dir string) (string, error)
	SnapshotTree(ctx context.Context, dir, tmpDir string, exclude ...string) (string, error)
	Head(ctx context.Context, dir string) string
	Lock(ctx context.Context, root string) (func(), error)
}

// Options configure a Service. DB, Tasks, Members, Git, Dir and WorktreeBase are required.
type Options struct {
	DB      DB
	Tasks   Tasks
	Members Members
	Git     Git
	// Accounts lists the accounts for the quota plan.
	Accounts func(ctx context.Context) ([]PoolAccount, error)
	Settings func() config.Settings
	// Dir holds the winners' patches and the temporary indexes; WorktreeBase holds the variant worktrees.
	Dir          string
	WorktreeBase string
	// SharedDirs returns the folders linked into a worktree of the repository at root (repository settings,
	// milestone 15); none when nil.
	SharedDirs func(root string) []string
	// Changed is called after a contest changed (contest.updated).
	Changed func(api.ContestSummary)
	Now     func() time.Time
	Logger  *slog.Logger
}

// Service runs contests.
type Service struct {
	o      Options
	log    *slog.Logger
	trees  *worktree.Trees
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	changed  chan struct{}
	watching map[string]bool
	closed   bool
	// busy serializes the git work of one contest (pick, cancel, cleanup).
	busyMu sync.Mutex
	busy   map[string]*sync.Mutex
}

// New returns a service; Start resumes the contests an earlier daemon left going.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Changed == nil {
		o.Changed = func(api.ContestSummary) {}
	}
	if o.Settings == nil {
		o.Settings = config.Defaults
	}
	if o.Accounts == nil {
		o.Accounts = func(context.Context) ([]PoolAccount, error) { return nil, nil }
	}
	if o.SharedDirs == nil {
		o.SharedDirs = func(string) []string { return nil }
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, trees: &worktree.Trees{Git: o.Git, Base: o.WorktreeBase}, ctx: ctx,
		cancel: cancel, changed: make(chan struct{}), watching: map[string]bool{}, busy: map[string]*sync.Mutex{}}
}

// Start fails contests that were starting, and resumes the watchers of the others that are not finished or not
// cleaned up.
func (s *Service) Start(ctx context.Context) error {
	var ids []string
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		ids = nil
		rows, err := tx.QueryContext(ctx, `SELECT id FROM contests WHERE status IN (?, ?, ?, ?)
			OR json_extract(data, '$.cleanedAt') IS NULL`, api.ContestStatusStarting, api.ContestStatusRunning,
			api.ContestStatusJudging, api.ContestStatusReady)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return fmt.Errorf("contest: open contests: %w", err)
	}
	for _, id := range ids {
		_, err := s.update(ctx, id, func(c *api.Contest) error {
			if c.Status != api.ContestStatusStarting {
				return errUnchanged
			}
			c.Status = api.ContestStatusFailed
			c.Error = "the daemon stopped while the contest was starting"
			return nil
		})
		if err != nil && !errors.Is(err, errUnchanged) {
			s.log.Warn("contest: resume", "contest", id, "err", err)
		}
		s.logStep(ctx, id, "reattach", nil)
		s.watch(id)
	}
	s.spawn(s.follow)
	return nil
}

// Close stops the watchers. Variant tasks keep going in the task engine; the next Start picks them up.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *Service) spawn(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("contest: panic", "panic", fmt.Sprint(v))
			}
		}()
		fn()
	}()
}

func (s *Service) lockContest(id string) func() {
	s.busyMu.Lock()
	m := s.busy[id]
	if m == nil {
		m = &sync.Mutex{}
		s.busy[id] = m
	}
	s.busyMu.Unlock()
	m.Lock()
	return m.Unlock
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func badStatef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadState, fmt.Sprintf(format, args...))
}

func (s *Service) now() string { return api.FormatTime(s.o.Now()) }

func newID(now time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "c" + now.UTC().Format("060102150405") + "-" + hex.EncodeToString(b[:])
}

// ---- plan -------------------------------------------------------------------------------------------------------

func (s *Service) memberLookup(ctx context.Context) func(string) (api.Member, bool) {
	cache := map[string]api.Member{}
	return func(id string) (api.Member, bool) {
		if id == "" {
			return api.Member{}, false
		}
		if m, ok := cache[id]; ok {
			return m, true
		}
		m, err := s.o.Members.Usable(ctx, id)
		if err != nil {
			return api.Member{}, false
		}
		cache[id] = m
		return m, true
	}
}

// plan resolves the variant specs of a request and the quota plan.
func (s *Service) plan(ctx context.Context, req *api.ContestRequest) ([]api.VariantSpec, api.ContestEstimate, error) {
	n := req.Variants
	if n == 0 {
		n = len(req.VariantSpecs)
	}
	if n < MinVariants || n > MaxVariants {
		return nil, api.ContestEstimate{}, invalidf("variants must be from %d to %d", MinVariants, MaxVariants)
	}
	if len(req.VariantSpecs) > n {
		return nil, api.ContestEstimate{}, invalidf("variantSpecs has %d entries but variants is %d",
			len(req.VariantSpecs), n)
	}
	st := s.o.Settings()
	for i, sp := range req.VariantSpecs {
		if sp.Backend != "" {
			if _, ok := st.Backends.Backend(sp.Backend); !ok {
				return nil, api.ContestEstimate{}, invalidf("variantSpecs[%d]: unknown backend %q", i, sp.Backend)
			}
		}
		if sp.Member != "" {
			if _, err := s.o.Members.Usable(ctx, sp.Member); err != nil {
				return nil, api.ContestEstimate{}, memberErr(fmt.Sprintf("variantSpecs[%d]", i), err)
			}
		}
	}
	if req.Member != "" {
		if _, err := s.o.Members.Usable(ctx, req.Member); err != nil {
			return nil, api.ContestEstimate{}, memberErr("member", err)
		}
	}
	accounts, err := s.o.Accounts(ctx)
	if err != nil {
		return nil, api.ContestEstimate{}, fmt.Errorf("contest: accounts: %w", err)
	}
	member := s.memberLookup(ctx)
	base := api.VariantSpec{Backend: req.Backend, Model: cmpOr(req.Model, req.Tier), Member: req.Member,
		Provider: req.Provider}
	all := make([]api.VariantSpec, 0, n)
	for _, sp := range req.VariantSpecs {
		all = append(all, api.VariantSpec{Backend: cmpOr(sp.Backend, base.Backend), Model: cmpOr(sp.Model, base.Model),
			Member: cmpOr(sp.Member, base.Member), Provider: cmpOr(sp.Provider, base.Provider)})
	}
	all = append(all, defaultSpecs(n-len(all), base, st, accounts, member)...)
	keep, est, err := quotaPlan(all, len(req.VariantSpecs), st, accounts, member)
	if err != nil {
		return nil, est, err
	}
	out := make([]api.VariantSpec, 0, len(keep))
	for _, i := range keep {
		out = append(out, all[i])
	}
	return out, est, nil
}

// memberErr keeps a member's own conflict (member_disabled) and makes anything else an invalid argument.
func memberErr(what string, err error) error {
	var detailed interface{ APIDetails() map[string]any }
	if errors.As(err, &detailed) {
		return err
	}
	return invalidf("%s: %v", what, err)
}

// Estimate returns the quota plan of a contest request without starting anything.
func (s *Service) Estimate(ctx context.Context, req api.ContestRequest) (api.ContestEstimate, error) {
	_, est, err := s.plan(ctx, &req)
	var qe *QuotaError
	if errors.As(err, &qe) {
		est.Refused = qe.Error()
		return est, nil
	}
	return est, err
}

// ---- create -----------------------------------------------------------------------------------------------------

// Create starts a contest: the base snapshot, one worktree and one task per variant.
func (s *Service) Create(ctx context.Context, req api.ContestRequest) (api.Contest, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return api.Contest{}, invalidf("prompt is required")
	}
	if len(req.After) > 0 {
		return api.Contest{}, invalidf("a contest cannot wait on other tasks (after); start it when they are done")
	}
	if req.Mode == task.ModePlan {
		return api.Contest{}, invalidf("a contest compares changes; mode plan makes none")
	}
	if !filepath.IsAbs(req.Cwd) {
		return api.Contest{}, invalidf("cwd must be an absolute path")
	}
	if st, err := os.Stat(req.Cwd); err != nil || !st.IsDir() {
		return api.Contest{}, invalidf("cwd %s is not a folder", req.Cwd)
	}
	deadline := req.DeadlineMinutes
	if deadline == 0 {
		deadline = DefaultDeadline
	}
	if deadline < 1 || deadline > MaxDeadline {
		return api.Contest{}, invalidf("deadlineMinutes must be from 1 to %d", MaxDeadline)
	}
	judge := strings.TrimSpace(req.Judge)
	if judge != "" && !req.NoJudge {
		m, err := s.o.Members.Usable(ctx, judge)
		if err != nil {
			return api.Contest{}, memberErr("judge", err)
		}
		judge = m.ID
	}
	cwd, err := fsutil.Canonical(req.Cwd)
	if err != nil {
		return api.Contest{}, invalidf("cwd: %v", err)
	}
	root, err := s.o.Git.Root(ctx, cwd)
	if errors.Is(err, gitx.ErrNotRepo) {
		return api.Contest{}, invalidf("a contest needs a git repository (%s is not inside one)", cwd)
	}
	if err != nil {
		return api.Contest{}, err
	}
	sub := ""
	if rel, err := filepath.Rel(root, cwd); err == nil && rel != "." {
		sub = rel
	}
	specs, est, err := s.plan(ctx, &req)
	if err != nil {
		var qe *QuotaError
		if errors.As(err, &qe) {
			return api.Contest{}, fmt.Errorf("%w: %w", ErrConflict, err)
		}
		return api.Contest{}, err
	}
	now := s.o.Now()
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title, _, _ = strings.Cut(prompt, "\n")
		title = clip(title, 80)
	}
	c := api.Contest{ID: newID(now), Title: clip(title, maxTitle), Status: api.ContestStatusStarting, Cwd: cwd,
		Root: root, Sub: sub, Prompt: prompt, Judge: judge, NoJudge: req.NoJudge, DeadlineMinutes: deadline,
		Estimate: &est, Variants: []api.ContestVariant{}, Scoreboard: []api.ScoreRow{}, MachineRanking: []string{},
		Warnings: []string{}, CreatedAt: api.FormatTime(now), UpdatedAt: api.FormatTime(now)}
	if err := s.insert(ctx, &c); err != nil {
		return api.Contest{}, err
	}
	s.logStep(ctx, c.ID, "create", map[string]any{"specs": specs, "estimate": est})
	if err := s.start(ctx, &c, req, specs); err != nil {
		s.mu.Lock()
		c.Status = api.ContestStatusFailed
		c.Error = clip(err.Error(), 600)
		for vi := range c.Variants {
			v := &c.Variants[vi]
			if v.TaskID != "" {
				_, _, _ = s.o.Tasks.Cancel(context.WithoutCancel(ctx), v.TaskID, api.CancelRequest{})
			}
		}
		if serr := s.save(ctx, &c); serr != nil {
			s.log.Warn("contest: save", "contest", c.ID, "err", serr)
		}
		s.mu.Unlock()
		s.logStep(ctx, c.ID, "create_failed", map[string]any{"error": c.Error})
		s.watch(c.ID) // cleans the worktrees once nothing runs
		return api.Contest{}, err
	}
	s.watch(c.ID)
	return s.Get(ctx, c.ID)
}

// start makes the base snapshot, the worktrees and the variant tasks.
func (s *Service) start(ctx context.Context, c *api.Contest, req api.ContestRequest, specs []api.VariantSpec) error {
	tmp := filepath.Join(s.o.Dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	tree, err := s.o.Git.SnapshotTree(ctx, c.Root, tmp)
	if err != nil {
		return fmt.Errorf("snapshot the working tree: %w", err)
	}
	args := append(gitx.Identity(), "commit-tree", tree)
	if head := s.o.Git.Head(ctx, c.Root); head != "" {
		args = append(args, "-p", head)
	}
	args = append(args, "-m", "seelegent contest "+c.ID+" base")
	out, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{}, args...)
	if err != nil {
		return fmt.Errorf("base commit: %w", err)
	}
	c.BaseSha = strings.TrimSpace(string(out))
	shared := s.o.SharedDirs(c.Root)
	for i, sp := range specs {
		v := api.ContestVariant{Label: label(i), Spec: sp}
		wt, err := s.trees.Add(ctx, c.Root, filepath.Join(s.o.WorktreeBase, c.ID, v.Label), c.BaseSha)
		if err != nil {
			return fmt.Errorf("worktree %s: %w", v.Label, err)
		}
		v.Worktree = wt
		v.Cwd = wt
		if c.Sub != "" {
			v.Cwd = filepath.Join(wt, c.Sub)
		}
		env := s.trees.Setup(ctx, c.Root, wt, shared)
		v.Env = &env
		c.Variants = append(c.Variants, v)
	}
	if err := s.saveLocked(ctx, c); err != nil {
		return err
	}
	for i := range c.Variants {
		v := &c.Variants[i]
		r := req.DelegateRequest
		r.Prompt = c.Prompt
		r.Cwd = v.Cwd
		r.Title = "[" + v.Label + "] " + c.Title
		r.Backend, r.Member, r.Provider = v.Spec.Backend, v.Spec.Member, v.Spec.Provider
		r.Model, r.Tier = "", ""
		if isTier(v.Spec.Model) {
			r.Tier = v.Spec.Model
		} else {
			r.Model = v.Spec.Model
		}
		if r.Spec != nil {
			sp := *r.Spec
			sp.Dir = mapInto(sp.Dir, c.Root, v.Worktree)
			r.Spec = &sp
		}
		sum, err := s.o.Tasks.DelegateWith(ctx, r, task.Spawn{ContestID: c.ID, Variant: v.Label, ProjectCwd: c.Cwd})
		if err != nil {
			return fmt.Errorf("variant %s: %w", v.Label, err)
		}
		v.TaskID = sum.ID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.Status = api.ContestStatusRunning
	c.DeadlineAt = api.FormatTime(s.o.Now().Add(time.Duration(c.DeadlineMinutes) * time.Minute))
	if err := s.save(ctx, c); err != nil {
		return err
	}
	labels := make([]map[string]any, 0, len(c.Variants))
	for vi := range c.Variants {
		v := &c.Variants[vi]
		labels = append(labels, map[string]any{"label": v.Label, "taskId": v.TaskID, "spec": v.Spec, "env": v.Env})
	}
	s.logStep(ctx, c.ID, "started", map[string]any{"base": c.BaseSha, "variants": labels})
	return nil
}

func (s *Service) saveLocked(ctx context.Context, c *api.Contest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(ctx, c)
}

// mapInto moves an absolute path inside the repository at root to the same place in the worktree wt; a relative
// path (relative to cwd, which the worktree mirrors) or a path outside the repository stays.
func mapInto(p, root, wt string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	c, err := fsutil.Canonical(p)
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(root, c)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return p
	}
	return filepath.Join(wt, rel)
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// ---- storage ----------------------------------------------------------------------------------------------------

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func decode(data string) (api.Contest, error) {
	var c api.Contest
	if err := json.Unmarshal([]byte(data), &c); err != nil {
		return api.Contest{}, fmt.Errorf("contest record: %w", err)
	}
	return c, nil
}

func (s *Service) load(ctx context.Context, id string) (api.Contest, error) {
	var data string
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT data FROM contests WHERE id = ?`, id).Scan(&data)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.Contest{}, fmt.Errorf("%w: no contest %s", ErrNotFound, id)
	}
	if err != nil {
		return api.Contest{}, err
	}
	return decode(data)
}

func (s *Service) insert(ctx context.Context, c *api.Contest) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO contests (id, status, created_at, updated_at, data)
			VALUES (?, ?, ?, ?, ?)`, c.ID, c.Status, c.CreatedAt, c.UpdatedAt, string(data))
		return err
	})
}

// save stores a contest and tells waiters and the stream. The caller holds mu.
func (s *Service) save(ctx context.Context, c *api.Contest) error {
	c.UpdatedAt = s.now()
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE contests SET status = ?, updated_at = ?, data = ? WHERE id = ?`,
			c.Status, c.UpdatedAt, string(data), c.ID)
		return err
	}); err != nil {
		return err
	}
	close(s.changed)
	s.changed = make(chan struct{})
	s.o.Changed(Summary(c))
	return nil
}

// update loads a contest, applies fn and saves it, under mu.
func (s *Service) update(ctx context.Context, id string, fn func(c *api.Contest) error) (api.Contest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(ctx, id)
	if err != nil {
		return api.Contest{}, err
	}
	if err := fn(&c); err != nil {
		return api.Contest{}, err
	}
	if err := s.save(ctx, &c); err != nil {
		return api.Contest{}, err
	}
	return c, nil
}

func (s *Service) logStep(ctx context.Context, id, typ string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err == nil {
		ctx = context.WithoutCancel(ctx)
		err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO team_log (scope, ref, at, type, data) VALUES ('contest', ?, ?, ?, ?)`,
				id, s.o.Now().UTC().Format(timeFormat), typ, string(raw))
			return err
		})
	}
	if err != nil {
		s.log.Warn("contest: step log", "contest", id, "err", err)
	}
}

// Events returns the step log of a contest, oldest first.
func (s *Service) Events(ctx context.Context, id string) ([]api.TeamLogEntry, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	out := []api.TeamLogEntry{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `SELECT id, at, type, data FROM team_log WHERE scope = 'contest' AND ref = ?
			ORDER BY id`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e api.TeamLogEntry
			var data string
			if err := rows.Scan(&e.ID, &e.At, &e.Type, &data); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(data), &e.Data); err != nil {
				return fmt.Errorf("team log %d: %w", e.ID, err)
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// ---- queries ----------------------------------------------------------------------------------------------------

// Summary is the list view of a contest.
func Summary(c *api.Contest) api.ContestSummary {
	sum := api.ContestSummary{ID: c.ID, Title: c.Title, Status: c.Status, Cwd: c.Cwd, Variants: len(c.Variants),
		Winner: c.Winner, DeadlineAt: c.DeadlineAt, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.Proposal != nil {
		sum.Proposal = c.Proposal.Winner
	}
	return sum
}

// Get returns a contest with each variant's task status, backend, model and member.
func (s *Service) Get(ctx context.Context, id string) (api.Contest, error) {
	c, err := s.load(ctx, id)
	if err != nil {
		return api.Contest{}, err
	}
	for i := range c.Variants {
		v := &c.Variants[i]
		if v.TaskID == "" {
			continue
		}
		d, err := s.o.Tasks.Get(ctx, v.TaskID)
		if err != nil {
			v.Status = "missing"
			continue
		}
		v.Status, v.Backend, v.Model, v.Member = string(d.Status), d.Backend, d.Model, d.Member
	}
	return c, nil
}

// List returns the newest contests first (limit 1..200, 50 when 0).
func (s *Service) List(ctx context.Context, limit int) ([]api.ContestSummary, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, invalidf("limit must be from 1 to 200")
	}
	out := []api.ContestSummary{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `SELECT data FROM contests ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			c, err := decode(data)
			if err != nil {
				return err
			}
			out = append(out, Summary(&c))
		}
		return rows.Err()
	})
	return out, err
}

// waitable reports whether a contest needs the lead or has ended.
func waitable(status string) bool {
	return status != api.ContestStatusStarting && status != api.ContestStatusRunning &&
		status != api.ContestStatusJudging
}

// Wait blocks up to d (capped at MaxWait) until the contest is ready or ended, and returns it.
func (s *Service) Wait(ctx context.Context, id string, d time.Duration) (api.Contest, error) {
	d = min(d, MaxWait)
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		s.mu.Lock()
		ch := s.changed
		s.mu.Unlock()
		c, err := s.Get(ctx, id)
		if err != nil || waitable(c.Status) || d <= 0 {
			return c, err
		}
		select {
		case <-ch:
		case <-timer.C:
			return s.Get(ctx, id)
		case <-ctx.Done():
			return c, nil
		case <-s.ctx.Done():
			return c, nil
		}
	}
}
