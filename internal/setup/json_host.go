package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// entryStyle is how a host writes an MCP server entry.
type entryStyle int

const (
	// styleStd: {"command": "<exe>", "args": ["mcp"], "env": {...}} (Claude Desktop, Antigravity, Gemini CLI, Cursor,
	// Windsurf, Claude Code).
	styleStd entryStyle = iota
	// styleOpenCode: {"type": "local", "command": ["<exe>", "mcp"], "environment": {...}, "enabled": true}.
	styleOpenCode
)

// jsonHost is a host whose MCP servers live in a JSON file that setup edits itself.
type jsonHost struct {
	hostID, hostName string
	path             func(Env) string
	// container is the key of the object that holds the servers: "mcpServers", or "mcp" for OpenCode.
	container string
	style     entryStyle
	// extra are keys added to Seelegent's entry when missing (for example a tool timeout); values the user changed
	// are kept.
	extra   []jmember
	detects func(Env) (bool, string)
	skills  func(Env) string
}

func (h jsonHost) id() string                            { return h.hostID }
func (h jsonHost) name() string                          { return h.hostName }
func (h jsonHost) configPath(e Env) string               { return h.path(e) }
func (h jsonHost) detect(e Env) (found bool, how string) { return h.detects(e) }
func (h jsonHost) skillDir(e Env) string                 { return callOr(h.skills, e) }
func callOr(f func(Env) string, e Env) string { // a nil func means "none"
	if f == nil {
		return ""
	}
	return f(e)
}

// entryState is how Seelegent's entry looks in a host config.
type entryState int

const (
	stateAbsent  entryState = iota
	stateCurrent            // ours and as wanted
	stateStale              // ours, but different
	stateForeign            // someone else's entry under our name
)

// jsonConfig is a parsed host config with Seelegent's entry located.
type jsonConfig struct {
	file      hostFile
	doc       *jsonDoc
	container *jobject
	entry     *jobject // nil when absent or not an object
	have      Entry
	state     entryState
}

// load reads the host config and classifies Seelegent's entry against want.
func (h jsonHost) load(want Entry, goos, path string) (*jsonConfig, error) {
	f, err := readHostFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := loadJSONDoc(f.data, f.existed)
	if err != nil {
		return nil, err
	}
	c, err := doc.root.childObject(h.container)
	if err != nil {
		return nil, err
	}
	cfg := &jsonConfig{file: f, doc: doc, container: c}
	raw, ok := c.get(ServerName)
	if !ok {
		return cfg, nil
	}
	obj, err := parseObject(raw)
	if err != nil {
		cfg.state = stateForeign
		return cfg, nil
	}
	cfg.entry = obj
	have, ok := readEntry(obj)
	switch {
	case !ok || !isOurs(have):
		cfg.state = stateForeign
	case matches(have, want, goos) && h.flagsCurrent(obj):
		cfg.have, cfg.state = have, stateCurrent
	default:
		cfg.have, cfg.state = have, stateStale
	}
	return cfg, nil
}

// flagsCurrent checks the keys besides the command line that make the entry work: OpenCode's type and enabled,
// a "disabled": true some hosts support, and the extra keys.
func (h jsonHost) flagsCurrent(obj *jobject) bool {
	if raw, ok := obj.get("disabled"); ok && string(bytes.TrimSpace(raw)) == "true" {
		return false
	}
	if h.style == styleOpenCode {
		if raw, _ := obj.get("type"); string(bytes.TrimSpace(raw)) != `"local"` {
			return false
		}
		if raw, _ := obj.get("enabled"); string(bytes.TrimSpace(raw)) != "true" {
			return false
		}
	}
	for _, x := range h.extra {
		if _, ok := obj.get(x.key); !ok {
			return false
		}
	}
	return true
}

// readEntry reads command, arguments and environment from an entry in either style.
func readEntry(obj *jobject) (Entry, bool) {
	var e Entry
	raw, ok := obj.get("command")
	if !ok {
		return e, false
	}
	var parts []string
	if json.Unmarshal(raw, &e.Command) != nil {
		if json.Unmarshal(raw, &parts) != nil || len(parts) == 0 {
			return e, false
		}
		e.Command, e.Args = parts[0], parts[1:]
	}
	if raw, ok := obj.get("args"); ok && parts == nil {
		if json.Unmarshal(raw, &e.Args) != nil {
			return e, false
		}
	}
	for _, k := range []string{"env", "environment"} {
		if raw, ok := obj.get(k); ok {
			if json.Unmarshal(raw, &e.Env) != nil {
				return e, false
			}
		}
	}
	return e, true
}

