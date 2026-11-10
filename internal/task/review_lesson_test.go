package task

import (
	"fmt"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/memory"
)

// doneTask runs one task to done and returns its id.
func (h *harness) doneTask() string {
	h.t.Helper()
	h.agy("first", 1)
	h.queue.Push(h.t, okScript("conv-1", "Done."))
	sum := h.delegate(api.DelegateRequest{Prompt: "Do it."})
	h.waitStatus(sum.ID, StatusDone)
	return sum.ID
}

// A review's rule goes into the project's notes after the verdict.
func TestReviewSavesTheRule(t *testing.T) {
	h := newHarness(t)
	id := h.doneTask()
	sum, err := h.s.Review(t.Context(), id, api.ReviewRequest{Verdict: api.VerdictRejected, Notes: "no tests",
		Rule: " Add a test for every bug you fix.\n "})
	if err != nil || !sum.LessonSaved || len(sum.Warnings) != 0 || sum.Reminder != "" {
		t.Fatalf("review = %+v, %v", sum, err)
	}
	n, err := h.memory.Get(t.Context(), h.cwd)
	if want := memory.LessonsHeading + "\n- Add a test for every bug you fix.\n"; err != nil || n.Text != want {
		t.Fatalf("notes = %q, %v; want %q", n.Text, err, want)
	}
	hist, err := h.memory.History(t.Context(), h.cwd, memory.HistoryShown)
	if err != nil || len(hist) != 1 || !strings.Contains(hist[0], "rejected - no tests") {
		t.Fatalf("history = %q, %v", hist, err)
	}
	// The same rule again is kept once, and still reported as saved.
	if sum, err = h.s.Review(t.Context(), id, api.ReviewRequest{Verdict: api.VerdictAccepted,
		Rule: "add a test for every bug you fix."}); err != nil || !sum.LessonSaved {
		t.Fatalf("second review = %+v, %v", sum, err)
	}
	if again, _ := h.memory.Get(t.Context(), h.cwd); again.Text != n.Text {
		t.Fatalf("notes after a duplicate = %q", again.Text)
	}
}

// A rule that cannot be saved is a warning: the review itself succeeds.
func TestReviewRuleFailureIsAWarning(t *testing.T) {
	h := newHarness(t)
	id := h.doneTask()
	full := strings.Repeat("x", memory.MaxBytes-10) + "\n"
	if _, err := h.memory.Replace(t.Context(), h.cwd, full); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ rule, want string }{
		{"Keep the notes short, they are in every brief.", "rule not saved: too large: project memory would be"},
		{strings.Repeat("y", memory.MaxLessonBytes+1), "rule not saved: invalid: the rule is 301 bytes"},
	}
	for _, tc := range cases {
		sum, err := h.s.Review(t.Context(), id, api.ReviewRequest{Verdict: api.VerdictAccepted, Rule: tc.rule})
		if err != nil || sum.LessonSaved || len(sum.Warnings) != 1 || !strings.HasPrefix(sum.Warnings[0], tc.want) {
			t.Fatalf("review with %.20q = %+v, %v; want warning %q", tc.rule, sum, err, tc.want)
		}
		if tk := h.task(id); tk.Review == nil || tk.Review.Verdict != api.VerdictAccepted {
			t.Fatalf("review = %+v", tk.Review)
		}
	}
	if n, _ := h.memory.Get(t.Context(), h.cwd); n.Text != full {
		t.Fatalf("notes changed: %d bytes", len(n.Text))
	}
	if hist, err := h.memory.History(t.Context(), h.cwd, memory.HistoryShown); err != nil || len(hist) != 2 {
		t.Fatalf("history = %q, %v", hist, err)
	}
}

// A rejection without a rule reminds the lead once 3 of the project's last 10 verdicts are rejections.
func TestReviewRejectedReminder(t *testing.T) {
	h := newHarness(t)
	id := h.doneTask()
	seed := func(verdicts ...string) {
		t.Helper()
		for i, v := range verdicts {
			if err := h.memory.AddVerdict(t.Context(), memory.Verdict{Cwd: h.cwd, TaskID: fmt.Sprintf("old%d", i),
				Verdict: v}); err != nil {
				t.Fatal(err)
			}
		}
	}
	review := func(verdict, rule string) api.TaskSummary {
		t.Helper()
		sum, err := h.s.Review(t.Context(), id, api.ReviewRequest{Verdict: verdict, Rule: rule})
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	// 2 of the last 10: no reminder.
	seed(api.VerdictRejected)
	if sum := review(api.VerdictRejected, ""); sum.Reminder != "" {
		t.Fatalf("reminder at 2 = %q", sum.Reminder)
	}
	// 3 of 3.
	if sum := review(api.VerdictRejected, ""); sum.Reminder != "3 of the last 3 reviews in this project were "+
		"rejected. If they share a cause, pass rule to keep it in every brief." {
		t.Fatalf("reminder at 3 = %q", sum.Reminder)
	}
	// Not with a rule, nor on an acceptance.
	if sum := review(api.VerdictRejected, "Read the spec first."); sum.Reminder != "" || !sum.LessonSaved {
		t.Fatalf("rejected with a rule = %+v", sum)
	}
	if sum := review(api.VerdictAccepted, ""); sum.Reminder != "" {
		t.Fatalf("accepted = %q", sum.Reminder)
	}
	// Rejections older than the last 10 do not count: 4 rejected, then 9 accepted, then these.
	seed(api.VerdictAccepted, api.VerdictAccepted, api.VerdictAccepted, api.VerdictAccepted,
		api.VerdictAccepted, api.VerdictAccepted, api.VerdictAccepted, api.VerdictAccepted)
	for n := 1; n <= 2; n++ {
		if sum := review(api.VerdictRejected, ""); sum.Reminder != "" {
			t.Fatalf("reminder at %d of the last 10 = %q", n, sum.Reminder)
		}
	}
	if sum := review(api.VerdictRejected, ""); !strings.HasPrefix(sum.Reminder, "3 of the last 10 reviews") {
		t.Fatalf("reminder at 3 of the last 10 = %q", sum.Reminder)
	}
}
