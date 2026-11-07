package task

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// The gates of milestone 13 (docs/gates.md): what a task's gate checks is resolved when the task is delegated (and
// again at a followup that changes it) and stored in Task.Proof. Lane claims are checked then too.

// Ledger records the gate results of spec tasks and counts lane decisions; *proof.Ledger satisfies it.
type Ledger interface {
	Record(ctx context.Context, project string, f proof.Feature, results []proof.Result) error
	Bump(ctx context.Context, project, name string) error
	Counters(ctx context.Context, project string) (map[string]int, error)
}

// Proof is what a task's gate checks, frozen when the task starts (old bug D10: the doctrine was read from the
// worktree, which the worker can edit, at every gate).
type Proof struct {
	// GateOff: the lead turned the gate's checks off (lane and hygiene still run).
	GateOff bool `json:"gateOff,omitempty"`
	// Detect: the lead asked for the repository's test, lint and build commands (gate: true).
	Detect bool `json:"detect,omitempty"`
	// LeadCommands are the lead's own gate commands, Commands every command the gate runs.
	LeadCommands []string          `json:"leadCommands,omitempty"`
	Commands     []api.GateCommand `json:"commands,omitempty"`
	Lane         *api.TaskLane     `json:"lane,omitempty"`
	// NoLane: the lead dropped the lane a spec would give.
	NoLane        bool          `json:"noLane,omitempty"`
	Verify        bool          `json:"verify,omitempty"`
	Doctrine      []proof.Check `json:"doctrine,omitempty"`
	DoctrineError string        `json:"doctrineError,omitempty"`
	Bar           proof.Bar     `json:"bar,omitempty"`
	BarChanged    []string      `json:"barChanged,omitempty"`
	// AutoFixes counts the automatic fix turns since the lead's last message; Budget is the most allowed.
	AutoFixes int `json:"autoFixes,omitempty"`
	Budget    int `json:"budget"`
	// Project is the ledger key of the repository; Feature, FeatureKey and FeatureDir name the spec feature.
	Project    string    `json:"project,omitempty"`
	Feature    string    `json:"feature,omitempty"`
	FeatureKey string    `json:"featureKey,omitempty"`
	FeatureDir string    `json:"featureDir,omitempty"`
	FrozenAt   time.Time `json:"frozenAt"`
}

// GatePhase is the gate (or verifier) of turn Turn running after the turn ended.
type GatePhase struct {
	State string    `json:"state"`
	Turn  int       `json:"turn"`
	Since time.Time `json:"since"`
	// ChildID is the verify task while State is verifying.
	ChildID string `json:"childId,omitempty"`
}

// proofRequest is the gate part of a delegate or followup.
type proofRequest struct {
	gate         *bool
	gateCommands []string
	lane         []string
	noLane       bool
	forceLane    bool
	verify       *bool
}

func (r proofRequest) empty() bool {
	return r.gate == nil && len(r.gateCommands) == 0 && len(r.lane) == 0 && !r.noLane && !r.forceLane &&
		r.verify == nil
}

// asksGate reports whether the request turns on something only an edit task has.
func (r proofRequest) asksGate() bool {
	return (r.gate != nil && *r.gate) || len(r.gateCommands) > 0 || len(r.lane) > 0 || r.forceLane ||
		(r.verify != nil && *r.verify)
}

