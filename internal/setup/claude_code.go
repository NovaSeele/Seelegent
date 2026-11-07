package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// claudeTimeout bounds one run of the claude CLI.
const claudeTimeout = time.Minute

// claudeCode registers with Claude Code through its own CLI (`claude mcp add --scope user`): Claude Code rewrites
// ~/.claude.json all the time while it runs, so editing that file from outside could lose either side's change.
// Setup only reads the file, to know the current state and to check the CLI's work.
type claudeCode struct{}

// claudeConfigDir is $CLAUDE_CONFIG_DIR or ~/.claude.
func claudeConfigDir(e Env) string {
	if v := e.getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	return e.home(".claude")
}

// claudeJSON is the file that holds Claude Code's user-scope MCP servers: $CLAUDE_CONFIG_DIR/.claude.json, else
// the legacy ~/.claude/.config.json when it exists, else ~/.claude.json.
func claudeJSON(e Env) string {
	if v := e.getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, ".claude.json")
	}
	if legacy := e.home(".claude", ".config.json"); exists(legacy) {
		return legacy
	}
	return e.home(".claude.json")
}

// reader reads the entry the CLI writes: {"type": "stdio", "command", "args", "env"}.
func (claudeCode) reader() jsonHost {
	return jsonHost{hostID: "claude", container: "mcpServers", style: styleStd,
		extra: []jmember{{key: "type", val: []byte(`"stdio"`)}}}
}

func (claudeCode) id() string              { return "claude" }
func (claudeCode) name() string            { return "Claude Code" }
func (claudeCode) configPath(e Env) string { return claudeJSON(e) }
func (claudeCode) skillDir(e Env) string   { return filepath.Join(claudeConfigDir(e), "skills") }
func (claudeCode) cli(e Env) string        { return e.lookPath("claude") }
func (c claudeCode) detect(e Env) (found bool, how string) {
	switch {
	case c.cli(e) != "":
		return true, "claude CLI at " + c.cli(e)
	case exists(claudeJSON(e)):
		return true, "config file " + claudeJSON(e)
	case isDir(claudeConfigDir(e)):
		return true, "config folder " + claudeConfigDir(e)
	}
	return false, "no claude CLI on PATH and no " + claudeConfigDir(e)
}

// addArgs is the claude command line that registers want; the server name goes before -e, which takes several
// values and would swallow it.
func addArgs(want Entry, env map[string]string) []string {
	args := []string{"mcp", "add", ServerName, "--scope", "user"}
	for _, k := range sortedKeys(env) {
		args = append(args, "-e", k+"="+env[k])
	}
	return append(append(args, "--", want.Command), want.Args...)
}

var removeArgs = []string{"mcp", "remove", "--scope", "user", ServerName}

