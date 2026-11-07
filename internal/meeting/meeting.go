// Package meeting runs structured meetings of department members before a decision (docs/team.md): round 1 is
// blind, from round 2 every attendee sees the others under neutral labels and answers the strongest opposing
// argument; claims cite path:line and every citation is checked. The lead answers questions between rounds,
// decides, peers that disagree dissent formally (their dissent watches the files it cites for 30 days), and the
// decision turns into delegated tasks. Each attendee is one hidden read-only run (task kind meeting); each round
// is a turn of it. Meetings live in SQLite (old bug D6), their step log in team_log.
package meeting

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
	mrand "math/rand/v2"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Errors; the HTTP API maps them to not_found, invalid_argument and bad_state.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrBadState = errors.New("bad state")

	// errUnchanged ends an update without saving.
	errUnchanged = errors.New("unchanged")
)

// Limits.
const (
	maxAttendees  = 6
	maxOptions    = 6
	maxContext    = 20
	maxText       = 8000
	maxRoundsCap  = 5
	defaultRounds = 3
	maxActItems   = 20
	maxQPerMember = 5
	// MaxWait caps a wait on a meeting.
	MaxWait = 1500 * time.Second
	// watchDays is how long a dissent watches its files.
	watchDays = 30
	poll      = 5 * time.Second
)

const optionKeys = "ABCDEF"

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Tasks is the task engine (task.Service).
type Tasks interface {
	DelegateWith(ctx context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error)
	Followup(ctx context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error)
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	Cancel(ctx context.Context, id string, req api.CancelRequest) (api.TaskSummary, bool, error)
	CloseHiddenRun(id, notes string)
	Bus() *task.Bus
}

// Members are the department members (member.Store).
type Members interface {
	Usable(ctx context.Context, id string) (api.Member, error)
}

// Options configure a Service. DB, Tasks and Members are required.
type Options struct {
	DB      DB
	Tasks   Tasks
	Members Members
	// Root returns the repository root of a folder (gitx.Repos.Root); the folder itself is used when nil or when
	// it fails.
	Root func(ctx context.Context, dir string) (string, error)
	// ConsultStats adds the consult counts to the members' track records (task.Service.ConsultStats); optional.
	ConsultStats func(ctx context.Context) (map[string]api.MemberTrack, error)
	// Changed is called after a meeting changed (meeting.updated).
	Changed func(api.MeetingSummary)
	Now     func() time.Time
	Logger  *slog.Logger
}

// Service runs meetings. Every change to a meeting happens under mu.
type Service struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
	// changed is closed and replaced at every save (waiters).
	changed  chan struct{}
	watching map[string]bool
	closed   bool
	// watchedRoots is the dissent memory index: open dissents with watched files by repository; nil when it must
	// be rebuilt.
	watched []watchEntry
	dirty   bool
}

// New returns a service; Start resumes the meetings an earlier daemon left running.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	if o.Changed == nil {
		o.Changed = func(api.MeetingSummary) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, ctx: ctx, cancel: cancel, changed: make(chan struct{}),
		watching: map[string]bool{}, dirty: true}
}

// Start resumes the watchers of running meetings and starts the dissent memory.
func (s *Service) Start(ctx context.Context) error {
	var ids []string
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		ids = nil
		rows, err := tx.QueryContext(ctx, `SELECT id FROM meetings WHERE status IN (?, ?)`,
			api.MeetingStatusRound, api.MeetingStatusDissent)
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
		return fmt.Errorf("meeting: running meetings: %w", err)
	}
	for _, id := range ids {
		s.logStep(ctx, id, "reattach", nil)
		s.watch(id)
	}
	s.spawn(s.memory)
	return nil
}

// Close stops the watchers. Attendee runs keep going in the task engine; the next Start picks them up.
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
				s.log.Error("meeting: panic", "panic", fmt.Sprint(v))
			}
		}()
		fn()
	}()
}

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func badStatef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadState, fmt.Sprintf(format, args...))
}

func (s *Service) now() string { return api.FormatTime(s.o.Now()) }

// ---- open -------------------------------------------------------------------------------------------------------

// resolveAttendees checks the attendee list and looks up the members.
func (s *Service) resolveAttendees(ctx context.Context, refs []api.AttendeeRef) ([]api.Attendee, error) {
	if len(refs) == 0 {
		return nil, invalidf("at least one attendee is required")
	}
	if len(refs) > maxAttendees {
		return nil, invalidf("at most %d attendees", maxAttendees)
	}
	var out []api.Attendee
	seen := map[string]bool{}
	for _, r := range refs {
		id := strings.TrimSpace(r.Member)
		if id == "" {
			return nil, invalidf("an attendee is a member id or {member, rank}")
		}
		if seen[id] {
			return nil, invalidf("attendee %q is listed twice", id)
		}
		seen[id] = true
		m, err := s.o.Members.Usable(ctx, id)
		if err != nil {
			var detailed interface{ APIDetails() map[string]any }
			if errors.As(err, &detailed) {
				return nil, err
			}
			return nil, invalidf("attendee %q: %v", id, err)
		}
		rank := strings.TrimSpace(r.Rank)
		if rank == "" {
			rank = m.Rank
		}
		if rank != api.RankWorker && rank != api.RankSenior && rank != api.RankPeer {
			return nil, invalidf("rank %q of %q must be worker, senior or peer", rank, id)
		}
		out = append(out, api.Attendee{Member: m.ID, Name: m.Name, Rank: rank, Backend: m.Backend, Model: m.Model,
			Tier: m.Tier, Provider: m.Provider, Present: true})
	}
	return out, nil
}

