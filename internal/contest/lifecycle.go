package contest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/worktree"
)

// ---- watching ---------------------------------------------------------------------------------------------------

// follow reopens a ready contest when a variant runs again (a followup on it), so it is scored again when every
// variant ended; it also tells the stream when a variant's task changed.
func (s *Service) follow() {
	bus := s.o.Tasks.Bus()
	sub := bus.Subscribe(notifyBuf)
	defer func() { sub.Close() }()
	for {
		select {
		case <-s.ctx.Done():
			return
		case n, open := <-sub.C():
			if !open {
				if s.ctx.Err() != nil {
					return
				}
				sub = bus.Subscribe(notifyBuf)
				continue
			}
			if n.Kind != task.NoticeTask || n.Created {
				continue
			}
			d, err := s.o.Tasks.Get(s.ctx, n.TaskID)
			if err != nil || d.ContestID == "" || d.Kind != task.KindNormal {
				continue
			}
			s.reopen(d.ContestID, &d)
		}
	}
}

func (s *Service) reopen(id string, d *api.TaskDetail) {
	c, err := s.update(s.ctx, id, func(c *api.Contest) error {
		if c.Status != api.ContestStatusReady || !task.Pending(d.Status) {
			return errUnchanged
		}
		c.Status = api.ContestStatusRunning
		c.Proposal = nil
		return nil
	})
	if err != nil {
		return
	}
	s.logStep(s.ctx, c.ID, "reopened", map[string]any{"variant": d.Variant, "taskId": d.ID})
	s.watch(c.ID)
}

// watch follows a contest until it needs the lead or is cleaned up.
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
			pending, wake, more := s.step(id)
			if !more {
				return
			}
			changed, release := s.o.Tasks.Bus().TasksChanged(pending)
			d := poll
			if !wake.IsZero() {
				d = max(10*time.Millisecond, min(d, time.Until(wake)))
			}
			timer := time.NewTimer(d)
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

// pendingTasks returns the variant tasks still going.
func (s *Service) pendingTasks(ctx context.Context, c *api.Contest) []string {
	var out []string
	for vi := range c.Variants {
		v := &c.Variants[vi]
		if v.TaskID == "" {
			continue
		}
		if d, err := s.o.Tasks.Get(ctx, v.TaskID); err == nil && task.Pending(d.Status) {
			out = append(out, v.TaskID)
		}
	}
	return out
}

// step moves a contest on as far as its tasks allow. It returns the tasks to wait for, when to look again at the
// latest (the deadline) and whether to keep watching.
func (s *Service) step(id string) (pending []string, wake time.Time, more bool) {
	ctx := s.ctx
	c, err := s.load(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			s.log.Warn("contest: load", "contest", id, "err", err)
			return nil, time.Time{}, true
		}
		return nil, time.Time{}, false
	}
	switch c.Status {
	case api.ContestStatusRunning:
		pending = s.pendingTasks(ctx, &c)
		if len(pending) == 0 {
			s.score(ctx, id)
			return nil, time.Now(), true
		}
		deadline, _ := time.Parse(time.RFC3339Nano, c.DeadlineAt)
		if c.Scorings == 0 && !c.DeadlineHit && !deadline.IsZero() {
			if !s.o.Now().Before(deadline) {
				s.hitDeadline(ctx, id, pending)
				return nil, time.Now(), true
			}
			wake = deadline
		}
		return pending, wake, true
	case api.ContestStatusJudging:
		if c.JudgeRun == nil || c.JudgeRun.RunTaskID == "" {
			s.finishJudge(ctx, id)
			return nil, time.Now(), true
		}
		d, err := s.o.Tasks.Get(ctx, c.JudgeRun.RunTaskID)
		if err == nil && task.Pending(d.Status) {
			return []string{d.ID}, time.Time{}, true
		}
		s.finishJudge(ctx, id)
		return nil, time.Time{}, true
	case api.ContestStatusReady:
		return nil, time.Time{}, false
	case api.ContestStatusApplied, api.ContestStatusCancelled, api.ContestStatusFailed:
		if c.CleanedAt != "" {
			return nil, time.Time{}, false
		}
		pending = s.pendingTasks(ctx, &c)
		if len(pending) > 0 {
			return pending, time.Time{}, true
		}
		if err := s.cleanup(ctx, id); err != nil && ctx.Err() == nil {
			s.log.Warn("contest: cleanup", "contest", id, "err", err)
		}
		return nil, time.Time{}, false // a failed cleanup is retried at the next start
	}
	return nil, time.Time{}, false
}

