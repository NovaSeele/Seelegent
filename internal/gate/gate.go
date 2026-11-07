package gate

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// maxListed caps the files a step lists.
const maxListed = 20

// Input is what a gate plan is built from.
type Input struct {
	// Files are the files (relative to the repository root) the turns since the lead's message changed.
	Files []string
	// LaneMode is off, warn or enforce; Lane is the task's lane (nil: no lane).
	LaneMode string
	Lane     []string
	// Hygiene are the red hygiene findings still open.
	Hygiene []api.Finding
	// Hygienic is true when the hygiene step applies (setting hygiene and checked turns).
	Hygienic bool
	// Gates is the gates setting: false keeps only the lane and hygiene steps.
	Gates bool
	// Disabled is true when the lead turned the task's gate off: lane and hygiene still run.
	Disabled   bool
	AutoChecks bool
	// Doctrine are the frozen doctrine checks; DoctrineError why the doctrine file was refused.
	Doctrine      []proof.Check
	DoctrineError string
	Commands      []api.GateCommand
}

// autoExts are the extensions the auto step checks.
var autoExts = []string{".json", ".go", ".js", ".cjs", ".mjs"}

// Plan builds the steps of a gate: lane, hygiene, then (when gates are on) auto checks, doctrine and the task's
// commands. Lane and hygiene are decided here; the other steps are pending until Run.
func Plan(in Input) []api.GateStep {
	var steps []api.GateStep
	if in.LaneMode != api.LaneOff && in.Lane != nil {
		s := api.GateStep{Kind: api.StepLane, Label: "lane", State: api.StepGreen}
		if out := proof.Outside(in.Lane, in.Files); len(out) > 0 {
			s.Files = out
			s.Tail = "Out of lane:\n" + strings.Join(listed(out), "\n")
			s.State = api.StepRed
			if in.LaneMode == api.LaneWarn {
				s.State = api.StepWarn
			}
		}
		steps = append(steps, s)
	}
	if in.Hygienic && (len(in.Files) > 0 || len(in.Hygiene) > 0) {
		s := api.GateStep{Kind: api.StepHygiene, Label: "hygiene", State: api.StepGreen}
		if len(in.Hygiene) > 0 {
			s.State, s.Findings = api.StepRed, in.Hygiene
			var b strings.Builder
			for _, f := range in.Hygiene {
				fmt.Fprintf(&b, "%s: %s\n", f.Path, f.Kind)
			}
			s.Tail = strings.TrimSpace(b.String())
		}
		steps = append(steps, s)
	}
	if !in.Gates || in.Disabled {
		return steps
	}
	if in.AutoChecks {
		var files []string
		for _, f := range in.Files {
			if slices.Contains(autoExts, proof.Ext(f)) {
				files = append(files, f)
			}
		}
		if len(files) > 0 {
			steps = append(steps, api.GateStep{Kind: api.StepAuto, Label: "syntax", State: api.StepPending,
				Files: files})
		}
	}
	if in.DoctrineError != "" {
		steps = append(steps, api.GateStep{Kind: api.StepDoctrine, Label: proof.DoctrineFile, State: api.StepPending,
			Tail: in.DoctrineError})
	} else {
		for _, c := range proof.DoctrineFor(in.Doctrine, in.Files) {
			steps = append(steps, api.GateStep{Kind: api.StepDoctrine, Label: c.ID, Cmd: c.Run, State: api.StepPending})
		}
	}
	for _, c := range in.Commands {
		label := c.Cmd
		if len(c.TaskIDs) > 0 {
			label = strings.Join(c.TaskIDs, ", ")
		}
		kind := api.StepTask
		if c.Kind == api.StepDetect {
			kind = api.StepDetect
		}
		steps = append(steps, api.GateStep{Kind: kind, Label: label, Cmd: c.Cmd, Dir: c.Dir,
			TaskIDs: slices.Clone(c.TaskIDs), State: api.StepPending})
	}
	return steps
}