func maxRounds(n int) (int, error) {
	if n == 0 {
		return defaultRounds, nil
	}
	if n < 1 || n > maxRoundsCap {
		return 0, invalidf("maxRounds must be 1..%d", maxRoundsCap)
	}
	return n, nil
}

func estimate(as []api.Attendee, rounds int) api.RunEstimate {
	e := api.RunEstimate{ByBackend: map[string]int{}}
	for i := range as {
		k := rounds
		if as[i].Rank == api.RankPeer {
			k++ // a possible dissent turn
		}
		e.Runs += k
		e.ByBackend[as[i].Backend] += k
	}
	return e
}

// Estimate returns how many worker runs a meeting would take at most.
func (s *Service) Estimate(ctx context.Context, req api.MeetingRequest) (api.RunEstimate, error) {
	rounds, err := maxRounds(req.MaxRounds)
	if err != nil {
		return api.RunEstimate{}, err
	}
	as, err := s.resolveAttendees(ctx, req.Attendees)
	if err != nil {
		return api.RunEstimate{}, err
	}
	return estimate(as, rounds), nil
}

func (s *Service) rootOf(ctx context.Context, dir string) string {
	if s.o.Root != nil {
		if r, err := s.o.Root(ctx, dir); err == nil && r != "" {
			if c, err := fsutil.Canonical(r); err == nil {
				return c
			}
			return r
		}
	}
	return dir
}

func newMeetingID(now time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "m" + now.UTC().Format("060102150405") + "-" + hex.EncodeToString(b[:])
}

// Open starts a meeting: it checks the request, labels the attendees in a random order and starts round 1.
func (s *Service) Open(ctx context.Context, req api.MeetingRequest) (api.Meeting, error) {
	question := strings.TrimSpace(req.Question)
	if question == "" {
		return api.Meeting{}, invalidf("question is required")
	}
	if utf8.RuneCountInString(question) > maxText {
		return api.Meeting{}, invalidf("question is longer than %d characters", maxText)
	}
	rounds, err := maxRounds(req.MaxRounds)
	if err != nil {
		return api.Meeting{}, err
	}
	attendees, err := s.resolveAttendees(ctx, req.Attendees)
	if err != nil {
		return api.Meeting{}, err
	}
	if len(req.Options) == 1 || len(req.Options) > maxOptions {
		return api.Meeting{}, invalidf("give 0 or 2..%d options", maxOptions)
	}
	options := []api.MeetingOption{}
	for i, o := range req.Options {
		o = strings.TrimSpace(o)
		if o == "" {
			return api.Meeting{}, invalidf("option %c is empty", optionKeys[i])
		}
		options = append(options, api.MeetingOption{Key: string(optionKeys[i]), Text: answer.Clip(o, 2000)})
	}
	if strings.TrimSpace(req.Cwd) == "" {
		return api.Meeting{}, invalidf("cwd is required")
	}
	cwd, _, _, err := fsutil.ProjectOf(req.Cwd)
	if err != nil {
		return api.Meeting{}, invalidf("%v", err)
	}
	root := s.rootOf(ctx, cwd)
	files := []string{}
	if len(req.Context) > maxContext {
		return api.Meeting{}, invalidf("at most %d context paths", maxContext)
	}
	for _, p := range req.Context {
		rel, err := relInRepo(root, p)
		if err != nil {
			return api.Meeting{}, err
		}
		if !containsKey(files, rel) {
			files = append(files, rel)
		}
	}
	sealed := strings.TrimSpace(req.Sealed)
	if utf8.RuneCountInString(sealed) > maxText {
		return api.Meeting{}, invalidf("the sealed position is longer than %d characters", maxText)
	}
	n := len(attendees)
	if sealed != "" {
		n++
	}
	labels := make([]string, n)
	for i := range labels {
		labels[i] = fmt.Sprintf("P%d", i+1)
	}
	//nolint:gosec // the labels only hide the order of the list from the attendees; nothing secret depends on them
	mrand.Shuffle(n, func(i, j int) { labels[i], labels[j] = labels[j], labels[i] })
	for i := range attendees {
		attendees[i].Label = labels[i]
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = answer.Clip(answer.Collapse(question), 60)
	}
	title = answer.Clip(title, 120)
	now := s.o.Now()
	est := estimate(attendees, rounds)
	m := api.Meeting{ID: newMeetingID(now), Title: title, Question: question, Options: options, Context: files,
		Cwd: cwd, RepoRoot: root, Status: api.MeetingStatusRound, MaxRounds: rounds, Attendees: attendees,
		Rounds: []api.MeetingRound{}, LeadQA: []api.LeadQuestion{}, LeadNotes: []api.LeadNote{},
		Dissents: []api.Dissent{}, AdviceNotTaken: []api.AdviceNotTaken{}, Actions: []api.MeetingAction{},
		DissentRuns: []api.MeetingRun{}, Estimate: &est,
		Usage:     api.MeetingUsage{ByBackend: map[string]int{}},
		CreatedAt: api.FormatTime(now), UpdatedAt: api.FormatTime(now)}
	if sealed != "" {
		sum := sha256.Sum256([]byte(sealed))
		m.Sealed = &api.SealedPosition{Label: labels[n-1], Text: sealed, Hash: hex.EncodeToString(sum[:]),
			RevealedRound: 2}
	}
	s.mu.Lock()
	if err := s.insert(ctx, &m); err != nil {
		s.mu.Unlock()
		return api.Meeting{}, err
	}
	var who []map[string]any
	for i := range attendees {
		a := &attendees[i]
		who = append(who, map[string]any{"member": a.Member, "rank": a.Rank, "label": a.Label})
	}
	s.logStep(ctx, m.ID, "open", map[string]any{"title": title, "attendees": who, "sealed": m.Sealed != nil})
	s.startRound(ctx, &m, 1)
	err = s.save(ctx, &m)
	s.mu.Unlock()
	if err != nil {
		return api.Meeting{}, err
	}
	s.watch(m.ID)
	return m, nil
}

