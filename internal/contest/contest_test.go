package contest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
)

type fakeTasks struct {
	mu      sync.Mutex
	bus     *task.Bus
	tasks   map[string]*api.TaskDetail
	spawns  map[string]task.Spawn
	reviews map[string]string
	skipped map[string]bool
	n       int
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{bus: task.NewBus(), tasks: map[string]*api.TaskDetail{}, spawns: map[string]task.Spawn{},
		reviews: map[string]string{}, skipped: map[string]bool{}}
}

func (f *fakeTasks) Bus() *task.Bus { return f.bus }

func (f *fakeTasks) DelegateWith(_ context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	id := fmt.Sprintf("t%d", f.n)
	kind := cmpOr(sp.Kind, task.KindNormal)
	d := &api.TaskDetail{TaskSummary: api.TaskSummary{ID: id, Status: api.StatusRunning, Kind: kind, Cwd: req.Cwd,
		Member: req.Member, Backend: cmpOr(req.Backend, "agy"), Model: cmpOr(req.Model, req.Tier), ContestID: sp.ContestID,
		Variant: sp.Variant, CreatedAt: api.FormatTime(time.Now())}, Prompt: req.Prompt,
		Turns: []api.Turn{{N: 1, Message: req.Prompt, Status: task.TurnRunning}}}
	f.tasks[id] = d
	f.spawns[id] = sp
	return d.TaskSummary, nil
}

func (f *fakeTasks) Get(_ context.Context, id string) (api.TaskDetail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.tasks[id]
	if d == nil {
		return api.TaskDetail{}, task.ErrNotFound
	}
	cp := *d
	cp.Turns = append([]api.Turn(nil), d.Turns...)
	return cp, nil
}

func (f *fakeTasks) Cancel(_ context.Context, id string, _ api.CancelRequest) (api.TaskSummary, bool, error) {
	f.mu.Lock()
	d := f.tasks[id]
	if d != nil && task.Pending(d.Status) {
		d.Status = api.StatusCancelled
	}
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: id})
	return api.TaskSummary{}, true, nil
}

func (f *fakeTasks) ReviewWith(_ context.Context, id string, req api.ReviewRequest, o task.ReviewOptions) (
	api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviews[id] = req.Verdict
	f.skipped[id] = o.SkipHistory
	f.tasks[id].Review = &api.Review{Verdict: req.Verdict}
	return api.TaskSummary{}, nil
}

func (f *fakeTasks) CloseHiddenRun(string, string) {}

func (f *fakeTasks) finish(id, response string) {
	f.mu.Lock()
	d := f.tasks[id]
	tu := &d.Turns[len(d.Turns)-1]
	tu.Status, tu.Response, tu.EndedAt = task.TurnOK, response, api.FormatTime(time.Now())
	tu.Gate = &api.GateResult{State: api.GateGreen}
	d.Status = api.StatusDone
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: id})
}

type fakeMembers map[string]api.Member

func (f fakeMembers) Usable(_ context.Context, id string) (api.Member, error) {
	m, ok := f[id]
	if !ok {
		return api.Member{}, errors.New("no member " + id)
	}
	return m, nil
}

func (f fakeMembers) List(context.Context) ([]api.Member, error) {
	out := make([]api.Member, 0, len(f))
	for k := range f {
		out = append(out, f[k])
	}
	return out, nil
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c",
		"commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type env struct {
	s     *Service
	tasks *fakeTasks
	root  string
	data  string
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := filepath.Join(dir, "repo")
	write(t, filepath.Join(root, "a.txt"), "alpha\n")
	write(t, filepath.Join(root, "b.txt"), "beta\n")
	// A shared folder (like node_modules), ignored without a trailing slash so its link (a junction on Windows, a
	// symlink elsewhere) is ignored too and Setup links it on every OS.
	write(t, filepath.Join(root, ".gitignore"), "deps\n")
	write(t, filepath.Join(root, "deps", "lib.txt"), "lib\n")
	git(t, root, "init", "-q")
	git(t, root, "config", "core.autocrlf", "false")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init")
	// The user's uncommitted work: a changed file and an untracked one.
	write(t, filepath.Join(root, "b.txt"), "beta\nmine\n")
	write(t, filepath.Join(root, "notes.txt"), "untracked\n")
	ft := newFakeTasks()
	members := fakeMembers{"jud": {ID: "jud", Name: "Judy", Backend: "agy", Rank: api.RankPeer, Enabled: true}}
	full := 0.9
	data := filepath.Join(dir, "data")
	s := New(Options{DB: db, Tasks: ft, Members: members, Git: gitx.New(nil, nil), Dir: filepath.Join(data, "contests"),
		WorktreeBase: filepath.Join(data, "worktrees", "contests"),
		SharedDirs:   func(string) []string { return []string{"deps"} },
		Accounts: func(context.Context) ([]PoolAccount, error) {
			return []PoolAccount{{ID: "a1", Backend: "agy", Usable: true, Remaining: &full},
				{ID: "a2", Backend: "agy", Usable: true}}, nil
		}})
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return &env{s: s, tasks: ft, root: root, data: data}
}