// followupProof applies the gate part of a followup to t inside its mutate: the automatic fix budget starts again,
// and a request that changes the gate, the lane or the spec tasks resolves the proof again (checking a new lane).
// changed reports whether the lane or the gate commands changed, so the continuation repeats them.
func (s *Service) followupProof(ctx context.Context, t *Task, req proofRequest, tasksChanged bool) (
	[]NewEvent, bool, error) {
	if !gated(t) {
		if req.asksGate() {
			return nil, false, invalidf("a plan task has no gate, lane or verifier")
		}
		return nil, false, nil
	}
	prev := t.Proof
	if prev != nil && req.empty() && !tasksChanged {
		prev.AutoFixes = 0
		return nil, false, nil
	}
	if t.RepoRoot == "" {
		t.RepoRoot = s.gitRoot(ctx, t.Cwd)
	}
	p, err := s.resolveProof(t, req, prev)
	if err != nil {
		return nil, false, err
	}
	var evs []NewEvent
	if p.Lane != nil {
		same := prev != nil && prev.Lane != nil && slices.Equal(prev.Lane.Paths, p.Lane.Paths)
		if same {
			p.Lane.Forced = slices.Clone(prev.Lane.Forced)
		}
		if !same || req.forceLane {
			s.claimMu.Lock()
			evs, err = s.checkLane(ctx, t, p, req.forceLane)
			s.claimMu.Unlock()
			if err != nil {
				return nil, false, err
			}
		}
	}
	turn := 0
	if tu := t.LastTurn(); tu != nil {
		turn = tu.N
	}
	for i := range evs {
		evs[i].Turn = turn
	}
	lane, cmds := s.briefGate(t)
	t.Proof = p
	lane2, cmds2 := s.briefGate(t)
	return evs, !slices.Equal(lane, lane2) || !slices.Equal(cmds, cmds2), nil
}

// LaneConflictError refuses a lane that overlaps the claims of other tasks (lanes: enforce).
type LaneConflictError struct {
	Conflicts []api.LaneConflict
}

func (e *LaneConflictError) Error() string {
	var parts []string
	for _, c := range e.Conflicts {
		var ps []string
		for _, p := range c.Paths {
			ps = append(ps, p.Mine+" ~ "+p.Theirs)
		}
		parts = append(parts, fmt.Sprintf("%s (%s, %s): %s", c.TaskID, c.Title, c.Status, strings.Join(ps, ", ")))
	}
	return "the lane overlaps the files other tasks hold: " + strings.Join(parts, "; ") +
		"; wait for them, pick other files, or pass forceLane"
}

// Is makes errors.Is(err, ErrConflict) true.
func (e *LaneConflictError) Is(target error) bool { return target == ErrConflict }

// APIDetails are the details of the API error.
func (e *LaneConflictError) APIDetails() map[string]any {
	return map[string]any{"reason": "lane_conflict", "conflicts": e.Conflicts}
}

// gitRoot returns the repository root of dir (canonical), or "" when dir is not in a git repository.
func (s *Service) gitRoot(ctx context.Context, dir string) string {
	var out []byte
	if err := s.o.Git.Run(func(git string) error {
		var err error
		out, err = runGit(ctx, git, dir, "rev-parse", "--show-toplevel")
		return err
	}); err != nil {
		return ""
	}
	root := filepath.Clean(strings.TrimSpace(string(out)))
	if c, err := fsutil.Canonical(root); err == nil {
		root = c
	}
	return root
}

// claimRoot is the folder lane claims are compared in: the repository root, or the task's folder outside git.
func claimRoot(t *Task) string {
	if t.RepoRoot != "" {
		return t.RepoRoot
	}
	return t.Cwd
}

// gated reports whether a task gets a gate at all: a task the lead delegated that may edit files.
func gated(t *Task) bool { return (t.Kind == KindNormal || t.Kind == "") && t.Mode != ModePlan }

// specFeature returns the absolute folder of the task's spec feature, or "".
func specFeature(t *Task) string {
	if t.Spec == nil {
		return ""
	}
	dir := filepath.FromSlash(t.Spec.Dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(t.Cwd, dir)
	}
	if c, err := fsutil.Canonical(dir); err == nil {
		dir = c
	}
	return dir
}

