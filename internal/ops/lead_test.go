package ops

import (
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDetectLead(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "cli"},
		{map[string]string{"SEELEGENT_LEAD": "OpenCode"}, "opencode"},
		{map[string]string{"SEELEGENT_LEAD": "agy", "CLAUDE_CODE_SESSION_ID": "x"}, "agy"},
		{map[string]string{"SEELEGENT_LEAD": "copilot", "CLAUDE_CODE_SESSION_ID": "x"}, "claude"},
		{map[string]string{"CODEX_THREAD_ID": "t"}, "codex"},
		{map[string]string{"GEMINI_CLI_SESSION": "g"}, "gemini"},
	}
	for _, c := range cases {
		if got := DetectLead(envOf(c.env)); got != c.want {
			t.Errorf("DetectLead(%v) = %q, want %q", c.env, got, c.want)
		}
	}
}

func TestSessions(t *testing.T) {
	env := envOf(map[string]string{"CLAUDE_CODE_SESSION_ID": "cc", "CLAUDE_CODE_HOST_SESSION_ID": "host"})
	if s := MCPSession(env); *s != (api.Session{ID: "cc", HostID: "host", Lead: "claude"}) {
		t.Errorf("MCPSession = %+v", s)
	}
	// The command line names only SEELEGENT_LEAD, else cli; it never sends the host session id.
	if s := CLISession(env); *s != (api.Session{Lead: "cli"}) {
		t.Errorf("CLISession = %+v", s)
	}
	env = envOf(map[string]string{"SEELEGENT_LEAD": "Codex", "SEELEGENT_SESSION": "s1"})
	if s := CLISession(env); *s != (api.Session{ID: "s1", Lead: "codex"}) {
		t.Errorf("CLISession = %+v", s)
	}
}
