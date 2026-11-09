// Package mcpwire holds what the MCP server (internal/mcpserver through internal/ops), the daemon's relay route
// (internal/httpapi) and the companion's MCP relay (internal/relay, in cmd/seelegent-launch) must agree on, without
// any dependency: the environment variables the MCP server reads, the worker guard table and the relay protocol.
//
// It imports only the standard library's slices and time, so the companion, which must link no crypto package
// (cmd/seelegent-launch TestLinksNoCrypto), can use it. Keep it that way.
package mcpwire

import (
	"slices"
	"time"
)

// Environment variables the MCP server reads from the host's environment (ops.IsWorker, ops.MCPSession and
// ops.DetectLead). The relay forwards exactly these, never the rest of the environment, which holds the lead's API
// keys and tokens.
const (
	// EnvWorker marks a worker process: the daemon sets SEELEGENT_WORKER=1 in every worker's environment
	// (account.EnvWorker). An MCP server or a command a worker starts inherits it.
	EnvWorker = "SEELEGENT_WORKER"
	// EnvLead names the lead host (setup writes it into every MCP entry): one of api.Leads.
	EnvLead = "SEELEGENT_LEAD"
	// EnvSession is the lead's session id; it wins over the host's own variables.
	EnvSession = "SEELEGENT_SESSION"
	// The hosts' own session variables.
	EnvClaudeSession     = "CLAUDE_CODE_SESSION_ID"
	EnvClaudeHostSession = "CLAUDE_CODE_HOST_SESSION_ID"
	EnvCodexSession      = "CODEX_SESSION_ID"
	EnvCodexThread       = "CODEX_THREAD_ID"
	EnvGeminiSession     = "GEMINI_SESSION_ID"
	EnvGeminiCLISession  = "GEMINI_CLI_SESSION"
)

// SessionEnv lists every variable the MCP server reads from the host's environment, in a fixed order. The relay
// sends these (when set) in its handshake; the daemon's MCP session reads nothing else.
func SessionEnv() []string {
	return []string{EnvWorker, EnvLead, EnvSession, EnvClaudeSession, EnvClaudeHostSession, EnvCodexSession,
		EnvCodexThread, EnvGeminiSession, EnvGeminiCLISession}
}

// Forwarded reports whether the relay may forward the variable key.
func Forwarded(key string) bool { return slices.Contains(SessionEnv(), key) }

// StopGrace is how long a call whose daemon went away looks (without starting one) for a daemon that comes back,
// as after `seelegent restart` or `seelegent update`: the stop takes a few seconds and the start up to 20 s
// (ops.DefaultStopGrace, and the relay's reconnect).
const StopGrace = 40 * time.Second

// Mutating reports whether operation name with this action changes anything, so a worker may not run it (old
// mcp.js isMutating, inventory §1.3), and a relay must not send it again after the daemon restarted under it. The
// daemon refuses workers too (contract §1.3); this is the second layer, which also covers operations whose daemon
// side is not built yet. Names are the MCP tool names; the command line maps its commands onto them (run is
// delegate; stop, restart and ui are "daemon"). setup, migrate and restore are command-line only: they change the
// host configs, the data folder and the user's files.
func Mutating(name, action string) bool {
	notIn := func(read ...string) bool { return !slices.Contains(read, action) }
	switch name {
	case "delegate", "followup", "review", "cancel", "consult", "daemon", "setup", "migrate", "restore":
		return true
	case "meeting":
		return notIn("status", "list", "wait")
	case "issues":
		return notIn("repos", "list", "tickets", "ticket")
	case "memory":
		return action != "read"
	case "profiles":
		return action == "upsert" || action == "delete"
	case "proof":
		return action == "run" || action == "cancel_run"
	case "notes":
		return action != "list"
	case "automations":
		// The command line's show and preview only read: they are guarded as list. The daemon refuses every
		// automation and GitHub endpoint to a worker all the same (its allow-list), so they answer forbidden there.
		return notIn("list", "runs", "show", "preview")
	case "contest":
		return notIn("status", "wait", "list", "diff")
	case "members":
		// The MCP tool only lists (the old table never refused it); the command line's add, set and remove change.
		return slices.Contains([]string{"add", "set", "remove"}, action)
	case "hooks":
		return action != "list"
	case "update":
		// Command-line only, every mode: it replaces the executable and restarts the daemon (old C-B12).
		return true
	}
	return false
}

// The relay protocol (docs/api.md, "MCP relay"). The relay opens `POST RelayPath` with `Connection: Upgrade`,
// `Upgrade: UpgradeToken` and `X-Seelegent: 1` and no Origin; the daemon answers 101 and the connection then carries
// one Handshake line from the relay, one Reply line from the daemon, and after that newline-delimited MCP JSON-RPC
// in both directions.
const (
	// RelayPath is the route of the relay upgrade.
	RelayPath = "/api/v1/mcp/relay"
	// UpgradeToken is the Upgrade header's value; the route takes no other.
	UpgradeToken = "seelegent-mcp" //nolint:gosec // an HTTP Upgrade token, not a credential
	// Protocol is the version of the handshake and the control messages. A daemon refuses another one, and the
	// relay then runs `seelegent mcp` instead.
	Protocol = 1
	// MaxHandshake bounds the handshake line.
	MaxHandshake = 64 << 10
	// StoppingMethod is the control notification the daemon writes before it closes a relayed session because it
	// shuts down (stop, restart, update). The relay consumes it: for StopGrace after it the relay starts no daemon,
	// whether calls are pending or not; it only looks for one that comes back, as a wait does after 503
	// daemon_stopping, so a relay never starts a daemon that was just stopped on purpose (or is being replaced by an
	// update).
	StoppingMethod = "seelegent/relay/stopping"
)

// Reply codes of a refused handshake.
const (
	CodeProtocol  = "protocol"
	CodeOtherHome = "other_home"
	CodeBadHello  = "bad_handshake"
)

// Handshake is the relay's first line.
type Handshake struct {
	Protocol int `json:"protocol"`
	// Env holds the host's values of SessionEnv that are set; the daemon ignores any other key.
	Env map[string]string `json:"env"`
	// Cwd is the relay's working directory: relative paths of the tools resolve against it.
	Cwd string `json:"cwd"`
	// PID is the relay's process id (logs only).
	PID int `json:"pid"`
	// Home is the relay's data directory: a daemon of another one refuses the session (CodeOtherHome).
	Home string `json:"home,omitempty"`
}

// Reply is the daemon's answer to the handshake.
type Reply struct {
	OK       bool   `json:"ok"`
	Version  string `json:"version,omitempty"`
	Revision string `json:"revision,omitempty"`
	Code     string `json:"code,omitempty"`
	Error    string `json:"error,omitempty"`
}
