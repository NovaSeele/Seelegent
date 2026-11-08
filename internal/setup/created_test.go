package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordFile is a record of created files in a data folder outside the fake home.
func recordFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "data", CreatedFile)
}

func recorded(t *testing.T, path string) []createdPath {
	t.Helper()
	c, err := loadCreated(path)
	if err != nil {
		t.Fatal(err)
	}
	return c.Paths
}

// Remove deletes what setup created once nothing else is in it, and never what existed before.
func TestRemoveDeletesWhatSetupCreated(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	fakeClaude(t, m)
	m.mkdir(".cursor")
	m.mkdir(".codex")
	m.mkdir(".claude")
	m.write(".claude.json", `{}`)
	rec := recordFile(t)

	rep := run(t, m, Request{Exe: exe, Hosts: []string{"claude", "cursor", "codex"}, Hooks: true, Created: rec})
	for _, it := range rep.Items {
		if it.Failed() {
			t.Fatalf("%+v", it)
		}
	}
	made := map[string]bool{}
	for _, p := range recorded(t, rec) {
		rel, _ := filepath.Rel(m.home, p.Path)
		made[filepath.ToSlash(rel)] = p.Dir
	}
	for rel, dir := range map[string]bool{".cursor/mcp.json": false, ".codex/config.toml": false,
		".claude/settings.json": false, ".claude/skills": true, ".agents": true, ".agents/skills": true} {
		if d, ok := made[rel]; !ok || d != dir {
			t.Errorf("record lacks %s (dir %v): %v", rel, dir, made)
		}
	}
	for _, rel := range []string{".cursor", ".codex", ".claude", ".claude.json"} {
		if _, ok := made[rel]; ok {
			t.Errorf("record has %s, which existed before", rel)
		}
	}

	// An update backs up the file setup created; remove deletes that backup with it.
	exe2 := m.write("bin2/"+exeBase, "binary")
	wantAction(t, run(t, m, Request{Exe: exe2, Hosts: []string{"cursor"}, NoSkill: true, Created: rec}), "cursor",
		KindMCP, ActionUpdated)

	rep = run(t, m, Request{Remove: true, Created: rec})
	if it := wantAction(t, rep, "cursor", KindMCP, ActionRemoved); !strings.Contains(it.Detail, "deleted the file") ||
		!strings.Contains(it.Detail, "backup") {
		t.Errorf("cursor detail %q", it.Detail)
	}
	for _, rel := range []string{".cursor/mcp.json", ".cursor/mcp.json" + BackupSuffix, ".codex/config.toml",
		".claude/settings.json", ".claude/skills", ".agents"} {
		if exists(m.path(filepath.FromSlash(rel))) {
			t.Errorf("%s left behind", rel)
		}
	}
	for _, rel := range []string{".cursor", ".codex", ".claude", ".claude.json"} {
		if !exists(m.path(rel)) {
			t.Errorf("%s, which existed before, was deleted", rel)
		}
	}
	folders := 0
	for _, it := range rep.Items {
		if it.Kind == KindFolder && it.Action == ActionRemoved {
			folders++
		}
	}
	if folders != 3 { // .claude/skills, .agents/skills, .agents
		t.Errorf("%d folders removed: %+v", folders, rep.Items)
	}
	if left := recorded(t, rec); len(left) != 0 {
		t.Errorf("record keeps %+v", left)
	}
}

func TestRemoveKeepsWhatExistedOrWasAdded(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".agents/skills")                                   // existed before setup
	cursor := m.write(".cursor/mcp.json", `{"mcpServers": {}}`) // existed before setup
	m.mkdir(".gemini/antigravity")
	rec := recordFile(t)
	run(t, m, Request{Exe: exe, Hosts: []string{"cursor", "codex", "antigravity"}, Created: rec})
	m.mkdir(".codex")
	run(t, m, Request{Exe: exe, Hosts: []string{"codex"}, Created: rec})

	// The user adds a server to the file setup created.
	anti := m.path(".gemini", "antigravity", "mcp_config.json")
	data := strings.Replace(read(t, anti), `"mcpServers": {`, `"mcpServers": {"mine": {"command": "x"},`, 1)
	m.write(".gemini/antigravity/mcp_config.json", data)
	// And something next to the codex config.
	m.write(".codex/auth.json", "{}")

	run(t, m, Request{Remove: true, Created: rec})
	if got := read(t, cursor); got != `{"mcpServers": {}}` {
		t.Errorf("cursor file %q", got)
	}
	if !isDir(m.path(".agents", "skills")) {
		t.Error("the skills folder that existed before was removed")
	}
	if got := read(t, anti); !strings.Contains(got, `"mine"`) || strings.Contains(got, ServerName) {
		t.Errorf("antigravity file %q", got)
	}
	if exists(m.path(".codex", "config.toml")) || !exists(m.path(".codex", "auth.json")) {
		t.Error("codex: the created config should go, the user's file stay")
	}
	// The file the user still uses stays recorded, so a later remove can still decide about it.
	keep := false
	for _, p := range recorded(t, rec) {
		keep = keep || p.Path == anti
	}
	if !keep {
		t.Errorf("record dropped %s", anti)
	}
}