// render builds Seelegent's entry: base is the existing entry (its other keys are kept) or nil for a new one.
func (h jsonHost) render(base *jobject, want Entry, haveEnv map[string]string) (json.RawMessage, error) {
	obj := &jobject{}
	if base != nil {
		obj = &jobject{members: append([]jmember(nil), base.members...)}
	}
	env := mergedEnv(haveEnv, want.Env)
	envObj := &jobject{}
	for _, k := range sortedKeys(env) {
		v, err := marshalNoEscape(env[k])
		if err != nil {
			return nil, err
		}
		envObj.set(k, v)
	}
	envRaw, err := envObj.compact()
	if err != nil {
		return nil, err
	}
	set := func(k string, v any) error {
		raw, err := marshalNoEscape(v)
		if err != nil {
			return err
		}
		obj.set(k, raw)
		return nil
	}
	switch h.style {
	case styleOpenCode:
		err = errors.Join(set("type", "local"), set("command", append([]string{want.Command}, want.Args...)))
		obj.del("args")
		obj.del("env")
		if len(env) > 0 {
			obj.set("environment", envRaw)
		}
		err = errors.Join(err, set("enabled", true))
	default:
		err = errors.Join(set("command", want.Command), set("args", want.Args))
		obj.del("environment")
		if len(env) > 0 {
			obj.set("env", envRaw)
		}
		if raw, ok := obj.get("disabled"); ok && string(bytes.TrimSpace(raw)) == "true" {
			err = errors.Join(err, set("disabled", false))
		}
	}
	if err != nil {
		return nil, err
	}
	for _, x := range h.extra {
		if _, ok := obj.get(x.key); !ok {
			obj.set(x.key, x.val)
		}
	}
	return obj.compact()
}

// snippet is the entry to paste by hand under the container key.
func (h jsonHost) snippet(want Entry) string {
	raw, err := h.render(nil, want, nil)
	if err != nil {
		return ""
	}
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return ""
	}
	return fmt.Sprintf("Add under %q:\n%q: %s", h.container, ServerName, b.String())
}

func (h jsonHost) install(_ context.Context, o op, want Entry) Item {
	path := h.path(o.env)
	it := Item{Host: h.hostID, Kind: KindMCP, Path: path}
	cfg, err := h.load(want, o.env.goos(), path)
	if errors.Is(err, errJSONC) {
		it.Action, it.Detail, it.Snippet = ActionManual, "the file has comments or trailing commas, which setup "+
			"would lose; add the entry by hand", h.snippet(want)
		return it
	}
	if err != nil {
		return failItem(it, err)
	}
	var base *jobject
	switch cfg.state {
	case stateCurrent:
		it.Action, it.Detail = ActionUnchanged, "already registered"
		return it
	case stateForeign:
		if !o.req.Force {
			it.Action, it.Detail = ActionConflict, "an entry named \"seelegent\" that is not Seelegent's is there; "+
				"nothing was written (rename it, or rerun with --force to replace it)"
			return it
		}
		it.Action, it.Detail = ActionUpdated, "replaced the entry that was not Seelegent's (--force)"
	case stateStale:
		base = cfg.entry
		it.Action, it.Detail = ActionUpdated, "updated Seelegent's entry"
	default:
		it.Action, it.Detail = ActionAdded, "registered the MCP server"
	}
	if o.req.DryRun {
		return planned(it)
	}
	raw, err := h.render(base, want, cfg.have.Env)
	if err != nil {
		return failItem(it, err)
	}
	cfg.container.set(ServerName, raw)
	it.Backup, err = cfg.save(h.container)
	if err != nil {
		return failItem(it, err)
	}
	return it
}

func (h jsonHost) remove(_ context.Context, o op) Item {
	path := h.path(o.env)
	it := Item{Host: h.hostID, Kind: KindMCP, Path: path}
	cfg, err := h.load(Entry{}, o.env.goos(), path)
	if errors.Is(err, errJSONC) {
		if f, rerr := readHostFile(path); rerr == nil && !bytes.Contains(f.data, []byte(`"`+ServerName+`"`)) {
			it.Action, it.Detail = ActionSkipped, "not registered"
			return it
		}
		it.Action, it.Detail = ActionManual, "the file has comments or trailing commas, which setup would lose; "+
			"remove the \"seelegent\" entry by hand"
		return it
	}
	if err != nil {
		return failItem(it, err)
	}
	switch cfg.state {
	case stateAbsent:
		it.Action, it.Detail = ActionSkipped, "not registered"
		return it
	case stateForeign:
		it.Action, it.Detail = ActionSkipped, "the \"seelegent\" entry there is not Seelegent's; left alone"
		return it
	}
	it.Action, it.Detail = ActionRemoved, "removed the MCP server"
	if o.req.DryRun {
		return planned(it)
	}
	cfg.container.del(ServerName)
	it.Backup, err = cfg.save(h.container)
	if err != nil {
		return failItem(it, err)
	}
	return it
}

// save writes the changed container back into the file.
func (c *jsonConfig) save(container string) (string, error) {
	raw, err := c.container.compact()
	if err != nil {
		return "", err
	}
	c.doc.root.set(container, raw)
	data, err := c.doc.bytes()
	if err != nil {
		return "", err
	}
	return c.file.write(data)
}

// failItem turns an error into an error item.
func failItem(it Item, err error) Item {
	it.Action, it.Detail = ActionError, err.Error()
	it.Backup = ""
	return it
}
