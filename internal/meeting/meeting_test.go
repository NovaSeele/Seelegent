package meeting

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// fakeTasks is a task engine whose runs the test answers.
type fakeTasks struct {
	mu     sync.Mutex
	bus    *task.Bus
	tasks  map[string]*api.TaskDetail
	n      int
	closed map[string]bool
	spawns map[string]task.Spawn
}

func newFakeTasks() *fakeTasks {
	return &fakeTasks{bus: task.NewBus(), tasks: map[string]*api.TaskDetail{}, closed: map[string]bool{},
		spawns: map[string]task.Spawn{}}
}

func (f *fakeTasks) Bus() *task.Bus { return f.bus }

func (f *fakeTasks) DelegateWith(_ context.Context, req api.DelegateRequest, sp task.Spawn) (api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Member == "broken" {
		return api.TaskSummary{}, errors.New("no account")
	}
	f.n++
	id := fmt.Sprintf("t%d", f.n)
	kind := sp.Kind
	if kind == "" {
		kind = task.KindNormal
	}
	d := &api.TaskDetail{TaskSummary: api.TaskSummary{ID: id, Status: api.StatusRunning, Kind: kind, Cwd: req.Cwd,
		Member: req.Member, Backend: "agy", Model: "m1", TurnCount: 1, MeetingID: sp.MeetingID},
		Prompt: req.Prompt, Turns: []api.Turn{{N: 1, Message: req.Prompt, Status: task.TurnRunning}}}
	f.tasks[id] = d
	f.spawns[id] = sp
	return d.TaskSummary, nil
}

func (f *fakeTasks) Followup(_ context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.tasks[id]
	if d == nil {
		return api.TaskSummary{}, task.ErrNotFound
	}
	d.Turns = append(d.Turns, api.Turn{N: len(d.Turns) + 1, Message: req.Message, Status: task.TurnRunning})
	d.Status, d.TurnCount = api.StatusRunning, len(d.Turns)
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
		d.Turns[len(d.Turns)-1].Status = task.TurnCancelled
	}
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: id})
	return api.TaskSummary{}, true, nil
}

func (f *fakeTasks) CloseHiddenRun(id, _ string) {
	f.mu.Lock()
	f.closed[id] = true
	f.mu.Unlock()
}

// answer ends the running turn of id with text.
func (f *fakeTasks) answer(id, text string) {
	f.mu.Lock()
	d := f.tasks[id]
	tu := &d.Turns[len(d.Turns)-1]
	tu.Status, tu.Response, tu.EndedAt = task.TurnOK, text, "2026-11-07T10:00:00Z"
	d.Status = api.StatusDone
	f.mu.Unlock()
	f.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: id})
}

func (f *fakeTasks) lastMessage(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.tasks[id]
	return d.Turns[len(d.Turns)-1].Message
}

type fakeMembers map[string]api.Member

func (f fakeMembers) Usable(_ context.Context, id string) (api.Member, error) {
	m, ok := f[id]
	if !ok {
		return api.Member{}, errors.New("no member " + id)
	}
	return m, nil
}

func setup(t *testing.T) (*Service, *fakeTasks, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src", "cache.go"), []byte("package src\n\nvar ttl = 0\n"),
		0o600); err != nil {
		t.Fatal(err)
	}
	ft := newFakeTasks()
	members := fakeMembers{
		"ada":    {ID: "ada", Name: "Ada", Backend: "agy", Rank: api.RankPeer, Enabled: true},
		"bob":    {ID: "bob", Name: "Bobby", Backend: "codex", Rank: api.RankSenior, Enabled: true},
		"broken": {ID: "broken", Name: "Broken", Backend: "agy", Rank: api.RankWorker, Enabled: true},
	}
	s := New(Options{DB: db, Tasks: ft, Members: members,
		Root: func(context.Context, string) (string, error) { return repo, nil }})
	if err := s.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, ft, repo
}

