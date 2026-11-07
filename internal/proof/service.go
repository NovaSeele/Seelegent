package proof

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Errors of the proof service.
var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// ConflictError is a refused regression run, with details for the API (runId, activeTaskIds).
type ConflictError struct {
	Msg     string
	Details map[string]any
}

func (e *ConflictError) Error() string { return e.Msg }

// Is makes errors.Is(err, ErrConflict) true.
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// APIDetails are the details of the API error.
func (e *ConflictError) APIDetails() map[string]any { return e.Details }

// CmdResult is how one command of a regression run ended.
type CmdResult struct {
	Exit *int
	Ms   int64
	Tail string
}

// Git is what the service needs from git.
type Git interface {
	// Root returns the repository root of dir (an error when dir is not in a repository).
	Root(ctx context.Context, dir string) (string, error)
	// Head returns the HEAD commit of root and whether the worktree has uncommitted changes.
	Head(ctx context.Context, root string) (head string, dirty bool)
}

// Options configure the service.
type Options struct {
	Ledger *Ledger
	Git    Git
	// Exec runs one command in dir with the gate timeout, writing its output to logFile.
	Exec func(ctx context.Context, cmd, dir, logFile string) CmdResult
	// Active returns the ids of the active tasks in the repository at root.
	Active func(ctx context.Context, root string) ([]string, error)
	// TaskFeatures returns the feature folders (absolute) the tasks of the repository at root work on.
	TaskFeatures func(ctx context.Context, root string) []string
	// Publish announces a regression run's progress (SSE proof.run).
	Publish func(api.ProofRun)
	// LogDir holds the logs of the regression runs.
	LogDir string
	Now    func() time.Time
	Logger *slog.Logger
}

// Service answers the proof views and runs regression runs.
type Service struct {
	o    Options
	log  *slog.Logger
	root context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu     sync.Mutex
	active map[string]*activeRun // by project key
}

type activeRun struct {
	id     string
	cancel context.CancelFunc
}

// New returns the service; Close stops its runs.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, stop := context.WithCancel(context.Background())
	return &Service{o: o, log: o.Logger, root: ctx, stop: stop, active: map[string]*activeRun{}}
}

// Start marks the runs an earlier daemon left running as errors.
func (s *Service) Start(ctx context.Context) error { return s.o.Ledger.AbandonRuns(ctx) }

// Close cancels the running regression runs and waits for them.
func (s *Service) Close() {
	s.stop()
	s.wg.Wait()
}

// Ledger returns the service's ledger.
func (s *Service) Ledger() *Ledger { return s.o.Ledger }

// Project is a repository as the proof views see it.
type Project struct {
	Root string
	Key  string
}

// ProjectOf resolves the repository of cwd (cwd itself when it is not in a git repository).
func (s *Service) ProjectOf(ctx context.Context, cwd string) (Project, error) {
	canonical, _, _, err := fsutil.ProjectOf(cwd)
	if err != nil {
		return Project{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	root := canonical
	if s.o.Git != nil {
		if r, err := s.o.Git.Root(ctx, canonical); err == nil && r != "" {
			if c, err := fsutil.Canonical(r); err == nil {
				root = c
			}
		}
	}
	key, _, err := fsutil.ProjectKey(root)
	if err != nil {
		return Project{}, err
	}
	return Project{Root: root, Key: key}, nil
}

// ResolveFeature resolves a feature folder (relative to the repository root, or absolute) that holds a spec.md or
// a tasks.md.
func ResolveFeature(root, dir string) (abs string, f Feature, err error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", Feature{}, fmt.Errorf("%w: feature is required", ErrInvalid)
	}
	abs = filepath.FromSlash(dir)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
		if in, err := fsutil.Within(root, abs); err != nil || !in {
			return "", Feature{}, fmt.Errorf("%w: feature %q leaves the repository; pass an absolute path", ErrInvalid, dir)
		}
	}
	if c, err := fsutil.Canonical(abs); err == nil {
		abs = c
	}
	if !exists(filepath.Join(abs, "spec.md")) && !exists(filepath.Join(abs, "tasks.md")) {
		return "", Feature{}, fmt.Errorf("%w: feature folder %q has neither spec.md nor tasks.md", ErrNotFound, dir)
	}
	return abs, FeatureOf(root, abs), nil
}

// FeatureOf names an existing feature folder (absolute, canonical) for the ledger: relative to root when inside.
func FeatureOf(root, abs string) Feature {
	k, err := fsutil.Key(abs)
	if err != nil {
		k = filepath.ToSlash(abs)
	}
	display := filepath.ToSlash(abs)
	if in, err := fsutil.Within(root, abs); err == nil && in {
		if rel, err := filepath.Rel(root, abs); err == nil {
			display = filepath.ToSlash(rel)
		}
	}
	return Feature{Key: k, Display: display}
}

