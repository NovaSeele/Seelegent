package setup

import (
	"strings"
	"testing"
)

// codexConfig is a realistic config.toml with comments, other tables, an array of tables and a multi-line string.
const codexConfig = `# Codex settings
model = "gpt-5.5-codex" # the default model
approval_policy = "on-request"

[profiles.fast]
model = "gpt-5.5-mini"

[mcp_servers.github]
command = "npx"
args = [
  "-y",   # yes
  "@modelcontextprotocol/server-github",
]
env = { GITHUB_TOKEN = "ghp_example" }

[[notify]]
program = 'C:\tools\notify.exe'
message = """
multi-line [mcp_servers.seelegent]
text"""
`

func codexRun(t *testing.T, m *machine, req Request) (rep Report, config string) {
	t.Helper()
	req.Hosts, req.NoSkill = []string{"codex"}, true
	rep = run(t, m, req)
	cfg := m.path(".codex", "config.toml")
	if !exists(cfg) {
		return rep, ""
	}
	return rep, read(t, cfg)
}

func TestCodexAddUnchangedRemove(t *testing.T) {
	for _, tc := range []struct {
		name, orig string
	}{
		{"lf", codexConfig},
		{"crlf", strings.ReplaceAll(codexConfig, "\n", "\r\n")},
		{"no final newline", strings.TrimSuffix(codexConfig, "\n")},
		{"bom", "\xef\xbb\xbf" + codexConfig},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newMachine(t)
			exe := m.exe()
			m.write(".codex/config.toml", tc.orig)
			rep, got := codexRun(t, m, Request{Exe: exe})
			wantAction(t, rep, "codex", KindMCP, ActionAdded)
			if !strings.HasPrefix(got, strings.TrimSuffix(tc.orig, "\n")) && tc.orig != "" {
				t.Errorf("the existing lines changed:\n%q", got)
			}
			crlf := strings.Contains(tc.orig, "\r\n")
			if crlf && strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
				t.Errorf("mixed line ends: %q", got)
			}
			for _, want := range []string{codexStartMarker, "[mcp_servers.seelegent]", "command = '" + exe + "'",
				"args = ['mcp']", "env = { SEELEGENT_LEAD = 'codex' }", "tool_timeout_sec = 3600", codexEndMarker} {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
			f, err := parseTOML(got)
			if err != nil {
				t.Fatalf("written file does not parse: %v", err)
			}
			if f.render() != got {
				t.Error("render is not the identity")
			}

			rep, again := codexRun(t, m, Request{Exe: exe})
			wantAction(t, rep, "codex", KindMCP, ActionUnchanged)
			if again != got {
				t.Error("unchanged run rewrote the file")
			}

			rep, back := codexRun(t, m, Request{Remove: true})
			wantAction(t, rep, "codex", KindMCP, ActionRemoved)
			want := tc.orig
			if !strings.HasSuffix(want, "\n") && want != "" {
				want += "\n" // the line before the block keeps the line end it was given
			}
			if back != want {
				t.Errorf("remove:\n got %q\nwant %q", back, want)
			}
		})
	}
}

func TestCodexNewFile(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".codex")
	rep, got := codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionAdded)
	if !strings.HasPrefix(got, codexStartMarker+"\n[mcp_servers.seelegent]\n") || !strings.HasSuffix(got, codexEndMarker+"\n") {
		t.Errorf("new file:\n%s", got)
	}
	rep, got = codexRun(t, m, Request{Remove: true})
	wantAction(t, rep, "codex", KindMCP, ActionRemoved)
	if got != "" {
		t.Errorf("after remove: %q", got)
	}
}

func TestCodexUpdatesNodeEraTable(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := `model = "o3"

# >>> seelegent (managed by ` + "`seelegent setup`" + `) >>>
[mcp_servers.seelegent]
command = "node"
# keep this comment
args = [
  "C:\\Users\\me\\AppData\\Local\\Programs\\seelegent\\versions\\0.3.0\\src\\cli.js",
  "mcp",
]
env = { SEELEGENT_LEAD = "codex", SEELEGENT_HOME = "D:\\data" }
startup_timeout_sec = 30
# <<< seelegent <<<

[mcp_servers.other]
command = "other"
`
	m.write(".codex/config.toml", orig)
	rep, got := codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionUpdated)
	want := strings.Replace(orig, `command = "node"
# keep this comment
args = [
  "C:\\Users\\me\\AppData\\Local\\Programs\\seelegent\\versions\\0.3.0\\src\\cli.js",
  "mcp",
]
env = { SEELEGENT_LEAD = "codex", SEELEGENT_HOME = "D:\\data" }
startup_timeout_sec = 30
`, "command = '"+exe+`'
# keep this comment
args = ['mcp']
env = { SEELEGENT_HOME = 'D:\data', SEELEGENT_LEAD = 'codex' }
startup_timeout_sec = 30
tool_timeout_sec = 3600
`, 1)
	if got != want {
		t.Errorf("update:\n got %s\nwant %s", got, want)
	}
	rep, _ = codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionUnchanged)

	rep, got = codexRun(t, m, Request{Remove: true})
	wantAction(t, rep, "codex", KindMCP, ActionRemoved)
	if got != "model = \"o3\"\n\n[mcp_servers.other]\ncommand = \"other\"\n" {
		t.Errorf("remove: %q", got)
	}
}