func waitStatus(t *testing.T, s *Service, id, want string) api.Meeting {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		m, err := s.Get(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Status == want {
			return m
		}
		if time.Now().After(deadline) {
			t.Fatalf("meeting is %s, want %s", m.Status, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func taskOf(m api.Meeting, member string) string {
	for i := range m.Attendees {
		if a := &m.Attendees[i]; a.Member == member {
			return a.TaskID
		}
	}
	return ""
}

func labelOf(m api.Meeting, member string) string {
	for i := range m.Attendees {
		if a := &m.Attendees[i]; a.Member == member {
			return a.Label
		}
	}
	return ""
}

func TestMeetingToOutcome(t *testing.T) {
	s, ft, repo := setup(t)
	ctx := t.Context()
	req := api.MeetingRequest{Question: "Cache or not?", Options: []string{"Keep the cache", "Drop it"}, Cwd: repo,
		Context: []string{"src/cache.go"}, MaxRounds: 2, Sealed: "Drop it.",
		Attendees: []api.AttendeeRef{{Member: "ada"}, {Member: "bob"}}}
	est, err := s.Estimate(ctx, req)
	if err != nil || est.Runs != 5 || est.ByBackend["agy"] != 3 || est.ByBackend["codex"] != 2 {
		t.Fatalf("estimate = %+v %v", est, err)
	}
	m, err := s.Open(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != api.MeetingStatusRound || len(m.Rounds) != 1 || m.Sealed == nil || len(m.Sealed.Hash) != 64 {
		t.Fatalf("opened = %+v", m)
	}
	ada, bob := taskOf(m, "ada"), taskOf(m, "bob")
	if sp := ft.spawns[ada]; sp.Kind != task.KindMeeting || sp.MeetingID != m.ID {
		t.Errorf("spawn = %+v", sp)
	}
	brief := ft.lastMessage(ada)
	if !strings.Contains(brief, "You are "+labelOf(m, "ada")) || !strings.Contains(brief, "A. Keep the cache") ||
		!strings.Contains(brief, "src/cache.go") || strings.Contains(brief, "Drop it.") {
		t.Errorf("round 1 brief:\n%s", brief)
	}
	ft.answer(ada, "## Stance\nOption: B\nThe cache is never read. Ada thinks so.\n## Confidence\n80\n## Claims\n"+
		"- ttl is zero [src/cache.go:3] \"var ttl = 0\"\n## Questions for the lead\n- Is the cache used in production?\n")
	ft.answer(bob, "## Stance\nOption: B\nBobby agrees with dropping.\n## Confidence\n60\n## Questions for the lead\n- none\n")
	m = waitStatus(t, s, m.ID, api.MeetingStatusWaitingLead)
	if len(m.LeadQA) != 1 || m.LeadQA[0].ID != "q1" {
		t.Fatalf("questions = %+v", m.LeadQA)
	}
	if a := answerOf(&m.Rounds[0], labelOf(m, "ada")); a == nil || a.Option != "B" || *a.Confidence != 80 ||
		a.Claims[0].Cites[0].State != "verified" {
		t.Fatalf("ada's answer = %+v", a)
	}
	if _, err := s.Decide(ctx, m.ID, api.MeetingDecideRequest{Decision: "x"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("decide while waiting = %v", err)
	}
	if _, err := s.Post(ctx, m.ID, api.MeetingPostRequest{Answers: []api.QuestionAnswer{{ID: "q9", Answer: "x"}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown question = %v", err)
	}
	m, err = s.Post(ctx, m.ID, api.MeetingPostRequest{Answers: []api.QuestionAnswer{{ID: "q1", Answer: "Yes, daily."}},
		Note: "Think about memory."})
	if err != nil || m.Status != api.MeetingStatusRound || m.Round != 2 {
		t.Fatalf("post = %+v %v", m.Status, err)
	}
	msg := ft.lastMessage(ada)
	for _, want := range []string{"Round 2 of 2", "Answers from the lead", "Yes, daily.", "Think about memory.",
		"Stance: Drop it.", "[redacted] agrees with dropping"} {
		if !strings.Contains(msg, want) {
			t.Errorf("round 2 message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Bobby") {
		t.Errorf("round 2 message names another attendee:\n%s", msg)
	}
	ft.answer(ada, "## Stance\nOption: B\nStill drop.\n## Confidence\n85\n## Response to others\n"+
		"Strongest opposing argument: none\nMoved: no - nothing new\n")
	ft.answer(bob, "## Stance\nOption: B\nDrop.\n## Confidence\n60\n")
	m = waitStatus(t, s, m.ID, api.MeetingStatusDeciding)
	m, err = s.Decide(ctx, m.ID, api.MeetingDecideRequest{Decision: "Keep the cache for now.", Option: "a",
		Rationale: "Production uses it."})
	if err != nil || m.Status != api.MeetingStatusDissent || len(m.DissentRuns) != 1 || len(m.AdviceNotTaken) != 1 ||
		m.AdviceNotTaken[0].Member != "bob" {
		t.Fatalf("decide = %+v %v", m, err)
	}
	if !strings.Contains(ft.lastMessage(ada), "Dissent or Concede") {
		t.Errorf("dissent message:\n%s", ft.lastMessage(ada))
	}
	ft.answer(ada, "## Position\nDissent\n## Statement\nThe cache will rot.\n## Claims\n- see [src/cache.go:3]\n")
	m = waitStatus(t, s, m.ID, api.MeetingStatusClosed)
	if len(m.Dissents) != 1 || m.Dissents[0].Kind != "dissent" || len(m.Dissents[0].WatchPaths) != 1 ||
		m.Dissents[0].WatchPaths[0] != "src/cache.go" {
		t.Fatalf("dissents = %+v", m.Dissents)
	}
	if !ft.closed[ada] || !ft.closed[bob] {
		t.Error("attendee runs were not closed")
	}
	if !strings.Contains(m.Minutes, "# Minutes: Cache or not?") || !strings.Contains(m.Minutes, "formal dissent") {
		t.Errorf("minutes:\n%s", m.Minutes)
	}
	res, err := s.Act(ctx, m.ID, api.MeetingActRequest{Items: []api.ActItem{{Text: "Document the cache."}}})
	if err != nil || len(res.Tasks) != 1 {
		t.Fatalf("act = %+v %v", res, err)
	}
	act := ft.tasks[res.Tasks[0]]
	if act.Kind != task.KindNormal || ft.spawns[act.ID].MeetingID != m.ID ||
		!strings.Contains(act.Prompt, "Dissent on record") || !strings.Contains(act.Prompt, "Chosen option: A") {
		t.Errorf("action task = %+v", act)
	}

	// A later rejected task on the watched file is a signal for the dissent.
	ft.mu.Lock()
	ft.tasks["later"] = &api.TaskDetail{TaskSummary: api.TaskSummary{ID: "later", Status: api.StatusDone,
		Kind: task.KindNormal, Cwd: repo, Review: &api.Review{Verdict: api.VerdictRejected},
		UpdatedAt: api.FormatTime(time.Now().Add(time.Minute))}, RepoRoot: repo,
		Turns: []api.Turn{{N: 1, Status: task.TurnOK, Files: []string{"src/cache.go"}}}}
	ft.mu.Unlock()
	ft.bus.Publish(task.Notice{Kind: task.NoticeTask, TaskID: "later"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		m, err = s.Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(m.Dissents[0].Signals) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no dissent signal")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sg := m.Dissents[0].Signals[0]; sg.TaskID != "later" || sg.Reason != "rejected" {
		t.Errorf("signal = %+v", sg)
	}
	if _, err := s.DissentOutcome(ctx, m.ID, "d1", "vindicated"); err != nil {
		t.Fatal(err)
	}
	track, err := s.Track(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byMember := map[string]api.MemberTrack{}
	for _, r := range track {
		byMember[r.Member] = r
	}
	if r := byMember["ada"]; r.Meetings != 1 || r.Dissents != 1 || r.Signals != 1 || r.Vindicated != 1 {
		t.Errorf("ada's record = %+v", r)
	}
	if r := byMember["bob"]; r.AdviceNotTaken != 1 {
		t.Errorf("bob's record = %+v", r)
	}
	evs, err := s.Events(ctx, m.ID)
	if err != nil || len(evs) < 8 || evs[0].Type != "open" {
		t.Errorf("events = %d %v", len(evs), err)
	}
	list, err := s.List(ctx, 0)
	if err != nil || len(list) != 1 || list[0].Dissents != 1 {
		t.Errorf("list = %+v %v", list, err)
	}
}

func TestMeetingStableAndCancel(t *testing.T) {
	s, ft, repo := setup(t)
	ctx := t.Context()
	m, err := s.Open(ctx, api.MeetingRequest{Question: "Rename?", Cwd: repo,
		Attendees: []api.AttendeeRef{{Member: "ada", Rank: api.RankWorker}, {Member: "broken"}}})
	if err != nil {
		t.Fatal(err)
	}
	if a := answerOf(&m.Rounds[0], labelOf(m, "broken")); a == nil || !a.Absent {
		t.Fatalf("broken attendee = %+v", a)
	}
	ada := taskOf(m, "ada")
	ft.answer(ada, "## Stance\nYes.\n## Confidence\n70\n")
	m = waitStatus(t, s, m.ID, api.MeetingStatusRound)
	deadline := time.Now().Add(10 * time.Second)
	for m.Round != 2 {
		if time.Now().After(deadline) {
			t.Fatal("no round 2")
		}
		time.Sleep(10 * time.Millisecond)
		if m, err = s.Get(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	ft.answer(ada, "## Stance\nYes.\n## Confidence\n72\n## Response to others\nMoved: no\n")
	m = waitStatus(t, s, m.ID, api.MeetingStatusDeciding) // stable before round 3
	if m.Round != 2 {
		t.Errorf("round = %d", m.Round)
	}
	m, err = s.Decide(ctx, m.ID, api.MeetingDecideRequest{Decision: "Rename it."})
	if err != nil || m.Status != api.MeetingStatusClosed {
		t.Fatalf("decide with a worker only = %s %v", m.Status, err)
	}
	if _, err := s.Cancel(ctx, m.ID); !errors.Is(err, ErrBadState) {
		t.Errorf("cancel a closed meeting = %v", err)
	}

	m2, err := s.Open(ctx, api.MeetingRequest{Question: "Again?", Cwd: repo,
		Attendees: []api.AttendeeRef{{Member: "ada"}}})
	if err != nil {
		t.Fatal(err)
	}
	m2, err = s.Cancel(ctx, m2.ID)
	if err != nil || m2.Status != api.MeetingStatusCancelled {
		t.Fatalf("cancel = %s %v", m2.Status, err)
	}
	if d, _ := ft.Get(ctx, taskOf(m2, "ada")); d.Status != api.StatusCancelled {
		t.Errorf("run = %s", d.Status)
	}
}

func TestOpenValidation(t *testing.T) {
	s, _, repo := setup(t)
	ctx := t.Context()
	att := []api.AttendeeRef{{Member: "ada"}}
	cases := []api.MeetingRequest{
		{Question: "", Cwd: repo, Attendees: att},
		{Question: "q", Cwd: repo},
		{Question: "q", Cwd: repo, Attendees: []api.AttendeeRef{{Member: "ada"}, {Member: "ada"}}},
		{Question: "q", Cwd: repo, Attendees: []api.AttendeeRef{{Member: "nobody"}}},
		{Question: "q", Cwd: repo, Attendees: []api.AttendeeRef{{Member: "ada", Rank: "boss"}}},
		{Question: "q", Cwd: repo, Attendees: att, Options: []string{"only"}},
		{Question: "q", Cwd: repo, Attendees: att, MaxRounds: 6},
		{Question: "q", Cwd: repo, Attendees: att, Context: []string{"../outside.go"}},
		{Question: "q", Cwd: "relative", Attendees: att},
	}
	for _, c := range cases {
		if _, err := s.Open(ctx, c); !errors.Is(err, ErrInvalid) {
			t.Errorf("Open(%+v) = %v", c, err)
		}
	}
}

func TestParseAnswer(t *testing.T) {
	a := parseAnswer("**Option:** (b) because\nconfidence: 140", []string{"A", "B"})
	if a.Option != "B" || a.Confidence == nil || *a.Confidence != 100 || a.Stance == "" {
		t.Errorf("loose answer = %+v", a)
	}
	a = parseAnswer("## Stance\nOption: C\nx\n## Response to others\nStrongest opposing argument: P2 says y\n"+
		"Moved: yes - fair", []string{"A", "B"})
	if a.Option != "" || a.Moved != "yes" || a.Rebuttal != "P2 says y" {
		t.Errorf("answer = %+v", a)
	}
	d := parseDissent("## Position\nI concede.\n## Statement\nFine.")
	if d.kind != "concede" || d.text != "Fine." {
		t.Errorf("dissent = %+v", d)
	}
}
