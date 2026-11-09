package gate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
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
	// LaneFiles are the files the lane judges: the net change since the lead's instruction (nil: Files).
	LaneFiles []string
	// Touched reports whether a worker plausibly touched a file (its edit tools, or a command that writes it). Only
	// such a file outside the lane makes the lane step red; the others are Unseen, a warning. nil: every file counts.
	Touched func(file string) bool
	// Why explains a file Touched counts though no worker edit or command names it ("changed while `make` ran"),
	// for the lane step's tail; "" otherwise. nil: no reasons.
	Why func(file string) string
	// UnknownWrites are the workers' commands that wrote somewhere Seelegent could not read (a variable, a command's
	// output, the whole repository): with one, a file outside the lane that nothing else explains counts as touched.
	UnknownWrites []string
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
		files := in.LaneFiles
		if files == nil {
			files = in.Files
		}
		steps = append(steps, LaneStep(in.Lane, files, in.LaneMode, in.Touched, in.Why, in.UnknownWrites))
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

// UnseenNote heads the lane's files that changed without a worker edit or command Seelegent saw.
const UnseenNote = "changed during the turn but not by a worker edit or command Seelegent saw"

// TimedNote heads the out-of-lane files counted because they changed while a worker's command ran, though no edit
// or command names them; each line says which command.
const TimedNote = "counted by time (no worker edit or command names them)"

// UnknownNote explains the out-of-lane files counted because a worker's command wrote somewhere Seelegent could not
// read.
const UnknownNote = "counted: no worker edit or readable command names them, but a worker's command wrote where " +
	"Seelegent could not read the path"

// LaneStep judges files against lane. A file outside the lane that touched reports (nil: every file) makes the
// step red (warn under lanes: warn); one it does not report is listed in Unseen and makes the step warn at most,
// unless unknown holds a worker command that wrote where Seelegent could not read: then it counts too (fail closed;
// the files other tasks changed are no turn's change to begin with).
func LaneStep(lane, files []string, mode string, touched func(string) bool, why func(string) string,
	unknown []string) api.GateStep {
	s := api.GateStep{Kind: api.StepLane, Label: "lane", State: api.StepGreen}
	var guessed, reasons []string
	for _, f := range proof.Outside(lane, files) {
		switch {
		case touched == nil || touched(f):
			s.Files = append(s.Files, f)
			if why != nil {
				if r := why(f); r != "" {
					reasons = append(reasons, f+": "+r)
				}
			}
		case len(unknown) > 0:
			s.Files = append(s.Files, f)
			guessed = append(guessed, f)
		default:
			s.Unseen = append(s.Unseen, f)
		}
	}
	var parts []string
	if len(s.Files) > 0 {
		parts = append(parts, "Out of lane:\n"+strings.Join(listed(s.Files), "\n"))
		if len(reasons) > 0 {
			parts = append(parts, "Of these, "+TimedNote+":\n"+strings.Join(listed(reasons), "\n"))
		}
		if len(guessed) > 0 {
			parts = append(parts, fmt.Sprintf("Of these, %s %s: %s", strings.Join(listed(guessed), ", "), UnknownNote,
				strings.Join(cut(unknown, 3, 160), " | ")))
		}
		s.State = api.StepRed
		if mode == api.LaneWarn {
			s.State = api.StepWarn
		}
	}
	if len(s.Unseen) > 0 {
		parts = append(parts, "Out of lane, "+UnseenNote+" (not counted):\n"+strings.Join(listed(s.Unseen), "\n"))
		if s.State == api.StepGreen {
			s.State = api.StepWarn
		}
	}
	s.Tail = strings.Join(parts, "\n\n")
	return s
}