// resolveProof works out what the gate of t checks from the request, the settings, the spec tasks and the
// repository, keeping what prev (the task's current proof) had for the options the request leaves out. It
// returns nil for a task without a gate.
func (s *Service) resolveProof(t *Task, req proofRequest, prev *Proof) (*Proof, error) {
	if !gated(t) {
		return nil, nil
	}
	set := s.settings()
	root := claimRoot(t)
	p := &Proof{Budget: set.GateMaxAutoFixes, FrozenAt: s.now()}
	if prev != nil {
		p.GateOff, p.Detect, p.LeadCommands, p.NoLane, p.Verify = prev.GateOff, prev.Detect, prev.LeadCommands,
			prev.NoLane, prev.Verify
		if prev.Lane != nil && prev.Lane.Source == "lead" {
			p.Lane = &api.TaskLane{Paths: slices.Clone(prev.Lane.Paths), Source: "lead"}
		}
		p.Doctrine, p.DoctrineError, p.Bar = prev.Doctrine, prev.DoctrineError, maps.Clone(prev.Bar)
		p.Project, p.FrozenAt = prev.Project, prev.FrozenAt
	} else {
		p.Verify = set.Verifier
	}
	if req.gate != nil {
		p.GateOff, p.Detect = !*req.gate, *req.gate
	}
	if req.gateCommands != nil {
		p.LeadCommands = nil
		for _, c := range req.gateCommands {
			if c = strings.TrimSpace(c); c != "" && !slices.Contains(p.LeadCommands, c) {
				p.LeadCommands = append(p.LeadCommands, c)
			}
		}
		if len(p.LeadCommands) > 0 {
			p.GateOff = false
		}
	}
	if req.verify != nil {
		p.Verify = *req.verify
	}
	if req.noLane {
		p.NoLane, p.Lane = true, nil
	}
	if len(req.lane) > 0 {
		lane, err := s.leadLane(t, root, req.lane)
		if err != nil {
			return nil, err
		}
		p.NoLane, p.Lane = false, &api.TaskLane{Paths: lane, Source: "lead"}
	}
	for _, c := range p.LeadCommands {
		p.Commands = append(p.Commands, api.GateCommand{Cmd: c, Kind: api.StepTask})
	}
	feature := specFeature(t)
	lines := map[string]string{}
	if feature != "" {
		f := proof.FeatureOf(root, feature)
		p.Feature, p.FeatureKey, p.FeatureDir = f.Display, f.Key, feature
		if err := s.specProof(t, root, feature, p, lines); err != nil {
			return nil, err
		}
	}
	if p.Detect {
		p.Commands = append(p.Commands, gate.Detect(root, t.Cwd)...)
	}
	if prev == nil {
		key, _, err := fsutil.ProjectKey(root)
		if err != nil {
			return nil, invalidf("%v", err)
		}
		p.Project = key
		if t.RepoRoot != "" {
			checks, err := proof.ReadDoctrine(t.RepoRoot)
			if err != nil {
				p.DoctrineError = err.Error()
			}
			p.Doctrine = checks
		}
	}
	var cmds []string
	for _, c := range p.Commands {
		cmds = append(cmds, c.Cmd)
	}
	bar := proof.SnapshotBar(t.RepoRoot, feature, lines, cmds)
	if prev == nil || prev.Bar == nil {
		p.Bar = bar
	} else {
		// A followup that changes the gate on purpose moves the bar for the gate commands only.
		p.Bar["gate"] = bar["gate"]
		for k, v := range bar {
			if strings.HasPrefix(k, "tasks.md#") {
				p.Bar[k] = v
			}
		}
	}
	return p, nil
}

// leadLane turns the lead's lane paths (relative to the task's folder) into paths relative to the claim root.
func (s *Service) leadLane(t *Task, root string, paths []string) ([]string, error) {
	prefix := ""
	if rel, ok := relInside(root, t.Cwd); ok && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	in := make([]string, 0, len(paths))
	for _, p := range paths {
		c := proof.Clean(p)
		if c == "" || c == "." || c == "./" {
			c = ""
		}
		in = append(in, prefix+c)
	}
	isDir := func(rel string) bool {
		st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil && st.IsDir()
	}
	lane, bad := proof.NormalizeLane(in, isDir)
	if bad != "" {
		return nil, invalidf("lane path %q must be relative and stay inside the repository", bad)
	}
	if len(lane) == 0 {
		return nil, invalidf("lane has no paths")
	}
	return lane, nil
}

