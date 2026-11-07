package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// proofPoll is how often proof run with wait_seconds asks for the run's state (the old server's 500 ms).
const proofPoll = 500 * time.Millisecond

// proofArgs are the arguments of proof.
type proofArgs struct {
	Action      string  `json:"action"`
	Cwd         string  `json:"cwd"`
	Feature     string  `json:"feature"`
	Force       optBool `json:"force"`
	RunID       string  `json:"run_id"`
	WaitSeconds optNum  `json:"wait_seconds"`
	Since       string  `json:"since"`
}

func (s *Server) proof(ctx context.Context, k *call) (result, error) {
	var a proofArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	switch a.Action {
	case "matrix":
		if a.Feature == "" {
			return result{}, errors.New("feature is required for matrix action")
		}
	case "run_status", "cancel_run":
		if a.RunID == "" {
			return result{}, fmt.Errorf("run_id is required for %s action", a.Action)
		}
	case "index", "run", "stats", "lanes", "contention":
	default:
		return result{}, fmt.Errorf("unknown proof action: %s (matrix, index, run, run_status, cancel_run, stats, "+
			"lanes or contention)", a.Action)
	}
	// Old behaviour: cwd defaults to the server's folder.
	cwd, err := s.absPath(a.Cwd)
	if err != nil {
		return result{}, err
	}
	if cwd == "" {
		if cwd, err = s.o.Getwd(); err != nil {
			return result{}, fmt.Errorf("cwd: %w", err)
		}
	}
	// The feature folder goes as given: the daemon resolves a relative one against the repository root.
	feature := a.Feature
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "matrix":
		m, err := c.ProofMatrix(ctx, cwd, feature)
		if err != nil {
			return result{}, err
		}
		return result{value: m, summary: fmt.Sprintf("Feature %s: %s.", m.Feature, countsText(m.Counts))}, nil
	case "index":
		ix, err := c.ProofIndex(ctx, cwd)
		if err != nil {
			return result{}, err
		}
		return result{value: ix, summary: fmt.Sprintf("%d feature(s) in %s: %s.", len(ix.Features), ix.RepoRoot,
			countsText(ix.Totals))}, nil
	case "run":
		return s.proofRun(ctx, c, cwd, feature, a)
	case "run_status":
		r, err := c.ProofRunStatus(ctx, cwd, a.RunID)
		if err != nil {
			return result{}, err
		}
		return result{value: r, summary: runText(r)}, nil
	case "cancel_run":
		r, err := c.ProofCancelRun(ctx, cwd, a.RunID)
		if err != nil {
			return result{}, err
		}
		return result{value: r, summary: runText(r)}, nil
	case "stats":
		st, err := c.ProofStats(ctx, cwd, a.Since)
		if err != nil {
			return result{}, err
		}
		return result{value: st, summary: fmt.Sprintf("%d task(s), %d gated; %d gate run(s), %d red on the "+
			"first run, %d automatic fix turn(s).", st.Tasks, st.GatedTasks, st.GateRuns, st.FirstRunRed,
			st.AutoFixTurns)}, nil
	case "lanes":
		v, err := c.Lanes(ctx, cwd)
		if err != nil {
			return result{}, err
		}
		return result{value: v, summary: fmt.Sprintf("Lanes %s in %s: %d claim(s).", v.Mode, v.RepoRoot,
			len(v.Claims))}, nil
	default: // contention
		v, err := c.Contention(ctx, cwd)
		if err != nil {
			return result{}, err
		}
		return result{value: v, summary: fmt.Sprintf("%d file(s) changed by several tasks in the last %d hour(s).",
			len(v.Files), v.WindowHours)}, nil
	}
}

// proofRun starts a regression run and, with wait_seconds, polls it until it ends or the wait is over.
func (s *Server) proofRun(ctx context.Context, c *client.Client, cwd, feature string, a proofArgs) (result, error) {
	r, err := c.ProofRun(ctx, api.ProofRunRequest{Cwd: cwd, Feature: feature, Force: a.Force.or(false)})
	if err != nil {
		return result{}, err
	}
	secs := clampWait(a.WaitSeconds, 0, maxWait)
	if secs > 0 && r.State == api.RunRunning {
		deadline := time.NewTimer(time.Duration(secs) * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(proofPoll)
		defer tick.Stop()
	poll:
		for r.State == api.RunRunning {
			select {
			case <-ctx.Done():
				return result{}, ctx.Err()
			case <-deadline.C:
				break poll
			case <-tick.C:
				cur, err := c.ProofRunStatus(ctx, cwd, r.ID)
				if err != nil {
					if ctx.Err() != nil {
						return result{}, ctx.Err()
					}
					continue // like the old server: a failed poll is tried again
				}
				r = cur
			}
		}
	}
	return result{value: r, summary: runText(r)}, nil
}

func countsText(c api.ProofCounts) string {
	return fmt.Sprintf("%d proven, %d unproven, %d in progress, %d unmapped, %d failing, %d regressed",
		c.Proven, c.Unproven, c.InProgress, c.Unmapped, c.Failing, c.Regressed)
}

func runText(r api.ProofRun) string {
	text := fmt.Sprintf("Regression run %s: %s (%d green, %d red, %d error of %d).", r.ID, r.State, r.Counts.Green,
		r.Counts.Red, r.Counts.Error, r.Total)
	if r.Noisy {
		text += " Other tasks were active, so results may reflect their unfinished work."
	}
	if r.Error != "" {
		text += " " + r.Error
	}
	return text
}