// relInRepo makes a context path relative to root ('/'); it must stay inside.
func relInRepo(root, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", invalidf("context paths must not be empty")
	}
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	if c, err := fsutil.Canonical(abs); err == nil {
		abs = c
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(rel) {
		return "", invalidf("context path outside the repository: %s", p)
	}
	return filepath.ToSlash(rel), nil
}

// relKey compares repository-relative paths the way the file system does.
func relKey(p string) string {
	p = filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		p = strings.ToLower(p)
	}
	return p
}

func containsKey(list []string, p string) bool {
	for _, x := range list {
		if relKey(x) == relKey(p) {
			return true
		}
	}
	return false
}

// ---- rounds -----------------------------------------------------------------------------------------------------

// startRound opens round n: attendees without a run get one, the others a followup. A run that cannot start makes
// the attendee absent for the round. The caller holds mu and saves.
func (s *Service) startRound(ctx context.Context, m *api.Meeting, n int) {
	r := api.MeetingRound{N: n, StartedAt: s.now(), Runs: []api.MeetingRun{}, Answers: []api.MeetingAnswer{}}
	order := sortedAttendees(m)
	m.Round, m.Status = n, api.MeetingStatusRound
	m.Rounds = append(m.Rounds, r)
	cur := &m.Rounds[len(m.Rounds)-1]
	for _, i := range order {
		a := &m.Attendees[i]
		var sum api.TaskSummary
		var err error
		if a.TaskID == "" {
			brief := roundBrief(m, a)
			if n > 1 {
				brief += "\n\n" + roundMessage(m, a, n)
			}
			sum, err = s.o.Tasks.DelegateWith(ctx, api.DelegateRequest{Prompt: brief, Cwd: m.Cwd,
				Title: "Meeting: " + m.Title, Member: a.Member, Mode: task.ModePlan},
				task.Spawn{Kind: task.KindMeeting, ParentID: m.ID, MeetingID: m.ID})
			if err == nil {
				a.TaskID, a.Backend, a.Model, a.Provider = sum.ID, sum.Backend, sum.Model, sum.Provider
			}
		} else {
			sum, err = s.o.Tasks.Followup(ctx, a.TaskID, api.FollowupRequest{Message: roundMessage(m, a, n)})
		}
		if err != nil {
			cur.Answers = append(cur.Answers, absent(a.Label, err.Error()))
			continue
		}
		cur.Runs = append(cur.Runs, api.MeetingRun{Label: a.Label, TaskID: sum.ID, Turn: sum.TurnCount})
		m.Usage.Runs++
		m.Usage.ByBackend[a.Backend]++
	}
	runs := map[string]string{}
	for _, x := range cur.Runs {
		runs[x.Label] = x.TaskID
	}
	s.logStep(ctx, m.ID, "round_start", map[string]any{"round": n, "runs": runs})
}

func absent(label, why string) api.MeetingAnswer {
	return api.MeetingAnswer{Label: label, Absent: true, Error: answer.Clip(why, 500), Arguments: []string{},
		Claims: []api.Claim{}, Risks: []string{}, Actions: []string{}, Questions: []string{}}
}

// sortedAttendees returns the attendee indexes by label.
func sortedAttendees(m *api.Meeting) []int {
	idx := make([]int, len(m.Attendees))
	for i := range idx {
		idx[i] = i
	}
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && labelNum(m.Attendees[idx[j]].Label) < labelNum(m.Attendees[idx[j-1]].Label); j-- {
			idx[j], idx[j-1] = idx[j-1], idx[j]
		}
	}
	return idx
}

// runState is what a run's turn came to.
type runState struct {
	pending  bool
	response string
	files    []string
	err      string
}

func (s *Service) readRun(ctx context.Context, run api.MeetingRun) runState {
	d, err := s.o.Tasks.Get(ctx, run.TaskID)
	if err != nil {
		if ctx.Err() != nil {
			return runState{pending: true}
		}
		return runState{err: "the run is gone"}
	}
	if task.Pending(d.Status) {
		return runState{pending: true}
	}
	if run.Turn < 1 || run.Turn > len(d.Turns) {
		return runState{err: "the run has no such turn"}
	}
	tu := d.Turns[run.Turn-1]
	switch {
	case tu.Status == task.TurnRunning:
		return runState{pending: true}
	case d.Status == api.StatusCancelled || tu.Status == task.TurnCancelled:
		return runState{err: "the run was cancelled"}
	case tu.Status != task.TurnOK || strings.TrimSpace(tu.Response) == "":
		why := "the run ended " + string(d.Status) + " without an answer"
		if d.Error != "" {
			why += ": " + d.Error
		}
		return runState{err: why}
	}
	return runState{response: tu.Response, files: tu.Files}
}

