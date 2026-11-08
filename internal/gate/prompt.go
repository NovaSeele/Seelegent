package gate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
)

// Other is another active task of the same repository, named in a fix prompt.
type Other struct {
	ID    string
	Title string
	Lane  []string
	Files []string
}

// Failing returns the first red or error step of a result that is the task's own (not foreign), or nil: the step an
// automatic fix turn works on.
func Failing(res api.GateResult) *api.GateStep {
	for i := range res.Steps {
		if failed(res.Steps[i].State) && !res.Steps[i].Foreign {
			return &res.Steps[i]
		}
	}
	return nil
}

// Fixable returns the first red or error step of a result that is the task's own and that an automatic fix turn may
// work on: not foreign and not the lane, which only the lead decides (field report 4, F4: a fix turn told to get
// back inside its lane deleted seven files of its own work). nil when there is none.
func Fixable(res api.GateResult) *api.GateStep {
	for i := range res.Steps {
		s := &res.Steps[i]
		if failed(s.State) && !s.Foreign && s.Kind != api.StepLane {
			return s
		}
	}
	return nil
}

// NoDeleteLine is the fix prompt line that keeps a worker from destroying its own work (field report 4, F4): it
// tells the worker to leave the files outside its lane alone.
const NoDeleteLine = "Do NOT delete, revert or move any file to get back inside the lane: the lead decides about " +
	"those files. Leave them as they are and name them under ## Open issues."

// Folders locate a task for the fix prompt's data-folder line (brief.DataRule, field report 4, F10): Seelegent's
// data folder ("" names it ~/.seelegent), the task's folder, and its spec folder outside the repository (absolute;
// "" for none).
type Folders struct {
	Data, Cwd, Spec string
}

// FixPrompt is the message of an automatic fix turn after a failed gate (attempt of budget). lane is the task's lane:
// when the lane step lists files outside it, the prompt names the lane and those files as information only, and
// tells the worker not to delete, revert or move them. It ends with the worker brief's data-folder rule, with the
// same exception for a Spec Kit workspace in the data folder.
func FixPrompt(res api.GateResult, lane []string, attempt, budget int, others []Other, where Folders) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Seelegent ran the gate for your work and it failed (automatic fix %d/%d).\n", attempt, budget)
	s := Fixable(res)
	if s != nil {
		b.WriteString("\n")
		fmt.Fprintf(&b, "Step: %s (%s)\n", s.Label, s.Kind)
		if s.Cmd != "" {
			fmt.Fprintf(&b, "Command: %s\n", s.Cmd)
			if s.Dir != "" {
				fmt.Fprintf(&b, "Folder: %s\n", s.Dir)
			}
		}
		switch {
		case s.TimedOut:
			b.WriteString("Exit: timeout\n")
		case s.Exit != nil:
			fmt.Fprintf(&b, "Exit: %d\n", *s.Exit)
		}
		if len(s.Failures) > 0 {
			b.WriteString("Failures:\n")
			for _, f := range s.Failures {
				fmt.Fprintf(&b, "- %s: %s\n", f.File, f.Message)
			}
		}
		if len(s.Findings) > 0 {
			b.WriteString("Hygiene findings:\n")
			for _, f := range s.Findings {
				fmt.Fprintf(&b, "- %s (%s)\n", f.Path, f.Kind)
			}
		}
		if s.Tail != "" && s.Kind != api.StepHygiene && s.Kind != api.StepAuto {
			b.WriteString("```\n" + strings.TrimRight(s.Tail, "\n") + "\n```\n")
		}
	}
	b.WriteString("\nFix only what makes this check fail. Do not weaken, skip or delete tests or gate commands. Then " +
		"reply with the normal report format.\n")
	b.WriteString("If the failure is in a file you did not change, do not edit that file: report it under " +
		"## Open issues.\n")
	if ls := laneOf(res); ls != nil && len(ls.Files) > 0 {
		b.WriteString("\nFor your information, not to fix: ")
		if len(lane) > 0 {
			fmt.Fprintf(&b, "your lane is %s (a path covers that file or folder and everything under it). ",
				strings.Join(lane, ", "))
		}
		fmt.Fprintf(&b, "These files you changed are outside it: %s.\n", strings.Join(listed(ls.Files), ", "))
		b.WriteString(NoDeleteLine + "\n")
	}
	if s != nil && s.Kind == api.StepHygiene {
		var kinds []string
		for _, f := range s.Findings {
			kinds = append(kinds, f.Kind)
		}
		if slices.Contains(kinds, api.FindingEOLChanged) || slices.Contains(kinds, api.FindingBOMChanged) ||
			slices.Contains(kinds, api.FindingBOMNewFile) {
			b.WriteString("\nRestore the original line endings and BOM of those files without changing anything else " +
				"(re-save them in the style they had before you edited them; never git checkout, restore, stash, reset " +
				"or clean).\n")
		}
		if slices.Contains(kinds, api.FindingJunkFile) {
			b.WriteString("\nThe junk files look like leftovers (logs, temporary or backup files). Remove one only when " +
				"you are sure your own commands left it and it holds no work; otherwise leave it and name it under " +
				"## Open issues.\n")
		}
	}
	if len(others) > 0 {
		var parts []string
		for _, o := range others {
			p := fmt.Sprintf("%s (%s)", o.ID, o.Title)
			if len(o.Lane) > 0 {
				p += " on " + strings.Join(o.Lane, ", ")
			}
			parts = append(parts, p)
		}
		fmt.Fprintf(&b, "\nOther workers are active in this repository: %s; failures in their files are not yours "+
			"to fix.\n", strings.Join(parts, "; "))
	}
	b.WriteString("\n" + brief.DataRule(where.Data, where.Cwd, where.Spec) + "\n")
	return strings.TrimRight(b.String(), "\n")
}