// specProof adds what the spec tasks of t give: their Gate: commands, the feature gate, and (without a lead lane)
// the lane from their path tokens; lines gets their raw lines for the bar.
func (s *Service) specProof(t *Task, root, feature string, p *Proof, lines map[string]string) error {
	text, _, err := fsutil.ReadRegular(filepath.Join(feature, "tasks.md"), 4<<20)
	ids := t.Spec.TaskIDs
	if errors.Is(err, os.ErrNotExist) {
		return nil // a feature without tasks.md: the ids are the lead's word
	}
	if err != nil {
		return invalidf("spec tasks.md: %v", err)
	}
	tasks := proof.ParseTasks(string(text))
	var tokens []string
	add := func(cmd string, ids []string) {
		for i := range p.Commands {
			if p.Commands[i].Cmd == cmd {
				for _, id := range ids {
					if !slices.Contains(p.Commands[i].TaskIDs, id) {
						p.Commands[i].TaskIDs = append(p.Commands[i].TaskIDs, id)
					}
				}
				return
			}
		}
		p.Commands = append(p.Commands, api.GateCommand{Cmd: cmd, TaskIDs: slices.Clone(ids), Kind: api.StepTask})
	}
	for _, id := range ids {
		tl, ok := tasks.Find(id)
		if !ok {
			return invalidf("task %s is not in %s/tasks.md", id, t.Spec.Dir)
		}
		lines[id] = tl.Line
		tokens = append(tokens, tl.PathTokens...)
		for _, g := range tl.Gates {
			add(g, []string{id})
		}
	}
	if len(ids) > 0 {
		for _, g := range tasks.FeatureGate {
			add(g, ids)
		}
	}
	if (p.Lane == nil || p.Lane.Source != "lead") && !p.NoLane && len(tokens) > 0 {
		featureRel := ""
		if rel, ok := relInside(root, feature); ok {
			featureRel = filepath.ToSlash(rel)
		}
		stat := func(rel string) string {
			st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
			switch {
			case err != nil:
				return ""
			case st.IsDir():
				return "dir"
			}
			return "file"
		}
		if lane := proof.ResolveLane(tokens, featureRel, stat); len(lane) > 0 {
			p.Lane = &api.TaskLane{Paths: lane, Source: "spec"}
		}
	}
	return nil
}

// held reports whether a task holds its lane claim at now: while it is pending, and when it is done but not
// reviewed within laneHoldHours of its last change.
func held(t *Task, holdHours int, now time.Time) (bool, time.Time) {
	switch {
	case Pending(t.Status):
		return true, time.Time{}
	case t.Status == StatusDone && t.Review == nil:
		until := t.UpdatedAt.Add(time.Duration(holdHours) * time.Hour)
		return now.Before(until), until
	}
	return false, time.Time{}
}

// claims returns the lane claims held in the repository at root, without task skip.
func (s *Service) claims(ctx context.Context, root, skip string) ([]api.LaneClaim, error) {
	set := s.settings()
	now := s.now()
	hold := max(set.LaneHoldHours, 0)
	all, err := s.repo.Recent(ctx, now.Add(-time.Duration(hold)*time.Hour), 1000)
	if err != nil {
		return nil, err
	}
	key := rootKey(root)
	var out []api.LaneClaim
	for _, o := range all {
		if o.ID == skip || o.Proof == nil || o.Proof.Lane == nil || len(o.Proof.Lane.Paths) == 0 ||
			rootKey(claimRoot(o)) != key {
			continue
		}
		ok, until := held(o, hold, now)
		if !ok {
			continue
		}
		out = append(out, api.LaneClaim{TaskID: o.ID, Title: o.Title, Status: o.Status,
			Paths: slices.Clone(o.Proof.Lane.Paths), Source: o.Proof.Lane.Source, HeldUntil: api.FormatTime(until)})
	}
	return out, nil
}