func (s *Service) hitDeadline(ctx context.Context, id string, pending []string) {
	_, err := s.update(ctx, id, func(c *api.Contest) error {
		c.DeadlineHit = true
		for i := range c.Variants {
			if slices.Contains(pending, c.Variants[i].TaskID) {
				c.Variants[i].DeadlineCancelled = true
			}
		}
		return nil
	})
	if err != nil {
		s.log.Warn("contest: deadline", "contest", id, "err", err)
		return
	}
	for _, t := range pending {
		_, _, _ = s.o.Tasks.Cancel(ctx, t, api.CancelRequest{})
	}
	s.logStep(ctx, id, "deadline", map[string]any{"cancelled": pending})
}

// ---- scoring ----------------------------------------------------------------------------------------------------

// treeOf is the tree of a variant now: its worktree's snapshot, or its final commit once the worktree is gone.
func (s *Service) treeOf(ctx context.Context, v *api.ContestVariant) (string, error) {
	if v.Removed || v.Worktree == "" {
		if v.Commit == "" {
			return "", errors.New("the variant has no worktree and no commit")
		}
		return v.Commit, nil
	}
	if _, err := os.Stat(v.Worktree); err != nil {
		if v.Commit != "" {
			return v.Commit, nil
		}
		return "", err
	}
	tmp := filepath.Join(s.o.Dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "", err
	}
	return s.o.Git.SnapshotTree(ctx, v.Worktree, tmp, linked(v.Env)...)
}

// linked are the shared-folder link paths of a worktree, which no snapshot or commit of it may hold.
func linked(env *api.WorktreeEnv) []string {
	if env == nil {
		return nil
	}
	var out []string
	for _, raw := range env.Linked {
		if rel, _ := worktree.RelPath(raw); rel != "" {
			out = append(out, rel)
		}
	}
	return out
}

// snapshotCommit records the variant's worktree as a commit on top of its HEAD without changing the worktree:
// HEAD, its index and its shared-folder links stay, so a pick that fails leaves the variant as it was and a
// follow-up still runs with its links. The final commit (Seal) is made at cleanup.
func (s *Service) snapshotCommit(ctx context.Context, v *api.ContestVariant, message string) (string, error) {
	tmp := filepath.Join(s.o.Dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return "", err
	}
	tree, err := s.o.Git.SnapshotTree(ctx, v.Worktree, tmp, linked(v.Env)...)
	if err != nil {
		return "", err
	}
	args := append(gitx.Identity(), "commit-tree", tree, "-m", message)
	if head := s.o.Git.Head(ctx, v.Worktree); head != "" {
		args = append(args, "-p", head)
	}
	out, err := s.o.Git.Exec(ctx, v.Worktree, gitx.ExecOptions{}, args...)
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(out))
	if commit == "" {
		return "", errors.New("commit-tree gave no commit")
	}
	return commit, nil
}

func (s *Service) scoreVariant(ctx context.Context, c *api.Contest, v *api.ContestVariant) api.ScoreRow {
	row := api.ScoreRow{Label: v.Label, TaskID: v.TaskID, Status: "missing", Gate: "none", FileList: []string{}}
	d, err := s.o.Tasks.Get(ctx, v.TaskID)
	var dp *api.TaskDetail
	if err == nil {
		dp = &d
		row.Status, row.Backend, row.Model, row.Member, row.Provider = string(d.Status), d.Backend, d.Model, d.Member,
			d.Provider
		row.Tokens = d.TokensUsed
		if n := len(d.Turns); n > 0 {
			last := &d.Turns[n-1]
			if last.Gate != nil {
				row.Gate = last.Gate.State
				for si := range last.Gate.Steps {
					st := &last.Gate.Steps[si]
					if st.Kind == "lane" && st.State == "red" {
						row.LaneViolation = true
					}
				}
			}
			if last.Verifier != nil {
				row.Verifier = last.Verifier.Verdict
			}
			start, err1 := time.Parse(time.RFC3339Nano, d.CreatedAt)
			end, err2 := time.Parse(time.RFC3339Nano, last.EndedAt)
			if err1 == nil && err2 == nil {
				ms := max(0, end.Sub(start).Milliseconds())
				row.DurationMs = &ms
			}
		}
		for i := range d.Turns {
			if by := d.Turns[i].By; by == "gate" || by == "verifier" {
				row.AutoFixTurns++
			}
		}
	}
	if tree, err := s.treeOf(ctx, v); err == nil {
		out, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff", "--numstat",
			"--no-renames", c.BaseSha, tree)
		if err == nil {
			files, added, removed := parseNumstat(string(out))
			row.Files, row.Added, row.Removed = len(files), added, removed
			row.FileList = files[:min(len(files), maxFileList)]
			if dp != nil && dp.Proof != nil && dp.Proof.Lane != nil && len(dp.Proof.Lane.Paths) > 0 {
				row.OutOfLane = len(proof.Outside(dp.Proof.Lane.Paths, files))
			}
		}
	}
	row.Disqualified = disqualification(dp, &row, v.DeadlineCancelled)
	return row
}