// laneOf returns the lane step of a result, or nil.
func laneOf(res api.GateResult) *api.GateStep {
	for i := range res.Steps {
		if res.Steps[i].Kind == api.StepLane {
			return &res.Steps[i]
		}
	}
	return nil
}

var (
	pathStartRe = regexp.MustCompile(`^\s*(?:[a-zA-Z]:[/\\])?([a-zA-Z0-9_.\-]+(?:[/\\][a-zA-Z0-9_.\-]+)*\.[a-zA-Z0-9]+)(?::|\(|\s|$)`)
	pathAnyRe   = regexp.MustCompile(`(?:^|[\s('"` + "`" + `])([a-zA-Z0-9_.\-]+[/\\][a-zA-Z0-9_.\-/\\]*\.[a-zA-Z0-9]+)(?:[:)\s'"` + "`" + `]|$)`)
)

// tailPaths extracts the file paths a failing command's output names.
func tailPaths(tail string) []string {
	var out []string
	add := func(p string) {
		p = strings.ReplaceAll(p, `\`, "/")
		if !strings.HasPrefix(p, "node:") && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	for _, line := range strings.Split(tail, "\n") {
		if m := pathStartRe.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
		for _, m := range pathAnyRe.FindAllStringSubmatch(line, -1) {
			add(m[1])
		}
	}
	return out
}

// ForeignCause returns the other tasks a red doctrine check may be the doing of: every path its output names is a
// file one of them changed and none is a file this task changed. Only doctrine checks qualify (as in the old
// version), and only the files the other tasks actually changed count, never their lanes. A foreign step keeps the
// gate red; it only spares the task an automatic fix turn it could not do anything with.
func ForeignCause(s api.GateStep, own []string, others []Other) []api.GateCause {
	if s.State != api.StepRed || s.Kind != api.StepDoctrine || len(others) == 0 {
		return nil
	}
	paths := tailPaths(s.Tail)
	if len(paths) == 0 {
		return nil
	}
	match := func(list []string, p string) bool {
		return slices.ContainsFunc(list, func(f string) bool { return f == p || strings.HasSuffix(p, "/"+f) })
	}
	for _, p := range paths {
		if match(own, p) {
			return nil
		}
	}
	var by []api.GateCause
	for _, p := range paths {
		i := slices.IndexFunc(others, func(o Other) bool { return match(o.Files, p) })
		if i < 0 {
			return nil
		}
		if !slices.ContainsFunc(by, func(c api.GateCause) bool { return c.TaskID == others[i].ID }) {
			by = append(by, api.GateCause{TaskID: others[i].ID, Title: others[i].Title})
		}
	}
	return by
}

// OwnFailure reports whether a failed gate has a failing step that is not foreign: one an automatic fix turn can
// work on.
func OwnFailure(res api.GateResult) bool {
	return Failing(res) != nil
}
