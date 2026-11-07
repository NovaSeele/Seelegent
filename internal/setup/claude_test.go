package setup

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// fakeClaude makes the machine's claude CLI edit ~/.claude.json the way `claude mcp add/remove --scope user` does.
func fakeClaude(t *testing.T, m *machine) string {
	t.Helper()
	m.bins["claude"] = m.path("bin", "claude.exe")
	cfg := m.path(".claude.json")
	m.onRun = func(_ string, args []string) RunResult {
		data, err := os.ReadFile(cfg)
		if err != nil {
			data = []byte("{}")
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return RunResult{Code: 1, Stderr: err.Error()}
		}
		servers, _ := doc["mcpServers"].(map[string]any)
		if servers == nil {
			servers = map[string]any{}
		}
		switch {
		case slices.Equal(args, removeArgs):
			if _, ok := servers[ServerName]; !ok {
				return RunResult{Code: 1, Stderr: "No user-scoped MCP server found with name: seelegent"}
			}
			delete(servers, ServerName)
		case len(args) > 3 && args[1] == "add":
			if _, ok := servers[ServerName]; ok {
				return RunResult{Code: 1, Stderr: "MCP server seelegent already exists in user config"}
			}
			sep := slices.Index(args, "--")
			env := map[string]string{}
			for i := 5; i < sep; i += 2 {
				k, v, _ := strings.Cut(args[i+1], "=")
				env[k] = v
			}
			servers[args[2]] = map[string]any{"type": "stdio", "command": args[sep+1], "args": args[sep+2:], "env": env}
		default:
			return RunResult{Code: 1, Stderr: "unexpected " + strings.Join(args, " ")}
		}
		doc["mcpServers"] = servers
		out, _ := json.MarshalIndent(doc, "", "  ")
		if err := os.WriteFile(cfg, out, 0o600); err != nil {
			return RunResult{Code: 1, Stderr: err.Error()}
		}
		return RunResult{}
	}
	return cfg
}

func TestClaudeCodeThroughCLI(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	cfg := fakeClaude(t, m)
	m.write(".claude.json", `{"numStartups": 5, "mcpServers": {"other": {"command": "x"}}}`)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"claude"}})
	it := wantAction(t, rep, "claude", KindMCP, ActionAdded)
	if it.Backup != cfg+BackupSuffix || !strings.Contains(read(t, it.Backup), "numStartups") {
		t.Errorf("backup %q", it.Backup)
	}
	want := []string{"mcp", "add", "seelegent", "--scope", "user", "-e", "SEELEGENT_LEAD=claude", "--", exe, "mcp"}
	if len(m.runs) != 1 || !slices.Equal(m.runs[0][1:], want) {
		t.Fatalf("runs %q", m.runs)
	}
	skill := wantAction(t, rep, "claude", KindSkill, ActionAdded)
	if skill.Path != m.path(".claude", "skills", "seelegent", "SKILL.md") || !exists(skill.Path) {
		t.Errorf("skill at %s", skill.Path)
	}

	m.runs = nil
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"claude"}})
	wantAction(t, rep, "claude", KindMCP, ActionUnchanged)
	wantAction(t, rep, "claude", KindSkill, ActionUnchanged)
	if len(m.runs) != 0 {
		t.Errorf("unchanged run ran %q", m.runs)
	}

	// A moved executable: remove, then add again with the env the user added kept.
	data := strings.Replace(read(t, cfg), `"SEELEGENT_LEAD": "claude"`, `"SEELEGENT_LEAD": "claude", "SEELEGENT_HOME": "D:\\data"`, 1)
	m.write(".claude.json", data)
	exe2 := m.write("bin2/seelegent.exe", "binary")
	rep = run(t, m, Request{Exe: exe2, Hosts: []string{"claude"}})
	wantAction(t, rep, "claude", KindMCP, ActionUpdated)
	if len(m.runs) != 2 || !slices.Equal(m.runs[0][1:], removeArgs) || !slices.Contains(m.runs[1], `SEELEGENT_HOME=D:\data`) {
		t.Errorf("runs %q", m.runs)
	}

	m.runs = nil
	rep = run(t, m, Request{Remove: true, Hosts: []string{"claude"}})
	wantAction(t, rep, "claude", KindMCP, ActionRemoved)
	wantAction(t, rep, "claude", KindSkill, ActionRemoved)
	if exists(m.path(".claude", "skills", "seelegent")) {
		t.Error("skill folder left behind")
	}
	if strings.Contains(read(t, cfg), "seelegent") || !strings.Contains(read(t, cfg), "numStartups") {
		t.Errorf("after remove: %s", read(t, cfg))
	}
}