// score writes the scoreboard and starts the judge, or proposes the machine's winner.
func (s *Service) score(ctx context.Context, id string) {
	unlock := s.lockContest(id)
	defer unlock()
	c, err := s.load(ctx, id)
	if err != nil || c.Status != api.ContestStatusRunning {
		return
	}
	rows := make([]api.ScoreRow, 0, len(c.Variants))
	for i := range c.Variants {
		rows = append(rows, s.scoreVariant(ctx, &c, &c.Variants[i]))
	}
	ranking := rank(rows)
	judge := ""
	if len(ranking) >= 2 {
		judge = s.judgeMember(ctx, &c)
	}
	var run *api.ContestJudge
	if judge != "" {
		run = s.startJudge(ctx, &c, rows, ranking, judge)
	}
	c2, err := s.update(ctx, id, func(c *api.Contest) error {
		if c.Status != api.ContestStatusRunning {
			return errUnchanged
		}
		c.Scoreboard, c.MachineRanking = rows, ranking
		c.Scorings++
		c.ScoredAt = s.now()
		c.JudgeRun = run
		if run != nil && run.Status == "running" {
			c.Status = api.ContestStatusJudging
		} else {
			ready(c)
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, errUnchanged) {
			s.log.Warn("contest: score", "contest", id, "err", err)
		}
		return
	}
	dq := make([]map[string]any, 0, len(rows))
	for i := range rows {
		dq = append(dq, map[string]any{"label": rows[i].Label, "disqualified": rows[i].Disqualified})
	}
	s.logStep(ctx, id, "scored", map[string]any{"ranking": ranking, "rows": dq})
	if c2.Status == api.ContestStatusReady {
		s.logStep(ctx, id, "ready", map[string]any{"proposal": c2.Proposal})
	}
}

// ready proposes the winner: the judge's when it is qualified and backed by a verified citation, else the
// machine's first.
func ready(c *api.Contest) {
	machine := ""
	if len(c.MachineRanking) > 0 {
		machine = c.MachineRanking[0]
	}
	j := c.JudgeRun
	c.Error = ""
	switch {
	case j != nil && j.Status == "done" && j.WinnerLabel != "" && slices.Contains(c.MachineRanking, j.WinnerLabel) &&
		j.Verified >= 1:
		c.Proposal = &api.ContestProposal{Winner: j.WinnerLabel, Source: "judge", MachineWinner: machine}
	case machine != "":
		p := &api.ContestProposal{Winner: machine, Source: "machine", MachineWinner: machine}
		if j != nil {
			if j.Status == "done" {
				p.Reason = "the judge gave no verified citation for a qualified winner"
			} else {
				p.Reason = "judge " + j.Status
			}
		}
		c.Proposal = p
	default:
		c.Proposal = nil
		c.Error = "no variant qualified (see the scoreboard); pick one explicitly or cancel"
	}
	c.Status = api.ContestStatusReady
}

// judgeMember is the contest's judge: the one named, or the first enabled peer; "" for none.
func (s *Service) judgeMember(ctx context.Context, c *api.Contest) string {
	if c.NoJudge {
		return ""
	}
	if c.Judge != "" {
		if m, err := s.o.Members.Usable(ctx, c.Judge); err == nil {
			return m.ID
		}
		return ""
	}
	list, err := s.o.Members.List(ctx)
	if err != nil {
		return ""
	}
	for i := range list {
		m := &list[i]
		if m.Rank == api.RankPeer && m.Enabled && m.Invalid == "" {
			return m.ID
		}
	}
	return ""
}

// diffText is the diff of a variant against the base, cut at limit bytes.
func (s *Service) diffText(ctx context.Context, c *api.Contest, v *api.ContestVariant, limit int) (text string, truncated bool,
	err error) {
	tree, err := s.treeOf(ctx, v)
	if err != nil {
		return "", false, err
	}
	out, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{Timeout: gitx.ReadTimeout, Limit: limit}, "diff",
		"--no-color", "--no-ext-diff", c.BaseSha, tree)
	var ge *gitx.Error
	if errors.As(err, &ge) && ge.Stderr == "" && len(out) >= limit {
		return string(out), true, nil
	}
	return string(out), false, err
}

