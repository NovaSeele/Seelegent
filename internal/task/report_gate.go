package task

import (
	"slices"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// reportTailChars caps the output tail of a failing step in the lead's report.
const reportTailChars = 1500

// gateReport adds the gate, lane, bar and verifier fields to a report (docs/gates.md): what happened since the
// lead's latest instruction.
func gateReport(t *Task, rep *api.TaskReport) {
	p := t.Proof
	if p == nil || len(t.Turns) == 0 {
		return
	}
	start := instructionStart(t, t.Turns[len(t.Turns)-1].N)
	var g *api.GateResult
	var v *api.VerifierResult
	gateTurn := 0
	for i := len(t.Turns) - 1; i >= start; i-- {
		tu := t.Turns[i]
		if g == nil && tu.Gate != nil {
			g, gateTurn = tu.Gate, tu.N
		}
		if v == nil && tu.Verifier != nil {
			v = tu.Verifier
		}
	}
	last := t.Turns[len(t.Turns)-1]
	switch {
	case t.GatePhase != nil:
		rep.LastGate = &api.ReportLastGate{Turn: t.GatePhase.Turn, State: api.GateRunning, Phase: t.GatePhase.State}
	case (last.By == ByGate || last.By == ByVerifier) && Active(t.Status) && g != nil:
		rep.LastGate = &api.ReportLastGate{Turn: gateTurn, State: g.State, Fixing: true}
	case g != nil:
		rg := &api.ReportGate{State: g.State, AutoFixTurns: p.AutoFixes, NotRun: g.NotRun, Warnings: g.Warnings}
		for i := range g.Steps {
			st := &g.Steps[i]
			if st.State != api.StepRed && st.State != api.StepError {
				continue
			}
			tail := st.Tail
			if r := []rune(tail); len(r) > reportTailChars {
				tail = "…" + string(r[len(r)-reportTailChars:])
			}
			files := slices.Clone(st.Files)
			for _, f := range st.Failures {
				if f.File != "" && !slices.Contains(files, f.File) {
					files = append(files, f.File)
				}
			}
			rg.Steps = append(rg.Steps, api.ReportGateStep{Label: st.Label, Kind: st.Kind, Cmd: st.Cmd, Exit: st.Exit,
				Files: files, Tail: tail, CausedBy: st.CausedBy})
		}
		rep.Gate = rg
	}
	if p.Lane != nil && len(p.Lane.Paths) > 0 {
		rl := &api.ReportLane{Paths: p.Lane.Paths, Forced: p.Lane.Forced}
		if g != nil {
			if st := laneStep(*g); st != nil && len(st.Files) > 0 {
				rl.OutOfLane = st.Files
				if st.State == api.StepWarn {
					rl.Warnings = append(rl.Warnings, "changed files outside the lane (lanes: warn)")
				}
			}
		}
		rep.Lane = rl
	}
	if len(p.BarChanged) > 0 {
		rep.Bar = &api.ReportBar{Changed: p.BarChanged}
	}
	if v != nil && v.Verdict != api.GateRunning {
		rv := &api.ReportVerifier{Verdict: v.Verdict, Reasons: v.Reasons, Detail: v.Detail}
		for _, c := range v.Cites {
			if c.State != proof.CiteVerified && c.State != proof.CiteLocated {
				rv.Unverified++
			}
		}
		rep.Verifier = rv
	}
}
