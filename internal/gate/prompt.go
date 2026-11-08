package gate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
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

// FixPrompt is the message of an automatic fix turn after a failed gate (attempt of budget).
func FixPrompt(res api.GateResult, attempt, budget int, others []Other) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Seelegent ran the gate for your work and it failed (automatic fix %d/%d).\n", attempt, budget)
	s := Failing(res)
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
		if s.Kind == api.StepLane && len(s.Files) > 0 {
			fmt.Fprintf(&b, "Out of lane: %s\n", strings.Join(listed(s.Files), ", "))
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
		if s.Tail != "" && s.Kind != api.StepHygiene && s.Kind != api.StepAuto && s.Kind != api.StepLane {
			b.WriteString("```\n" + strings.TrimRight(s.Tail, "\n") + "\n```\n")
		}
	}
	b.WriteString("\nFix only what makes this check fail. Stay inside your lane. Do not weaken, skip or delete tests " +
		"or gate commands. Then reply with the normal report format.\n")
	b.WriteString("If the failure is in a file you did not change, do not edit that file: report it under " +
		"## Open issues.\n")
	if s != nil && s.Kind == api.StepLane {
		b.WriteString("\nUndo only your own edits to those files. Never use git checkout, git restore, git stash, git " +
			"reset or git clean: other people have uncommitted work in this folder. If the change is required, stop " +
			"and explain it under ## Blocked.\n")
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
			b.WriteString("\nDelete the junk files you or your commands created.\n")
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
	return strings.TrimRight(b.String(), "\n")
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
