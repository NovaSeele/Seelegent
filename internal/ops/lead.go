// Package ops holds what the lead's two front ends, the MCP server (internal/mcpserver) and the command line
// (internal/cli), do the same way over the daemon client: who the lead is (the task's session), which operations
// a worker may not run (the local worker guard), the list of tasks that need the lead, the recent worker steps of a
// task, a wait that survives a daemon restart, and the "not available in this version yet" refusal.
//
// It knows nothing about MCP or cobra: arguments arrive as plain values and results are internal/api shapes.
package ops

import (
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

// Environment variables that name the lead session.
const (
	// EnvLead names the lead host (setup writes it into every MCP entry): one of api.Leads.
	EnvLead = mcpwire.EnvLead
	// EnvSession is the lead's session id; it wins over the host's own variables.
	EnvSession = mcpwire.EnvSession
)

// normLead returns the lead in lower case when it is one of api.Leads, else "".
func normLead(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if slices.Contains(api.Leads(), s) {
		return s
	}
	return ""
}

// DetectLead names the lead host of an MCP server from the host's environment (the old detectLead): SEELEGENT_LEAD
// when it names a known lead, else claude, codex or gemini when that host's session variable is set, else cli.
func DetectLead(getenv func(string) string) string {
	if l := normLead(getenv(EnvLead)); l != "" {
		return l
	}
	switch {
	case getenv(mcpwire.EnvClaudeSession) != "":
		return api.LeadClaude
	case getenv(mcpwire.EnvCodexSession) != "" || getenv(mcpwire.EnvCodexThread) != "":
		return api.LeadCodex
	case getenv(mcpwire.EnvGeminiSession) != "" || getenv(mcpwire.EnvGeminiCLISession) != "":
		return api.LeadGemini
	}
	return api.LeadCLI
}

// MCPSession is the session an MCP server sends with a delegate: the session id (SEELEGENT_SESSION, else Claude
// Code's), Claude Code's host session id and the detected lead.
func MCPSession(getenv func(string) string) *api.Session {
	id := getenv(EnvSession)
	if id == "" {
		id = getenv(mcpwire.EnvClaudeSession)
	}
	return &api.Session{ID: id, HostID: getenv(mcpwire.EnvClaudeHostSession), Lead: DetectLead(getenv)}
}

// CLISession is the session the command line sends with a delegate: SEELEGENT_SESSION as its id and the lead as
// the old buildCliSession named it (SEELEGENT_LEAD || detectLead): a known SEELEGENT_LEAD, else the host whose
// session variable is set (a `seelegent run` from Claude Code's shell names Claude Code), else cli.
func CLISession(getenv func(string) string) *api.Session {
	return &api.Session{ID: getenv(EnvSession), Lead: DetectLead(getenv)}
}