// collectRound reads the answers of the finished runs of the current round. It reports whether every run has
// answered.
func (s *Service) collectRound(ctx context.Context, m *api.Meeting) bool {
	r := &m.Rounds[len(m.Rounds)-1]
	keys := make([]string, 0, len(m.Options))
	for _, o := range m.Options {
		keys = append(keys, o.Key)
	}
	done := true
	for _, run := range r.Runs {
		if answerOf(r, run.Label) != nil {
			continue
		}
		st := s.readRun(ctx, run)
		switch {
		case st.pending:
			done = false
		case st.err != "":
			r.Answers = append(r.Answers, absent(run.Label, st.err))
		default:
			a := parseAnswer(st.response, keys)
			a.Label = run.Label
			a.Claims = answer.CheckClaims(m.RepoRoot, a.claims)
			if len(st.files) > 0 {
				a.EditedFiles = st.files[:min(len(st.files), 50)]
				s.logStep(ctx, m.ID, "attendee_edited", map[string]any{"label": run.Label, "files": a.EditedFiles})
			}
			r.Answers = append(r.Answers, a.MeetingAnswer)
		}
	}
	return done
}

// stable reports whether label held its position from the round before: the same option (or "moved: no" without
// options) and a confidence within 10 points.
func stable(m *api.Meeting, label string, n int) bool {
	if n < 2 {
		return false
	}
	cur, prev := answerOf(&m.Rounds[n-1], label), answerOf(&m.Rounds[n-2], label)
	if cur == nil || cur.Absent || prev == nil || prev.Absent {
		return false
	}
	same := cur.Moved == "no"
	if len(m.Options) > 0 {
		same = cur.Option == prev.Option
	}
	switch {
	case !same:
		return false
	case cur.Confidence == nil && prev.Confidence == nil:
		return true
	case cur.Confidence == nil || prev.Confidence == nil:
		return false
	}
	d := *cur.Confidence - *prev.Confidence
	return d > -10 && d < 10
}

// afterRound moves the meeting on once every run of the round answered: questions for the lead, a decision, or
// the next round.
func (s *Service) afterRound(ctx context.Context, m *api.Meeting) {
	r := &m.Rounds[len(m.Rounds)-1]
	r.EndedAt = s.now()
	var present []string
	for i := range m.Attendees {
		a := &m.Attendees[i]
		if ans := answerOf(r, a.Label); ans != nil {
			a.Present = !ans.Absent
		}
		if a.Present && answerOf(r, a.Label) != nil {
			present = append(present, a.Label)
		}
	}
	s.refreshTokens(ctx, m)
	s.logStep(ctx, m.ID, "round_end", map[string]any{"round": r.N, "present": present})
	if r.N == 1 && len(present) == 0 {
		m.Status, m.Error = api.MeetingStatusFailed, "every attendee was absent in round 1"
		s.logStep(ctx, m.ID, "failed", map[string]any{"message": m.Error})
		s.closeRuns(m)
		return
	}
	added := 0
	for _, i := range sortedAttendees(m) {
		a := m.Attendees[i]
		ans := answerOf(r, a.Label)
		if ans == nil || ans.Absent {
			continue
		}
		for k, q := range ans.Questions {
			if k == maxQPerMember {
				break
			}
			m.LeadQA = append(m.LeadQA, api.LeadQuestion{ID: fmt.Sprintf("q%d", len(m.LeadQA)+1), Round: r.N,
				Label: a.Label, Question: q})
			added++
		}
	}
	if added > 0 {
		m.Status = api.MeetingStatusWaitingLead
		s.logStep(ctx, m.ID, "waiting_lead", map[string]any{"questions": added})
		return
	}
	allStable := true
	for _, l := range present {
		if !stable(m, l, r.N) {
			allStable = false
		}
	}
	switch {
	case r.N >= m.MaxRounds:
		m.Status = api.MeetingStatusDeciding
		s.logStep(ctx, m.ID, "deciding", map[string]any{"reason": "max_rounds"})
	case r.N >= 2 && allStable:
		m.Status = api.MeetingStatusDeciding
		s.logStep(ctx, m.ID, "deciding", map[string]any{"reason": "stable"})
	default:
		s.startRound(ctx, m, m.Round+1)
	}
}

// refreshTokens sums the tokens of the attendee runs.
func (s *Service) refreshTokens(ctx context.Context, m *api.Meeting) {
	var tokens int64
	for i := range m.Attendees {
		a := &m.Attendees[i]
		if a.TaskID == "" {
			continue
		}
		if d, err := s.o.Tasks.Get(ctx, a.TaskID); err == nil {
			tokens += d.TokensUsed
		}
	}
	m.Usage.Tokens = tokens
}

// closeRuns marks the attendee runs as handled, so they never wait for a review.
func (s *Service) closeRuns(m *api.Meeting) {
	for i := range m.Attendees {
		if a := &m.Attendees[i]; a.TaskID != "" {
			s.o.Tasks.CloseHiddenRun(a.TaskID, "meeting "+m.Status)
		}
	}
}

// ---- watchers ---------------------------------------------------------------------------------------------------

// watch follows the runs of a meeting in round or dissent until it needs the lead or ends.
func (s *Service) watch(id string) {
	s.mu.Lock()
	if s.watching[id] || s.closed {
		s.mu.Unlock()
		return
	}
	s.watching[id] = true
	s.mu.Unlock()
	s.spawn(func() {
		defer func() {
			s.mu.Lock()
			delete(s.watching, id)
			s.mu.Unlock()
		}()
		for s.ctx.Err() == nil {
			pending, more := s.step(id)
			if !more {
				return
			}
			changed, release := s.o.Tasks.Bus().TasksChanged(pending)
			timer := time.NewTimer(poll)
			select {
			case <-changed:
			case <-timer.C:
			case <-s.ctx.Done():
			}
			timer.Stop()
			release()
		}
	})
}

