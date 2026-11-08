package app

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/task"
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
// time limit, output tail, Rust toolchain pin, testParallelism variables and gatePriority. Each command is admitted
// like a task gate (task.Service.AdmitHeavy: one of the gateConcurrency slots, then free memory) and the memory
// watchdog may stop it (TrackHeavy); a stopped command says so in its tail.
func (a *App) proofExec(ctx context.Context, cmd, dir, logFile string) proof.CmdResult {
	release, err := a.Tasks.AdmitHeavy(ctx)
	if err != nil {
		return proof.CmdResult{Tail: "cancelled while waiting for a gate slot or for free memory"}
	}
	defer release()
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	defer a.Tasks.TrackHeavy(cancel)()
	set := a.Settings()
	extra := task.ParallelVars(a.env, a.Limits().TestParallelism)
	if a.toolchains != nil {
		maps.Copy(extra, a.toolchains.Env(ctx, dir, a.env))
	}
	env := make([]string, 0, len(a.env)+len(extra)+2)
	for _, kv := range a.env {
		name, _, _ := strings.Cut(kv, "=")
		if _, over := extra[name]; !over {
			env = append(env, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		env = append(env, k+"="+extra[k])
	}
	env = append(env, "CI=1", "SEELEGENT_GATE=1")
	res := gate.Exec(ctx, cmd, gate.ExecOptions{Dir: dir, Env: env,
		Timeout:   time.Duration(max(set.GateTimeoutSec, 1)) * time.Second,
		TailBytes: max(set.GateOutputTailBytes, 1), LogFile: logFile, Tree: task.GateTree(set)})
	tail := res.Tail
	if res.Err != "" && tail == "" {
		tail = res.Err
	}
	if res.TimedOut {
		tail += "\n(timed out)"
	}
	if res.Stopped == api.StepStoppedLowMemory {
		tail = strings.TrimSpace(tail + "\nstopped: machine low on memory")
	}
	return proof.CmdResult{Exit: res.Exit, Ms: res.Ms, Tail: tail, Stopped: res.Stopped == api.StepStoppedLowMemory}
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
