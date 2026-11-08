package task

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// gitOut runs git in dir and returns its trimmed output.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	p, err := gitx.NewBinary(nil, nil).Path()
	if err != nil {
		t.Skipf("no usable git: %v", err)
	}
	cmd := exec.CommandContext(t.Context(), p, args...)
	cmd.Dir = dir
	cmd.Env = gitx.Env(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestGitStartRecordedOnce: the first turn records where the repository stood (HEAD and branch); a followup after
// the worker committed keeps it, and the git scope carries it with the spans of both turns.
func TestGitStartRecordedOnce(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.initRepo(map[string]string{"a.txt": "one\n"})
	start := gitOut(t, h.cwd, "rev-parse", "HEAD")
	h.queue.Push(t, okScript("conv-1", "first"))
	sum := h.delegate(api.DelegateRequest{})
	tk := h.waitStatus(sum.ID, StatusDone)
	if tk.GitStart == nil || tk.GitStart.Head != start || tk.GitStart.Branch != "main" || tk.GitStart.At.IsZero() {
		t.Fatalf("start = %+v, want %s on main", tk.GitStart, start)
	}

	// The worker's commit between the turns does not move the start.
	h.write("a.txt", "two\n")
	gitIn(t, h.cwd, "commit", "-q", "-am", "worker commit")
	h.queue.Push(t, okScript("conv-1", "second"))
	if _, err := h.s.Followup(t.Context(), sum.ID, api.FollowupRequest{Message: "More."}); err != nil {
		t.Fatal(err)
	}
	tk = h.waitStatus(sum.ID, StatusDone)
	if tk.GitStart == nil || tk.GitStart.Head != start {
		t.Fatalf("start after the followup = %+v, want %s", tk.GitStart, start)
	}
	g, err := h.s.GitScope(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Start == nil || g.Start.Head != start || g.Start.Branch != "main" || len(g.Turns) != 2 ||
		g.Turns[0].N != 1 || g.Turns[1].N != 2 || g.Turns[0].Start.IsZero() || g.Turns[0].End.IsZero() {
		t.Fatalf("scope start %+v, turns %+v", g.Start, g.Turns)
	}
}

// TestGitStartOutsideGit: a folder outside git records no start (the Git view has no repository to show).
func TestGitStartOutsideGit(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	plain := filepath.Join(h.dir, "plain")
	if err := os.MkdirAll(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-1", "ok"))
	sum := h.delegate(api.DelegateRequest{Cwd: plain})
	tk := h.waitStatus(sum.ID, StatusDone)
	if !tk.BaselineTaken || tk.GitStart != nil {
		t.Fatalf("baseline taken %v, start %+v", tk.BaselineTaken, tk.GitStart)
	}
	if g, err := h.s.GitScope(t.Context(), sum.ID); err != nil || g.Start != nil {
		t.Fatalf("scope start %+v, %v", g.Start, err)
	}
}

// TestTaskStartFallback: a task from before starts were recorded uses its first turn's start snapshot, head and
// repository (gitx uses it only there); a snapshot without a head or a root is no start, and nor is nothing.
func TestTaskStartFallback(t *testing.T) {
	sha := strings.Repeat("ab", 20)
	root := filepath.Join(t.TempDir(), "repo")
	cases := []struct {
		name string
		task Task
		want *gitx.TaskStart
	}{
		{"recorded", Task{GitStart: &GitStart{Head: sha, Branch: "dev"},
			Turns: []*Turn{{N: 1, Snap: &TurnSnap{Head: "other"}}}}, &gitx.TaskStart{Head: sha, Branch: "dev"}},
		{"snapshot", Task{Turns: []*Turn{{N: 1, Snap: &TurnSnap{Root: root, Head: sha}}}},
			&gitx.TaskStart{Head: sha, Root: root}},
		{"snapshot without a head", Task{Turns: []*Turn{{N: 1, Snap: &TurnSnap{Root: root}}}}, nil},
		{"snapshot without a root", Task{Turns: []*Turn{{N: 1, Snap: &TurnSnap{Head: sha}}}}, nil},
		{"later turn's snapshot only", Task{Turns: []*Turn{{N: 1}, {N: 2, Snap: &TurnSnap{Root: root, Head: sha}}}},
			nil},
		{"nothing", Task{}, nil},
	}
	for _, c := range cases {
		got := taskStart(&c.task)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%s: taskStart = %+v, want %+v", c.name, got, c.want)
		}
	}
}