func TestDryRunRemoveKeepsRecord(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.mkdir(".cursor")
	rec := recordFile(t)
	run(t, m, Request{Exe: exe, Hosts: []string{"cursor"}, Created: rec})
	before, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	run(t, m, Request{Remove: true, DryRun: true, Created: rec})
	after, err := os.ReadFile(rec)
	if err != nil || !bytes.Equal(after, before) || !exists(m.path(".cursor", "mcp.json")) {
		t.Error("a dry run changed something")
	}
}

func TestUnreadableRecord(t *testing.T) {
	m := newMachine(t)
	rec := recordFile(t)
	if err := os.MkdirAll(filepath.Dir(rec), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"not json", `{"version": 9, "created": []}`} {
		if err := os.WriteFile(rec, []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(t.Context(), m.env(), Request{Remove: true, Created: rec}); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
	var c created
	if err := json.Unmarshal([]byte(`{"version": 1, "created": [{"path": "/x", "dir": true}]}`), &c); err != nil ||
		len(c.Paths) != 1 || !c.Paths[0].Dir {
		t.Errorf("format %+v %v", c, err)
	}
}

// TestRemoveUninstalledHost (H22): remove cleans Seelegent's entry out of a host whose program is gone (Gemini CLI
// uninstalled, its settings.json left).
func TestRemoveUninstalledHost(t *testing.T) {
	m := newMachine(t)
	exe := m.exe()
	m.bins["gemini"] = m.path("bin", "gemini.exe")
	rec := recordFile(t)
	run(t, m, Request{Exe: exe, Hosts: []string{"gemini"}, Created: rec})
	settings := m.path(".gemini", "settings.json")
	if !strings.Contains(read(t, settings), exe[len(exe)-len(exeBase):]) {
		t.Fatalf("gemini entry missing: %s", read(t, settings))
	}
	delete(m.bins, "gemini") // the Gemini CLI is uninstalled; its files stay
	rep := run(t, m, Request{Remove: true, Created: rec})
	wantAction(t, rep, "gemini", KindMCP, ActionRemoved)
	if exists(settings) && strings.Contains(read(t, settings), ServerName) {
		t.Errorf("entry left: %s", read(t, settings))
	}
	// Without its files a missing host is still skipped, and an explicit --host says why.
	rep = run(t, m, Request{Remove: true, Hosts: []string{"cursor"}, Created: rec})
	wantAction(t, rep, "cursor", KindMCP, ActionSkipped)
}

// TestCancelledRunKeepsRecord (H22): a run cancelled after the first host still records the files that host got,
// so a later --remove can delete them.
func TestCancelledRunKeepsRecord(t *testing.T) {
	m2 := newMachine(t)
	exe2 := m2.exe()
	fakeClaude(t, m2)
	m2.mkdir(".claude")
	m2.mkdir(".cursor")
	rec2 := recordFile(t)
	ctx, cancel := context.WithCancel(t.Context())
	inner := m2.onRun
	m2.onRun = func(name string, args []string) RunResult {
		res := inner(name, args)
		cancel()
		return res
	}
	rep, err := Run(ctx, m2.env(), Request{Exe: exe2, Hosts: []string{"claude", "cursor"}, Hooks: true,
		Created: rec2, Port: 47821, Version: "v9.9.9"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	made := map[string]bool{}
	for _, p := range recorded(t, rec2) {
		rel, _ := filepath.Rel(m2.home, p.Path)
		made[filepath.ToSlash(rel)] = true
	}
	if !made[".claude/settings.json"] || !made[".claude/skills"] || made[".cursor/mcp.json"] {
		t.Errorf("record after cancel: %v (items %+v)", made, rep.Items)
	}
}