// step advances a meeting as far as its runs allow. It returns the runs still going and whether to keep watching.
func (s *Service) step(id string) (pending []string, more bool) {
	ctx := s.ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			s.log.Warn("meeting: load", "meeting", id, "err", err)
			return nil, true
		}
		return nil, false
	}
	switch m.Status {
	case api.MeetingStatusRound:
		if !s.collectRound(ctx, &m) {
			r := m.Rounds[len(m.Rounds)-1]
			for _, run := range r.Runs {
				if answerOf(&r, run.Label) == nil {
					pending = append(pending, run.TaskID)
				}
			}
			if err := s.save(ctx, &m); err != nil {
				s.log.Warn("meeting: save", "meeting", id, "err", err)
			}
			return pending, true
		}
		s.afterRound(ctx, &m)
	case api.MeetingStatusDissent:
		if !s.collectDissents(ctx, &m) {
			for _, run := range m.DissentRuns {
				if !run.Done {
					pending = append(pending, run.TaskID)
				}
			}
			return pending, true
		}
		s.closeMeeting(ctx, &m)
	default:
		return nil, false
	}
	if err := s.save(ctx, &m); err != nil {
		s.log.Warn("meeting: save", "meeting", id, "err", err)
		return nil, true
	}
	return nil, m.Status == api.MeetingStatusRound || m.Status == api.MeetingStatusDissent
}

// ---- lead actions -----------------------------------------------------------------------------------------------

// update loads a meeting, applies fn and saves it, under mu.
func (s *Service) update(ctx context.Context, id string, fn func(m *api.Meeting) error) (api.Meeting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.load(ctx, id)
	if err != nil {
		return api.Meeting{}, err
	}
	if err := fn(&m); err != nil {
		return api.Meeting{}, err
	}
	if err := s.save(ctx, &m); err != nil {
		return api.Meeting{}, err
	}
	return m, nil
}

// Post answers the attendees' questions (and adds a note for the next round), then starts the next round or, after
// the last one, asks for the decision.
func (s *Service) Post(ctx context.Context, id string, req api.MeetingPostRequest) (api.Meeting, error) {
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > maxText {
		return api.Meeting{}, invalidf("note is longer than %d characters", maxText)
	}
	m, err := s.update(ctx, id, func(m *api.Meeting) error {
		if m.Status != api.MeetingStatusWaitingLead {
			return badStatef("meeting %s is %s, not waiting for the lead", id, m.Status)
		}
		type upd struct {
			i    int
			text string
		}
		var ups []upd
		for _, a := range req.Answers {
			qid := strings.TrimSpace(a.ID)
			i := -1
			for k := range m.LeadQA {
				if m.LeadQA[k].ID == qid {
					i = k
				}
			}
			if i < 0 {
				return invalidf("unknown question %q", qid)
			}
			text := strings.TrimSpace(a.Answer)
			if text == "" {
				return invalidf("the answer to %s is empty", qid)
			}
			if utf8.RuneCountInString(text) > maxText {
				return invalidf("the answer to %s is longer than %d characters", qid, maxText)
			}
			ups = append(ups, upd{i, text})
		}
		at := s.now()
		var ids []string
		for _, u := range ups {
			m.LeadQA[u.i].Answer, m.LeadQA[u.i].AnsweredAt = u.text, at
			ids = append(ids, m.LeadQA[u.i].ID)
		}
		if note != "" {
			m.LeadNotes = append(m.LeadNotes, api.LeadNote{Round: m.Round + 1, Text: note, At: at})
		}
		s.logStep(ctx, id, "post", map[string]any{"answers": ids, "note": note != ""})
		if m.Round < m.MaxRounds {
			s.startRound(ctx, m, m.Round+1)
		} else {
			m.Status = api.MeetingStatusDeciding
			s.logStep(ctx, id, "deciding", map[string]any{"reason": "max_rounds"})
		}
		return nil
	})
	if err == nil && m.Status == api.MeetingStatusRound {
		s.watch(id)
	}
	return m, err
}

