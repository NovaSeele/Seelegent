package app

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// proofGit adapts the git views to proof.Git.
type proofGit struct{ repos *gitx.Repos }

func (g proofGit) Root(ctx context.Context, dir string) (string, error) {
	return g.repos.Root(ctx, dir)
}

func (g proofGit) Head(ctx context.Context, root string) (string, bool) {
	head, files, err := g.repos.Dirty(ctx, root)
	if err != nil {
		return "", false
	}
	return head, len(files) > 0
}

// proofExec runs one command of a regression run like a gate command: through the shell, from dir, with the gate
// time limit, output tail and Rust toolchain pin.
func (a *App) proofExec(ctx context.Context, cmd, dir, logFile string) proof.CmdResult {
	set := a.Settings()
	env := append([]string(nil), a.env...)
	if a.toolchains != nil {
		pin := a.toolchains.Env(ctx, dir, a.env)
		for _, k := range slices.Sorted(maps.Keys(pin)) {
			env = append(env, k+"="+pin[k])
		}
	}
	env = append(env, "CI=1", "SEELEGENT_GATE=1")
	res := gate.Exec(ctx, cmd, gate.ExecOptions{Dir: dir, Env: env,
		Timeout:   time.Duration(max(set.GateTimeoutSec, 1)) * time.Second,
		TailBytes: max(set.GateOutputTailBytes, 1), LogFile: logFile})
	tail := res.Tail
	if res.Err != "" && tail == "" {
		tail = res.Err
	}
	if res.TimedOut {
		tail += "\n(timed out)"
	}
	return proof.CmdResult{Exit: res.Exit, Ms: res.Ms, Tail: tail}
}

// publishProofRun forwards a regression run's progress to the SSE stream once the HTTP API runs.
func (a *App) publishProofRun(r api.ProofRun) {
	a.mu.Lock()
	srv := a.api
	a.mu.Unlock()
	if srv != nil {
		srv.PublishProofRun(r)
	}
}
