package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// machine is a fake computer: a temporary home, environment variables, programs on PATH and a recorder of the
// host CLIs setup runs. Nothing reaches the real home directory.
type machine struct {
	t    *testing.T
	home string
	vars map[string]string
	bins map[string]string
	runs [][]string
	// onRun answers a host CLI run (nil: exit 0).
	onRun func(name string, args []string) RunResult
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	home := t.TempDir()
	return &machine{t: t, home: home, bins: map[string]string{}, vars: map[string]string{
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
	}}
}

func (m *machine) env() Env {
	return Env{
		Home:   m.home,
		GOOS:   runtime.GOOS,
		Getenv: func(k string) string { return m.vars[k] },
		LookPath: func(name string) (string, error) {
			if p, ok := m.bins[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (RunResult, error) {
			m.runs = append(m.runs, append([]string{name}, args...))
			if m.onRun == nil {
				return RunResult{}, nil
			}
			return m.onRun(name, args), nil
		},
	}
}

// path joins elem onto the fake home.
func (m *machine) path(elem ...string) string {
	return filepath.Join(append([]string{m.home}, elem...)...)
}

// write creates a file under the fake home.
func (m *machine) write(rel, content string) string {
	m.t.Helper()
	p := m.path(filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		m.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		m.t.Fatal(err)
	}
	return p
}

func (m *machine) mkdir(rel string) {
	m.t.Helper()
	if err := os.MkdirAll(m.path(filepath.FromSlash(rel)), 0o750); err != nil {
		m.t.Fatal(err)
	}
}

// exe makes a fake seelegent executable and returns its path.
func (m *machine) exe() string {
	m.t.Helper()
	return m.write("bin/seelegent.exe", "binary")
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func run(t *testing.T, m *machine, req Request) Report {
	t.Helper()
	if req.Port == 0 {
		req.Port = 47821
	}
	if req.Version == "" {
		req.Version = "v9.9.9"
	}
	rep, err := Run(context.Background(), m.env(), req)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// item returns the report's item for host and kind.
func item(t *testing.T, rep Report, host, kind string) Item {
	t.Helper()
	for _, it := range rep.Items {
		if it.Host == host && it.Kind == kind {
			return it
		}
	}
	t.Fatalf("no %s/%s item in %+v", host, kind, rep.Items)
	return Item{}
}

func wantAction(t *testing.T, rep Report, host, kind string, want Action) Item {
	t.Helper()
	it := item(t, rep, host, kind)
	if it.Action != want {
		t.Fatalf("%s/%s: action %s (%s), want %s", host, kind, it.Action, it.Detail, want)
	}
	return it
}

func TestRunNeedsAbsoluteHomeAndExe(t *testing.T) {
	m := newMachine(t)
	if _, err := Run(context.Background(), Env{Home: "relative"}, Request{Exe: m.exe()}); err == nil {
		t.Error("relative home accepted")
	}
	if _, err := Run(context.Background(), m.env(), Request{Exe: "seelegent"}); err == nil {
		t.Error("relative exe accepted")
	}
	if _, err := Run(context.Background(), m.env(), Request{Exe: m.path("missing.exe")}); err == nil {
		t.Error("missing exe accepted")
	}
	_, err := Run(context.Background(), m.env(), Request{Exe: m.exe(), Hosts: []string{"vim"}})
	if !errors.Is(err, ErrUnknownHost) {
		t.Errorf("unknown host: %v", err)
	}
}

func TestNothingDetected(t *testing.T) {
	m := newMachine(t)
	rep := run(t, m, Request{Exe: m.exe()})
	if len(rep.Items) != 0 {
		t.Fatalf("items on an empty machine: %+v", rep.Items)
	}
	var out bytes.Buffer
	if err := rep.WriteText(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No lead host was found") {
		t.Errorf("text: %s", out.String())
	}
	// A named host that is missing is reported, not silently skipped.
	rep = run(t, m, Request{Exe: m.exe(), Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionSkipped)
}

// cursorConfig is a realistic Cursor mcp.json, indented the way json.Indent writes it, so a remove gives back the
// same bytes.
const cursorConfig = `{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-github"
      ],
      "env": {
        "GITHUB_TOKEN": "ghp_example"
      }
    },
    "zeta": {
      "url": "http://localhost:3000/mcp",
      "ratio": 1.50e3
    }
  },
  "aFirstKeyAfter": true
}
`

func TestJSONHostAddUnchangedRemove(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	cfg := m.write(".cursor/mcp.json", cursorConfig)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	it := wantAction(t, rep, "cursor", KindMCP, ActionAdded)
	if it.Backup != cfg+BackupSuffix || read(t, it.Backup) != cursorConfig {
		t.Errorf("backup %q (want %q) does not hold the old file: %q", it.Backup, cfg+BackupSuffix, read(t, it.Backup))
	}
	got := read(t, cfg)
	var doc struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatalf("written file is not JSON: %v\n%s", err, got)
	}
	var e Entry
	if err := json.Unmarshal(doc.MCPServers["seelegent"], &e); err != nil {
		t.Fatal(err)
	}
	if e.Command != exe || len(e.Args) != 1 || e.Args[0] != "mcp" || e.Env != nil {
		// Cursor has no SEELEGENT_LEAD value the MCP server knows, so none is written.
		t.Errorf("entry %+v", e)
	}
	for _, keep := range []string{`"ratio": 1.50e3`, `"GITHUB_TOKEN": "ghp_example"`, `"aFirstKeyAfter": true`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if strings.Index(got, `"github"`) > strings.Index(got, `"zeta"`) {
		t.Error("key order changed")
	}
	if skill := item(t, rep, "cursor", KindSkill); skill.Action != ActionSkipped {
		t.Errorf("cursor skill: %+v", skill)
	}

	rep = run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionUnchanged)
	if read(t, cfg) != got {
		t.Error("an unchanged run rewrote the file")
	}

	rep = run(t, m, Request{Remove: true, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionRemoved)
	if after := read(t, cfg); after != cursorConfig {
		t.Errorf("remove did not restore the file:\n%s", after)
	}
	rep = run(t, m, Request{Remove: true, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionSkipped)
}

// A hand-written file keeps its layout: setup only inserts, replaces and deletes its own member.
func TestJSONHostKeepsOneLineServers(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	const before = "{\n  \"mcpServers\": {\n    \"a\": { \"command\": \"a\", \"args\": [\"x\"] },\n" +
		"    \"b\": {\"url\": \"http://h/\\\"q\\\"\"}\n  },\n  \"n\": 1.50e3\n}\n"
	cfg := m.write(".cursor/mcp.json", before)
	run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	got := read(t, cfg)
	head := strings.TrimSuffix(before[:strings.Index(before, "\n  },")], "\n")
	if !strings.HasPrefix(got, head+",\n    \"seelegent\": {\n") ||
		!strings.HasSuffix(got, "    }\n  },\n  \"n\": 1.50e3\n}\n") {
		t.Errorf("other servers changed:\n%s", got)
	}
	// An update (another executable) replaces only the entry.
	other := m.path("bin2", "seelegent.exe")
	m.write("bin2/seelegent.exe", "")
	wantAction(t, run(t, m, Request{Exe: other, Hosts: []string{"cursor"}}), "cursor", KindMCP, ActionUpdated)
	if got2 := read(t, cfg); !strings.HasPrefix(got2, head+",\n") || !strings.Contains(got2, "seelegent.exe") {
		t.Errorf("update changed other servers:\n%s", got2)
	}
	run(t, m, Request{Remove: true, Hosts: []string{"cursor"}})
	if after := read(t, cfg); after != before {
		t.Errorf("remove did not give back the file:\n%s", after)
	}
}

func TestJSONHostKeepsCRLFAndBOM(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := "\xef\xbb\xbf{\r\n\t\"mcpServers\": {}\r\n}"
	cfg := m.write(".gemini/antigravity/mcp_config.json", orig)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"antigravity"}})
	wantAction(t, rep, "antigravity", KindMCP, ActionAdded)
	got := read(t, cfg)
	if !strings.HasPrefix(got, "\xef\xbb\xbf{\r\n\t\"mcpServers\"") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("style lost: %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("a final newline was added: %q", got)
	}
	run(t, m, Request{Remove: true, Hosts: []string{"antigravity"}})
	if got := read(t, cfg); got != orig {
		t.Errorf("remove: %q, want %q", got, orig)
	}
}

func TestJSONHostConflictAndForce(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := `{"mcpServers": {"seelegent": {"command": "python", "args": ["other.py"]}}}`
	cfg := m.write(".cursor/mcp.json", orig)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionConflict)
	if !rep.Failed() || read(t, cfg) != orig {
		t.Fatal("a conflict must fail and write nothing")
	}
	if _, err := os.Stat(cfg + BackupSuffix); err == nil {
		t.Error("a conflict left a backup")
	}
	rep = run(t, m, Request{Remove: true, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionSkipped)
	if read(t, cfg) != orig {
		t.Error("remove touched a foreign entry")
	}
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}, Force: true})
	wantAction(t, rep, "cursor", KindMCP, ActionUpdated)
	if got := read(t, cfg); strings.Contains(got, "other.py") || !strings.Contains(got, "seelegent.exe") {
		t.Errorf("force did not replace the entry:\n%s", got)
	}
}

