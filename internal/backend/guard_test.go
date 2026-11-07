package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

func collect(t *testing.T, p Parser, lines ...string) Outcome {
	t.Helper()
	var c Collector
	for _, l := range lines {
		for _, ev := range p.Line([]byte(l)) {
			c.Add(ev)
		}
	}
	exit := launch.Exit{Reason: launch.ReasonExited}
	for _, ev := range p.Finish(exit, "") {
		c.Add(ev)
	}
	return c.Outcome(exit)
}

// The refusal the guard prints reaches BlockedCommands through each parser (claude: both shell tools).
func TestGuardRefusalIsBlocked(t *testing.T) {
	reason := guard.Check("git stash").Reason
	enc, _ := json.Marshal(reason)
	for _, tool := range []string{"Bash", "PowerShell"} {
		o := collect(t, mustBackend(t, "claude").NewParser(nil),
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"`+tool+`","input":{"command":"git stash"}}]},"session_id":"s"}`,
			`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","is_error":true,"content":`+string(enc)+`}]},"session_id":"s"}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"s"}`)
		if !slices.Equal(o.BlockedCommands, []string{"git stash"}) {
			t.Errorf("claude %s: blocked %q", tool, o.BlockedCommands)
		}
	}
	msg, _ := json.Marshal("tool call denied by pre-tool hook: " + reason)
	o := collect(t, mustBackend(t, "agy").NewParser(nil),
		`{"event":"step_update","step_update":{"step_index":1,"step_type":"tool","state":"ERROR","tool_name":"send_command_input","tool_info":{"parameters":{"CommandId":"7","Input":"git stash"},"error":{"message":`+string(msg)+`}}}}`,
		`{"event":"result","result":{"status":"SUCCESS","response":"ok","conversation_id":"c"}}`)
	if !slices.Equal(o.BlockedCommands, []string{"git stash"}) {
		t.Errorf("agy send_command_input: blocked %q", o.BlockedCommands)
	}
}

func TestPrepareInstallsGuard(t *testing.T) {
	prof := testProfile(t)
	on := true
	g := WithGuard(Guard{Exe: filepath.Join(t.TempDir(), "seelegent"), Enabled: func() bool { return on }})
	for _, name := range []string{"agy", "claude"} {
		b, err := New(name, "/usr/bin/"+name, g)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.Prepare(prof, nil); err != nil {
			t.Fatal(err)
		}
	}
	claude, _ := os.ReadFile(guard.ClaudeSettingsPath(prof.Home))
	hooks, _ := os.ReadFile(filepath.Join(guard.AgyConfigDir(prof.Home), "hooks.json"))
	if !strings.Contains(string(claude), "__guard claude") || !strings.Contains(string(hooks), "seelegent-guard") {
		t.Fatalf("claude settings %s\nagy hooks %s", claude, hooks)
	}
	on = false
	for _, name := range []string{"agy", "claude"} {
		b, _ := New(name, "/usr/bin/"+name, g)
		if err := b.Prepare(prof, nil); err != nil {
			t.Fatal(err)
		}
	}
	claude, _ = os.ReadFile(guard.ClaudeSettingsPath(prof.Home))
	hooks, _ = os.ReadFile(filepath.Join(guard.AgyConfigDir(prof.Home), "hooks.json"))
	if strings.Contains(string(claude), "__guard") || strings.Contains(string(hooks), "seelegent-guard") {
		t.Fatalf("guard left after it was turned off:\n%s\n%s", claude, hooks)
	}
	// A system account's real home is never written.
	sys := account.Profile{Kind: account.KindSystem, Home: t.TempDir()}
	b, _ := New("agy", "/usr/bin/agy", g)
	on = true
	if err := b.Prepare(sys, nil); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(sys.Home); len(entries) != 0 {
		t.Fatalf("system home written: %v", entries)
	}
	if !NeedsShim(b, sys) || NeedsShim(b, prof) || !NeedsShim(mustBackend(t, "codex"), prof) {
		t.Error("NeedsShim")
	}
}