// Decide records the lead's decision. Seniors whose final stance differs are recorded as advice not taken; peers
// whose stance differs are asked to dissent or concede (status dissent); otherwise the meeting closes.
func (s *Service) Decide(ctx context.Context, id string, req api.MeetingDecideRequest) (api.Meeting, error) {
	text := strings.TrimSpace(req.Decision)
	if text == "" {
		return api.Meeting{}, invalidf("decision is required")
	}
	if utf8.RuneCountInString(text) > maxText {
		return api.Meeting{}, invalidf("decision is longer than %d characters", maxText)
	}
	m, err := s.update(ctx, id, func(m *api.Meeting) error {
		if m.Status != api.MeetingStatusDeciding {
			return badStatef("meeting %s is %s, not deciding", id, m.Status)
		}
		opt := strings.ToUpper(strings.TrimSpace(req.Option))
		if opt != "" && optionText(m, opt) == "" {
			return invalidf("option %q is not one of the meeting's options", req.Option)
		}
		m.Decision = &api.MeetingDecision{Text: text, Rationale: strings.TrimSpace(req.Rationale), Option: opt,
			At: s.now()}
		m.DissentRuns, m.AdviceNotTaken = []api.MeetingRun{}, []api.AdviceNotTaken{}
		for _, i := range sortedAttendees(m) {
			a := m.Attendees[i]
			if !a.Present || a.Rank == api.RankWorker || a.TaskID == "" {
				continue
			}
			final, _ := lastAnswer(m, a.Label, len(m.Rounds)+1)
			differs := true
			if len(m.Options) > 0 {
				differs = final == nil || final.Option != opt
			}
			if !differs {
				continue
			}
			if a.Rank == api.RankSenior {
				m.AdviceNotTaken = append(m.AdviceNotTaken, api.AdviceNotTaken{Label: a.Label, Member: a.Member,
					Stance: stanceSummary(final)})
				continue
			}
			sum, err := s.o.Tasks.Followup(ctx, a.TaskID, api.FollowupRequest{Message: dissentMessage(m, final)})
			if err != nil {
				s.recordDissent(m, a, "", err.Error())
				continue
			}
			m.DissentRuns = append(m.DissentRuns, api.MeetingRun{Label: a.Label, TaskID: sum.ID, Turn: sum.TurnCount})
			m.Usage.Runs++
			m.Usage.ByBackend[a.Backend]++
		}
		var dl, al []string
		for _, r := range m.DissentRuns {
			dl = append(dl, r.Label)
		}
		for _, x := range m.AdviceNotTaken {
			al = append(al, x.Label)
		}
		s.logStep(ctx, id, "decide", map[string]any{"option": opt, "dissentTurns": dl, "adviceNotTaken": al})
		if len(m.DissentRuns) == 0 {
			s.closeMeeting(ctx, m)
		} else {
			m.Status = api.MeetingStatusDissent
		}
		return nil
	})
	if err == nil && m.Status == api.MeetingStatusDissent {
		s.watch(id)
	}
	return m, err
}

func stanceSummary(a *api.MeetingAnswer) string {
	if a == nil {
		return "(no answer)"
	}
	s := answer.Clip(answer.Collapse(a.Stance), 600)
	if a.Option != "" {
		s = a.Option + ": " + s
	}
	return s
}

// collectDissents reads the finished dissent turns; it reports whether all are in.
func (s *Service) collectDissents(ctx context.Context, m *api.Meeting) bool {
	done := true
	for i := range m.DissentRuns {
		run := &m.DissentRuns[i]
		if run.Done {
			continue
		}
		st := s.readRun(ctx, *run)
		if st.pending {
			done = false
			continue
		}
		for i := range m.Attendees {
			if a := m.Attendees[i]; a.Label == run.Label {
				s.recordDissent(m, a, st.response, st.err)
			}
		}
		run.Done = true
	}
	return done
}

// recordDissent stores a peer's dissent (or concession) with the files it watches.
func (s *Service) recordDissent(m *api.Meeting, a api.Attendee, response, errText string) {
	d := api.Dissent{ID: fmt.Sprintf("d%d", len(m.Dissents)+1), Label: a.Label, Member: a.Member, Kind: "dissent",
		Text: "(no response)", Claims: []api.Claim{}, WatchPaths: []string{}, Signals: []api.DissentSignal{},
		Error: answer.Clip(errText, 500)}
	if response != "" {
		p := parseDissent(response)
		d.Kind, d.Claims = p.kind, answer.CheckClaims(m.RepoRoot, p.claims)
		d.Text = p.text
		if d.Text == "" {
			d.Text = answer.Clip(strings.TrimSpace(response), maxStatement)
		}
	}
	if d.Kind == "dissent" {
		claims := d.Claims
		if final, _ := lastAnswer(m, a.Label, len(m.Rounds)+1); final != nil {
			claims = append(claims[:len(claims):len(claims)], final.Claims...)
		}
		for _, c := range claims {
			for _, ci := range c.Cites {
				if (ci.State == "verified" || ci.State == "located") && ci.Path != "" && !containsKey(d.WatchPaths, ci.Path) {
					d.WatchPaths = append(d.WatchPaths, ci.Path)
				}
			}
		}
	}
	m.Dissents = append(m.Dissents, d)
}

func (s *Service) closeMeeting(ctx context.Context, m *api.Meeting) {
	m.Status = api.MeetingStatusClosed
	s.refreshTokens(ctx, m)
	var ds []map[string]any
	for i := range m.Dissents {
		d := &m.Dissents[i]
		ds = append(ds, map[string]any{"id": d.ID, "label": d.Label, "kind": d.Kind})
	}
	s.logStep(ctx, m.ID, "closed", map[string]any{"dissents": ds})
	s.closeRuns(m)
}