func exists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func readDoc(p string) (text string, found bool, err error) {
	data, _, err := fsutil.ReadRegular(p, maxDocBytes)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(data), true, nil
}

// ReadFeature parses the spec.md and tasks.md of a feature folder (either may be missing).
func ReadFeature(abs string) (Spec, Tasks, error) {
	spec := Spec{Stories: []Story{}, Requirements: []Requirement{}}
	tasks := Tasks{FeatureGate: []string{}, Tasks: []TaskLine{}}
	text, ok, err := readDoc(filepath.Join(abs, "spec.md"))
	if err != nil {
		return spec, tasks, err
	}
	if ok {
		spec = ParseSpec(text)
	}
	text, ok, err = readDoc(filepath.Join(abs, "tasks.md"))
	if err != nil {
		return spec, tasks, err
	}
	if ok {
		tasks = ParseTasks(text)
	}
	return spec, tasks, nil
}

// Matrix returns the requirement matrix of a feature of the repository of cwd.
func (s *Service) Matrix(ctx context.Context, cwd, feature string) (api.ProofMatrix, error) {
	p, err := s.ProjectOf(ctx, cwd)
	if err != nil {
		return api.ProofMatrix{}, err
	}
	abs, f, err := ResolveFeature(p.Root, feature)
	if err != nil {
		return api.ProofMatrix{}, err
	}
	return s.matrix(ctx, p, abs, f)
}

func (s *Service) matrix(ctx context.Context, p Project, abs string, f Feature) (api.ProofMatrix, error) {
	spec, tasks, err := ReadFeature(abs)
	if err != nil {
		return api.ProofMatrix{}, err
	}
	entries, err := s.o.Ledger.Entries(ctx, p.Key, f.Key)
	if err != nil {
		return api.ProofMatrix{}, err
	}
	return BuildMatrix(f.Display, spec, tasks, entries), nil
}

// features lists the feature folders of a project: specs/* under the root, the folders the ledger knows and the
// folders its tasks work on, each once (by key), in display order.
func (s *Service) features(ctx context.Context, p Project) ([]string, error) {
	var dirs []string
	if ents, err := os.ReadDir(filepath.Join(p.Root, "specs")); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(p.Root, "specs", e.Name()))
			}
		}
	}
	known, err := s.o.Ledger.Features(ctx, p.Key)
	if err != nil {
		return nil, err
	}
	for _, f := range known {
		d := filepath.FromSlash(f.Display)
		if !filepath.IsAbs(d) {
			d = filepath.Join(p.Root, d)
		}
		dirs = append(dirs, d)
	}
	if s.o.TaskFeatures != nil {
		dirs = append(dirs, s.o.TaskFeatures(ctx, p.Root)...)
	}
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		if !exists(filepath.Join(d, "spec.md")) && !exists(filepath.Join(d, "tasks.md")) {
			continue
		}
		if c, err := fsutil.Canonical(d); err == nil {
			d = c
		}
		k, err := fsutil.Key(d)
		if err != nil || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return FeatureOf(p.Root, out[i]).Display < FeatureOf(p.Root, out[j]).Display
	})
	return out, nil
}

// Index returns the proof overview of the repository of cwd.
func (s *Service) Index(ctx context.Context, cwd string) (api.ProofIndex, error) {
	p, err := s.ProjectOf(ctx, cwd)
	if err != nil {
		return api.ProofIndex{}, err
	}
	out := api.ProofIndex{Key: p.Key, RepoRoot: p.Root, Features: []api.ProofFeature{}}
	dirs, err := s.features(ctx, p)
	if err != nil {
		return out, err
	}
	for _, d := range dirs {
		f := FeatureOf(p.Root, d)
		m, err := s.matrix(ctx, p, d, f)
		pf := api.ProofFeature{Dir: f.Display}
		if err != nil {
			pf.Error = err.Error()
		} else {
			pf.Title, pf.Counts, pf.Tasks = m.Title, m.Counts, m.Tasks
			out.Totals.Plus(m.Counts)
		}
		out.Features = append(out.Features, pf)
	}
	if out.Runs, err = s.o.Ledger.Runs(ctx, p.Key, 10); err != nil {
		return out, err
	}
	return out, nil
}

// runCmd is one command of a regression run.
type runCmd struct {
	cmd  string
	kind string
	keys []string
	// feature and task ids per key, for the ledger.
	feature map[string]Feature
}