func TestCodexForeignTable(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := "[mcp_servers.seelegent]\ncommand = \"python\"\nargs = [\"x.py\"]\n\n[other]\nk = 1\n"
	m.write(".codex/config.toml", orig)
	rep, got := codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionConflict)
	if got != orig {
		t.Error("a conflict wrote the file")
	}
	rep, got = codexRun(t, m, Request{Remove: true})
	wantAction(t, rep, "codex", KindMCP, ActionSkipped)
	if got != orig {
		t.Error("remove touched a foreign table")
	}
	rep, got = codexRun(t, m, Request{Exe: exe, Force: true})
	wantAction(t, rep, "codex", KindMCP, ActionUpdated)
	if strings.Contains(got, "python") || !strings.HasSuffix(got, codexEndMarker+"\n\n[other]\nk = 1\n") {
		t.Errorf("force:\n%s", got)
	}
}

func TestCodexLayoutsSetupRefuses(t *testing.T) {
	for name, orig := range map[string]string{
		"twice":           "[mcp_servers.seelegent]\ncommand = 'a'\n[mcp_servers.seelegent]\ncommand = 'b'\n",
		"sub-table":       "[mcp_servers.seelegent]\ncommand = 'a'\n[mcp_servers.seelegent.env]\nK = 'v'\n",
		"dotted root":     "mcp_servers.seelegent.command = 'a'\n",
		"dotted in table": "[mcp_servers]\nseelegent.command = 'a'\n",
		"inline":          "mcp_servers = { seelegent = { command = 'a' } }\n",
		"inline in table": "[mcp_servers]\nseelegent = { command = 'a' }\n",
		"dotted managed":  "[mcp_servers.seelegent]\nenv.SEELEGENT_LEAD = 'codex'\ncommand = 'a'\n",
	} {
		t.Run(name, func(t *testing.T) {
			m := newMachine(t)
			exe := m.exe()
			m.write(".codex/config.toml", orig)
			rep, got := codexRun(t, m, Request{Exe: exe})
			wantAction(t, rep, "codex", KindMCP, ActionError)
			if got != orig {
				t.Error("file changed")
			}
			rep, _ = codexRun(t, m, Request{Remove: true})
			wantAction(t, rep, "codex", KindMCP, ActionManual)
		})
	}
}

func TestCodexBrokenFile(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := "model = \"o3\nargs = [\n"
	m.write(".codex/config.toml", orig)
	rep, got := codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionError)
	if got != orig {
		t.Error("file changed")
	}
}

func TestCodexMultilineArgsWithoutTrailingNewline(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	orig := "[mcp_servers.seelegent]\r\ncommand = \"/opt/x/seelegent\"\r\nargs = [\r\n  \"mcp\",\r\n]"
	m.write(".codex/config.toml", orig)
	rep, got := codexRun(t, m, Request{Exe: exe})
	wantAction(t, rep, "codex", KindMCP, ActionUpdated)
	want := "[mcp_servers.seelegent]\r\ncommand = '" + exe + "'\r\nargs = ['mcp']\r\n" +
		"env = { SEELEGENT_LEAD = 'codex' }\r\ntool_timeout_sec = 3600"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestTOMLValues(t *testing.T) {
	v, err := parseValue(`[ "a\"b", 'c:\d', """e
f""", '''g''' ] # comment`)
	if err != nil {
		t.Fatal(err)
	}
	list, ok := v.strings()
	if !ok || strings.Join(list, "|") != "a\"b|c:\\d|e\nf|g" {
		t.Errorf("list %q", list)
	}
	v, err = parseValue(`{ A = "1", "b.c" = '2' }`)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := v.stringMap(); !ok || m["A"] != "1" || m["b.c"] != "2" {
		t.Errorf("map %v", m)
	}
	for _, bad := range []string{`"open`, `[1, 2`, `{ a = }`} {
		if _, err := parseValue(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	for in, want := range map[string]string{
		`C:\x`:  `'C:\x'`,
		`it's`:  `"it's"`,
		"a\nb":  `"a\nb"`,
		`q"'`:   `"q\"'"`,
		"plain": `'plain'`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
		if v, err := parseValue(tomlString(in)); err != nil || v.str == nil || *v.str != in {
			t.Errorf("round trip of %q failed: %v", in, err)
		}
	}
	if tomlKey("SEELEGENT_LEAD") != "SEELEGENT_LEAD" || tomlKey("a.b") != `'a.b'` {
		t.Error("tomlKey")
	}
}