func TestJSONHostUpdatesNodeEntry(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".gemini/antigravity-cli")
	cfg := m.write(".gemini/config/mcp_config.json", `{
  "mcpServers": {
    "seelegent": {
      "args": [
        "C:\\Users\\me\\AppData\\Local\\Programs\\seelegent\\versions\\0.3.0\\src\\cli.js",
        "mcp"
      ],
      "command": "C:\\node\\node.exe",
      "disabled": true,
      "env": {
        "SEELEGENT_LEAD": "agy",
        "SEELEGENT_HOME": "D:\\data"
      }
    },
    "xermius-mcp": {
      "serverUrl": "http://localhost:9/mcp",
      "type": "http"
    }
  }
}
`)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"agy"}, NoSkill: true})
	wantAction(t, rep, "agy", KindMCP, ActionUpdated)
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(read(t, cfg)), &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.MCPServers["seelegent"]
	if e["command"] != exe || e["disabled"] != false {
		t.Errorf("entry: %v", e)
	}
	env, _ := e["env"].(map[string]any)
	if env["SEELEGENT_HOME"] != `D:\data` || env["SEELEGENT_LEAD"] != "agy" {
		t.Errorf("env: %v", env)
	}
	if doc.MCPServers["xermius-mcp"]["type"] != "http" {
		t.Error("lost another server")
	}
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"agy"}, NoSkill: true})
	wantAction(t, rep, "agy", KindMCP, ActionUnchanged)
}

