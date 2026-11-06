// Package brief writes the prompts workers get: the full worker brief of a turn that starts a conversation, the
// short prompts of turns that continue one, and the fixed retry, background-wait and hand-back prompts. It is pure:
// the caller gathers the inputs (role, project memory, constitution, earlier turns) and the same input always gives
// the same text, which the golden tests pin.
package brief

import (
	"fmt"
	"strings"
)

// workerRules is the fixed part of every worker brief (old WORKER_BRIEF).
const workerRules = `You are a worker engineer on a team led by a lead engineer (the lead). The lead designs, reviews and tests; you implement.
Rules:
- Do exactly the task below. Stay inside its scope; do not refactor or "improve" unrelated code.
- Add only what the task asks for: no extra options, configuration or abstractions nobody asked for, and no handling for situations that cannot happen. If 200 lines could be 50, rewrite it.
- Clean up only what your own change left behind, such as imports or functions it made unused. If you notice other dead code, mention it in your report and leave it.
- Follow the conventions of the surrounding code (naming, comments, formatting, libraries).
- Never commit, push, publish, delete branches or install global software unless the task says so.
- Never run git stash, checkout, restore, reset, clean, switch, rebase, merge, pull or similar: this folder holds other people's uncommitted work, and these commands will be refused. To undo your own edit, use your edit tool. Your report must mention anything you changed by accident.
- Run builds, tests and anything whose result you need in the foreground and wait for it, even when it takes many minutes. Never start long commands in the background (Start-Process, a trailing &, start /b, nohup, or a tool's background/async option): the worker CLI then sits idle for up to 45 minutes waiting for them and the lead cannot see your work progress. Give scratch scripts a timeout.
- Never modify files outside this repository: toolchains, package caches (~/.cargo, ~/.npm and the like), global config, other projects. If the environment looks broken (wrong compiler, corrupt cache, missing tool), stop and report it to the lead under Open issues instead of repairing it.
- When your report states facts from outside the repo (library or API behavior, versions, documentation), say how you know: write "fetched <URL>" only for pages you fetched in this task; otherwise mark the claim "(unverified)".
- Files you created only to check your work (throwaway test settings, scratch scripts, debug output) must be deleted before your report. Do not add a test folder or test settings to a repo that has none unless the brief asks for them; if it does, name them under Open issues so the lead can remove them when the work is finished.
- If the task names a way to verify (tests, build, lint, a command), run it and fix what fails. If it names none, run the cheapest relevant check you can find.
- If something is ambiguous or blocked, make the most reasonable choice, keep going, and report it.
- If you cannot continue without a decision only the lead can make, stop, and add this section at the end of your report:
## Blocked
<one question; options as a list if there are any>
- Finish with a report in exactly this shape:
## Summary
<what you did, 1-5 lines>
## Files changed
<one path per line with a few words each>
## Verification
<commands you ran and their results>
## Open issues
<anything unfinished, assumptions, questions for the lead; "none" if none>`

// Retry continues a conversation after the connection to the model dropped (old RETRY_PROMPT).
const Retry = "The connection to the model dropped mid-turn (network error). Continue the task from where you stopped. " +
	"If you had already finished, just repeat your final report in the required format."

// BackgroundWait continues a conversation whose turn ended while the CLI waited for a background command (old
// BACKGROUND_WAIT_PROMPT).
const BackgroundWait = "Your turn ended while the worker CLI was still waiting for a command you had started in the " +
	"background. Do not start anything in the background. Check that command's result in the foreground now " +
	"(re-run it in the foreground if you cannot read its output), fix what failed, and finish with your report in " +
	"the required format."

// Handback is the message of the turn that follows a takeover: the worker summarizes what the user did.
const Handback = "## Hand-back: summarize what the user did while steering, list changed files, run tests if any, " +
	"and state next steps"

// Caps on the text taken from inputs.
const (
	// ConstitutionChars caps the project constitution.
	ConstitutionChars = 6000
	// TurnChars caps each earlier message and report in the handoff section.
	TurnChars = 6000
	// BaselineShown is how many already-changed files are listed.
	BaselineShown = 30
	// DepChars caps each prerequisite's report in the dependency context.
	DepChars = 1500
	// PreviewChars is the length of a brief's preview in worker_prompt events.
	PreviewChars = 500
)

// Input is everything a worker brief is made of. Empty fields leave their section out.
type Input struct {
	// Lead names the lead ("Claude Code", "Codex", ...); empty leaves the line out.
	Lead string
	// Cwd is the task's working folder.
	Cwd string
	// Plan is true in plan mode (shell commands are refused).
	Plan bool
	// Role is the task's role profile.
	Role *Role
	// Memory is the project memory the lead keeps for workers.
	Memory string
	// Constitution is the Spec Kit constitution of the project (.specify/memory/constitution.md).
	Constitution string
	// History lists the recent tasks of the project and the lead's verdicts, one line each.
	History []string
	// Spec is the Spec Kit feature the task works on.
	Spec *Spec
	// Baseline lists the files that already had uncommitted changes when the task started.
	Baseline []string
	// Deps is the context from prerequisite tasks (DepsContext).
	Deps string
	// Earlier are the turns before this one (the handoff section).
	Earlier []Turn
	// Interrupted is true when an earlier attempt of this turn ran on another account (or conversation) and did not
	// finish.
	Interrupted bool
	// Message is this turn's request.
	Message string
}

