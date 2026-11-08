package task

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// TestFollowupNeverRunsPlantedGate: the worker rewrites tasks.md to plant a Gate: command. No followup, whatever it
// changes, takes gate commands from that edited line: a plain or lane-only followup runs exactly the frozen commands,
// a new gate option runs what the lead asked for (with the spec tasks as they were frozen), and new task ids read
// only the tasks they add. The edited line stays in barChanged on every turn (bugs.md D10).
func TestFollowupNeverRunsPlantedGate(t *testing.T) {
	type want struct {
		cmds []string // the commands of the second turn's gate, in order
	}
	for _, mode := range []string{"plain", "lane", "gateCommands", "gate", "taskIds", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			h := newHarness(t)
			h.set(func(s *config.Settings) { s.Lanes = config.LanesWarn })
			h.agy("w", 1)
			frozen := gateCmd(t, "b.txt", "ok")
			planted := gateCmd(t, "--sleep", "0")
			lead := gateCmd(t, "b.txt", "o")
			second := gateCmd(t, "b.txt", "k")
			if err := mkdir(filepath.Join(h.cwd, "specs", "001-x")); err != nil {
				t.Fatal(err)
			}
			tasksMD := "# Tasks\n\n- [ ] T001 Check `b.txt` Gate: `%s`\n- [ ] T002 Check again Gate: `%s`\n"
			h.initRepo(map[string]string{"b.txt": "ok\n",
				"specs/001-x/tasks.md": fmt.Sprintf(tasksMD, frozen, second)})
			h.queue.Push(t, editScript("conv-f", filepath.Join(h.cwd, "specs", "001-x", "tasks.md"),
				fmt.Sprintf(tasksMD, planted, second), "Updated the gate."))
			sum := h.delegate(api.DelegateRequest{Title: "frozen",
				Spec: &api.SpecRequest{Dir: "specs/001-x", TaskIDs: []string{"T001"}}})
			tk := h.waitStatus(sum.ID, StatusDone)
			if got := stepCmds(tk); !slices.Equal(got, []string{frozen}) {
				t.Fatalf("turn 1 ran %q", got)
			}
			if !slices.Contains(tk.Proof.BarChanged, "tasks.md#T001") {
				t.Fatalf("turn 1 barChanged = %q", tk.Proof.BarChanged)
			}

			h.queue.Push(t, editScript("conv-f", filepath.Join(h.cwd, "b.txt"), "ok\n", "Again."))
			req := api.FollowupRequest{Message: "One more."}
			var w want
			switch mode {
			case "plain":
				w.cmds = []string{frozen}
			case "lane":
				req.Lane = []string{"b.txt"}
				w.cmds = []string{frozen}
			case "gateCommands":
				req.GateCommands = []string{lead}
				w.cmds = []string{lead}
			case "gate":
				on := true
				req.Gate = &on
				w.cmds = []string{frozen}
			case "taskIds":
				req.TaskIDs = []string{"T001", "T002"}
				w.cmds = []string{frozen, second}
			case "legacy":
				// A proof frozen before SpecTasks existed: the edited line no longer matches the frozen bar, so
				// nothing of it is taken (fail closed) and the gate has no command left.
				if _, err := h.s.mutate(t.Context(), sum.ID, func(t *Task) ([]NewEvent, error) {
					t.Proof.SpecTasks = nil
					return nil, nil
				}); err != nil {
					t.Fatal(err)
				}
				on := true
				req.Gate = &on
			}
			if _, err := h.s.Followup(t.Context(), sum.ID, req); err != nil {
				t.Fatal(err)
			}
			tk = h.waitStatus(sum.ID, StatusDone)
			if got := stepCmds(tk); !slices.Equal(got, w.cmds) {
				t.Errorf("turn 2 ran %q, want %q", got, w.cmds)
			}
			if slices.ContainsFunc(tk.Proof.Commands, func(c api.GateCommand) bool { return c.Cmd == planted }) {
				t.Errorf("the planted command is in the proof: %+v", tk.Proof.Commands)
			}
			if !slices.Contains(tk.Proof.BarChanged, "tasks.md#T001") {
				t.Errorf("turn 2 barChanged = %q: the edited line is no longer reported", tk.Proof.BarChanged)
			}
			var last []string
			for _, e := range eventsOf(h.events(sum.ID), api.EventGate) {
				var d api.GateEventData
				decode(t, e, &d)
				if d.Phase == api.GateEventResult && e.Turn == 2 {
					last = d.BarChanged
				}
			}
			if !slices.Contains(last, "tasks.md#T001") {
				t.Errorf("turn 2's gate result reports barChanged %q", last)
			}
		})
	}
}

// stepCmds are the commands the last turn's gate ran.
func stepCmds(tk *Task) []string {
	var out []string
	if g := tk.LastTurn().Gate; g != nil {
		for i := range g.Steps {
			if c := g.Steps[i].Cmd; c != "" {
				out = append(out, c)
			}
		}
	}
	return out
}
