package setup

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// CodexToolTimeout is the tool_timeout_sec setup writes for Codex (old C-B5): Codex's default (60 s) is far below
// Seelegent's blocking calls, which wait up to 3000 s.
const CodexToolTimeout = 3600

// Markers around the table setup writes into config.toml (kept from the Node version).
const (
	codexStartMarker = "# >>> seelegent (managed by `seelegent setup`) >>>"
	codexEndMarker   = "# <<< seelegent <<<"
)

// codexTable is the path of Seelegent's table.
var codexTable = []string{"mcp_servers", ServerName}

// codex edits ~/.codex/config.toml (or $CODEX_HOME/config.toml) with the line-based TOML editor, changing only
// the lines of [mcp_servers.seelegent].
type codex struct{}

func codexHome(e Env) string {
	if v := e.getenv("CODEX_HOME"); v != "" {
		return v
	}
	return e.home(".codex")
}

func (codex) id() string              { return "codex" }
func (codex) name() string            { return "Codex CLI" }
func (codex) configPath(e Env) string { return filepath.Join(codexHome(e), "config.toml") }
func (codex) skillDir(e Env) string   { return e.home(".agents", "skills") }
func (codex) detect(e Env) (found bool, how string) {
	if p := e.lookPath("codex"); p != "" {
		return true, "codex CLI at " + p
	}
	if isDir(codexHome(e)) {
		return true, "config folder " + codexHome(e)
	}
	return false, "no codex CLI on PATH and no " + codexHome(e)
}

// errLayout is a config.toml that defines Seelegent's server in a form setup cannot edit safely.
type errLayout struct{ msg string }

func (e errLayout) Error() string { return e.msg + "; edit [mcp_servers.seelegent] by hand" }

// codexSpot is Seelegent's table located in a config.toml.
type codexSpot struct {
	header int // -1 when there is no table
	// start and end bound the lines removed with the table: the start marker, the header, the body up to the last
	// key/value line or the end marker.
	start, end int
	// keys maps the managed keys found in the table to their key line.
	keys map[string]int
	// last is the line after which missing keys are inserted.
	last int
}

var codexManaged = []string{"command", "args", "env", "tool_timeout_sec"}

// locate finds Seelegent's table and refuses layouts setup cannot edit: the table twice, sub-tables of it, dotted
// keys or inline tables that define mcp_servers.seelegent elsewhere (old C-B14).
func (codex) locate(f *tomlFile) (codexSpot, error) {
	spot := codexSpot{header: -1, keys: map[string]int{}}
	var cur []string
	inOurs := false
	for i, l := range f.lines {
		switch l.kind {
		case lineHeader:
			cur, inOurs = l.path, false
			switch {
			case !l.arrayTable && slices.Equal(l.path, codexTable):
				if spot.header >= 0 {
					return spot, errLayout{"[mcp_servers.seelegent] appears twice"}
				}
				spot.header, inOurs = i, true
			case len(l.path) >= 2 && slices.Equal(l.path[:2], codexTable):
				return spot, errLayout{fmt.Sprintf("[%s] is a separate table", strings.Join(l.path, "."))}
			}
		case lineKey:
			full := append(slices.Clone(cur), l.path...)
			switch {
			case inOurs:
				name := l.path[0]
				if slices.Contains(codexManaged, name) {
					if len(l.path) > 1 {
						return spot, errLayout{fmt.Sprintf("%s is set with dotted keys", name)}
					}
					spot.keys[name] = i
				}
			case len(full) >= 2 && slices.Equal(full[:2], codexTable):
				return spot, errLayout{"mcp_servers.seelegent is defined with dotted keys or an inline table"}
			case slices.Equal(full, codexTable[:1]) && strings.Contains(f.valueText(i), ServerName):
				return spot, errLayout{"mcp_servers is an inline table that names seelegent"}
			}
		}
	}
	if spot.header < 0 {
		return spot, nil
	}
	spot.start, spot.end, spot.last = spot.header, spot.header+1, spot.header
	if spot.header > 0 && strings.TrimSpace(f.lines[spot.header-1].text) == codexStartMarker {
		spot.start = spot.header - 1
	}
	for j := spot.header + 1; j < len(f.lines) && f.lines[j].kind != lineHeader; j++ {
		switch l := f.lines[j]; {
		case l.kind == lineKey || l.kind == lineCont:
			spot.last, spot.end = j, j+1
		case l.kind == lineComment && strings.TrimSpace(l.text) == codexEndMarker:
			spot.end = j + 1
			return spot, nil
		}
	}
	return spot, nil
}

