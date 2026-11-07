package task

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestBootProjectsAndGitScope checks the dashboard's first-paint task set, the project list and the git view of a
// task.
func TestBootProjectsAndGitScope(t *testing.T) {
	h := newHarness(t)
	h.agy("only", 0)
	h.queue.Push(t, script(agyInit("conv-b"), agyEdit(1, filepath.Join(h.cwd, "a.go")), agyResult("conv-b", "edited")))
	done := h.delegate(api.DelegateRequest{Title: "first"})
	h.waitStatus(done.ID, StatusDone)
	other := filepath.Join(h.dir, "other")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	h.queue.Push(t, okScript("conv-c", "ok"))
	h.clk.Advance(time.Second) // the order is by creation time; equal times would fall back to the random id
	reviewed := h.delegate(api.DelegateRequest{Title: "second", Cwd: other})
	h.waitStatus(reviewed.ID, StatusDone)
	if _, err := h.s.Review(t.Context(), reviewed.ID, api.ReviewRequest{Verdict: api.VerdictAccepted}); err != nil {
		t.Fatal(err)
	}

	boot, err := h.s.Boot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(boot) != 2 || boot[0].ID != reviewed.ID || boot[1].ID != done.ID {
		t.Fatalf("boot = %+v", boot)
	}

	projects, err := h.s.Projects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("projects = %+v", projects)
	}
	for _, p := range projects {
		if p.TaskCount != 1 || p.ActiveCount != 0 || p.Name == "" || p.LastActivityAt == "" || p.Key == "" {
			t.Errorf("project = %+v", p)
		}
	}

	g, err := h.s.GitScope(t.Context(), done.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Cwd != h.task(done.ID).Cwd || g.Files["a.go"] != 1 || g.LastResponse != "edited" || g.Title != "first" {
		t.Errorf("git scope = %+v", g)
	}
	if _, err := h.s.GitScope(t.Context(), "nope"); err == nil {
		t.Error("an unknown task has a git scope")
	}
}