// checkLane checks a new lane of task t against the claims of the other tasks of its repository. Under enforce an
// overlap is refused unless force; the forced or warned overlaps are returned with their events.
func (s *Service) checkLane(ctx context.Context, t *Task, p *Proof, force bool) ([]NewEvent, error) {
	mode := s.settings().Lanes
	if p == nil || p.Lane == nil || mode == config.LanesOff {
		return nil, nil
	}
	claims, err := s.claims(ctx, claimRoot(t), t.ID)
	if err != nil {
		return nil, err
	}
	var conflicts []api.LaneConflict
	for _, c := range claims {
		if pairs := proof.Overlap(p.Lane.Paths, c.Paths); len(pairs) > 0 {
			lc := api.LaneConflict{TaskID: c.TaskID, Title: c.Title, Status: c.Status}
			for _, pp := range pairs {
				lc.Paths = append(lc.Paths, api.PathPair{Mine: pp.Mine, Theirs: pp.Theirs})
			}
			conflicts = append(conflicts, lc)
		}
	}
	if len(conflicts) == 0 {
		return nil, nil
	}
	if mode == config.LanesEnforce && !force {
		s.bump(ctx, p.Project, proof.CounterLaneRefusals)
		return nil, &LaneConflictError{Conflicts: conflicts}
	}
	kind := api.LaneWarning
	if mode == config.LanesEnforce {
		kind = api.LaneForced
		s.bump(ctx, p.Project, proof.CounterLaneForced)
		for _, c := range conflicts {
			if !slices.Contains(p.Lane.Forced, c.TaskID) {
				p.Lane.Forced = append(p.Lane.Forced, c.TaskID)
			}
		}
	}
	return []NewEvent{{Type: api.EventLane, Data: api.LaneEventData{Kind: kind, Paths: p.Lane.Paths,
		Conflicts: conflicts}}}, nil
}

// bump counts a lane decision in the ledger (statistics only: a failure is logged).
func (s *Service) bump(ctx context.Context, project, name string) {
	if s.o.Ledger == nil || project == "" {
		return
	}
	if err := s.o.Ledger.Bump(ctx, project, name); err != nil {
		s.log.Warn("task: count a lane decision", "err", err)
	}
}

// Lanes returns the lane claims held in the repository of cwd.
func (s *Service) Lanes(ctx context.Context, cwd string) (api.LanesView, error) {
	canonical, _, _, err := fsutil.ProjectOf(cwd)
	if err != nil {
		return api.LanesView{}, invalidf("%v", err)
	}
	root := canonical
	if r := s.gitRoot(ctx, canonical); r != "" {
		root = r
	}
	claims, err := s.claims(ctx, root, "")
	if err != nil {
		return api.LanesView{}, err
	}
	return api.LanesView{RepoRoot: root, Mode: s.settings().Lanes, Claims: api.NonNil(claims)}, nil
}

// briefGate is what the worker's brief says about the task's lane and gate commands.
func (s *Service) briefGate(t *Task) (lane, cmds []string) {
	p := t.Proof
	if p == nil {
		return nil, nil
	}
	if p.Lane != nil && len(p.Lane.Paths) > 0 && s.settings().Lanes != config.LanesOff {
		lane = p.Lane.Paths
	}
	if !p.GateOff && s.settings().Gates {
		for _, c := range p.Commands {
			cmd := c.Cmd
			if c.Dir != "" {
				cmd = "cd " + c.Dir + " && " + cmd
			}
			if !slices.Contains(cmds, cmd) {
				cmds = append(cmds, cmd)
			}
		}
	}
	return lane, cmds
}

// gateSummary is the gate state a task summary shows: the running phase, else the last gate since the lead's
// latest instruction (null when none ran).
func gateSummary(t *Task) *api.Gate {
	fixes := 0
	if t.Proof != nil {
		fixes = t.Proof.AutoFixes
	}
	if ph := t.GatePhase; ph != nil {
		return &api.Gate{State: api.GateRunning, Phase: ph.State, AutoFixes: fixes, Turn: ph.Turn}
	}
	if len(t.Turns) == 0 {
		return nil
	}
	start := instructionStart(t, t.Turns[len(t.Turns)-1].N)
	for i := len(t.Turns) - 1; i >= start; i-- {
		if g := t.Turns[i].Gate; g != nil {
			return &api.Gate{State: g.State, AutoFixes: fixes, Turn: t.Turns[i].N}
		}
	}
	return nil
}

// proofAPI is the task's proof as the API shows it.
func proofAPI(p *Proof) *api.TaskProof {
	if p == nil {
		return nil
	}
	out := &api.TaskProof{Gate: !p.GateOff, Commands: api.NonNil(slices.Clone(p.Commands)), Lane: p.Lane,
		Verify: p.Verify, Doctrine: []string{}, DoctrineError: p.DoctrineError, BarChanged: api.NonNil(p.BarChanged),
		AutoFixes: p.AutoFixes, Budget: p.Budget, Feature: p.Feature}
	for _, c := range p.Doctrine {
		out.Doctrine = append(out.Doctrine, c.ID)
	}
	return out
}
