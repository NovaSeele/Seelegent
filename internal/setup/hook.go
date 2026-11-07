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
// both cmd and a POSIX shell read it the same way) and the hook subcommand.
func hookCommandLine(exe, goos string) string {
	if goos == "windows" {
		exe = filepath.ToSlash(exe)
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
	doc    *jsonDoc
	file   hostFile
	hooks  *jobject          // settings.hooks
	groups []json.RawMessage // hooks.PreToolUse
	group  int               // index of the group holding our hook, -1 when absent
	inner  int               // index of our hook inside the group
	cmd    string            // its command
}

// loadHooks reads settings.json and finds the reminder hook.
func loadHooks(path string) (*hookSpot, error) {
	f, err := readHostFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := loadJSONDoc(f.data, f.existed)
	if err != nil {
		return nil, err
	}
	hooks, err := doc.root.childObject("hooks")
	if err != nil {
		return nil, err
	}
	s := &hookSpot{doc: doc, file: f, hooks: hooks, group: -1}
	if raw, ok := hooks.get("PreToolUse"); ok && string(bytes.TrimSpace(raw)) != "null" {
		if json.Unmarshal(raw, &s.groups) != nil {
			return nil, errors.New("hooks.PreToolUse is not a list")
		}
	}
	for gi, g := range s.groups {
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

// save writes the PreToolUse list back (dropping empty containers) and returns the backup path.
func (s *hookSpot) save() (string, error) {
	if len(s.groups) == 0 {
		s.hooks.del("PreToolUse")
	} else {
		s.hooks.set("PreToolUse", rawList(s.groups))
	}
	if len(s.hooks.members) == 0 {
		s.doc.root.del("hooks")
	} else {
		raw, err := s.hooks.compact()
		if err != nil {
			return "", err
		}
		s.doc.root.set("hooks", raw)
	}
	data, err := s.doc.bytes()
	if err != nil {
		return "", err
	}
	return s.file.write(data)
}

// rawList joins raw JSON values into a list.
func rawList(items []json.RawMessage) json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(it)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// editGroup changes the hooks of group gi with fn (which gets and returns the inner hook list).
func (s *hookSpot) editGroup(gi int, fn func([]json.RawMessage) []json.RawMessage) error {
	g, err := parseObject(s.groups[gi])
	if err != nil {
		return err
	}
	var inner []json.RawMessage
	if raw, ok := g.get("hooks"); ok {
		if err := json.Unmarshal(raw, &inner); err != nil {
			return err
		}
	}
	inner = fn(inner)
	if len(inner) == 0 {
		s.groups = slices.Delete(s.groups, gi, gi+1)
		return nil
	}
	g.set("hooks", rawList(inner))
	s.groups[gi], err = g.compact()
	return err
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
		err = s.editGroup(s.group, func(in []json.RawMessage) []json.RawMessage {
			in[s.inner] = hook
			return in
		})
	default:
		it.Action, it.Detail = ActionAdded, "installed the reminder hook (PreToolUse, Agent tool)"
		var group json.RawMessage
		group, err = marshalNoEscape(struct {
			Matcher string            `json:"matcher"`
			Hooks   []json.RawMessage `json:"hooks"`
		}{hookMatcher, []json.RawMessage{hook}})
		s.groups = append(s.groups, group)
	}
	if err != nil {
		return failItem(it, err)
	}
	if o.req.DryRun {
		return planned(it)
	}
	if it.Backup, err = s.save(); err != nil {
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
	if err := s.editGroup(s.group, func(in []json.RawMessage) []json.RawMessage {
		return slices.Delete(in, s.inner, s.inner+1)
	}); err != nil {
		return failItem(it, err)
	}
	if it.Backup, err = s.save(); err != nil {
		return failItem(it, err)
	}
	return it
}