func TestJSONHostCommentsAndMalformed(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	jsonc := "{\n  // my servers\n  \"mcp\": {},\n}\n"
	cfg := m.write(".config/opencode/opencode.jsonc", jsonc)
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"opencode"}, NoSkill: true})
	it := wantAction(t, rep, "opencode", KindMCP, ActionManual)
	if !strings.Contains(it.Snippet, `"type": "local"`) || it.Path != cfg {
		t.Errorf("snippet %q path %q", it.Snippet, it.Path)
	}
	if read(t, cfg) != jsonc {
		t.Error("a JSONC file was rewritten")
	}
	rep = run(t, m, Request{Remove: true, Hosts: []string{"opencode"}, NoSkill: true})
	wantAction(t, rep, "opencode", KindMCP, ActionSkipped) // no entry to remove
	m.write(".config/opencode/opencode.jsonc", "{\n  // mine\n  \"mcp\": {\"seelegent\": {}},\n}\n")
	rep = run(t, m, Request{Remove: true, Hosts: []string{"opencode"}, NoSkill: true})
	wantAction(t, rep, "opencode", KindMCP, ActionManual)
	broken := `{"mcpServers": {"x": }`
	cur := m.write(".cursor/mcp.json", broken)
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}})
	wantAction(t, rep, "cursor", KindMCP, ActionError)
	if read(t, cur) != broken || !rep.Failed() {
		t.Error("a malformed file was rewritten or did not fail")
	}
}