// plan collects the commands of a regression run: the Gate: commands of every spec task, the feature gate (for
// the ticked tasks), and the doctrine checks, each command once.
func (s *Service) plan(p Project, dirs []string) ([]runCmd, error) {
	var out []runCmd
	add := func(cmd, kind string, keys []string, f Feature) {
		for i := range out {
			if out[i].cmd == cmd && out[i].kind == kind {
				for _, k := range keys {
					if !slices.Contains(out[i].keys, k) {
						out[i].keys = append(out[i].keys, k)
					}
					out[i].feature[k] = f
				}
				return
			}
		}
		rc := runCmd{cmd: cmd, kind: kind, keys: slices.Clone(keys), feature: map[string]Feature{}}
		for _, k := range keys {
			rc.feature[k] = f
		}
		out = append(out, rc)
	}
	for _, d := range dirs {
		_, tasks, err := ReadFeature(d)
		if err != nil {
			return nil, err
		}
		f := FeatureOf(p.Root, d)
		var ticked []string
		for i := range tasks.Tasks {
			t := &tasks.Tasks[i]
			key := f.Display + "#" + t.ID
			for _, g := range t.Gates {
				add(g, "task", []string{key}, f)
			}
			if t.Done {
				ticked = append(ticked, key)
			}
		}
		for _, g := range tasks.FeatureGate {
			add(g, "feature", ticked, f)
		}
	}
	checks, err := ReadDoctrine(p.Root)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalid, DoctrineFile, err)
	}
	for _, c := range checks {
		add(c.Run, "doctrine", nil, Feature{})
	}
	return out, nil
}

// newRunID is r<yymmddhhmmss>-<4 hex>.
func (s *Service) newRunID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return "r" + s.o.Now().UTC().Format("060102150405") + "-" + hex.EncodeToString(b[:])
}

