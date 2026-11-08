package task

import (
	"context"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/memory"
)

// leavingMemory ends the request's context as the review history is written, as a lead that closes the connection
// right after the verdict was saved does.
type leavingMemory struct {
	Memory
	leave func()
}

func (m leavingMemory) AddVerdict(ctx context.Context, v memory.Verdict) error {
	m.leave()
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.Memory.AddVerdict(ctx, v)
}

// A saved verdict reaches the review history even when the request ends at that moment: the history write is not
// tied to the request (M18 hunt H23).
func TestReviewHistoryOutlivesTheRequest(t *testing.T) {
	h := newHarness(t)
	h.agy("first", 1)
	h.queue.Push(t, okScript("conv-1", "Done."))
	sum := h.delegate(api.DelegateRequest{Prompt: "Do it."})
	h.waitStatus(sum.ID, StatusDone)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.s.o.Memory = leavingMemory{Memory: h.memory, leave: cancel}
	// The answer may fail (nobody is left to read it); the verdict and its history must not.
	_, _ = h.s.Review(ctx, sum.ID, api.ReviewRequest{Verdict: api.VerdictAccepted, Notes: "good"})
	if tk := h.task(sum.ID); tk.Review == nil || tk.Review.Verdict != api.VerdictAccepted {
		t.Fatalf("review = %+v", tk.Review)
	}
	hist, err := h.memory.History(t.Context(), h.cwd, memory.HistoryShown)
	if err != nil || len(hist) != 1 || !strings.Contains(hist[0], "accepted") {
		t.Fatalf("history = %q, %v", hist, err)
	}
}