func TestOpenCodeNewFile(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".config/opencode")
	rep := run(t, m, Request{Exe: exe, Hosts: []string{"opencode"}, NoSkill: true})
	it := wantAction(t, rep, "opencode", KindMCP, ActionAdded)
	var doc struct {
		MCP map[string]struct {
			Type        string            `json:"type"`
			Command     []string          `json:"command"`
			Environment map[string]string `json:"environment"`
			Enabled     bool              `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal([]byte(read(t, it.Path)), &doc); err != nil {
		t.Fatal(err)
	}
	e := doc.MCP["seelegent"]
	if e.Type != "local" || len(e.Command) != 2 || e.Command[0] != exe || e.Command[1] != "mcp" || !e.Enabled ||
		e.Environment[EnvLead] != "opencode" {
		t.Errorf("entry %+v", e)
	}
	if it.Backup != "" {
		t.Error("backup of a file that did not exist")
	}
	// A disabled entry is enabled again.
	data := strings.Replace(read(t, it.Path), `"enabled": true`, `"enabled": false`, 1)
	m.write(".config/opencode/opencode.json", data)
	rep = run(t, m, Request{Exe: exe, Hosts: []string{"opencode"}, NoSkill: true})
	wantAction(t, rep, "opencode", KindMCP, ActionUpdated)
}

func TestGeminiTimeout(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.bins["gemini"] = m.path("bin", "gemini")
	cfg := m.write(".gemini/settings.json", `{"theme": "dark"}`)
	run(t, m, Request{Exe: exe, Hosts: []string{"gemini"}})
	if got := read(t, cfg); !strings.Contains(got, `"timeout": 3600000`) || !strings.Contains(got, `"theme": "dark"`) {
		t.Errorf("gemini config:\n%s", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	cfg := m.write(".cursor/mcp.json", cursorConfig)
	m.mkdir(".codex")
	m.mkdir(".claude")
	m.bins["claude"] = m.path("bin", "claude")
	rep := run(t, m, Request{Exe: exe, DryRun: true, Hooks: true})
	wantAction(t, rep, "cursor", KindMCP, ActionAdded)
	wantAction(t, rep, "codex", KindMCP, ActionAdded)
	wantAction(t, rep, "codex", KindSkill, ActionAdded)
	wantAction(t, rep, "claude", KindMCP, ActionAdded)
	wantAction(t, rep, "claude", KindHook, ActionAdded)
	if read(t, cfg) != cursorConfig || len(m.runs) != 0 {
		t.Error("dry run wrote or ran something")
	}
	for _, p := range []string{".codex/config.toml", ".agents", ".claude/settings.json", ".claude/skills"} {
		if _, err := os.Stat(m.path(filepath.FromSlash(p))); err == nil {
			t.Errorf("dry run created %s", p)
		}
	}
	var out bytes.Buffer
	if err := rep.WriteText(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would add") || !strings.Contains(out.String(), "Dry run") {
		t.Errorf("text:\n%s", out.String())
	}
}

func TestDetectionAndAllHosts(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".gemini/antigravity")
	m.mkdir(".codeium/windsurf")
	desktop := filepath.Join(m.env().appConfigDir(), "Claude")
	if err := os.MkdirAll(desktop, 0o750); err != nil {
		t.Fatal(err)
	}
	rep := run(t, m, Request{Exe: exe})
	for _, id := range []string{"antigravity", "windsurf", "claude-desktop"} {
		wantAction(t, rep, id, KindMCP, ActionAdded)
	}
	got := map[string]bool{}
	for _, it := range rep.Items {
		got[it.Host] = true
	}
	for _, id := range []string{"claude", "codex", "opencode", "agy", "gemini", "cursor"} {
		if got[id] {
			t.Errorf("%s handled although not installed", id)
		}
	}
	if !exists(filepath.Join(desktop, "claude_desktop_config.json")) {
		t.Error("Claude Desktop config not written")
	}
	if len(rep.Hosts) != len(HostIDs()) {
		t.Errorf("hosts: %+v", rep.Hosts)
	}
}

func TestIsOurs(t *testing.T) {
	tests := []struct {
		e    Entry
		want bool
	}{
		{Entry{Command: `C:\Programs\seelegent\seelegent.exe`, Args: []string{"mcp"}}, true},
		{Entry{Command: "/usr/local/bin/seelegent", Args: []string{"mcp"}}, true},
		{Entry{Command: `C:\x\bin\seelegent.cmd`, Args: []string{"mcp"}}, true},
		{Entry{Command: "node", Args: []string{`C:\Programs\seelegent\versions\0.3.0\src\cli.js`, "mcp"}}, true},
		{Entry{Command: "node", Args: []string{"/opt/other/cli.js", "mcp"}}, false},
		{Entry{Command: "seelegent", Args: []string{"daemon"}}, false},
		{Entry{Command: "python", Args: []string{"seelegent.py"}}, false},
	}
	for _, tt := range tests {
		if got := isOurs(tt.e); got != tt.want {
			t.Errorf("isOurs(%+v) = %v", tt.e, got)
		}
	}
}

func TestLeadValues(t *testing.T) {
	want := map[string]string{"claude": "claude", "codex": "codex", "opencode": "opencode", "agy": "agy",
		"antigravity": "agy", "gemini": "gemini", "claude-desktop": "", "cursor": "", "windsurf": ""}
	for _, id := range HostIDs() {
		w, ok := want[id]
		if !ok {
			t.Errorf("host %s has no expected lead value", id)
		}
		if got := LeadValue(id); got != w {
			t.Errorf("LeadValue(%s) = %q, want %q", id, got, w)
		}
	}
	m := newMachine(t)
	exe := m.exe()
	cfg := m.write(".gemini/antigravity/mcp_config.json", `{"mcpServers": {}}`)
	run(t, m, Request{Exe: exe, Hosts: []string{"antigravity"}})
	if got := read(t, cfg); !strings.Contains(got, `"SEELEGENT_LEAD": "agy"`) {
		t.Errorf("antigravity entry:\n%s", got)
	}
}

func TestExeWarnings(t *testing.T) {
	if w := exeWarnings(`C:\Users\me\AppData\Local\Temp\go-build123\b001\exe\seelegent.exe`); len(w) != 1 {
		t.Errorf("go run build: %v", w)
	}
	if w := exeWarnings("/opt/seelegent/versions/1.0.0/seelegent"); len(w) != 1 {
		t.Errorf("versioned: %v", w)
	}
	if w := exeWarnings("/usr/local/bin/seelegent"); len(w) != 0 {
		t.Errorf("stable: %v", w)
	}
}