func (s *Service) startJudge(ctx context.Context, c *api.Contest, rows []api.ScoreRow, ranking []string,
	member string) *api.ContestJudge {
	top := slices.Clone(ranking[:min(3, len(ranking))])
	rand.Shuffle(len(top), func(i, j int) { top[i], top[j] = top[j], top[i] }) //nolint:gosec // anonymity only
	ids := make([]answer.Identity, 0, len(rows))
	for i := range rows {
		ids = append(ids, answer.Identity{Member: rows[i].Member, Backend: rows[i].Backend, Model: rows[i].Model,
			Provider: rows[i].Provider})
	}
	redact := answer.Redactor(ids)
	j := &api.ContestJudge{Status: "running", Member: member, Letters: map[string]string{}, Ranking: []string{},
		RankingLabels: []string{}, Reasons: []api.Claim{}, At: s.now()}
	var entries []judgeEntry
	for i, l := range top {
		letter := string(letters[i])
		j.Letters[letter] = l
		v := variantOf(c, l)
		row := rowOf(rows, l)
		diff, _, err := s.diffText(ctx, c, v, maxDiff)
		if err != nil {
			diff = "(the diff could not be read: " + err.Error() + ")"
		}
		tree := v.Cwd
		if tree == "" {
			tree = v.Worktree
		}
		entries = append(entries, judgeEntry{Letter: letter, Tree: tree, Files: row.Files, Added: row.Added,
			Removed: row.Removed, Gate: row.Gate, Diff: answer.Clip(redact(diff), judgeDiffMax)})
	}
	prompt := judgeBrief(answer.Clip(redact(c.Prompt), 12000), entries)
	sum, err := s.o.Tasks.DelegateWith(ctx, api.DelegateRequest{Prompt: prompt, Cwd: c.Cwd, Title: "Judge: " + c.Title,
		Member: member, Mode: task.ModePlan}, task.Spawn{Kind: task.KindJudge, ParentID: c.ID})
	if err != nil {
		j.Status, j.Error = "failed", clip(err.Error(), 400)
		return j
	}
	j.RunTaskID = sum.ID
	s.logStep(ctx, c.ID, "judge_start", map[string]any{"member": member, "runTaskId": sum.ID, "letters": j.Letters})
	return j
}

func variantOf(c *api.Contest, l string) *api.ContestVariant {
	for i := range c.Variants {
		if c.Variants[i].Label == l {
			return &c.Variants[i]
		}
	}
	return nil
}

func rowOf(rows []api.ScoreRow, l string) *api.ScoreRow {
	for i := range rows {
		if rows[i].Label == l {
			return &rows[i]
		}
	}
	return &api.ScoreRow{}
}

// finishJudge reads the judge's answer, checks its citations and proposes the winner.
func (s *Service) finishJudge(ctx context.Context, id string) {
	c, err := s.load(ctx, id)
	if err != nil || c.Status != api.ContestStatusJudging {
		return
	}
	j := c.JudgeRun
	if j == nil {
		j = &api.ContestJudge{Status: "failed", Error: "no judge run"}
	} else if j.RunTaskID != "" {
		s.readJudge(ctx, &c, j)
		s.o.Tasks.CloseHiddenRun(j.RunTaskID, "judge "+j.Status)
	}
	_, err = s.update(ctx, id, func(cur *api.Contest) error {
		if cur.Status != api.ContestStatusJudging {
			return errUnchanged
		}
		cur.JudgeRun = j
		ready(cur)
		return nil
	})
	if err == nil {
		s.logStep(ctx, id, "judge_done", map[string]any{"status": j.Status, "winner": j.WinnerLabel,
			"ranking": j.RankingLabels, "verified": j.Verified})
	}
}