func TestClaudeCodeCLIFailsOrMissing(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".claude")
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, NoSkill: true})
	it := wantAction(t, rep, "claude", KindMCP, ActionManual)
	if !strings.Contains(it.Snippet, "claude mcp add seelegent --scope user -e SEELEGENT_LEAD=claude -- ") {
		t.Errorf("snippet %q", it.Snippet)
	}
	m.bins["claude"] = m.path("bin", "claude.exe")
	m.onRun = func(string, []string) RunResult { return RunResult{Code: 2, Stderr: "boom"} }
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, NoSkill: true})
	it = wantAction(t, rep, "claude", KindMCP, ActionError)
	if !strings.Contains(it.Detail, "boom") {
		t.Errorf("detail %q", it.Detail)
	}
	// The CLI reports success but the entry did not appear.
	m.onRun = nil
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, NoSkill: true})
	wantAction(t, rep, "claude", KindMCP, ActionError)
}

func TestClaudeConfigDirEnv(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	dir := m.path("custom-claude")
	m.vars["CLAUDE_CONFIG_DIR"] = dir
	m.bins["claude"] = m.path("bin", "claude.exe")
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, DryRun: true, Hooks: true})
	if it := item(t, rep, "claude", KindMCP); it.Path != filepath.Join(dir, ".claude.json") {
		t.Errorf("config %s", it.Path)
	}
	if it := item(t, rep, "claude", KindSkill); it.Path != filepath.Join(dir, "skills", "seelegent", "SKILL.md") {
		t.Errorf("skill %s", it.Path)
	}
	if it := item(t, rep, "claude", KindHook); it.Path != filepath.Join(dir, "settings.json") {
		t.Errorf("hook %s", it.Path)
	}
}

func TestHookInstallRemove(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	fakeClaude(t, m)
	orig := `{
  "permissions": {
    "allow": ["Bash(go test:*)"]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{"type": "command", "command": "echo hi"}]
      }
    ]
  }
}
`
	cfg := m.write(".claude/settings.json", orig)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, Hooks: true, NoSkill: true})
	wantAction(t, rep, "claude", KindHook, ActionAdded)
	var doc struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type, Command string
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(read(t, cfg)), &doc); err != nil {
		t.Fatal(err)
	}
	groups := doc.Hooks.PreToolUse
	if len(groups) != 2 || groups[1].Matcher != "Agent" || groups[1].Hooks[0].Command != hookCommandLine(exe, m.env().goos()) {
		t.Fatalf("hooks %+v", groups)
	}
	if !strings.Contains(read(t, cfg), "go test:*") {
		t.Error("lost permissions")
	}
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, Hooks: true, NoSkill: true})
	wantAction(t, rep, "claude", KindHook, ActionUnchanged)
	// Without --hooks an install leaves the hook alone.
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"claude"}, NoSkill: true})
	if slices.ContainsFunc(rep.Items, func(it Item) bool { return it.Kind == KindHook }) {
		t.Error("hook item without --hooks")
	}
	rep = run(t, m, Request{Remove: true, Hosts: []string{"claude"}, NoSkill: true})
	wantAction(t, rep, "claude", KindHook, ActionRemoved)
	var after, before any
	if err := json.Unmarshal([]byte(read(t, cfg)), &after); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(orig), &before); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(after)
	b, _ := json.Marshal(before)
	if !bytes.Equal(a, b) {
		t.Errorf("remove left %s", read(t, cfg))
	}
}