// Act turns action items of a closed meeting into tasks; each brief carries the decision and the dissent on record.
func (s *Service) Act(ctx context.Context, id string, req api.MeetingActRequest) (api.MeetingActResult, error) {
	if len(req.Items) == 0 {
		return api.MeetingActResult{}, invalidf("items must not be empty")
	}
	if len(req.Items) > maxActItems {
		return api.MeetingActResult{}, invalidf("at most %d items", maxActItems)
	}
	for i, it := range req.Items {
		if strings.TrimSpace(it.Text) == "" {
			return api.MeetingActResult{}, invalidf("item %d has no text", i+1)
		}
	}
	var created []string
	var actErr error
	m, err := s.update(ctx, id, func(m *api.Meeting) error {
		if m.Status != api.MeetingStatusClosed {
			return badStatef("meeting %s is %s; act needs a closed meeting", id, m.Status)
		}
		for _, it := range req.Items {
			cwd := it.Cwd
			if cwd == "" {
				cwd = m.Cwd
			}
			text := strings.TrimSpace(it.Text)
			first, _, _ := strings.Cut(text, "\n")
			sum, err := s.o.Tasks.DelegateWith(ctx, api.DelegateRequest{Prompt: actionPrompt(m, text), Cwd: cwd,
				Title: answer.Clip(first, 80), Member: it.Member, Backend: it.Backend, Model: it.Model},
				task.Spawn{MeetingID: m.ID})
			if err != nil {
				actErr = err
				break
			}
			m.Actions = append(m.Actions, api.MeetingAction{Text: text, TaskID: sum.ID, Member: sum.Member})
			created = append(created, sum.ID)
		}
		if len(created) == 0 && actErr != nil {
			return actErr
		}
		data := map[string]any{"tasks": created}
		if actErr != nil {
			data["error"] = actErr.Error()
		}
		s.logStep(ctx, id, "act", data)
		return nil
	})
	if err != nil {
		return api.MeetingActResult{}, err
	}
	if actErr != nil {
		return api.MeetingActResult{}, fmt.Errorf("created %v, then: %w", created, actErr)
	}
	return api.MeetingActResult{Meeting: m, Tasks: created}, nil
}

// Cancel ends a meeting that has not ended and stops its running turns.
func (s *Service) Cancel(ctx context.Context, id string) (api.Meeting, error) {
	var stop []string
	m, err := s.update(ctx, id, func(m *api.Meeting) error {
		switch m.Status {
		case api.MeetingStatusClosed, api.MeetingStatusCancelled, api.MeetingStatusFailed:
			return badStatef("meeting %s is already %s", id, m.Status)
		}
		m.Status = api.MeetingStatusCancelled
		if n := len(m.Rounds); n > 0 {
			r := &m.Rounds[n-1]
			for _, run := range r.Runs {
				stop = append(stop, run.TaskID)
			}
			if r.EndedAt == "" {
				r.EndedAt = s.now()
			}
		}
		for _, run := range m.DissentRuns {
			stop = append(stop, run.TaskID)
		}
		s.logStep(ctx, id, "cancel", nil)
		return nil
	})
	if err != nil {
		return api.Meeting{}, err
	}
	for _, tid := range stop {
		if _, _, err := s.o.Tasks.Cancel(ctx, tid, api.CancelRequest{}); err != nil && !errors.Is(err, task.ErrNotFound) {
			s.log.Warn("meeting: cancel a run", "meeting", id, "task", tid, "err", err)
		}
	}
	s.closeRuns(&m)
	return m, nil
}

// DissentOutcome records that a dissent turned out right (vindicated) or not (dismissed).
func (s *Service) DissentOutcome(ctx context.Context, id, dissentID, outcome string) (api.Meeting, error) {
	if outcome != "vindicated" && outcome != "dismissed" {
		return api.Meeting{}, invalidf("outcome must be vindicated or dismissed")
	}
	return s.update(ctx, id, func(m *api.Meeting) error {
		for i := range m.Dissents {
			if d := &m.Dissents[i]; d.ID == dissentID {
				d.Outcome, d.OutcomeAt = outcome, s.now()
				s.logStep(ctx, id, "dissent_outcome", map[string]any{"dissentId": dissentID, "outcome": outcome})
				return nil
			}
		}
		return fmt.Errorf("%w: no dissent %s in meeting %s", ErrNotFound, dissentID, id)
	})
}

// ---- queries ----------------------------------------------------------------------------------------------------

// Get returns a meeting with its minutes.
func (s *Service) Get(ctx context.Context, id string) (api.Meeting, error) {
	m, err := s.load(ctx, id)
	if err != nil {
		return api.Meeting{}, err
	}
	if len(m.Rounds) > 0 {
		m.Minutes = minutes(&m)
	}
	return m, nil
}