func (s *Service) readJudge(ctx context.Context, c *api.Contest, j *api.ContestJudge) {
	j.EndedAt = s.now()
	d, err := s.o.Tasks.Get(ctx, j.RunTaskID)
	if err != nil {
		j.Status, j.Error = "failed", "run not found"
		return
	}
	n := len(d.Turns)
	if d.Status != api.StatusDone || n == 0 || d.Turns[n-1].Status != task.TurnOK || d.Turns[n-1].Response == "" {
		j.Status = "failed"
		j.Error = cmpOr(clip(d.Error, 400), "run "+string(d.Status))
		return
	}
	allowed := ""
	for l := range j.Letters {
		allowed += l
	}
	p := parseJudge(d.Turns[n-1].Response, allowed)
	j.Reasons = []api.Claim{}
	j.Verified = 0
	for _, r := range p.Reasons {
		cl := api.Claim{Text: r.Text, Cites: []api.Citation{}}
		for _, ci := range r.Cites {
			lbl := j.Letters[ci.Letter]
			v := variantOf(c, lbl)
			var cite api.Citation
			if v == nil || v.Worktree == "" {
				cite = api.Citation{Path: ci.Cite.Path, Line: ci.Cite.Line, EndLine: ci.Cite.EndLine, Quote: ci.Cite.Quote,
					State: proof.CiteMissingFile, Detail: "unknown variant", Variant: lbl}
			} else {
				cite = answer.Checked(v.Worktree, ci.Cite, lbl)
			}
			if cite.State == proof.CiteVerified {
				j.Verified++
			}
			cl.Cites = append(cl.Cites, cite)
		}
		j.Reasons = append(j.Reasons, cl)
	}
	j.Winner = p.Winner
	j.WinnerLabel = j.Letters[p.Winner]
	j.Ranking = api.NonNil(p.Ranking)
	j.RankingLabels = []string{}
	for _, l := range p.Ranking {
		j.RankingLabels = append(j.RankingLabels, j.Letters[l])
	}
	j.Status = "done"
}

// ---- diff, pick, cancel, cleanup --------------------------------------------------------------------------------

// Diff returns the diff of a variant against the base.
func (s *Service) Diff(ctx context.Context, id, lbl string) (api.ContestDiff, error) {
	c, err := s.load(ctx, id)
	if err != nil {
		return api.ContestDiff{}, err
	}
	v := variantOf(&c, strings.ToUpper(strings.TrimSpace(lbl)))
	if v == nil {
		return api.ContestDiff{}, fmt.Errorf("%w: contest %s has no variant %s", ErrNotFound, id, lbl)
	}
	text, truncated, err := s.diffText(ctx, &c, v, maxDiff)
	if err != nil {
		return api.ContestDiff{}, err
	}
	return api.ContestDiff{ID: id, Label: v.Label, Base: c.BaseSha, Diff: text,
		Files: gitx.SplitDiff(text, truncated), Truncated: truncated}, nil
}

// PickError is a winner's patch that does not apply to the user's working tree; nothing was changed.
type PickError struct {
	Winner string
	Detail string
	Files  []string
}

func (e *PickError) Error() string {
	return fmt.Sprintf("the %s patch does not apply cleanly to the working tree; nothing was changed: %s", e.Winner,
		e.Detail)
}

// Is makes a PickError a conflict.
func (e *PickError) Is(target error) bool { return target == ErrConflict }

// APIDetails are the details of the conflict error.
func (e *PickError) APIDetails() map[string]any {
	return map[string]any{"reason": "patch_conflict", "winner": e.Winner, "files": e.Files}
}