// entry reads command, args and env of Seelegent's table and reports whether it is Seelegent's (an unreadable
// command line means it is not).
func (codex) entry(f *tomlFile, spot codexSpot) (e Entry, ours bool, err error) {
	val := func(name string) (tval, bool, bool) {
		k, present := spot.keys[name]
		if !present {
			return tval{}, false, true
		}
		v, err := parseValue(f.valueText(k))
		return v, true, err == nil
	}
	cmd, _, ok := val("command")
	if !ok || cmd.str == nil {
		return e, false, nil
	}
	e.Command = *cmd.str
	if args, present, ok := val("args"); present {
		list, isList := args.strings()
		if !ok || !isList {
			return e, false, nil
		}
		e.Args = list
	}
	if !isOurs(e) {
		return e, false, nil
	}
	if env, present, ok := val("env"); present {
		m, isMap := env.stringMap()
		if !ok || !isMap {
			return e, true, errLayout{"env is not an inline table of strings"}
		}
		e.Env = m
	}
	return e, true, nil
}

// codexLines renders the managed keys of an entry.
func codexLines(want Entry, env map[string]string) map[string]string {
	args := make([]string, len(want.Args))
	for i, a := range want.Args {
		args[i] = tomlString(a)
	}
	pairs := make([]string, 0, len(env))
	for _, k := range sortedKeys(env) {
		pairs = append(pairs, tomlKey(k)+" = "+tomlString(env[k]))
	}
	return map[string]string{
		"command":          "command = " + tomlString(want.Command),
		"args":             "args = [" + strings.Join(args, ", ") + "]",
		"env":              "env = { " + strings.Join(pairs, ", ") + " }",
		"tool_timeout_sec": "tool_timeout_sec = " + strconv.Itoa(CodexToolTimeout),
	}
}

// block is the whole table setup writes, between its markers.
func codexBlock(want Entry) []string {
	kv := codexLines(want, want.Env)
	out := []string{codexStartMarker, "[mcp_servers.seelegent]"}
	for _, k := range codexManaged {
		out = append(out, kv[k])
	}
	return append(out, codexEndMarker)
}

func (c codex) install(_ context.Context, o op, want Entry) Item {
	path := c.configPath(o.env)
	it := Item{Host: c.id(), Kind: KindMCP, Path: path}
	file, err := readHostFile(path)
	if err != nil {
		return failItem(it, err)
	}
	f, err := parseTOML(string(file.data))
	if err != nil {
		return failItem(it, fmt.Errorf("cannot read %s: %w", path, err))
	}
	spot, err := c.locate(f)
	if err != nil {
		return failItem(it, err)
	}
	switch {
	case spot.header < 0:
		it.Action, it.Detail = ActionAdded, "registered the MCP server"
		c.appendBlock(f, want)
	default:
		have, ours, err := c.entry(f, spot)
		switch {
		case err != nil:
			return failItem(it, err)
		case !ours && !o.req.Force:
			it.Action, it.Detail = ActionConflict, "[mcp_servers.seelegent] is not Seelegent's; nothing was written "+
				"(rename it, or rerun with --force to replace it)"
			return it
		case !ours:
			it.Action, it.Detail = ActionUpdated, "replaced the table that was not Seelegent's (--force)"
			f.replace(spot.start, spot.end, codexBlock(want))
		case matches(have, want, o.env.goos()) && hasKey(spot, "tool_timeout_sec"):
			it.Action, it.Detail = ActionUnchanged, "already registered"
			return it
		default:
			it.Action, it.Detail = ActionUpdated, "updated Seelegent's entry"
			c.update(f, spot, want, mergedEnv(have.Env, want.Env))
		}
	}
	if o.req.DryRun {
		return planned(it)
	}
	if it.Backup, err = file.write([]byte(f.render())); err != nil {
		return failItem(it, err)
	}
	return it
}