// Role is a role profile as the brief shows it.
type Role struct {
	ID          string
	Name        string
	Description string
	Rules       string
	OutputStyle string
	// Skills are the role's skills that were found, Missing the names that were not.
	Skills  []Skill
	Missing []string
}

// Skill is one skill of a role.
type Skill struct {
	Name        string
	Description string
	// File is the path of its SKILL.md.
	File string
}

// Spec is the Spec Kit feature of a task.
type Spec struct {
	// Dir is the feature directory: absolute when Outside, else relative to the task's folder.
	Dir string
	// Outside is true when the spec workspace lies outside the repository.
	Outside bool
	// TaskIDs are the tasks of tasks.md the worker implements (empty: the lead names them in the request).
	TaskIDs []string
	// ImplementSkill is the path of the speckit-implement skill found for an outside workspace ("" when none).
	ImplementSkill string
}

// Turn is an earlier turn in the handoff section.
type Turn struct {
	N        int
	Message  string
	Response string
	Files    []string
}

// Worker returns the full brief of a turn that starts a new conversation: the rules, the context sections and the
// request. A turn after earlier ones (or after an interrupted attempt) carries the handoff section, so a worker on
// another account never loses the task (old bug B7).
func Worker(in Input) string {
	head := workerRules
	if in.Lead != "" {
		head += "\nThe lead for this task is " + in.Lead + "."
	}
	parts := []string{head, "Working directory: " + in.Cwd}
	if in.Plan {
		parts = append(parts, "Shell commands are refused in this mode, so read files with the file tools, and always "+
			"end with a text answer.")
	}
	if s := roleSection(in.Role); s != "" {
		parts = append(parts, s)
	}
	if m := strings.TrimSpace(in.Memory); m != "" {
		parts = append(parts, "## Project notes from the lead\n"+m)
	}
	if c := strings.TrimSpace(cut(in.Constitution, ConstitutionChars, "")); c != "" {
		parts = append(parts, "## Project constitution (binding)\n"+c)
	}
	if len(in.History) > 0 {
		parts = append(parts, "## Recent tasks in this project and the lead's verdicts\n"+strings.Join(in.History, "\n"))
	}
	if in.Spec != nil {
		parts = append(parts, specSection(in.Spec))
	}
	if s := baselineSection(in.Baseline); s != "" {
		parts = append(parts, s)
	}
	if d := strings.TrimSpace(in.Deps); d != "" {
		parts = append(parts, d)
	}
	if len(in.Earlier) > 0 || in.Interrupted {
		lines := []string{"## Handoff", "Another worker has been on this task before you (their session ended, so " +
			"you do not share their chat history). Inspect the current state of the files before acting; some work " +
			"below may be partially done."}
		for _, t := range in.Earlier {
			lines = append(lines, fmt.Sprintf("### Turn %d: the lead asked", t.N), cut(t.Message, TurnChars, ""))
			if t.Response != "" {
				lines = append(lines, fmt.Sprintf("### Turn %d: the worker reported", t.N), cut(t.Response, TurnChars, ""))
			}
			if len(t.Files) > 0 {
				lines = append(lines, "Files touched: "+strings.Join(t.Files, ", "))
			}
		}
		if in.Interrupted {
			lines = append(lines, "The previous attempt at the current request was interrupted before it finished "+
				"(quota or sign-in problem on that worker account).")
		}
		parts = append(parts, strings.Join(lines, "\n"), "## Current request from the lead\n"+in.Message)
	} else {
		parts = append(parts, "## Task\n"+in.Message)
	}
	return strings.Join(parts, "\n\n")
}

// Continue returns the prompt of a turn that continues the worker's conversation: the message, preceded by the
// spec tasks when the lead named them for this turn.
func Continue(message string, spec *Spec, taskIDsPassed bool) string {
	if taskIDsPassed && spec != nil {
		ids := "the tasks the lead names below"
		if len(spec.TaskIDs) > 0 {
			ids = strings.Join(spec.TaskIDs, ", ")
		}
		return fmt.Sprintf("Now work on tasks: %s (from %s/tasks.md).\n%s", ids, spec.Dir, message)
	}
	return message
}

// Dep is a prerequisite task in the dependency context.
type Dep struct {
	ID     string
	Title  string
	Status string
	// Response is the prerequisite's last report.
	Response string
}

