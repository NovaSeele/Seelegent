package setup

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// ServerName is the name of Seelegent's MCP server in every host config.
const ServerName = "seelegent"

// relayName is the companion's file name without extension (launchbin.Name), which runs the MCP relay with the
// argument mcp.
const relayName = "seelegent-launch"

// EnvLead tells the MCP server which host started it (ops.EnvLead, which setup does not import).
const EnvLead = "SEELEGENT_LEAD"

// Entry is an MCP server entry reduced to what setup manages: the command line and the environment.
type Entry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

// LeadValue returns the SEELEGENT_LEAD value setup writes for a host: one of api.Leads, or "" for a host the MCP
// server has no lead value for (Claude Desktop, Cursor, Windsurf). Those get no SEELEGENT_LEAD, and the server then
// detects the host from its session environment or falls back to "cli". The Antigravity app runs the agy agent.
func LeadValue(hostID string) string {
	lead := hostID
	if hostID == "antigravity" {
		lead = api.LeadAgy
	}
	if lead == api.LeadCLI || !slices.Contains(api.Leads(), lead) {
		return ""
	}
	return lead
}

// desired is the entry setup registers for a host: the command (seelegent, or the relay seelegent-launch) with the
// argument "mcp" and, when the MCP server knows the host, SEELEGENT_LEAD.
func desired(exe, hostID string) Entry {
	e := Entry{Command: exe, Args: []string{"mcp"}}
	if v := LeadValue(hostID); v != "" {
		e.Env = map[string]string{EnvLead: v}
	}
	return e
}

// isOurs reports whether e is a Seelegent entry: the Go executable (or the old seelegent.cmd shim), or the MCP relay
// seelegent-launch, with the single argument "mcp", or an entry of the Node version, `node <...seelegent...>/cli.js mcp`. Anything else under the name
// "seelegent" belongs to someone else and is never changed without --force.
func isOurs(e Entry) bool {
	base := strings.ToLower(path.Base(strings.ReplaceAll(e.Command, `\`, "/")))
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		base = strings.TrimSuffix(base, ext)
	}
	if (base == "seelegent" || base == relayName) && len(e.Args) == 1 && strings.EqualFold(e.Args[0], "mcp") {
		return true
	}
	// Node era: command node, args [<path>/cli.js, mcp] (or the whole line in command).
	parts := append([]string{e.Command}, e.Args...)
	cli := -1
	for i, p := range parts {
		n := strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
		if strings.HasSuffix(n, "/cli.js") && strings.Contains(n, "seelegent") {
			cli = i
		}
	}
	return cli >= 0 && cli+1 < len(parts) && strings.EqualFold(parts[cli+1], "mcp")
}

// samePath compares two executable paths the way the file system does: slashes and, on Windows and macOS, letter
// case do not matter.
func samePath(a, b, goos string) bool {
	norm := func(p string) string {
		p = strings.ReplaceAll(p, `\`, "/")
		for strings.Contains(p, "//") {
			p = strings.ReplaceAll(p, "//", "/")
		}
		p = strings.TrimSuffix(p, "/")
		if goos == "windows" || goos == "darwin" {
			p = strings.ToLower(p)
		}
		return p
	}
	return norm(a) == norm(b)
}

// matches reports whether have already is want: same command and arguments, and every variable of want set to
// the same value (variables the user added are allowed).
func matches(have, want Entry, goos string) bool {
	if !samePath(have.Command, want.Command, goos) || !slices.Equal(have.Args, want.Args) {
		return false
	}
	for k, v := range want.Env {
		if got, ok := have.Env[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// mergedEnv is want's variables over the ones have already sets, so variables the user added survive an update.
func mergedEnv(have, want map[string]string) map[string]string {
	out := maps.Clone(have)
	if out == nil {
		out = map[string]string{}
	}
	maps.Copy(out, want)
	return out
}

// sortedKeys returns the keys of m in order.
func sortedKeys(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}