// StartRun starts a regression run in the repository of req.Cwd (one feature, or every feature) and returns it
// running. It is refused while another run of the project goes on, and while tasks of the repository are active
// unless req.Force.
func (s *Service) StartRun(ctx context.Context, req api.ProofRunRequest) (api.ProofRun, error) {
	p, err := s.ProjectOf(ctx, req.Cwd)
	if err != nil {
		return api.ProofRun{}, err
	}
	var dirs []string
	feature := ""
	if strings.TrimSpace(req.Feature) != "" {
		abs, f, err := ResolveFeature(p.Root, req.Feature)
		if err != nil {
			return api.ProofRun{}, err
		}
		dirs, feature = []string{abs}, f.Display
	} else if dirs, err = s.features(ctx, p); err != nil {
		return api.ProofRun{}, err
	}
	var active []string
	if s.o.Active != nil {
		if active, err = s.o.Active(ctx, p.Root); err != nil {
			return api.ProofRun{}, err
		}
	}
	if len(active) > 0 && !req.Force {
		return api.ProofRun{}, &ConflictError{
			Msg:     fmt.Sprintf("tasks are active in this repository (%s); pass force to run anyway", strings.Join(active, ", ")),
			Details: map[string]any{"reason": "active_tasks", "activeTaskIds": active}}
	}
	cmds, err := s.plan(p, dirs)
	if err != nil {
		return api.ProofRun{}, err
	}
	head, dirty := "", false
	if s.o.Git != nil {
		head, dirty = s.o.Git.Head(ctx, p.Root)
	}
	run := api.ProofRun{ID: s.newRunID(), RepoRoot: p.Root, Feature: feature, State: api.RunRunning,
		StartedAt: api.FormatTime(s.o.Now()), GitHead: head, Dirty: dirty, Noisy: len(active) > 0,
		Total: len(cmds), Results: []api.ProofRunResult{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.active[p.Key]; a != nil {
		return api.ProofRun{}, &ConflictError{Msg: "a regression run is already running for this project",
			Details: map[string]any{"reason": "run_in_progress", "runId": a.id}}
	}
	if s.root.Err() != nil {
		return api.ProofRun{}, errors.New("the daemon is stopping")
	}
	// The record is written before the run is registered: a failed write leaves nothing stuck (old bug D11).
	if err := s.o.Ledger.SaveRun(ctx, p.Key, run); err != nil {
		return api.ProofRun{}, err
	}
	rctx, cancel := context.WithCancel(s.root)
	s.active[p.Key] = &activeRun{id: run.ID, cancel: cancel}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer cancel()
		s.execute(rctx, p, run, cmds)
	}()
	s.publish(run)
	return run, nil
}

func (s *Service) publish(r api.ProofRun) {
	if s.o.Publish != nil {
		s.o.Publish(r)
	}
}

// execute runs the commands of a regression run and records the results.
func (s *Service) execute(ctx context.Context, p Project, run api.ProofRun, cmds []runCmd) {
	defer func() {
		s.mu.Lock()
		if a := s.active[p.Key]; a != nil && a.id == run.ID {
			delete(s.active, p.Key)
		}
		s.mu.Unlock()
	}()
	save := func() {
		// The run's own context may be cancelled: the record is written with a fresh one.
		wctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.o.Ledger.SaveRun(wctx, p.Key, run); err != nil {
			s.log.Warn("proof: save the regression run", "run", run.ID, "err", err)
		}
		s.publish(run)
	}
	expected := map[string]int{}
	for _, c := range cmds {
		for _, k := range c.keys {
			expected[k]++
		}
	}
	got := map[string][]EntryCmd{}
	for i, c := range cmds {
		if ctx.Err() != nil {
			break
		}
		run.Results = append(run.Results, api.ProofRunResult{Cmd: c.cmd, Keys: api.NonNil(c.keys), Kind: c.kind,
			State: api.RunRunning})
		save()
		log := filepath.Join(s.o.LogDir, p.Key, run.ID, fmt.Sprintf("%d.log", i+1))
		res := s.o.Exec(ctx, c.cmd, p.Root, log)
		if ctx.Err() != nil {
			run.Results = run.Results[:len(run.Results)-1]
			break
		}
		state := api.GateGreen
		switch {
		case res.Exit == nil:
			state = api.GateError
		case *res.Exit != 0:
			state = api.GateRed
		}
		r := &run.Results[len(run.Results)-1]
		r.State, r.Exit, r.Ms = state, res.Exit, res.Ms
		if state != api.GateGreen {
			r.Tail = res.Tail
		}
		switch state {
		case api.GateGreen:
			run.Counts.Green++
		case api.GateRed:
			run.Counts.Red++
		default:
			run.Counts.Error++
		}
		for _, k := range c.keys {
			got[k] = append(got[k], EntryCmd{Cmd: c.cmd, Exit: res.Exit, Ms: res.Ms, State: state})
			if len(got[k]) == expected[k] {
				s.recordKey(p, c.feature[k], k, run, got[k])
			}
		}
	}
	run.EndedAt = api.FormatTime(s.o.Now())
	switch {
	case ctx.Err() != nil:
		run.State = api.RunCancelled
	case run.Counts.Red > 0:
		run.State = api.RunRed
	case run.Counts.Error > 0:
		run.State = api.RunError
	default:
		run.State = api.RunGreen
	}
	save()
}

// recordKey writes the aggregated result of one spec task of a regression run.
func (s *Service) recordKey(p Project, f Feature, key string, run api.ProofRun, cmds []EntryCmd) {
	_, id, ok := strings.Cut(key, "#")
	if !ok {
		return
	}
	state := api.GateGreen
	for _, c := range cmds {
		if c.State == api.GateError {
			state = api.GateError
		} else if c.State == api.GateRed && state == api.GateGreen {
			state = api.GateRed
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := s.o.Ledger.Record(ctx, p.Key, f, []Result{{TaskID: id, State: state, Source: SourceRegression,
		Ref: run.ID, GitHead: run.GitHead, Cmds: cmds}})
	if err != nil {
		s.log.Warn("proof: record a regression result", "run", run.ID, "task", key, "err", err)
	}
}

// RunStatus returns a regression run of the repository of cwd.
func (s *Service) RunStatus(ctx context.Context, cwd, id string) (api.ProofRun, error) {
	p, err := s.ProjectOf(ctx, cwd)
	if err != nil {
		return api.ProofRun{}, err
	}
	r, err := s.o.Ledger.Run(ctx, p.Key, id)
	if errors.Is(err, ErrNoRun) {
		return api.ProofRun{}, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	return r, err
}

// CancelRun stops a running regression run; a finished run is returned as it is.
func (s *Service) CancelRun(ctx context.Context, cwd, id string) (api.ProofRun, error) {
	p, err := s.ProjectOf(ctx, cwd)
	if err != nil {
		return api.ProofRun{}, err
	}
	s.mu.Lock()
	a := s.active[p.Key]
	if a != nil && a.id == id {
		a.cancel()
	}
	s.mu.Unlock()
	if a == nil || a.id != id {
		return s.RunStatus(ctx, cwd, id)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		still := s.active[p.Key] != nil && s.active[p.Key].id == id
		s.mu.Unlock()
		if !still {
			break
		}
		select {
		case <-ctx.Done():
			return api.ProofRun{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return s.RunStatus(ctx, cwd, id)
}

// Counters returns the lane counters of the repository of cwd.
func (s *Service) Counters(ctx context.Context, root string) (map[string]int, error) {
	key, _, err := fsutil.ProjectKey(root)
	if err != nil {
		return nil, err
	}
	return s.o.Ledger.Counters(ctx, key)
}