func waitFor(t *testing.T, s *Service, id string, ok func(api.Contest) bool) api.Contest {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		c, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if ok(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("contest is %s: %+v", c.Status, c)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func status(want string) func(api.Contest) bool {
	return func(c api.Contest) bool { return c.Status == want }
}

func TestContestToWinner(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	req := api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Add a greeting.", Cwd: e.root},
		Variants: 2}
	est, err := e.s.Estimate(ctx, req)
	if err != nil || est.Granted != 2 || est.Refused != "" {
		t.Fatalf("estimate = %+v %v", est, err)
	}
	c, err := e.s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != api.ContestStatusRunning || len(c.Variants) != 2 || c.BaseSha == "" {
		t.Fatalf("created = %+v", c)
	}
	v1, v2 := c.Variants[0], c.Variants[1]
	if sp := e.tasks.spawns[v1.TaskID]; sp.ContestID != c.ID || sp.Variant != "V1" || sp.ProjectCwd != c.Cwd {
		t.Errorf("spawn = %+v", sp)
	}
	if got := read(t, filepath.Join(v1.Worktree, "b.txt")); got != "beta\nmine\n" {
		t.Errorf("the worktree misses the user's uncommitted change: %q", got)
	}
	if got := read(t, filepath.Join(v1.Worktree, "notes.txt")); got != "untracked\n" {
		t.Errorf("the worktree misses the untracked file: %q", got)
	}
	write(t, filepath.Join(v1.Worktree, "greet.txt"), "hello world\n")
	write(t, filepath.Join(v2.Worktree, "a.txt"), "alpha\nhello\n")
	write(t, filepath.Join(v2.Worktree, "extra.txt"), "x\ny\nz\n")
	e.tasks.finish(v1.TaskID, "Added greet.txt.")
	e.tasks.finish(v2.TaskID, "Changed a.txt.")

	c = waitFor(t, e.s, c.ID, status(api.ContestStatusJudging))
	if len(c.Scoreboard) != 2 || c.MachineRanking[0] != "V1" || c.Scoreboard[0].Files != 1 {
		t.Fatalf("scoreboard = %+v ranking %v", c.Scoreboard, c.MachineRanking)
	}
	j := c.JudgeRun
	if j == nil || j.Member != "jud" || e.tasks.spawns[j.RunTaskID].Kind != task.KindJudge {
		t.Fatalf("judge = %+v", j)
	}
	jd, _ := e.tasks.Get(ctx, j.RunTaskID)
	if strings.Contains(jd.Prompt, "Judy") || !strings.Contains(jd.Prompt, "greet.txt") {
		t.Errorf("judge brief:\n%s", jd.Prompt)
	}
	l1, l2 := "", ""
	for l, lbl := range j.Letters {
		if lbl == "V2" {
			l2 = l
		} else {
			l1 = l
		}
	}
	// A verified citation about the loser does not count for the winner: Verified stays 1.
	e.tasks.finish(j.RunTaskID, fmt.Sprintf("## Winner\n%s\n## Ranking\n%s\n## Reasons\n"+
		"- It edits the existing file [%s a.txt:2] \"hello\"\n- The other adds a file [%s greet.txt:1] \"hello\"\n",
		l2, l2, l2, l1))
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	if c.Proposal == nil || c.Proposal.Winner != "V2" || c.Proposal.Source != "judge" || c.JudgeRun.Verified != 1 {
		t.Fatalf("proposal = %+v judge %+v", c.Proposal, c.JudgeRun)
	}
	diff, err := e.s.Diff(ctx, c.ID, "v2")
	if err != nil || !strings.Contains(diff.Diff, "+hello") {
		t.Fatalf("diff = %+v %v", diff, err)
	}
	// A ready contest still takes followups on its variants (they reopen it); an applied one does not (F46).
	if st, ended, err := e.s.Ended(ctx, c.ID); err != nil || ended || st != api.ContestStatusReady {
		t.Fatalf("ended while ready = %s %v %v", st, ended, err)
	}

	c, err = e.s.Pick(ctx, c.ID, api.ContestPickRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if st, ended, err := e.s.Ended(ctx, c.ID); err != nil || !ended || st != api.ContestStatusApplied {
		t.Fatalf("ended once applied = %s %v %v", st, ended, err)
	}
	if st, ended, err := e.s.Ended(ctx, "k-gone"); err != nil || !ended || st != "missing" {
		t.Fatalf("ended of a missing contest = %s %v %v", st, ended, err)
	}
	if c.Status != api.ContestStatusApplied || c.Winner != "V2" || c.CleanedAt == "" || len(c.Applied.Files) != 2 {
		t.Fatalf("picked = %+v", c)
	}
	if got := read(t, filepath.Join(e.root, "a.txt")); got != "alpha\nhello\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := read(t, filepath.Join(e.root, "b.txt")); got != "beta\nmine\n" {
		t.Errorf("the user's uncommitted change was lost: %q", got)
	}
	if got := read(t, filepath.Join(e.root, "notes.txt")); got != "untracked\n" {
		t.Errorf("the user's untracked file was lost: %q", got)
	}
	if staged := git(t, e.root, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the index changed: %q", staged)
	}
	for _, v := range c.Variants {
		if !v.Removed {
			t.Errorf("%s not removed: %s", v.Label, v.CleanupError)
		}
		if _, err := os.Stat(v.Worktree); !os.IsNotExist(err) {
			t.Errorf("%s worktree left: %v", v.Label, err)
		}
	}
	if list := git(t, e.root, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("worktrees left:\n%s", list)
	}
	if e.tasks.reviews[v2.TaskID] != api.VerdictAccepted || e.tasks.reviews[v1.TaskID] != api.VerdictRejected ||
		!e.tasks.skipped[v1.TaskID] {
		t.Errorf("reviews = %v skipped %v", e.tasks.reviews, e.tasks.skipped)
	}
	if evs, err := e.s.Events(ctx, c.ID); err != nil || len(evs) < 5 {
		t.Errorf("events = %d %v", len(evs), err)
	}
}

func TestContestDeadlineConflictAndCancel(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	c, err := e.s.Create(ctx, api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Change a.", Cwd: e.root},
		Variants: 2, NoJudge: true, DeadlineMinutes: 1})
	if err != nil {
		t.Fatal(err)
	}
	v1 := c.Variants[0]
	if v1.Env == nil || len(v1.Env.Linked) != 1 {
		t.Fatalf("V1 env = %+v", v1.Env)
	}
	write(t, filepath.Join(v1.Worktree, "a.txt"), "alpha\nfrom V1\n")
	e.tasks.finish(v1.TaskID, "done")
	// The deadline passes with V2 still running: V2 is cancelled and disqualified.
	_, err = e.s.update(ctx, c.ID, func(c *api.Contest) error {
		c.DeadlineAt = api.FormatTime(time.Now().Add(-time.Second))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.s.mu.Lock()
	delete(e.s.watching, c.ID) // wake the watcher at once
	e.s.mu.Unlock()
	e.s.watch(c.ID)
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	if !c.DeadlineHit || c.Scoreboard[1].Disqualified != "deadline" || c.Proposal.Winner != "V1" ||
		c.JudgeRun != nil {
		t.Fatalf("after the deadline = %+v", c)
	}
	// The user changed the same file meanwhile: the patch does not apply and nothing changes.
	write(t, filepath.Join(e.root, "a.txt"), "alpha changed by the user\n")
	_, err = e.s.Pick(ctx, c.ID, api.ContestPickRequest{Winner: "V1"})
	var pe *PickError
	if !errors.As(err, &pe) || !errors.Is(err, ErrConflict) {
		t.Fatalf("pick = %v", err)
	}
	if got := read(t, filepath.Join(e.root, "a.txt")); got != "alpha changed by the user\n" {
		t.Errorf("a.txt = %q", got)
	}
	// The variant is as it was: HEAD at the base, its change uncommitted, its shared-folder link in place, so a
	// follow-up runs with it and still has a diff.
	if head := strings.TrimSpace(git(t, v1.Worktree, "rev-parse", "HEAD")); head != c.BaseSha {
		t.Errorf("V1 HEAD = %s, base %s", head, c.BaseSha)
	}
	if st := git(t, v1.Worktree, "status", "--porcelain", "--", "a.txt"); !strings.Contains(st, "a.txt") {
		t.Errorf("V1 status = %q", st)
	}
	if got := read(t, filepath.Join(v1.Worktree, "deps", "lib.txt")); got != "lib\n" {
		t.Errorf("V1 deps/lib.txt = %q", got)
	}
	c, _ = e.s.Get(ctx, c.ID)
	if c.LastPickError == nil || c.Status != api.ContestStatusReady {
		t.Errorf("after the conflict = %+v", c)
	}
	c, err = e.s.Cancel(ctx, c.ID)
	if err != nil || c.Status != api.ContestStatusCancelled || c.CleanedAt == "" {
		t.Fatalf("cancel = %+v %v", c, err)
	}
	if got := read(t, filepath.Join(e.root, "b.txt")); got != "beta\nmine\n" {
		t.Errorf("b.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.data, "worktrees", "contests", c.ID)); !os.IsNotExist(err) {
		t.Errorf("contest folder left: %v", err)
	}
	if _, err := e.s.Pick(ctx, c.ID, api.ContestPickRequest{}); !errors.Is(err, ErrBadState) {
		t.Errorf("pick after cancel = %v", err)
	}
}

// TestDiffsOutliveGC: after a pick and its cleanup, the commits Seelegent made are referenced by nothing in the
// user's repository, and git gc prunes them. The contest and each variant's diff still read, from Seelegent's own
// data folder; nothing is added to the repository's refs.
func TestDiffsOutliveGC(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	refsBefore := git(t, e.root, "for-each-ref")
	c, err := e.s.Create(ctx, api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Greet.", Cwd: e.root},
		Variants: 2, NoJudge: true})
	if err != nil {
		t.Fatal(err)
	}
	v1, v2 := c.Variants[0], c.Variants[1]
	write(t, filepath.Join(v1.Worktree, "greet.txt"), "hello world\n")
	write(t, filepath.Join(v2.Worktree, "a.txt"), "alpha\nhello\n")
	e.tasks.finish(v1.TaskID, "Added greet.txt.")
	e.tasks.finish(v2.TaskID, "Changed a.txt.")
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	if c, err = e.s.Pick(ctx, c.ID, api.ContestPickRequest{Winner: "V1"}); err != nil {
		t.Fatal(err)
	}
	if c.CleanedAt == "" || c.Applied == nil || c.Applied.Commit == "" {
		t.Fatalf("picked = %+v", c)
	}
	if got := git(t, e.root, "for-each-ref"); got != refsBefore {
		t.Errorf("refs changed:\n%s\nwas\n%s", got, refsBefore)
	}
	git(t, e.root, "reflog", "expire", "--expire=now", "--expire-unreachable=now", "--all")
	git(t, e.root, "gc", "-q", "--prune=now")
	gone := []string{c.Applied.Commit}
	for _, v := range c.Variants {
		if v.Commit != "" {
			gone = append(gone, v.Commit)
		}
	}
	for _, sha := range gone {
		cmd := exec.CommandContext(ctx, "git", "cat-file", "-e", sha+"^{commit}")
		cmd.Dir = e.root
		if cmd.Run() == nil {
			t.Fatalf("commit %s survived gc; the test does not show what it should", sha)
		}
	}

	c, err = e.s.Get(ctx, c.ID)
	if err != nil || c.Status != api.ContestStatusApplied || len(c.Scoreboard) != 2 {
		t.Fatalf("contest after gc = %+v %v", c, err)
	}
	want := map[string][]string{"V1": {"greet.txt", "+hello world"}, "V2": {"a.txt", "+hello"}}
	for lbl, parts := range want {
		d, err := e.s.Diff(ctx, c.ID, lbl)
		if err != nil {
			t.Fatalf("%s diff after gc: %v", lbl, err)
		}
		if len(d.Files) != 1 || d.Files[0].Path != parts[0] || !strings.Contains(d.Diff, parts[1]) || d.Truncated {
			t.Errorf("%s diff = %+v", lbl, d)
		}
	}
	// The scoreboard's numbers come from the same stored copy when a removed variant is scored again.
	rows := e.s.scoreVariant(ctx, &c, &c.Variants[1])
	if rows.Files != 1 || rows.Added != 1 || rows.Removed != 0 {
		t.Errorf("V2 row from the stored copy = %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(e.data, "contests", c.ID, "V1.patch")); err != nil {
		t.Errorf("the applied patch was not kept: %v", err)
	}

	// A contest cleaned up before the store existed has no copy: a clear error, never an empty diff.
	if err := os.Remove(filepath.Join(e.data, "contests", c.ID, "V2.json")); err != nil {
		t.Fatal(err)
	}
	_, err = e.s.Diff(ctx, c.ID, "V2")
	var de *DataError
	if !errors.As(err, &de) || de.Reason != DataGone || !errors.Is(err, ErrNotFound) {
		t.Errorf("diff without a stored copy = %v", err)
	}
	// A damaged copy is told apart from a variant without changes.
	path := filepath.Join(e.data, "contests", c.ID, "V1.json")
	write(t, path, strings.Replace(read(t, path), "hello world", "hello there", 1))
	if _, err = e.s.Diff(ctx, c.ID, "V1"); !errors.As(err, &de) || de.Reason != DataDamaged ||
		!errors.Is(err, ErrBadState) {
		t.Errorf("diff of a damaged copy = %v", err)
	}
}

// TestCleanupKeepsWorktreeWhenStoreFails: a worktree whose changes cannot be stored is not removed; the failure is
// recorded and the next cleanup finishes the job.
func TestCleanupKeepsWorktreeWhenStoreFails(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	c, err := e.s.Create(ctx, api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Greet.", Cwd: e.root},
		Variants: 2, NoJudge: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range c.Variants {
		write(t, filepath.Join(v.Worktree, "greet.txt"), "hello "+v.Label+"\n")
		e.tasks.finish(v.TaskID, "done")
	}
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	// A file where the contest's data folder goes: nothing can be stored.
	block := filepath.Join(e.data, "contests", c.ID)
	write(t, block, "in the way")
	if c, err = e.s.Cancel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, v := range c.Variants {
		if v.Removed || !strings.Contains(v.CleanupError, "storing its changes failed") {
			t.Errorf("%s = removed %v, error %q", v.Label, v.Removed, v.CleanupError)
		}
		if _, err := os.Stat(v.Worktree); err != nil {
			t.Errorf("%s worktree was removed without a stored copy: %v", v.Label, err)
		}
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	if err := e.s.cleanup(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	c, _ = e.s.Get(ctx, c.ID)
	for _, v := range c.Variants {
		d, err := e.s.Diff(ctx, c.ID, v.Label)
		if !v.Removed || v.CleanupError != "" || err != nil || !strings.Contains(d.Diff, "+hello "+v.Label) {
			t.Errorf("%s after the retry = %+v, diff %v", v.Label, v, err)
		}
	}
}

// readyWithStoredV2 makes a two-variant contest whose V2 changed a.txt, waits until it is ready and stores V2's
// changes, as the first cleanup attempt does before its `git worktree remove` stops partway.
func readyWithStoredV2(t *testing.T, e *env) api.Contest {
	t.Helper()
	ctx := t.Context()
	c, err := e.s.Create(ctx, api.ContestRequest{DelegateRequest: api.DelegateRequest{Prompt: "Greet.", Cwd: e.root},
		Variants: 2, NoJudge: true})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(c.Variants[0].Worktree, "greet.txt"), "hello world\n")
	write(t, filepath.Join(c.Variants[1].Worktree, "a.txt"), "alpha\nhello\n")
	e.tasks.finish(c.Variants[0].TaskID, "Added greet.txt.")
	e.tasks.finish(c.Variants[1].TaskID, "Changed a.txt.")
	c = waitFor(t, e.s, c.ID, status(api.ContestStatusReady))
	if err := e.s.storeVariant(ctx, &c, &c.Variants[1]); err != nil {
		t.Fatal(err)
	}
	return c
}

func wantStoredV2(t *testing.T, e *env, id string) {
	t.Helper()
	d, err := e.s.Diff(t.Context(), id, "V2")
	if err != nil {
		t.Fatalf("V2 diff: %v", err)
	}
	if len(d.Files) != 1 || d.Files[0].Path != "a.txt" || !strings.Contains(d.Diff, "+hello") ||
		strings.Contains(d.Diff, "deleted file") {
		t.Errorf("V2 diff = %s", d.Diff)
	}
}

// A `git worktree remove` that stopped partway with .git still there: the retry must not replace the stored copy
// with the half-deleted tree.
func TestStoredCopyOutlivesHalfRemovedWorktree(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	c := readyWithStoredV2(t, e)
	wt := c.Variants[1].Worktree
	for _, f := range []string{"a.txt", "b.txt"} {
		if err := os.Remove(filepath.Join(wt, f)); err != nil {
			t.Fatal(err)
		}
	}
	c, err := e.s.Pick(ctx, c.ID, api.ContestPickRequest{Winner: "V1"})
	if err != nil {
		t.Fatal(err)
	}
	if v := c.Variants[1]; !v.Removed || v.CleanupError != "" {
		t.Errorf("V2 after the pick = %+v", v)
	}
	wantStoredV2(t, e, c.ID)
	if rows := e.s.scoreVariant(ctx, &c, &c.Variants[1]); rows.Files != 1 || rows.Added != 1 || rows.Removed != 0 {
		t.Errorf("V2 row = %+v", rows)
	}
}

// A `git worktree remove` that stopped partway after .git went (NTFS deletes it first): the views read the stored
// copy instead of running git in a broken folder, and cleanup deletes the folder without git and without ever
// following a link: the shared-folder link and a link the worker made into an outside folder leave their targets
// intact.
func TestCleanupDiscardsBrokenWorktree(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	c := readyWithStoredV2(t, e)
	wt := c.Variants[1].Worktree
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, filepath.Join(outside, "keep", "data.txt"), "precious\n")
	makeDirLink(t, outside, filepath.Join(wt, "ext"))
	for _, f := range []string{".git", "a.txt"} {
		if err := os.Remove(filepath.Join(wt, f)); err != nil {
			t.Fatal(err)
		}
	}
	wantStoredV2(t, e, c.ID)
	c, err := e.s.Pick(ctx, c.ID, api.ContestPickRequest{Winner: "V1"})
	if err != nil {
		t.Fatal(err)
	}
	if v := c.Variants[1]; !v.Removed || v.CleanupError != "" || c.CleanedAt == "" {
		t.Errorf("V2 after the pick = %+v (cleaned %q)", v, c.CleanedAt)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the broken worktree is still there: %v", err)
	}
	if got := git(t, e.root, "worktree", "list", "--porcelain"); strings.Contains(got, "V2") {
		t.Errorf("git still lists V2:\n%s", got)
	}
	if got := read(t, filepath.Join(outside, "keep", "data.txt")); got != "precious\n" {
		t.Errorf("the outside folder lost its file: %q", got)
	}
	if got := read(t, filepath.Join(e.root, "deps", "lib.txt")); got != "lib\n" {
		t.Errorf("the shared folder lost its file: %q", got)
	}
	wantStoredV2(t, e, c.ID)
}

// makeDirLink makes link point at the folder target: a junction on Windows (what a worker's tools make without
// privileges), a symbolic link elsewhere.
func makeDirLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	out, err := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, out)
	}
	if got := read(t, filepath.Join(link, "keep", "data.txt")); got != "precious\n" {
		t.Fatalf("the junction does not reach its target: %q", got)
	}
}

// A cut diff ends at a line end when one fits, else on a character boundary: never half a UTF-8 character.
func TestCutDiff(t *testing.T) {
	for _, c := range []struct {
		text  string
		limit int
		want  string
	}{
		{"ab\ncd\n", 10, "ab\ncd\n"},
		{"ab\ncdé\n", 6, "ab\n"},
		{"aé", 2, "a"},
		{"日本語", 7, "日本"},
		{"日本語", 2, ""},
	} {
		if got := cutDiff(c.text, c.limit); got != c.want {
			t.Errorf("cutDiff(%q, %d) = %q, want %q", c.text, c.limit, got, c.want)
		}
	}
}

func TestCreateValidationAndQuota(t *testing.T) {
	e := setup(t)
	ctx := t.Context()
	base := api.DelegateRequest{Prompt: "x", Cwd: e.root}
	for _, r := range []api.ContestRequest{
		{DelegateRequest: base, Variants: 1},
		{DelegateRequest: base, Variants: 6},
		{DelegateRequest: api.DelegateRequest{Prompt: "x", Cwd: t.TempDir()}, Variants: 2},
		{DelegateRequest: api.DelegateRequest{Prompt: "x", Cwd: e.root, After: []string{"t1"}}, Variants: 2},
		{DelegateRequest: base, Variants: 2, Judge: "nobody"},
		{DelegateRequest: base, Variants: 2, DeadlineMinutes: MaxDeadline + 1},
	} {
		if _, err := e.s.Create(ctx, r); !errors.Is(err, ErrInvalid) {
			t.Errorf("Create(%+v) = %v", r, err)
		}
	}
	// Three variants on two agy accounts with two slots each: all granted. Five: one dropped, still a contest.
	low := 0.1
	st := config.Defaults()
	accounts := []PoolAccount{{ID: "a", Backend: "agy", Usable: true}, {ID: "b", Backend: "agy", Usable: true, Busy: 2}}
	member := func(string) (api.Member, bool) { return api.Member{}, false }
	specs := defaultSpecs(3, api.VariantSpec{}, st, accounts, member)
	keep, est, err := quotaPlan(specs, 0, st, accounts, member)
	if err != nil || len(keep) != 2 || len(est.Dropped) != 1 || !est.Reduced {
		t.Errorf("plan = %v %+v %v", keep, est, err)
	}
	_, _, err = quotaPlan([]api.VariantSpec{{Backend: "agy"}, {Backend: "agy"}}, 2, st,
		[]PoolAccount{{ID: "a", Backend: "agy", Usable: true, Remaining: &low}}, member)
	var qe *QuotaError
	if !errors.As(err, &qe) {
		t.Errorf("low quota = %v", err)
	}
}

func TestParseJudge(t *testing.T) {
	a := parseJudge("## Winner\n**B**\n## Ranking\nB>A>C\n## Reasons\n- B is cleaner [B src/x.go:3-4] \"x := \"y\"\" "+
		"vs [A src/y.go:9] \"z\"\n- unsupported claim\n  continued\n- [D z.go:1] ignored letter", "ABC")
	if a.Winner != "B" || strings.Join(a.Ranking, "") != "BAC" || len(a.Reasons) != 3 {
		t.Fatalf("judge = %+v", a)
	}
	r := a.Reasons[0]
	if len(r.Cites) != 2 || r.Cites[0].Cite.Quote != `x := "y"` || r.Cites[0].Cite.EndLine != 4 ||
		r.Cites[1].Letter != "A" || r.Text != "B is cleaner" {
		t.Errorf("reason = %+v", r)
	}
	if a.Reasons[1].Text != "unsupported claim continued" || len(a.Reasons[2].Cites) != 0 {
		t.Errorf("reasons = %+v", a.Reasons)
	}
}
