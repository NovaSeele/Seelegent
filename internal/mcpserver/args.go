package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Tool arguments are decoded leniently, as the old server read them: numbers and booleans may come as strings
// ("600", "true"), and null means "not given". An argument the tool's input schema does not have is refused
// (checkArgs): the old server ignored it, so a lead that wrote `tier: "strong"` ran on the default tier without a
// word (field report 3, B3). The schemas are not enforced beyond that and what each handler needs.

// decodeArgs decodes the raw arguments of a call into dst. Missing or null arguments are {}.
func decodeArgs(raw json.RawMessage, dst any) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field != "" {
			return fmt.Errorf("invalid argument %s: expected %s", te.Field, jsonKind(te.Type.Kind().String()))
		}
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// unknownArgsError is a call with arguments its tool does not have.
type unknownArgsError struct {
	tool    string
	unknown []string
	valid   []string
}

func (e *unknownArgsError) Error() string {
	quoted := make([]string, len(e.unknown))
	for i, u := range e.unknown {
		quoted[i] = strconv.Quote(u)
	}
	noun := "argument"
	if len(e.unknown) > 1 {
		noun = "arguments"
	}
	msg := fmt.Sprintf("%s has no %s %s", e.tool, noun, strings.Join(quoted, ", "))
	if slices.Contains(e.unknown, "tier") && slices.Contains(e.valid, "model") {
		msg += `: tiers go in "model", e.g. model: "strong"`
	}
	if len(e.valid) == 0 {
		return msg + ". It takes no arguments."
	}
	return msg + ". Valid arguments: " + strings.Join(e.valid, ", ") + "."
}

// checkArgs refuses the top-level arguments of raw that are not in valid (the tool's input schema properties,
// sorted). Arguments that are not an object are left to decodeArgs.
func checkArgs(tool string, raw json.RawMessage, valid []string) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var args map[string]json.RawMessage
	if json.Unmarshal(raw, &args) != nil {
		return nil
	}
	var unknown []string
	for name := range args {
		if _, ok := slices.BinarySearch(valid, name); !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return &unknownArgsError{tool: tool, unknown: unknown, valid: valid}
}

func jsonKind(goKind string) string {
	switch goKind {
	case "slice", "array":
		return "an array"
	case "string":
		return "a string"
	case "map", "struct":
		return "an object"
	}
	return goKind
}

// optNum is an optional number; a numeric string is accepted too.
type optNum struct {
	set bool
	v   float64
}

func (n *optNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*n = optNum{}
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
		if s == "" {
			*n = optNum{}
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%q is not a number", s)
	}
	*n = optNum{set: true, v: v}
	return nil
}

// or returns the number, or def when it was not given.
func (n optNum) or(def float64) float64 {
	if !n.set {
		return def
	}
	return n.v
}

// optBool is an optional boolean; "true" and "false" strings are accepted too.
type optBool struct {
	set bool
	v   bool
}

func (b *optBool) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	switch strings.ToLower(strings.Trim(s, `"`)) {
	case "null", "":
		*b = optBool{}
	case "true", "1":
		*b = optBool{set: true, v: true}
	case "false", "0":
		*b = optBool{set: true, v: false}
	default:
		return fmt.Errorf("%s is not a boolean", s)
	}
	return nil
}

// or returns the value, or def when it was not given.
func (b optBool) or(def bool) bool {
	if !b.set {
		return def
	}
	return b.v
}

// ptr returns nil when the value was not given.
func (b optBool) ptr() *bool {
	if !b.set {
		return nil
	}
	v := b.v
	return &v
}

// strList is a list of strings; a single string is accepted as a list of one.
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	s := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(s, []byte("null")):
		*l = nil
		return nil
	case len(s) > 0 && s[0] == '"':
		var one string
		if err := json.Unmarshal(s, &one); err != nil {
			return err
		}
		*l = strList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(s, &many); err != nil {
		return errors.New("expected a list of strings")
	}
	*l = many
	return nil
}

// gateArg is the gate argument: a command, a list of commands, true (the repository's detected test, lint and
// build commands) or false (no gate commands; the lane and hygiene checks still run).
type gateArg struct {
	set  bool
	off  bool
	on   bool
	cmds []string
}

func (g *gateArg) UnmarshalJSON(b []byte) error {
	s := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(s, []byte("null")):
		*g = gateArg{}
	case bytes.Equal(s, []byte("false")):
		*g = gateArg{set: true, off: true}
	case bytes.Equal(s, []byte("true")):
		*g = gateArg{set: true, on: true}
	default:
		var l strList
		if err := l.UnmarshalJSON(s); err != nil {
			return errors.New("gate is a command, a list of commands, or false")
		}
		*g = gateArg{set: true, cmds: l}
	}
	return nil
}

// request maps the argument onto DelegateRequest.Gate and GateCommands.
func (g gateArg) request() (gate *bool, commands []string) {
	switch {
	case g.off:
		f := false
		return &f, nil
	case g.on:
		t := true
		return &t, nil
	}
	return nil, g.cmds
}

// followup maps the argument onto FollowupRequest.Gate and GateCommands: an explicit empty list stays an empty list
// (clear the lead's commands), never "left out".
func (g gateArg) followup() (gate *bool, commands *[]string) {
	gate, cmds := g.request()
	if g.set && gate == nil {
		if cmds == nil {
			cmds = []string{}
		}
		commands = &cmds
	}
	return gate, commands
}

// laneArg is the lane argument: a list of paths or globs, or false.
type laneArg struct {
	set   bool
	off   bool
	paths []string
}

func (a *laneArg) UnmarshalJSON(b []byte) error {
	s := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(s, []byte("null")):
		*a = laneArg{}
	case bytes.Equal(s, []byte("false")):
		*a = laneArg{set: true, off: true}
	default:
		var l strList
		if err := l.UnmarshalJSON(s); err != nil {
			return errors.New("lane is a list of paths or false")
		}
		*a = laneArg{set: true, paths: l}
	}
	return nil
}

// clampWait is the old wait clamp: default def, then 0..max whole seconds.
func clampWait(n optNum, def, maxSeconds int) int {
	v := n.or(float64(def))
	v = math.Min(math.Max(v, 0), float64(maxSeconds))
	return int(math.Round(v))
}