func hasKey(spot codexSpot, k string) bool { _, ok := spot.keys[k]; return ok }

func (c codex) remove(_ context.Context, o op) Item {
	path := c.configPath(o.env)
	it := Item{Host: c.id(), Kind: KindMCP, Path: path}
	file, err := readHostFile(path)
	if err != nil {
		return failItem(it, err)
	}
	f, err := parseTOML(string(file.data))
	if err != nil {
		return failItem(it, fmt.Errorf("cannot read %s: %w", path, err))
	}
	spot, err := c.locate(f)
	var layout errLayout
	switch {
	case errors.As(err, &layout):
		it.Action, it.Detail = ActionManual, err.Error()
		return it
	case err != nil:
		return failItem(it, err)
	case spot.header < 0:
		it.Action, it.Detail = ActionSkipped, "not registered"
		return it
	}
	if _, ours, _ := c.entry(f, spot); !ours {
		it.Action, it.Detail = ActionSkipped, "[mcp_servers.seelegent] is not Seelegent's; left alone"
		return it
	}
	it.Action, it.Detail = ActionRemoved, "removed the MCP server"
	if o.req.DryRun {
		return planned(it)
	}
	f.cut(spot.start, spot.end)
	if it.Backup, err = file.write([]byte(f.render())); err != nil {
		return failItem(it, err)
	}
	return it
}

// appendBlock adds the table at the end of the file, after a blank line.
func (codex) appendBlock(f *tomlFile, want Entry) {
	if n := len(f.lines); n > 0 {
		if f.lines[n-1].eol == "" {
			f.lines[n-1].eol = f.eol
		}
		if f.lines[n-1].kind != lineBlank {
			f.lines = append(f.lines, tline{eol: f.eol, kind: lineBlank})
		}
	}
	for _, s := range codexBlock(want) {
		f.lines = append(f.lines, tline{text: s, eol: f.eol, kind: lineComment})
	}
}

// update rewrites command, args and env in place (other keys and comments of the table stay) and adds missing
// managed keys after the last key of the table.
func (codex) update(f *tomlFile, spot codexSpot, want Entry, env map[string]string) {
	kv := codexLines(want, env)
	repl := map[int]string{}
	skip := map[int]bool{}
	for _, k := range []string{"command", "args", "env"} {
		if i, ok := spot.keys[k]; ok {
			repl[i] = kv[k]
			for j := i + 1; j < f.valueEnd(i); j++ {
				skip[j] = true
			}
		}
	}
	var add []string
	for _, k := range codexManaged {
		if _, ok := spot.keys[k]; !ok {
			add = append(add, kv[k])
		}
	}
	out := make([]tline, 0, len(f.lines)+len(add))
	for i, l := range f.lines {
		if s, ok := repl[i]; ok {
			l.text, l.eol = s, f.lines[f.valueEnd(i)-1].eol
			out = append(out, l)
		} else if !skip[i] {
			out = append(out, l)
		}
		if i == spot.last && len(add) > 0 {
			last := out[len(out)-1].eol
			if last == "" {
				out[len(out)-1].eol = f.eol
			}
			for n, s := range add {
				eol := f.eol
				if n == len(add)-1 && last == "" {
					eol = ""
				}
				out = append(out, tline{text: s, eol: eol, kind: lineKey})
			}
		}
	}
	f.lines = out
}

// replace swaps lines [start, end) for new lines.
func (f *tomlFile) replace(start, end int, lines []string) {
	last := f.lines[end-1].eol
	repl := make([]tline, len(lines))
	for i, s := range lines {
		repl[i] = tline{text: s, eol: f.eol, kind: lineComment}
	}
	if last == "" {
		repl[len(repl)-1].eol = ""
	}
	f.lines = slices.Concat(f.lines[:start], repl, f.lines[end:])
}

// cut removes lines [start, end) and one blank line that would otherwise double up.
func (f *tomlFile) cut(start, end int) {
	blank := func(i int) bool { return i >= 0 && i < len(f.lines) && f.lines[i].kind == lineBlank }
	atEnd := end >= len(f.lines)
	switch {
	case blank(start-1) && (atEnd || blank(end)):
		start--
	case start == 0 && blank(end):
		end++
	}
	f.lines = slices.Delete(f.lines, start, end)
}