// listed caps a file list for display.
func listed(files []string) []string {
	if len(files) <= maxListed {
		return files
	}
	return append(slices.Clone(files[:maxListed]), fmt.Sprintf("… %d more", len(files)-maxListed))
}

// Runner runs the pending steps of a plan.
type Runner struct {
	// Root is the repository root; commands run there (or in their Dir under it).
	Root      string
	Env       []string
	Timeout   time.Duration
	TailBytes int
	// LogPrefix, when set, is the path prefix of the step logs: <LogPrefix><step>.log.
	LogPrefix string
	// Checker runs the auto step's syntax checks.
	Checker Checker
}

// failed reports whether a step state stops the gate.
func failed(state string) bool { return state == api.StepRed || state == api.StepError }

// Run runs the steps in order; after the first red or error step the rest are not_run. progress, when set, gets
// each step as it starts and ends. A run cut short by ctx returns the steps run so far and ok = false.
func (r Runner) Run(ctx context.Context, steps []api.GateStep, progress func(i int, s api.GateStep)) (
	res api.GateResult, ok bool) {
	res = api.GateResult{Steps: slices.Clone(steps), NotRun: []string{}, Warnings: []string{}}
	stop := false
	for i := range res.Steps {
		s := &res.Steps[i]
		if stop {
			s.State = api.StepNotRun
			res.NotRun = append(res.NotRun, s.Label)
			continue
		}
		if s.State == api.StepPending {
			if ctx.Err() != nil {
				return res, false
			}
			s.State = api.StepRunning
			if progress != nil {
				progress(i, *s)
			}
			r.runStep(ctx, i, s)
			if ctx.Err() != nil {
				return res, false
			}
			if progress != nil {
				progress(i, *s)
			}
		}
		if s.State == api.StepWarn {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", s.Label, strings.Join(listed(s.Files), ", ")))
		}
		stop = failed(s.State)
	}
	res.State = Overall(res.Steps)
	return res, true
}

// Overall is the state of a gate from its steps: red when a step is red, else error when one is, else green.
func Overall(steps []api.GateStep) string {
	state := api.GateGreen
	for _, s := range steps {
		switch {
		case s.State == api.StepRed && !s.Foreign:
			return api.GateRed
		case s.State == api.StepError && !s.Foreign:
			state = api.GateError
		}
	}
	return state
}

// runStep runs one pending step.
func (r Runner) runStep(ctx context.Context, i int, s *api.GateStep) {
	switch {
	case s.Kind == api.StepAuto:
		began := time.Now()
		s.Failures = r.Checker.Check(ctx, r.Root, s.Files)
		s.Ms = time.Since(began).Milliseconds()
		s.State = api.StepGreen
		if len(s.Failures) > 0 {
			s.State = api.StepRed
			var b strings.Builder
			for _, f := range s.Failures {
				fmt.Fprintf(&b, "%s: %s\n", f.File, f.Message)
			}
			s.Tail = Tail(strings.TrimSpace(b.String()), max(r.TailBytes, DefaultTailBytes))
		}
	case s.Cmd == "":
		// A doctrine file that could not be read: the gate cannot check what the repository asks for.
		s.State = api.StepError
	default:
		dir := r.Root
		if s.Dir != "" {
			dir = filepath.Join(r.Root, filepath.FromSlash(s.Dir))
		}
		log := ""
		if r.LogPrefix != "" {
			log = fmt.Sprintf("%s%d.log", r.LogPrefix, i+1)
		}
		res := Exec(ctx, s.Cmd, ExecOptions{Dir: dir, Env: r.Env, Timeout: r.Timeout, TailBytes: r.TailBytes,
			LogFile: log})
		s.Exit, s.Ms, s.TimedOut, s.Tail = res.Exit, res.Ms, res.TimedOut, res.Tail
		switch {
		case res.OK():
			s.State, s.Tail = api.StepGreen, ""
		case res.Exit != nil:
			s.State = api.StepRed
		default:
			s.State = api.StepError
		}
	}
}
