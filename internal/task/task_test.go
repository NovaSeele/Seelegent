package task

import (
	"fmt"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/event"
)

func TestTaskHelpers(t *testing.T) {
	tk := &Task{}
	if tk.LastTurn() != nil || tk.TurnN(1) != nil || tk.TokensUsed() != 0 || tk.FilesChanged() != nil {
		t.Fatal("empty task")
	}
	tk.Turns = []*Turn{
		{N: 1, Files: []string{"a", "b"}, Attempts: []*Attempt{{N: 1, Usage: &event.Usage{TotalTokens: 10}},
			{N: 2}}},
		{N: 2, Files: []string{"b", "c"}, Attempts: []*Attempt{{N: 1, Usage: &event.Usage{InputTokens: 2,
			OutputTokens: 3}}}},
	}
	if tk.LastTurn().N != 2 || tk.TurnN(1).N != 1 || tk.TurnN(3) != nil {
		t.Fatal("turn lookup")
	}
	if tk.Turns[0].LastAttempt().N != 2 || (&Turn{}).LastAttempt() != nil {
		t.Fatal("attempt lookup")
	}
	if tk.TokensUsed() != 15 {
		t.Fatalf("tokens %d", tk.TokensUsed())
	}
	if got := fmt.Sprint(tk.FilesChanged()); got != "[a b c]" {
		t.Fatalf("files %s", got)
	}
}