// DepsContext returns the "Context from prerequisite tasks" section: each prerequisite's title and the start of its
// last report. It is "" without prerequisites.
func DepsContext(deps []Dep) string {
	if len(deps) == 0 {
		return ""
	}
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		title := d.Title
		if title == "" {
			title = "(untitled)"
		}
		text := cut(strings.TrimSpace(d.Response), DepChars, "…")
		if text == "" {
			text = "(no report)"
		}
		out = append(out, fmt.Sprintf("### %s: %s (%s)\n%s", d.ID, title, d.Status, text))
	}
	return "## Context from prerequisite tasks\n\nThese tasks ran before yours; their reports are summarized below.\n\n" +
		strings.Join(out, "\n\n")
}

// LeadName returns how the brief names the lead of a session host (claude, codex, opencode, gemini, agy, cli).
// An unknown host is "a CLI user"; an empty one gives "" (no lead line).
func LeadName(host string) string {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "":
		return ""
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	case "gemini", "agy":
		return "Gemini"
	}
	return "a CLI user"
}

// Preview returns the first PreviewChars characters of a brief.
func Preview(prompt string) string { return cut(prompt, PreviewChars, "") }

func roleSection(r *Role) string {
	if r == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("## Your role: %s (%s)", r.Name, r.ID)}
	if d := strings.TrimSpace(r.Description); d != "" {
		parts = append(parts, d)
	}
	if rules := strings.TrimSpace(r.Rules); rules != "" {
		parts = append(parts, "### Role rules (binding)\n"+rules)
	}
	if style := strings.TrimSpace(r.OutputStyle); style != "" {
		parts = append(parts, "### Output style\n"+style+"\nKeep the required report headings exactly; this style "+
			"applies to the prose under them.")
	}
	if len(r.Skills) > 0 || len(r.Missing) > 0 {
		lines := []string{"### Skills for your role"}
		if len(r.Skills) > 0 {
			lines = append(lines, "Read a skill's SKILL.md completely before doing work it covers.")
			for _, s := range r.Skills {
				desc := ""
				if s.Description != "" {
					desc = s.Description + " — "
				}
				lines = append(lines, fmt.Sprintf("- %s: %s%s", s.Name, desc, s.File))
			}
		}
		if len(r.Missing) > 0 {
			lines = append(lines, "(Missing skills: "+strings.Join(r.Missing, ", ")+")")
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func specSection(s *Spec) string {
	ids := "the tasks the lead names below"
	if len(s.TaskIDs) > 0 {
		ids = strings.Join(s.TaskIDs, ", ")
	}
	d := s.Dir
	if s.Outside {
		skill := ""
		if s.ImplementSkill != "" {
			skill = fmt.Sprintf(" Follow the discipline of the speckit-implement skill (%s) if present.", s.ImplementSkill)
		}
		return fmt.Sprintf(`## Spec Kit feature
This project uses GitHub Spec Kit. Feature directory: %[1]s. Before coding, read %[1]s/spec.md, %[1]s/plan.md and %[1]s/tasks.md, plus %[1]s/data-model.md, %[1]s/research.md, %[1]s/quickstart.md and everything in %[1]s/contracts/ that exists. They are the source of truth; contracts are binding interfaces other workers code against.
The spec folder is deliberately outside this repo, read it by absolute path. Never copy spec files or create `+"`specs/`, `.specify/`, `.agents/`"+` in this repo.
Your tasks from %[1]s/tasks.md: %[2]s. Implement only these, test-first where %[1]s/tasks.md says so.%[3]s
Do not edit %[1]s/tasks.md or any spec file: the lead ticks tasks after review. If the spec and your tasks conflict or are ambiguous, make no guess on behavior: report it under Open issues.`, d, ids, skill)
	}
	return fmt.Sprintf(`## Spec Kit feature
This project uses GitHub Spec Kit. Feature directory: %[1]s. Before coding, read %[1]s/spec.md, %[1]s/plan.md and %[1]s/tasks.md, plus %[1]s/data-model.md, %[1]s/research.md, %[1]s/quickstart.md and everything in %[1]s/contracts/ that exists. They are the source of truth; contracts are binding interfaces other workers code against.
Your tasks from tasks.md: %[2]s. Implement only these, test-first where tasks.md says so, and follow the discipline of the speckit-implement skill (.agents/skills/speckit-implement/SKILL.md) if present.
Do not edit tasks.md or any spec file: the lead ticks tasks after review. If the spec and your tasks conflict or are ambiguous, make no guess on behavior: report it under Open issues.`, d, ids)
}

func baselineSection(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	n := len(paths)
	shown := paths[:min(n, BaselineShown)]
	lines := make([]string, len(shown))
	for i, p := range shown {
		lines[i] = "- " + p
	}
	more := ""
	if n > BaselineShown {
		more = fmt.Sprintf("\n... and %d more", n-BaselineShown)
	}
	plural := "s"
	if n == 1 {
		plural = ""
	}
	return fmt.Sprintf("## Uncommitted work already in this folder\n%d file%s already have uncommitted changes from "+
		"other people, so git diff and git status will show them and this is expected and is not yours to clean:\n%s%s",
		n, plural, strings.Join(lines, "\n"), more)
}

// cut returns at most n characters (runes) of s, followed by tail when it was cut.
func cut(s string, n int, tail string) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + tail
}