// Summary is a meeting in lists.
func Summary(m *api.Meeting) api.MeetingSummary {
	sum := api.MeetingSummary{ID: m.ID, Title: m.Title, Status: m.Status, Round: m.Round, MaxRounds: m.MaxRounds,
		Attendees: len(m.Attendees), Cwd: m.Cwd, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	for _, q := range m.LeadQA {
		if q.AnsweredAt == "" {
			sum.PendingQuestions++
		}
	}
	for i := range m.Dissents {
		if m.Dissents[i].Kind == "dissent" {
			sum.Dissents++
		}
	}
	return sum
}

// List returns the newest meetings first (limit 1..200, 50 when 0).
func (s *Service) List(ctx context.Context, limit int) ([]api.MeetingSummary, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return nil, invalidf("limit must be 1..200")
	}
	out := []api.MeetingSummary{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `SELECT data FROM meetings ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
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
			out = append(out, Summary(&m))
		}
		return rows.Err()
	})
	return out, err
}

// waitable reports whether a meeting needs the lead or has ended.
func waitable(status string) bool {
	return status != api.MeetingStatusRound && status != api.MeetingStatusDissent
}

// Wait blocks up to d (capped at MaxWait) until the meeting needs the lead or ends, and returns it.
func (s *Service) Wait(ctx context.Context, id string, d time.Duration) (api.Meeting, error) {
	d = min(d, MaxWait)
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		s.mu.Lock()
		ch := s.changed
		s.mu.Unlock()
		m, err := s.Get(ctx, id)
		if err != nil || waitable(m.Status) || d <= 0 {
			return m, err
		}
		select {
		case <-ch:
		case <-timer.C:
			return s.Get(ctx, id)
		case <-ctx.Done():
			return m, nil
		case <-s.ctx.Done():
			return m, nil
		}
	}
}

// Events returns the step log of a meeting, oldest first.
func (s *Service) Events(ctx context.Context, id string) ([]api.TeamLogEntry, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, err
	}
	return readLog(ctx, s.o.DB, "meeting", id)
}

// Track returns the track record of every member that attended a meeting or gave a consult.
func (s *Service) Track(ctx context.Context) ([]api.MemberTrack, error) {
	recs := map[string]*api.MemberTrack{}
	rec := func(id string) *api.MemberTrack {
		if recs[id] == nil {
			recs[id] = &api.MemberTrack{Member: id}
		}
		return recs[id]
	}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		clear(recs)
		rows, err := tx.QueryContext(ctx, `SELECT data FROM meetings`)
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
			for ai := range m.Attendees {
				a := &m.Attendees[ai]
				for i := range m.Rounds {
					if ans := answerOf(&m.Rounds[i], a.Label); ans != nil && !ans.Absent {
						rec(a.Member).Meetings++
						break
					}
				}
			}
			for di := range m.Dissents {
				d := &m.Dissents[di]
				r := rec(d.Member)
				if d.Kind == "concede" {
					r.Concessions++
					continue
				}
				r.Dissents++
				r.Signals += len(d.Signals)
				switch d.Outcome {
				case "vindicated":
					r.Vindicated++
				case "dismissed":
					r.Dismissed++
				}
			}
			for _, x := range m.AdviceNotTaken {
				rec(x.Member).AdviceNotTaken++
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if s.o.ConsultStats != nil {
		cs, err := s.o.ConsultStats(ctx)
		if err != nil {
			return nil, err
		}
		for id, c := range cs {
			r := rec(id)
			r.Consults, r.Objections = c.Consults, c.Objections
		}
	}
	out := make([]api.MemberTrack, 0, len(recs))
	for _, r := range recs {
		out = append(out, *r)
	}
	sortTrack(out)
	return out, nil
}

func sortTrack(out []api.MemberTrack) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Member < out[j-1].Member; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
}

// ---- storage ----------------------------------------------------------------------------------------------------

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func decode(data string) (api.Meeting, error) {
	var m api.Meeting
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return api.Meeting{}, fmt.Errorf("meeting record: %w", err)
	}
	if m.Usage.ByBackend == nil {
		m.Usage.ByBackend = map[string]int{}
	}
	return m, nil
}

func (s *Service) load(ctx context.Context, id string) (api.Meeting, error) {
	var data string
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT data FROM meetings WHERE id = ?`, id).Scan(&data)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.Meeting{}, fmt.Errorf("%w: no meeting %s", ErrNotFound, id)
	}
	if err != nil {
		return api.Meeting{}, err
	}
	return decode(data)
}

func (s *Service) insert(ctx context.Context, m *api.Meeting) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meetings (id, status, created_at, updated_at, data)
			VALUES (?, ?, ?, ?, ?)`, m.ID, m.Status, m.CreatedAt, m.UpdatedAt, string(data))
		return err
	})
}

// save stores a meeting and tells waiters and the stream. The caller holds mu.
func (s *Service) save(ctx context.Context, m *api.Meeting) error {
	m.UpdatedAt = s.now()
	m.Minutes = ""
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE meetings SET status = ?, updated_at = ?, data = ? WHERE id = ?`,
			m.Status, m.UpdatedAt, string(data), m.ID)
		return err
	}); err != nil {
		return err
	}
	close(s.changed)
	s.changed = make(chan struct{})
	s.dirty = true
	s.o.Changed(Summary(m))
	return nil
}

// logStep appends to the meeting's step log; the log is a record, so a failure is only logged.
func (s *Service) logStep(ctx context.Context, id, typ string, data map[string]any) {
	if err := appendLog(ctx, s.o.DB, "meeting", id, typ, s.o.Now(), data); err != nil {
		s.log.Warn("meeting: step log", "meeting", id, "err", err)
	}
}

func appendLog(ctx context.Context, db DB, scope, ref, typ string, at time.Time, data map[string]any) error {
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	return db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO team_log (scope, ref, at, type, data) VALUES (?, ?, ?, ?, ?)`,
			scope, ref, at.UTC().Format(timeFormat), typ, string(raw))
		return err
	})
}

func readLog(ctx context.Context, db DB, scope, ref string) ([]api.TeamLogEntry, error) {
	out := []api.TeamLogEntry{}
	err := db.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		rows, err := tx.QueryContext(ctx, `SELECT id, at, type, data FROM team_log WHERE scope = ? AND ref = ?
			ORDER BY id`, scope, ref)
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

// Import adds a meeting kept by the old version (seelegent migrate); a meeting whose id exists is kept. A meeting
// that was still running is imported as cancelled: its runs belonged to the old daemon.
func (s *Service) Import(ctx context.Context, m api.Meeting) (bool, error) {
	if !waitable(m.Status) || m.Status == api.MeetingStatusWaitingLead || m.Status == api.MeetingStatusDeciding {
		m.Status = api.MeetingStatusCancelled
	}
	data, err := json.Marshal(m)
	if err != nil {
		return false, err
	}
	added := false
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO meetings (id, status, created_at, updated_at, data)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO NOTHING`, m.ID, m.Status, m.CreatedAt, m.UpdatedAt, string(data))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		added = n == 1
		return err
	})
	return added, err
}