// Pick applies the winner's changes to the user's working tree (never the index), accepts the winner's task,
// rejects the other finished variants (without a review-history entry: losing is not a defect) and removes the
// worktrees.
func (s *Service) Pick(ctx context.Context, id string, req api.ContestPickRequest) (api.Contest, error) {
	unlock := s.lockContest(id)
	defer unlock()
	c, err := s.load(ctx, id)
	if err != nil {
		return api.Contest{}, err
	}
	if c.Status != api.ContestStatusReady {
		return api.Contest{}, badStatef("contest %s is %s; pick needs ready", id, c.Status)
	}
	lbl := strings.ToUpper(strings.TrimSpace(req.Winner))
	if lbl == "" && c.Proposal != nil {
		lbl = c.Proposal.Winner
	}
	if lbl == "" {
		return api.Contest{}, invalidf("no proposal: name the winner (e.g. \"V2\")")
	}
	v := variantOf(&c, lbl)
	if v == nil {
		return api.Contest{}, invalidf("contest %s has no variant %s", id, lbl)
	}
	d, err := s.o.Tasks.Get(ctx, v.TaskID)
	if err != nil {
		return api.Contest{}, invalidf("variant %s has no task", lbl)
	}
	if task.Pending(d.Status) {
		return api.Contest{}, badStatef("variant %s is still %s", lbl, d.Status)
	}
	if d.Status == api.StatusFailed || d.Status == api.StatusCancelled {
		return api.Contest{}, invalidf("variant %s %s; it cannot win", lbl, d.Status)
	}
	if v.Removed || v.Worktree == "" {
		return api.Contest{}, badStatef("variant %s has no worktree any more", lbl)
	}
	if _, err := os.Stat(v.Worktree); err != nil {
		return api.Contest{}, badStatef("variant %s has no worktree any more", lbl)
	}
	commit, err := s.snapshotCommit(ctx, v, "seelegent contest "+c.ID+" "+lbl)
	if err != nil {
		return api.Contest{}, fmt.Errorf("commit the %s worktree: %w", lbl, err)
	}
	names, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff", "--name-only",
		"--no-renames", c.BaseSha, commit)
	if err != nil {
		return api.Contest{}, err
	}
	var files []string
	for _, f := range strings.Split(string(names), "\n") {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return api.Contest{}, invalidf("variant %s has no changes to apply", lbl)
	}
	patch, err := s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{Limit: maxPatch}, "diff", "--binary", "--full-index",
		"--no-color", "--no-ext-diff", c.BaseSha, commit)
	if err != nil {
		return api.Contest{}, fmt.Errorf("the %s patch: %w", lbl, err)
	}
	patchFile := filepath.Join(s.o.Dir, c.ID+"."+lbl+".patch")
	if err := fsutil.WriteFileAtomic(patchFile, patch, 0o600); err != nil {
		return api.Contest{}, err
	}
	release, err := s.o.Git.Lock(ctx, c.Root)
	if err != nil {
		return api.Contest{}, err
	}
	// The working tree only (no --index, no --3way); git apply changes all or nothing.
	_, err = s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{}, "apply", "--check", "--whitespace=nowarn", patchFile)
	if err == nil {
		_, err = s.o.Git.Exec(ctx, c.Root, gitx.ExecOptions{}, "apply", "--whitespace=nowarn", patchFile)
	}
	release()
	if err != nil {
		detail := err.Error()
		var ge *gitx.Error
		if errors.As(err, &ge) && ge.Stderr != "" {
			detail = ge.Stderr
		}
		detail = clip(answer.Collapse(detail), 800)
		_, _ = s.update(ctx, id, func(c *api.Contest) error {
			c.LastPickError = &api.ContestPickError{Winner: lbl, Detail: detail, At: s.now()}
			return nil
		})
		s.logStep(ctx, id, "pick_conflict", map[string]any{"winner": lbl, "detail": detail})
		return api.Contest{}, &PickError{Winner: lbl, Detail: detail, Files: files}
	}
	by := "lead"
	if req.By == "user" {
		by = "user"
	}
	var warnings []string
	if _, err := s.o.Tasks.ReviewWith(ctx, v.TaskID, api.ReviewRequest{Verdict: api.VerdictAccepted,
		Notes: "contest winner applied"}, task.ReviewOptions{}); err != nil {
		warnings = append(warnings, "winner review: "+clip(err.Error(), 300))
	}
	for i := range c.Variants {
		o := &c.Variants[i]
		if o.Label == lbl || o.TaskID == "" {
			continue
		}
		od, err := s.o.Tasks.Get(ctx, o.TaskID)
		if err != nil || od.Status != api.StatusDone || od.Review != nil {
			continue
		}
		if _, err := s.o.Tasks.ReviewWith(ctx, o.TaskID, api.ReviewRequest{Verdict: api.VerdictRejected,
			Notes: "lost contest " + c.ID}, task.ReviewOptions{SkipHistory: true}); err != nil {
			warnings = append(warnings, o.Label+" review: "+clip(err.Error(), 300))
		}
	}
	if _, err := s.update(ctx, id, func(c *api.Contest) error {
		c.Winner = lbl
		c.Applied = &api.ContestApplied{Commit: commit, Files: files, By: by, At: s.now()}
		c.Status = api.ContestStatusApplied
		c.LastPickError = nil
		c.Warnings = append(api.NonNil(c.Warnings), warnings...)
		if w := variantOf(c, lbl); w != nil {
			w.Commit = commit
		}
		return nil
	}); err != nil {
		return api.Contest{}, err
	}
	s.logStep(ctx, id, "applied", map[string]any{"winner": lbl, "commit": commit, "files": files})
	if err := s.cleanupLocked(ctx, id); err != nil {
		s.log.Warn("contest: cleanup", "contest", id, "err", err)
	}
	return s.Get(ctx, id)
}