func TestAgentHookOutput(t *testing.T) {
	out := AgentHookOutput([]byte(`{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","prompt":"x"}}`))
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" || !strings.Contains(got.HookSpecificOutput.AdditionalContext, "delegate") {
		t.Errorf("output %s", out)
	}
	for _, in := range []string{
		`{"tool_name":"Agent","tool_input":{"subagent_type":"Explore"}}`,
		`{"tool_name":"Bash","tool_input":{}}`,
		`not json`,
	} {
		if out := AgentHookOutput([]byte(in)); out != nil {
			t.Errorf("%s gave %s", in, out)
		}
	}
	if hookCommandLine(`C:\Program Files\seelegent\seelegent.exe`, "windows") != `"C:/Program Files/seelegent/seelegent.exe" hook claude-agent` {
		t.Error("windows hook command line")
	}
	if !isOurHook(`"C:/x/seelegent.exe" hook claude-agent`) || isOurHook("echo hook claude-agent") {
		t.Error("isOurHook")
	}
}

func TestSkillGolden(t *testing.T) {
	got, err := RenderSkill("v1.2.3", 47821)
	if err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join("testdata", "SKILL.md.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("skill differs from %s (rerun with -update after checking the change)", golden)
	}
	if !isOurSkill([]byte(got)) {
		t.Error("the rendered skill is not recognized as Seelegent's")
	}
	// The skill teaches only what exists (old F10): refused options and unavailable tools are named once, on the
	// "Not available" line, and nowhere else.
	var taught []string
	for line := range strings.SplitSeq(got, "\n") {
		if !strings.HasPrefix(line, "Not available in this version") {
			taught = append(taught, line)
		}
	}
	rest := strings.Join(taught, "\n")
	for _, banned := range []string{"lane", "variant", "`member`", "`proof`", "`members`", "`meeting`", "`consult`",
		"`contest`", "`issues`", "`automations`", "verify: true"} {
		if strings.Contains(rest, banned) {
			t.Errorf("skill teaches %q", banned)
		}
	}
	for _, tool := range []string{"delegate", "followup", "wait", "review", "cancel", "task", "status", "memory", "profiles"} {
		if !strings.Contains(got, "`"+tool+"`") {
			t.Errorf("skill does not name %s", tool)
		}
	}
	if !strings.Contains(got, "http://127.0.0.1:47821/") || !strings.Contains(got, "v1.2.3") {
		t.Error("dashboard or version missing")
	}
}

func TestIsOurSkill(t *testing.T) {
	for in, want := range map[string]bool{
		"---\nname: seelegent\nmetadata:\n  generator: seelegent\n---\n# x":        true,
		"---\r\nname: \"seelegent\"\r\nmetadata.generator: 'seelegent'\r\n---\r\n": true,
		"---\nname: seelegent\ndescription: mine\n---\n":                           false,
		"---\nname: other\nmetadata:\n  generator: seelegent\n---\n":               false,
		"---\nname: seelegent\ngenerator: seelegent\n---\n":                        false,
		"# no front matter": false,
	} {
		if got := isOurSkill([]byte(in)); got != want {
			t.Errorf("isOurSkill(%q) = %v", in, got)
		}
	}
}

func TestSkillConflictAndSharedFolder(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".codex")
	m.mkdir(".config/opencode")
	mine := m.write(".agents/skills/seelegent/SKILL.md", "---\nname: seelegent\ndescription: my own\n---\n")
	rep := run(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindSkill, ActionConflict)
	if it := wantAction(t, rep, "opencode", KindSkill, ActionSkipped); !strings.Contains(it.Detail, "codex") {
		t.Errorf("shared folder detail %q", it.Detail)
	}
	rep = run(t, m, Request{Exe: exe, Force: true})
	it := wantAction(t, rep, "codex", KindSkill, ActionUpdated)
	if !strings.Contains(read(t, it.Backup), "my own") || !isOurSkill([]byte(read(t, mine))) {
		t.Error("force did not back up and replace the skill")
	}
	// Removing only Codex keeps the skill OpenCode still uses.
	rep = run(t, m, Request{Remove: true, Hosts: []string{"codex"}})
	if it := wantAction(t, rep, "codex", KindSkill, ActionSkipped); !strings.Contains(it.Detail, "opencode") {
		t.Errorf("detail %q", it.Detail)
	}
	rep = run(t, m, Request{Remove: true})
	wantAction(t, rep, "codex", KindSkill, ActionRemoved)
	if !exists(m.path(".agents", "skills", "seelegent", "SKILL.md"+BackupSuffix)) {
		t.Error("the backup of the user's skill was deleted")
	}
}
