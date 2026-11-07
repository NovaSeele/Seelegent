package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

// HookCommand is the seelegent subcommand Claude Code runs as its PreToolUse hook for the Agent tool.
const HookCommand = "claude-agent"

// hookMatcher is the Claude Code tool the hook watches: the tool that starts Claude subagents.
const hookMatcher = "Agent"

// readOnlyAgents are the subagent types that only read or answer questions; the hook leaves them alone.
var readOnlyAgents = []string{"Explore", "Plan", "claude-code-guide", "statusline-setup"}

// hookReminder is what the hook adds to Claude Code's context before a coding subagent starts.
const hookReminder = "Seelegent is set up for this user: coding work goes to Seelegent workers through the " +
	"`delegate` tool (then `wait`, `followup`, `review`), not to Claude subagents. Use the Agent tool only for " +
	"read-only research (Explore, Plan), unless the user said to write the code yourself."

// AgentHookOutput is the answer of the Claude Code PreToolUse hook to the hook input on stdin: extra context that
// reminds the lead to delegate when it starts a subagent that could write code, and nothing otherwise. It never
// blocks the tool; unreadable input gives no output.
func AgentHookOutput(input []byte) []byte {
	var in struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			SubagentType string `json:"subagent_type"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(input, &in) != nil {
		return nil
	}
	if in.ToolName != "" && in.ToolName != hookMatcher && in.ToolName != "Task" {
		return nil
	}
	if slices.Contains(readOnlyAgents, in.ToolInput.SubagentType) {
		return nil
	}
	out, err := json.Marshal(map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName": "PreToolUse", "additionalContext": hookReminder}})
	if err != nil {
		return nil
	}
	return append(out, '\n')
}

// hookCommandLine is the command Claude Code runs: the executable in double quotes (forward slashes on Windows, so
// both cmd and a POSIX shell read it the same way) and the hook subcommand. The slashes are swapped by hand, not
// with filepath.ToSlash, because goos may differ from the OS this binary was built for.
func hookCommandLine(exe, goos string) string {
	if goos == "windows" {
		exe = strings.ReplaceAll(exe, `\`, "/")
	}
	return `"` + exe + `" hook ` + HookCommand
}

// isOurHook reports whether a hook command is Seelegent's reminder hook.
func isOurHook(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "seelegent") && strings.HasSuffix(strings.TrimSpace(cmd),
		" hook "+HookCommand)
}

// claudeSettings is Claude Code's user settings file, where hooks live.
func claudeSettings(e Env) string { return filepath.Join(claudeConfigDir(e), "settings.json") }

// hookSpot is the reminder hook located in settings.json.
type hookSpot struct {
	doc   *jsonDoc
	file  hostFile
	group int    // index in hooks.PreToolUse of the group holding our hook, -1 when absent
	inner int    // index of our hook in the group's hooks
	cmd   string // its command
}

// preToolUse is where Claude Code keeps the PreToolUse hook groups.
var preToolUse = jpath{"hooks", "PreToolUse"}

// loadHooks reads settings.json and finds the reminder hook.
func loadHooks(path string) (*hookSpot, error) {
	f, err := readHostFile(path)
	if err != nil {
		return nil, err
	}
	return findHook(f)
}

// findHook finds the reminder hook in the content of settings.json.
func findHook(f hostFile) (*hookSpot, error) {
	doc, err := loadJSONDoc(f.data, f.existed)
	if err != nil {
		return nil, err
	}
	hooks, err := doc.root.childObject("hooks")
	if err != nil {
		return nil, err
	}
	s := &hookSpot{doc: doc, file: f, group: -1}
	var groups []json.RawMessage
	if raw, ok := hooks.get("PreToolUse"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if json.Unmarshal(raw, &groups) != nil {
			return nil, errors.New("hooks.PreToolUse is not a list")
		}
	}
	for gi, g := range groups {
		var group struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(g, &group) != nil {
			continue
		}
		for hi, h := range group.Hooks {
			if isOurHook(h.Command) {
				s.group, s.inner, s.cmd = gi, hi, h.Command
				return s, nil
			}
		}
	}
	return s, nil
}

// at extends preToolUse with more path elements.
func at(elem ...any) jpath { return append(append(jpath{}, preToolUse...), elem...) }

// removeOurHook deletes the reminder hook, then the containers that are left empty: the group's hook list (with
// the group), PreToolUse and hooks.
func (s *hookSpot) removeOurHook() error {
	if _, err := s.doc.del(at(s.group, "hooks", s.inner)); err != nil {
		return err
	}
	steps := []struct{ ifEmpty, del jpath }{
		{at(s.group, "hooks"), at(s.group)},
		{preToolUse, preToolUse},
		{jpath{"hooks"}, jpath{"hooks"}},
	}
	for _, st := range steps {
		if !s.doc.isEmptyAt(st.ifEmpty) {
			return nil
		}
		if _, err := s.doc.del(st.del); err != nil {
			return err
		}
	}
	return nil
}

func (c claudeCode) installHook(o op) Item {
	path := claudeSettings(o.env)
	it := Item{Host: c.id(), Kind: KindHook, Path: path}
	want := hookCommandLine(o.req.Exe, o.env.goos())
	type commandHook struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	hook, err := marshalNoEscape(commandHook{Type: "command", Command: want})
	if err != nil {
		return failItem(it, err)
	}
	s, err := loadHooks(path)
	if errors.Is(err, errJSONC) {
		it.Action, it.Detail = ActionManual, "settings.json has comments or trailing commas, which setup would lose; "+
			"add the hook by hand"
		it.Snippet = `"hooks": {"PreToolUse": [{"matcher": "` + hookMatcher + `", "hooks": [` + string(hook) + `]}]}`
		return it
	}
	if err != nil {
		return failItem(it, err)
	}
	switch {
	case s.group >= 0 && s.cmd == want:
		it.Action, it.Detail = ActionUnchanged, "the reminder hook is installed"
		return it
	case s.group >= 0:
		it.Action, it.Detail = ActionUpdated, "updated the reminder hook"
		err = s.doc.set(at(s.group, "hooks", s.inner), hook)
	default:
		it.Action, it.Detail = ActionAdded, "installed the reminder hook (PreToolUse, Agent tool)"
		var group json.RawMessage
		group, err = marshalNoEscape(struct {
			Matcher string            `json:"matcher"`
			Hooks   []json.RawMessage `json:"hooks"`
		}{hookMatcher, []json.RawMessage{hook}})
		if err == nil {
			err = s.doc.appendTo(preToolUse, group)
		}
	}
	if err != nil {
		return failItem(it, err)
	}
	if o.req.DryRun {
		return planned(it)
	}
	if it.Backup, err = s.file.write(s.doc.bytes(), o.created); err != nil {
		return failItem(it, err)
	}
	return it
}

func (c claudeCode) removeHook(o op) Item {
	path := claudeSettings(o.env)
	it := Item{Host: c.id(), Kind: KindHook, Path: path}
	s, err := loadHooks(path)
	if errors.Is(err, errJSONC) {
		it.Action, it.Detail = ActionManual, "settings.json has comments or trailing commas; remove the "+
			"`seelegent hook "+HookCommand+"` hook by hand"
		return it
	}
	if err != nil {
		return failItem(it, err)
	}
	if s.group < 0 {
		it.Action, it.Detail = ActionSkipped, "no reminder hook installed"
		return it
	}
	it.Action, it.Detail = ActionRemoved, "removed the reminder hook"
	if o.req.DryRun {
		return planned(it)
	}
	if err := s.removeOurHook(); err != nil {
		return failItem(it, err)
	}
	var note string
	if it.Backup, note, err = o.writeHostFile(s.file, s.doc.bytes(), bareSettings); err != nil {
		return failItem(it, err)
	}
	it.Detail += note
	return it
}

// bareSettings reports whether settings.json holds nothing but, at most, the reminder hook.
func bareSettings(data []byte) bool {
	s, err := findHook(hostFile{data: data, existed: true})
	if err != nil {
		return false
	}
	if s.group >= 0 && s.removeOurHook() != nil {
		return false
	}
	return s.doc.onlyEmpty("hooks")
}
