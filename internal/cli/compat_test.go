package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The Node version's command line syntax still works (hidden flags and aliases; docs/cli.md "Old syntax").

// fileIn writes text to dir/name and returns the path.
func fileIn(t *testing.T, dir, name, text string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	writeFile(t, p, text)
	return p
}

func TestListState(t *testing.T) {
	for _, c := range []struct {
		state       string
		all, closed bool
		want        string
		bad         bool
	}{
		{want: ""}, {state: "closed", want: "closed"}, {all: true, want: "all"}, {closed: true, want: "closed"},
		{state: "open", all: true, bad: true}, {all: true, closed: true, bad: true},
	} {
		got, err := listState(c.state, c.all, c.closed)
		if c.bad != (err != nil) || got != c.want {
			t.Errorf("listState(%q, %v, %v) = %q, %v", c.state, c.all, c.closed, got, err)
		}
	}
}

func TestArgOrFlag(t *testing.T) {
	dir := t.TempDir()
	fileIn(t, dir, "q.md", "from the file")
	for _, c := range []struct {
		args []string
		flag string
		want string
		bad  bool
	}{
		{args: []string{"two", "words"}, want: "two words"},
		{flag: "the flag", want: "the flag"},
		{flag: "@q.md", want: "from the file"},
		{args: []string{"@q.md"}, want: "from the file"},
		{args: []string{"x"}, flag: "y", bad: true},
	} {
		got, err := argOrFlag(c.args, c.flag, "question", dir)
		if c.bad != (err != nil) || got != c.want {
			t.Errorf("argOrFlag(%q, %q) = %q, %v", c.args, c.flag, got, err)
		}
	}
}

// TestOldMeetingSyntax: meeting open --question/--seal, meeting act --item and meeting dissent.
func TestOldMeetingSyntax(t *testing.T) {
	d := newTestDaemon(t)
	td := testDeps(nil)
	files := t.TempDir()
	q := fileIn(t, files, "q.md", "Which cache do we keep?")
	s := fileIn(t, files, "s.md", "Keep the LRU cache.")
	must(t, td, "members", "add", "--id", "ana", "--name", "Ana", "--backend", "agy", "--rank", "peer")
	must(t, td, "members", "add", "--id", "bo", "--name", "Bo", "--backend", "agy", "--rank", "peer")

	var m api.Meeting
	must(t, td, "meeting", "open", "--cwd", d.cwd, "--question", "@"+q, "--seal", "@"+s, "--attendee", "ana,bo",
		"--rounds", "1", "--json").json(t, &m)
	if m.Question != "Which cache do we keep?" || m.Sealed == nil || m.Sealed.Text != "Keep the LRU cache." {
		t.Fatalf("open with the old flags = question %q, sealed %+v", m.Question, m.Sealed)
	}
	must(t, td, "meeting", "cancel", m.ID)
	wantErr(t, td, ExitUsage, "usage", "meeting", "open", "--question", "q", "also an argument", "--attendee",
		"ana", "--json")
	wantErr(t, td, ExitUsage, "usage", "meeting", "open", "--attendee", "ana", "--json")

	// --item gives one task per brief: both reach the daemon (the second is refused for its empty text).
	r := wantErr(t, td, ExitError, string(api.CodeInvalidArgument), "meeting", "act", m.ID, "--item", "first",
		"--item", " ", "--json")
	if !strings.Contains(r.stdout, "item 2 has no text") {
		t.Errorf("act --item --item: %s", r.stdout)
	}
	wantErr(t, td, ExitError, string(api.CodeBadState), "meeting", "act", m.ID, "--item", "@"+q, "--member", "ana",
		"--json")
	wantErr(t, td, ExitUsage, "usage", "meeting", "act", m.ID, "--item", "x", "a brief too", "--json")
	wantErr(t, td, ExitUsage, "usage", "meeting", "act", m.ID, "--json")

	// dissent is dissent-outcome under its old name.
	r = wantErr(t, td, ExitError, string(api.CodeInvalidArgument), "meeting", "dissent", m.ID, "d1", "maybe",
		"--json")
	if !strings.Contains(r.stdout, "vindicated, dismissed") {
		t.Errorf("dissent: %s", r.stdout)
	}
	wantErr(t, td, ExitError, string(api.CodeNotFound), "meeting", "dissent", m.ID, "d-nope", "vindicated", "--json")
}

