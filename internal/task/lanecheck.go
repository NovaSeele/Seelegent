package task

import (
	"context"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// The lane check (docs/gates.md, field report 4): the lane judges the net change since the lead's instruction (F5),
// and only a file a worker plausibly touched, with its edit tools or a command whose writes Seelegent can read,
// counts against it (F6). A file that changed without either (the lead or the user editing the same tree, a
// command's side effects) is a warning.

// laneTouched collects what the workers of t's turns since the instruction, up to turn n, touched: their edits and
// command writes, and the files that changed while their commands ran (end: turn n's end snapshot, or nil).
func (s *Service) laneTouched(ctx context.Context, t *Task, n int, end *snapshot.Manifest) *workerWrites {
	w := newWorkerWrites(t.Cwd, claimRoot(t), nil, nil)
	s.addRuns(ctx, w, t, n, addTurns(w, t, n), end)
	return w
}

// addTurns adds to w the edits and command writes of t's turns since the instruction up to turn n, and returns
// their commands.
func addTurns(w *workerWrites, t *Task, n int) []string {
	var commands []string
	for _, tu := range t.Turns[instructionStart(t, n):] {
		if tu.N > n {
			break
		}
		w.addEdited(turnFiles(t, tu, liveState{}))
		for _, c := range tu.Commands {
			w.addCommand(c)
		}
		commands = append(commands, tu.Commands...)
	}
	return commands
}

// netChanged reports whether rel differs between the snapshot taken when the instruction's turn started and the
// end snapshot of the latest turn, by their manifest entries (hash, else size and time: a large file that was not
// stored still has its hash). A file missing from a snapshot was clean then (not truncated, same HEAD: netLane).
func netChanged(start, end snapshot.Manifest, rel string) bool {
	before, inStart := start.Find(rel)
	after, inEnd := end.Find(rel)
	switch {
	case inStart && inEnd:
		changed, _ := snapshot.Changed(before, after)
		return changed
	case !inStart && !inEnd:
		return false
	case !inStart:
		return after.Status != "" // clean when the instruction began, dirty now
	default:
		return before.Status != "" // dirty when the instruction began, clean now
	}
}

// netLane keeps the files of union (changed by some turn since the instruction, up to turn n) that differ between
// the start snapshot of the instruction's turn and the end snapshot of turn n (end, or loaded when nil): a file
// created and deleted again, or changed and restored, is no change. When the snapshots cannot tell (one is missing
// or truncated, HEAD moved), union is returned as it is: the check fails closed.
func (s *Service) netLane(t *Task, n int, union []string, end *snapshot.Manifest) []string {
	if s.o.Snapshots == nil || len(union) == 0 {
		return union
	}
	first := t.Turns[instructionStart(t, n)]
	if first.Snap == nil {
		return union
	}
	start, err := s.o.Snapshots.Load(t.ID, first.N, snapshot.KindStart)
	if err != nil {
		return union
	}
	if end == nil {
		e, err := s.o.Snapshots.Load(t.ID, n, snapshot.KindEnd)
		if err != nil {
			return union
		}
		end = &e
	}
	if start.Truncated || end.Truncated || start.Head != end.Head {
		return union
	}
	out := []string{}
	for _, f := range union {
		if netChanged(start, *end, f) {
			out = append(out, f)
		}
	}
	return out
}

// laneFindings words a lane step as the warn-only scope findings of a task whose gate is off (field report 4, F12):
// the lane is still checked after each turn, but only as information. why explains a file counted by time.
func laneFindings(st api.GateStep, why func(string) string) []api.Finding {
	var out []api.Finding
	for _, f := range st.Files {
		detail := "outside the lane"
		if r := why(f); r != "" {
			detail += ", " + r
		}
		out = append(out, api.Finding{Kind: api.FindingOutsideLane, Severity: api.SeverityWarn, Path: f,
			Detail: detail})
	}
	for _, f := range st.Unseen {
		out = append(out, api.Finding{Kind: api.FindingOutsideLane, Severity: api.SeverityWarn, Path: f,
			Detail: "outside the lane, " + gate.UnseenNote})
	}
	return out
}

// laneOffGate reports whether t has a lane but its gate is off: no gate phase, the lane is checked after each turn
// as scope findings.
func laneOffGate(t *Task, laneMode string) bool {
	p := t.Proof
	return gated(t) && p != nil && p.GateOff && p.Lane != nil && laneMode != api.LaneOff && t.RepoRoot != ""
}

// laneOffFindings checks the lane of a gate-off task at the end of turn tu, whose own results (not stored yet) are
// changed, edited and commands, with end its end snapshot.
func (s *Service) laneOffFindings(ctx context.Context, t *Task, tu *Turn, changed, edited, commands []string,
	end *snapshot.Manifest) []api.Finding {
	laneMode := s.settings().Lanes
	if !laneOffGate(t, laneMode) {
		return nil
	}
	var union []string
	seen := map[string]bool{}
	add := func(fs []string) {
		for _, f := range fs {
			if k := snapshot.Key(f); !seen[k] {
				seen[k] = true
				union = append(union, f)
			}
		}
	}
	touch := newWorkerWrites(t.Cwd, claimRoot(t), nil, nil)
	var all []string
	if tu.N > 1 {
		prev, _, _ := gateInput(t, tu.N-1)
		if instructionStart(t, tu.N-1) == instructionStart(t, tu.N) {
			add(prev)
			all = addTurns(touch, t, tu.N-1)
		}
	}
	add(changed)
	touch.addEdited(edited)
	for _, c := range commands {
		touch.addCommand(c)
	}
	s.addRuns(ctx, touch, t, tu.N, append(all, commands...), end)
	files := s.netLane(t, tu.N, union, end)
	st := gate.LaneStep(t.Proof.Lane.Paths, files, laneMode, touch.wrote, touch.why, touch.unknown)
	return laneFindings(st, touch.why)
}
