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
)

// Environment variables that name the lead session.
const (
	// EnvLead names the lead host (setup writes it into every MCP entry): one of api.Leads.
	EnvLead = "SEELEGENT_LEAD"
	// EnvSession is the lead's session id; it wins over the host's own variables.
	EnvSession = "SEELEGENT_SESSION"
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
	case getenv("CLAUDE_CODE_SESSION_ID") != "":
		return api.LeadClaude
	case getenv("CODEX_SESSION_ID") != "" || getenv("CODEX_THREAD_ID") != "":
		return api.LeadCodex
	case getenv("GEMINI_SESSION_ID") != "" || getenv("GEMINI_CLI_SESSION") != "":
		return api.LeadGemini
	}
	return api.LeadCLI
}

// MCPSession is the session an MCP server sends with a delegate: the session id (SEELEGENT_SESSION, else Claude
// Code's), Claude Code's host session id and the detected lead.
func MCPSession(getenv func(string) string) *api.Session {
	id := getenv(EnvSession)
	if id == "" {
		id = getenv("CLAUDE_CODE_SESSION_ID")
	}
	return &api.Session{ID: id, HostID: getenv("CLAUDE_CODE_HOST_SESSION_ID"), Lead: DetectLead(getenv)}
}

// CLISession is the session the command line sends with a delegate: SEELEGENT_SESSION as its id and
// SEELEGENT_LEAD as the lead when it names a known one, else cli.
func CLISession(getenv func(string) string) *api.Session {
	lead := normLead(getenv(EnvLead))
	if lead == "" {
		lead = api.LeadCLI
	}
	return &api.Session{ID: getenv(EnvSession), Lead: lead}
}