// commandLine shows a command for the user to run by hand.
func commandLine(bin string, args []string) string {
	parts := []string{bin}
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			a = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func (c claudeCode) install(ctx context.Context, o op, want Entry) Item {
	path := claudeJSON(o.env)
	it := Item{Host: c.id(), Kind: KindMCP, Path: path}
	cfg, err := c.reader().load(want, o.env.goos(), path)
	if err != nil {
		return failItem(it, err)
	}
	switch cfg.state {
	case stateCurrent:
		it.Action, it.Detail = ActionUnchanged, "already registered"
		return it
	case stateForeign:
		if !o.req.Force {
			it.Action, it.Detail = ActionConflict, "an MCP server named \"seelegent\" that is not Seelegent's is "+
				"registered; nothing was written (remove it with `claude mcp remove --scope user seelegent`, or "+
				"rerun with --force)"
			return it
		}
		it.Action, it.Detail = ActionUpdated, "replaced the entry that was not Seelegent's (--force)"
	case stateStale:
		it.Action, it.Detail = ActionUpdated, "updated Seelegent's entry"
	default:
		it.Action, it.Detail = ActionAdded, "registered the MCP server"
	}
	env := mergedEnv(cfg.have.Env, want.Env)
	bin := c.cli(o.env)
	if bin == "" {
		it.Action, it.Detail = ActionManual, "the claude CLI is not on PATH; run this once it is"
		it.Snippet = commandLine("claude", addArgs(want, env))
		if cfg.state != stateAbsent {
			it.Snippet = commandLine("claude", removeArgs) + "\n" + it.Snippet
		}
		return it
	}
	if o.req.DryRun {
		return planned(it)
	}
	if it.Backup, err = cfg.file.backupOnly(); err != nil {
		return failItem(it, err)
	}
	var afterRemove []byte // the file as the remove left it
	if cfg.state != stateAbsent {
		if err := c.runCLI(ctx, o.env, bin, removeArgs); err != nil {
			return failItem(it, err)
		}
		if afterRemove, err = fsutil.ReadFile(cfg.file.real); err != nil {
			afterRemove = nil
		}
	}
	if err := c.runCLI(ctx, o.env, bin, addArgs(want, env)); err != nil {
		if cfg.state != stateAbsent {
			err = c.putBack(ctx, o.env, bin, cfg, afterRemove, it.Backup, err)
		}
		return failItem(it, err)
	}
	after, err := c.reader().load(want, o.env.goos(), path)
	if err != nil {
		return failItem(it, fmt.Errorf("claude mcp add ran, but %s cannot be read: %w", path, err))
	}
	if after.state != stateCurrent {
		return failItem(it, fmt.Errorf("claude mcp add ran, but %s does not show the entry", path))
	}
	return it
}

func (c claudeCode) remove(ctx context.Context, o op) Item {
	path := claudeJSON(o.env)
	it := Item{Host: c.id(), Kind: KindMCP, Path: path}
	cfg, err := c.reader().load(Entry{}, o.env.goos(), path)
	if err != nil {
		return failItem(it, err)
	}
	switch cfg.state {
	case stateAbsent:
		it.Action, it.Detail = ActionSkipped, "not registered"
		return it
	case stateForeign:
		it.Action, it.Detail = ActionSkipped, "the \"seelegent\" server there is not Seelegent's; left alone"
		return it
	}
	it.Action, it.Detail = ActionRemoved, "removed the MCP server"
	bin := c.cli(o.env)
	if bin == "" {
		it.Action, it.Detail = ActionManual, "the claude CLI is not on PATH; run this once it is"
		it.Snippet = commandLine("claude", removeArgs)
		return it
	}
	if o.req.DryRun {
		return planned(it)
	}
	if it.Backup, err = cfg.file.backupOnly(); err != nil {
		return failItem(it, err)
	}
	if err := c.runCLI(ctx, o.env, bin, removeArgs); err != nil {
		return failItem(it, err)
	}
	return it
}

// putBack restores the entry an update removed when the following `claude mcp add` failed (addErr). It first adds
// the old entry again through the CLI. When that is not possible, it copies the backup over the file, but only if
// the file is still exactly as the remove left it (afterRemove): anything Claude Code wrote since is kept. The
// returned error says what failed and how the entry was put back, or where the backup is.
func (c claudeCode) putBack(ctx context.Context, e Env, bin string, cfg *jsonConfig, afterRemove []byte,
	backup string, addErr error) error {
	var readdErr error
	if old, ok := readEntry(cfg.entryOrEmpty()); ok {
		if readdErr = c.runCLI(ctx, e, bin, addArgs(old, old.Env)); readdErr == nil {
			return fmt.Errorf("%w; the previous entry was added back, so nothing changed", addErr)
		}
	} else {
		readdErr = errors.New("the previous entry is not a command `claude mcp add` can take")
	}
	now, err := fsutil.ReadFile(cfg.file.real)
	switch {
	case err != nil:
		return fmt.Errorf("%w; adding the previous entry back failed too (%v), and %s cannot be read (%v); the "+
			"file before setup is at %s", addErr, readdErr, cfg.file.path, err, backup)
	case afterRemove == nil || !bytes.Equal(now, afterRemove):
		return fmt.Errorf("%w; adding the previous entry back failed too (%v), and %s changed since, so it was not "+
			"overwritten; the file before setup is at %s", addErr, readdErr, cfg.file.path, backup)
	}
	if err := fsutil.WriteFileAtomic(cfg.file.real, cfg.file.data, cfg.file.perm); err != nil {
		return fmt.Errorf("%w; adding the previous entry back failed too (%v), and restoring %s failed (%v); the "+
			"file before setup is at %s", addErr, readdErr, cfg.file.path, err, backup)
	}
	return fmt.Errorf("%w; adding the previous entry back failed too (%v), so %s was restored from %s",
		addErr, readdErr, cfg.file.path, backup)
}

// entryOrEmpty is the parsed entry, or an empty object.
func (c *jsonConfig) entryOrEmpty() *jobject {
	if c.entry == nil {
		return &jobject{}
	}
	return c.entry
}

// runCLI runs the claude CLI and turns a failure into an error with its output.
func (claudeCode) runCLI(ctx context.Context, e Env, bin string, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()
	res, err := e.run(ctx, bin, args...)
	if err == nil && res.Code != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		err = fmt.Errorf("exit code %d: %s", res.Code, msg)
	}
	if err != nil {
		return fmt.Errorf("claude %s: %w", strings.Join(args[:2], " "), err)
	}
	return nil
}
