package guard

import (
	"encoding/json"
	"errors"
	"runtime"
	"strings"
)

// Hook kinds: the worker CLIs whose pre-tool hook runs `seelegent __guard <kind>`.
const (
	HookClaude = "claude"
	HookAgy    = "agy"
)

// HookInputLimit caps what a hook reads from standard input.
const HookInputLimit = 4 << 20

// ErrHookKind is returned for a hook kind the guard does not know.
var ErrHookKind = errors.New("unknown hook kind")

// HookInput is the part of a hook's input the guard needs.
type HookInput struct {
	// Command is the shell command the tool is about to run ("" when the tool runs none).
	Command string
	// Cwd is the folder the command runs in, when the CLI says ("" otherwise).
	Cwd string
	// Shells are the dialects the command is read in (none: unknown, all of them).
	Shells []Shell
	// HeredocAsCommands is Checker.HeredocAsCommands: set for agy on Windows, whose run_command runs PowerShell.
	HeredocAsCommands bool
}

// Checker returns the checker for this input, resolving aliases with alias.
func (h HookInput) Checker(alias func(name string) (string, bool)) Checker {
	return Checker{Alias: alias, HeredocAsCommands: h.HeredocAsCommands}
}

// ParseHook reads the hook input of a worker CLI. Input that is not valid JSON is an error (the caller refuses).
func ParseHook(kind string, data []byte) (HookInput, error) {
	return parseHook(kind, data, runtime.GOOS)
}

// parseHook is ParseHook for the operating system goos. The shell agy runs commands in is not in its hook input:
// on Windows run_command uses PowerShell (a terminal of send_command_input may run any shell), elsewhere a POSIX
// shell. Both are read in every dialect; on Windows a POSIX here-document's lines are read as commands too.
func parseHook(kind string, data []byte, goos string) (HookInput, error) {
	switch kind {
	case HookClaude:
		var in struct {
			Cwd       string `json:"cwd"`
			ToolName  string `json:"tool_name"`
			ToolInput struct {
				Command string `json:"command"`
			} `json:"tool_input"`
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return HookInput{}, err
		}
		h := HookInput{Command: in.ToolInput.Command, Cwd: in.Cwd}
		switch strings.ToLower(in.ToolName) {
		case "bash":
			h.Shells = []Shell{POSIX}
		case "powershell":
			h.Shells = []Shell{PowerShell}
		}
		return h, nil
	case HookAgy:
		var in struct {
			Cwd      string `json:"cwd"`
			ToolCall struct {
				Name string                     `json:"name"`
				Args map[string]json.RawMessage `json:"args"`
			} `json:"toolCall"`
		}
		if err := json.Unmarshal(data, &in); err != nil {
			return HookInput{}, err
		}
		str := func(keys ...string) string {
			for _, k := range keys {
				var s string
				if raw, ok := in.ToolCall.Args[k]; ok && json.Unmarshal(raw, &s) == nil && s != "" {
					return s
				}
			}
			return ""
		}
		h := HookInput{Command: str("CommandLine", "commandLine", "command", "Command"), Cwd: in.Cwd,
			HeredocAsCommands: goos == "windows"}
		if h.Command == "" && strings.EqualFold(in.ToolCall.Name, "send_command_input") {
			h.Command = str("Input", "input")
		}
		if c := str("Cwd", "cwd"); c != "" {
			h.Cwd = c
		}
		return h, nil
	}
	return HookInput{}, ErrHookKind
}

// HookOutput is what the hook prints for a decision (nil: print nothing).
func HookOutput(kind string, r Result) []byte {
	var v any
	switch kind {
	case HookClaude:
		if !r.Deny {
			return nil
		}
		v = map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": r.Reason,
		}}
	case HookAgy:
		if !r.Deny {
			v = map[string]string{"decision": "allow"}
		} else {
			v = map[string]string{"decision": "deny", "reason": r.Reason}
		}
	default:
		return nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return append(out, '\n')
}

// Unreadable is the decision for hook input that could not be read: refused, since the guard cannot tell.
func Unreadable() Result {
	return Result{Deny: true, Reason: Marker + ": the guard could not read this tool call, so it was refused. " +
		"Try again; if it keeps happening, say so under ## Blocked."}
}