// LaneWarnings words a lane step's warnings for the lead: the out-of-lane files under lanes: warn, and the files
// that changed without a worker edit or command Seelegent saw.
func LaneWarnings(s api.GateStep) []string {
	var out []string
	if s.State == api.StepWarn && len(s.Files) > 0 {
		out = append(out, "outside the lane (lanes: warn): "+strings.Join(listed(s.Files), ", "))
	}
	if len(s.Unseen) > 0 {
		out = append(out, "outside the lane, "+UnseenNote+": "+strings.Join(listed(s.Unseen), ", "))
	}
	return out
}

// cut keeps the first n of list, each at most size bytes.
func cut(list []string, n, size int) []string {
	var out []string
	for i, c := range list {
		if i == n {
			out = append(out, fmt.Sprintf("… %d more", len(list)-n))
			break
		}
		if len(c) > size {
			c = c[:size] + "…"
		}
		out = append(out, c)
	}
	return out
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
	// Tree are the limits of each command's process tree (ExecOptions.Tree: gatePriority).
	Tree platform.TreeOptions
}

// failed reports whether a step state stops the gate.
func failed(state string) bool { return state == api.StepRed || state == api.StepError }

// Run runs the steps in order; after the first red or error step other than the lane the rest are not_run. progress, when set, gets
// each step as it starts and ends. A run cut short by ctx returns the steps run so far and ok = false.
//
// A run whose ctx ends with cause ErrLowMemory (the memory watchdog) is not cut short but stopped: the step it
// was at gets Stopped = api.StepStoppedLowMemory and state error (with the reason as its tail when it had no
// output), the rest are not_run, and Run returns ok = true. The caller runs the whole gate again later (StoppedAt
// finds the step); a caller that did not would see an error gate, never a green one.
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
				if !lowMemory(ctx) {
					return res, false
				}
				markStopped(s)
				stop = true
				continue
			}
			s.State = api.StepRunning
			if progress != nil {
				progress(i, *s)
			}
			r.runStep(ctx, i, s)
			switch {
			case s.Stopped != "" || (ctx.Err() != nil && lowMemory(ctx)):
				markStopped(s)
				if progress != nil {
					progress(i, *s)
				}
				stop = true
				continue
			case ctx.Err() != nil:
				return res, false
			}
			if progress != nil {
				progress(i, *s)
			}
		}
		switch {
		case s.Kind == api.StepLane:
			res.Warnings = append(res.Warnings, LaneWarnings(*s)...)
		case s.State == api.StepWarn:
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %s", s.Label, strings.Join(listed(s.Files), ", ")))
		}
		// A red lane does not stop the gate: whether the commands pass decides between an automatic fix turn and
		// handing the task to the lead (field report 4, F4).
		stop = failed(s.State) && s.Kind != api.StepLane
	}
	res.State = Overall(res.Steps)
	return res, true
}

// lowMemory reports whether ctx ended because the memory watchdog stopped the run.
func lowMemory(ctx context.Context) bool { return errors.Is(context.Cause(ctx), ErrLowMemory) }

// markStopped marks s as stopped by the memory watchdog.
func markStopped(s *api.GateStep) {
	s.Stopped, s.State = api.StepStoppedLowMemory, api.StepError
	if s.Tail == "" {
		s.Tail = ErrLowMemory.Error()
	}
}

// StoppedAt returns the index of the step the memory watchdog stopped in res, or -1 when it stopped none.
func StoppedAt(res api.GateResult) int {
	for i := range res.Steps {
		if res.Steps[i].Stopped != "" {
			return i
		}
	}
	return -1
}

// Overall is the state of a gate from its steps: red when a step is red, else error when one is, else green. A
// foreign step counts like any other: the gate never turns green because another task may be to blame.
func Overall(steps []api.GateStep) string {
	state := api.GateGreen
	for i := range steps {
		switch steps[i].State {
		case api.StepRed:
			return api.GateRed
		case api.StepError:
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
			LogFile: log, Tree: r.Tree})
		s.Exit, s.Ms, s.TimedOut, s.Tail, s.Stopped = res.Exit, res.Ms, res.TimedOut, res.Tail, res.Stopped
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