// TestOldIssuesSyntax: issues add --body, list --all/--closed, triage with the brief as an argument, and
// repo-settings --shared-dir/--no-shared-dirs.
func TestOldIssuesSyntax(t *testing.T) {
	gitIdentity(t)
	d := newTestDaemon(t)
	td := testDeps(nil)
	repo := filepath.Join(filepath.Dir(d.cwd), "repo")
	var r api.Repo
	must(t, td, "issues", "init", repo, "--json").json(t, &r)
	files := t.TempDir()
	body := fileIn(t, files, "body.md", "Steps to reproduce.")
	brief := fileIn(t, files, "brief.md", "Fix the flaky test.")

	var i api.Issue
	must(t, td, "issues", "add", r.ID, "--title", "Flaky test", "--body", "@"+body, "--json").json(t, &i)
	if i.Key != "local#1" || i.Body != "Steps to reproduce." {
		t.Fatalf("add --body = %+v", i)
	}
	wantErr(t, td, ExitUsage, "usage", "issues", "add", r.ID, "--title", "x", "--body", "b", "a body", "--json")

	var open, all, closed []api.Issue
	must(t, td, "issues", "list", r.ID, "--json").json(t, &open)
	must(t, td, "issues", "list", r.ID, "--all", "--json").json(t, &all)
	must(t, td, "issues", "list", r.ID, "--closed", "--json").json(t, &closed)
	if len(open) != 1 || len(all) != 1 || len(closed) != 0 {
		t.Fatalf("list: open %d, --all %d, --closed %d", len(open), len(all), len(closed))
	}
	wantErr(t, td, ExitUsage, "usage", "issues", "list", r.ID, "--all", "--state", "open", "--json")

	d.queue.Push(t, script("conv-t", "Fixed."))
	var tri api.TriageResult
	must(t, td, "issues", "triage", r.ID, "local#1", "assign", "@"+brief, "--no-gate", "--json").json(t, &tri)
	if tri.TaskID == "" || tri.Ticket == nil {
		t.Fatalf("triage with the brief as an argument = %+v", tri)
	}
	var task api.TaskDetail
	must(t, td, "task", tri.TaskID, "--full", "--json").json(t, &task)
	if !strings.Contains(task.Prompt, "Fix the flaky test.") {
		t.Errorf("the task's prompt does not hold the brief:\n%s", task.Prompt)
	}
	wantErr(t, td, ExitUsage, "usage", "issues", "triage", r.ID, "local#1", "assign", "brief", "--prompt", "too",
		"--json")

	if out := must(t, td, "issues", "repo-settings", r.ID, "--shared-dir", "node_modules,.venv", "--shared-dir",
		"dist").stdout; out != "node_modules\n.venv\ndist\n" {
		t.Fatalf("repo-settings --shared-dir: %q", out)
	}
	if out := must(t, td, "issues", "repo-settings", r.ID).stdout; out != "node_modules\n.venv\ndist\n" {
		t.Fatalf("repo-settings: %q", out)
	}
	if out := must(t, td, "issues", "repo-settings", r.ID, "--no-shared-dirs").stdout; out != "" {
		t.Fatalf("repo-settings --no-shared-dirs: %q", out)
	}
	wantErr(t, td, ExitUsage, "usage", "issues", "shared-dirs", r.ID, "dist", "--clear", "--json")
	must(t, td, "wait", tri.TaskID, "--seconds", "60")
}

// TestOldContestSyntax: contest status --full prints the whole record as JSON.
func TestOldContestSyntax(t *testing.T) {
	gitIdentity(t)
	d := newTestDaemon(t)
	td := testDeps(nil)
	repo := filepath.Join(filepath.Dir(d.cwd), "repo")
	must(t, td, "issues", "init", repo)
	d.queue.Push(t, script("conv-1", "One."))
	d.queue.Push(t, script("conv-2", "Two."))
	var k api.Contest
	must(t, td, "delegate", "--cwd", repo, "--variants", "2", "--no-judge", "--no-gate", "Say hi.", "--json").
		json(t, &k)
	var full api.Contest
	must(t, td, "contest", "status", k.ID, "--full").json(t, &full)
	if full.ID != k.ID || len(full.Variants) != 2 {
		t.Fatalf("contest status --full = %+v", full)
	}
	if out := must(t, td, "contest", "status", k.ID).stdout; !strings.HasPrefix(out, "Contest "+k.ID) {
		t.Errorf("contest status without --full:\n%s", out)
	}
	must(t, td, "contest", "cancel", k.ID)
	wantErr(t, td, ExitError, string(api.CodeNotFound), "contest", "status", "k-nope", "--full", "--json")
	// The help keeps to the new form.
	if out := must(t, td, "contest", "status", "--help").stdout; strings.Contains(out, "--full") {
		t.Errorf("--full shows in the help:\n%s", out)
	}
}
