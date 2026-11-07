package task

import (
	"context"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// statsTasks caps how many tasks the gate statistics read.
const statsTasks = 5000

// ProofStats counts what the gates did for the tasks changed since since (all when zero), in the repository of cwd
// when given (GET /proof/stats).
func (s *Service) ProofStats(ctx context.Context, cwd string, since time.Time) (api.ProofStats, error) {
	key := ""
	projects := map[string]bool{}
	if cwd != "" {
		canonical, _, _, err := fsutil.ProjectOf(cwd)
		if err != nil {
			return api.ProofStats{}, invalidf("%v", err)
		}
		root := canonical
		if r := s.gitRoot(ctx, canonical); r != "" {
			root = r
		}
		key = rootKey(root)
		if pk, _, err := fsutil.ProjectKey(root); err == nil {
			projects[pk] = true
		}
	}
	tasks, err := s.repo.Recent(ctx, since, statsTasks)
	if err != nil {
		return api.ProofStats{}, err
	}
	out := api.ProofStats{}
	if !since.IsZero() {
		out.Since = api.FormatTime(since)
	}
	var gatedFollowups, ungatedFollowups, gatedN, ungatedN int
	for _, t := range tasks {
		if (t.Kind != KindNormal && t.Kind != "") || (key != "" && rootKey(claimRoot(t)) != key) ||
			(!since.IsZero() && t.UpdatedAt.Before(since)) {
			continue
		}
		out.Tasks++
		followups := 0
		for _, tu := range t.Turns {
			if tu.N > 1 && tu.By == ByLead {
				followups++
			}
		}
		if t.Proof == nil {
			ungatedN++
			ungatedFollowups += followups
			continue
		}
		gatedN++
		gatedFollowups += followups
		out.GatedTasks++
		if t.Proof.Project != "" {
			projects[t.Proof.Project] = true
		}
		first := true
		for i, tu := range t.Turns {
			if tu.By == ByGate || tu.By == ByVerifier {
				out.AutoFixTurns++
			}
			if g := tu.Gate; g != nil && g.State != api.GateSkipped && g.State != api.GateRunning {
				out.GateRuns++
				failed := g.State == api.GateRed || g.State == api.GateError
				if first && failed {
					out.FirstRunRed++
				}
				first = false
				switch {
				case failed && g.AutoFix > 0:
					out.CaughtBeforeReview++
				case failed:
					out.ExhaustedBudgets++
				}
				if st := laneStep(*g); st != nil && len(st.Files) > 0 {
					out.LaneViolationsCaught++
				}
			}
			if v := tu.Verifier; v != nil && v.Verdict != api.GateRunning {
				out.Verifier.Runs++
				out.Verifier.Tokens += v.Tokens
				switch v.Verdict {
				case api.VerdictAccept:
					out.Verifier.Accepts++
					if slipped(t, i) {
						out.Verifier.Slipped++
					}
				case api.VerdictReject:
					out.Verifier.Rejects++
				default:
					out.Verifier.Errors++
				}
			}
		}
	}
	if gatedN > 0 {
		out.LeadFollowups.Gated = float64(gatedFollowups) / float64(gatedN)
	}
	if ungatedN > 0 {
		out.LeadFollowups.Ungated = float64(ungatedFollowups) / float64(ungatedN)
	}
	if s.o.Ledger != nil {
		for p := range projects {
			c, err := s.o.Ledger.Counters(ctx, p)
			if err != nil {
				return api.ProofStats{}, err
			}
			out.LaneRefusals += c[proof.CounterLaneRefusals]
			out.LaneForced += c[proof.CounterLaneForced]
		}
	}
	return out, nil
}

// slipped reports whether the lead still found a problem after the verifier accepted turn i: a later lead
// followup, or a rejected review when no turn came after.
func slipped(t *Task, i int) bool {
	for _, tu := range t.Turns[i+1:] {
		if tu.By == ByLead || tu.By == ByUser {
			return true
		}
	}
	return t.Review != nil && t.Review.Verdict == api.VerdictRejected
}