// Cancel stops the variants and the judge and removes the worktrees once nothing runs in them.
func (s *Service) Cancel(ctx context.Context, id string) (api.Contest, error) {
	unlock := s.lockContest(id)
	defer unlock()
	c, err := s.update(ctx, id, func(c *api.Contest) error {
		switch c.Status {
		case api.ContestStatusApplied:
			return badStatef("contest %s was applied", id)
		case api.ContestStatusCancelled, api.ContestStatusFailed:
			return errUnchanged
		}
		c.Status = api.ContestStatusCancelled
		c.CancelledAt = s.now()
		return nil
	})
	if errors.Is(err, errUnchanged) {
		c, err = s.load(ctx, id)
	} else if err == nil {
		s.logStep(ctx, id, "cancel", nil)
	}
	if err != nil {
		return api.Contest{}, err
	}
	for vi := range c.Variants {
		v := &c.Variants[vi]
		if v.TaskID != "" {
			if d, err := s.o.Tasks.Get(ctx, v.TaskID); err == nil && task.Pending(d.Status) {
				_, _, _ = s.o.Tasks.Cancel(ctx, v.TaskID, api.CancelRequest{})
			}
		}
	}
	if c.JudgeRun != nil && c.JudgeRun.RunTaskID != "" {
		if d, err := s.o.Tasks.Get(ctx, c.JudgeRun.RunTaskID); err == nil && task.Pending(d.Status) {
			_, _, _ = s.o.Tasks.Cancel(ctx, d.ID, api.CancelRequest{})
		}
		s.o.Tasks.CloseHiddenRun(c.JudgeRun.RunTaskID, "contest cancelled")
	}
	if len(s.pendingTasks(ctx, &c)) == 0 {
		if err := s.cleanupLocked(ctx, id); err != nil {
			s.log.Warn("contest: cleanup", "contest", id, "err", err)
		}
	} else {
		s.watch(id)
	}
	return s.Get(ctx, id)
}

func (s *Service) cleanup(ctx context.Context, id string) error {
	unlock := s.lockContest(id)
	defer unlock()
	return s.cleanupLocked(ctx, id)
}

// cleanupLocked removes every worktree whose task no longer runs: links first, then a commit inside the worktree,
// then plain `git worktree remove`. Problems are recorded per variant and retried at the next start.
func (s *Service) cleanupLocked(ctx context.Context, id string) error {
	c, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	type result struct {
		commit string
		err    error
		done   bool
	}
	results := map[string]result{}
	for i := range c.Variants {
		v := &c.Variants[i]
		if v.Removed || v.Worktree == "" {
			continue
		}
		if d, err := s.o.Tasks.Get(ctx, v.TaskID); err == nil && task.Pending(d.Status) {
			continue
		}
		commit, err := s.trees.Remove(ctx, c.Root, v.Worktree, v.Env, "seelegent contest "+c.ID+" "+v.Label)
		results[v.Label] = result{commit: commit, err: err, done: err == nil}
		if err != nil {
			s.logStep(ctx, id, "cleanup_failed", map[string]any{"variant": v.Label, "error": err.Error()})
		}
	}
	s.trees.RemoveEmpty(filepath.Join(s.o.WorktreeBase, c.ID))
	_, err = s.update(ctx, id, func(c *api.Contest) error {
		all := true
		for i := range c.Variants {
			v := &c.Variants[i]
			if r, ok := results[v.Label]; ok {
				if r.commit != "" && v.Commit == "" {
					v.Commit = r.commit
				}
				v.Removed = r.done
				v.CleanupError = ""
				if r.err != nil {
					v.CleanupError = clip(r.err.Error(), 400)
				}
			}
			if !v.Removed && v.Worktree != "" {
				all = false
			}
		}
		if all {
			c.CleanedAt = s.now()
		}
		return nil
	})
	return err
}
