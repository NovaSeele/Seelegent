package guard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseHook(t *testing.T) {
	h, err := ParseHook(HookClaude, []byte(`{"cwd":"/r","tool_name":"PowerShell","tool_input":{"command":"git stash"}}`))
	if err != nil || h.Command != "git stash" || h.Cwd != "/r" || len(h.Shells) != 1 || h.Shells[0] != PowerShell {
		t.Fatalf("claude: %+v %v", h, err)
	}
	h, err = ParseHook(HookClaude, []byte(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`))
	if err != nil || h.Shells[0] != POSIX {
		t.Fatalf("claude bash: %+v %v", h, err)
	}
	h, err = ParseHook(HookAgy, []byte(`{"toolCall":{"name":"run_command","args":{"CommandLine":"git reset --hard","Cwd":"D:\\r"}},"stepIdx":3}`))
	if err != nil || h.Command != "git reset --hard" || h.Cwd != `D:\r` || h.Shells != nil {
		t.Fatalf("agy: %+v %v", h, err)
	}
	h, err = ParseHook(HookAgy, []byte(`{"toolCall":{"name":"send_command_input","args":{"CommandId":"1","Input":"git stash\n"}}}`))
	if err != nil || h.Command != "git stash\n" {
		t.Fatalf("agy input: %+v %v", h, err)
	}
	if _, err := ParseHook(HookAgy, []byte(`not json`)); err == nil {
		t.Fatal("bad JSON accepted")
	}
	if _, err := ParseHook("codex", []byte(`{}`)); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

func TestHookOutput(t *testing.T) {
	r := Check("git stash")
	var claude struct {
		HookSpecificOutput struct {
			HookEventName, PermissionDecision, PermissionDecisionReason string
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(HookOutput(HookClaude, r), &claude); err != nil {
		t.Fatal(err)
	}
	o := claude.HookSpecificOutput
	if o.HookEventName != "PreToolUse" || o.PermissionDecision != "deny" || !strings.HasPrefix(o.PermissionDecisionReason, Marker) {
		t.Fatalf("claude deny output: %+v", o)
	}
	if out := HookOutput(HookClaude, allow); out != nil {
		t.Fatalf("claude allow prints %s", out)
	}
	var agy map[string]string
	if err := json.Unmarshal(HookOutput(HookAgy, r), &agy); err != nil || agy["decision"] != "deny" || !strings.HasPrefix(agy["reason"], Marker) {
		t.Fatalf("agy deny output: %v %v", agy, err)
	}
	if err := json.Unmarshal(HookOutput(HookAgy, allow), &agy); err != nil || agy["decision"] != "allow" {
		t.Fatalf("agy allow output: %v %v", agy, err)
	}
}

func TestInstallClaude(t *testing.T) {
	home := t.TempDir()
	path := ClaudeSettingsPath(home)
	other := `{"model":"x","hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"lint"}]}],"Stop":[]}}`
	if err := os.WriteFile(path, []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(home, "bin dir", "seelegent.exe")
	for range 2 { // installing twice keeps one entry
		if err := InstallClaude(home, exe, true); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(path)
	s := string(data)
	if strings.Count(s, HookSubcommand+" "+HookClaude) != 1 || !strings.Contains(s, `"Bash|PowerShell"`) ||
		!strings.Contains(s, `"lint"`) || !strings.Contains(s, `"model": "x"`) {
		t.Fatalf("settings after install:\n%s", s)
	}
	if err := InstallClaude(home, exe, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if strings.Contains(string(data), HookSubcommand) || !strings.Contains(string(data), `"lint"`) {
		t.Fatalf("settings after removal:\n%s", data)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallClaude(home, exe, true); err == nil {
		t.Fatal("an unreadable settings file was overwritten")
	}
}

func TestInstallAgy(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "100% sure", "seelegent.exe")
	if err := InstallAgy(home, exe, true); err != nil {
		t.Fatal(err)
	}
	dir := AgyConfigDir(home)
	var doc map[string]struct {
		PreToolUse []struct {
			Matcher string
			Hooks   []struct{ Type, Command string }
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "hooks.json"))
	if err != nil || json.Unmarshal(data, &doc) != nil {
		t.Fatalf("hooks.json: %s %v", data, err)
	}
	groups := doc[agyHookName].PreToolUse
	if len(groups) != 2 || groups[0].Matcher != "^run_command$" || groups[1].Matcher != "^send_command_input$" {
		t.Fatalf("groups: %+v", groups)
	}
	if runtime.GOOS == "windows" {
		script, err := os.ReadFile(filepath.Join(dir, agyScript))
		if err != nil || !strings.Contains(string(script), `100%% sure`) || groups[0].Hooks[0].Command != agyScript {
			t.Fatalf("script %q %v, command %q", script, err, groups[0].Hooks[0].Command)
		}
	} else if !strings.Contains(groups[0].Hooks[0].Command, "'"+exe+"' __guard agy") {
		t.Fatalf("command %q", groups[0].Hooks[0].Command)
	}
	if err := InstallAgy(home, exe, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "hooks.json"))
	if strings.Contains(string(data), agyHookName) {
		t.Fatalf("hooks.json after removal: %s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, agyScript)); !os.IsNotExist(err) {
		t.Fatal("script left behind")
	}
}

func TestInstallShim(t *testing.T) {
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src", filepath.Base(exe))
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "shim")
	for range 2 {
		if err := InstallShim(shim, src); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(shim, ShimName()))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if string(data) != "binary" {
			t.Fatalf("shim content %q", data)
		}
	} else if !strings.Contains(string(data), ShimSubcommand) {
		t.Fatalf("shim script %q", data)
	}
	env := WithoutDir([]string{"PATH=" + shim + string(os.PathListSeparator) + "/usr/bin", "HOME=/h"}, shim)
	if env[0] != "PATH=/usr/bin" || env[1] != "HOME=/h" {
		t.Fatalf("WithoutDir: %q", env)
	}
}
