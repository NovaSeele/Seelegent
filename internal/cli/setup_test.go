package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/ops"
	"github.com/NovaSeele/Seelegent/internal/setup"
)

// fakeHome points every home and config variable of this process at a temporary folder and empties PATH, so
// setup sees no real host and cannot reach a real config file.
func fakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME":              home,
		"USERPROFILE":       home,
		"APPDATA":           filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":      filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME":   filepath.Join(home, ".config"),
		"SEELEGENT_HOME":    filepath.Join(home, ".seelegent"),
		"PATH":              filepath.Join(home, "bin"),
		"CODEX_HOME":        "",
		"CLAUDE_CONFIG_DIR": "",
		"SEELEGENT_AGY":     "",
		account.EnvWorker:   "",
	} {
		t.Setenv(k, v)
	}
	return home
}

// setupExeBase is the file name setup accepts for the executable on this OS.
var setupExeBase = map[bool]string{true: "seelegent.exe", false: "seelegent"}[runtime.GOOS == "windows"]

// The test binary (cli.test) is not named seelegent: without --exe, setup refuses before writing anything, a dry
// run too; --remove still runs; `hook extra` is a usage error.
func TestSetupExeNameAndHookArgs(t *testing.T) {
	home := fakeHome(t)
	cfg := filepath.Join(home, ".cursor", "mcp.json")
	writeFile(t, cfg, `{"mcpServers": {}}`)
	for _, args := range [][]string{{"setup"}, {"setup", "--dry-run", "--json"}} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), args, &out, &errOut)
		if code != ExitError || !strings.Contains(out.String()+errOut.String(), "--exe <path to a seelegent executable>") {
			t.Errorf("%v: exit %d\n%s%s", args, code, out.String(), errOut.String())
		}
	}
	if data, _ := os.ReadFile(cfg); string(data) != `{"mcpServers": {}}` {
		t.Errorf("a refused setup wrote %s", data)
	}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"setup", "--remove"}, &out, &errOut); code != ExitOK {
		t.Errorf("remove: exit %d\n%s%s", code, out.String(), errOut.String())
	}
	for _, args := range [][]string{{"hook", "extra"}, {"hook", setup.HookCommand, "extra"}} {
		out.Reset()
		errOut.Reset()
		if code := Run(context.Background(), args, &out, &errOut); code != ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSetupCommand(t *testing.T) {
	home := fakeHome(t)
	exe := filepath.Join(home, "bin", setupExeBase)
	writeFile(t, exe, "binary")
	cfg := filepath.Join(home, ".cursor", "mcp.json")
	writeFile(t, cfg, `{"mcpServers": {}}`)

	out, _, err := execute(t, "setup", "--dry-run", "--json", "--exe", exe)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	var rep setup.Report
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if !rep.DryRun || len(rep.Items) == 0 || rep.Items[0].Host != "cursor" || rep.Items[0].Action != setup.ActionAdded {
		t.Fatalf("report %+v", rep)
	}
	if data, _ := os.ReadFile(cfg); string(data) != `{"mcpServers": {}}` {
		t.Fatal("dry run wrote the file")
	}

	out, _, err = execute(t, "setup", "--exe", exe)
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cursor") || !strings.Contains(out, "added") {
		t.Errorf("text:\n%s", out)
	}
	out, _, err = execute(t, "setup", "--exe", exe)
	if err != nil || !strings.Contains(out, "unchanged") || strings.Contains(out, "added") {
		t.Errorf("rerun (%v):\n%s", err, out)
	}
	out, _, err = execute(t, "setup", "--remove")
	if err != nil || !strings.Contains(out, "removed") {
		t.Errorf("remove (%v):\n%s", err, out)
	}
	if data, _ := os.ReadFile(cfg); strings.Contains(string(data), "seelegent") {
		t.Errorf("after remove: %s", data)
	}
}

func TestSetupCommandFails(t *testing.T) {
	home := fakeHome(t)
	exe := filepath.Join(home, "bin", setupExeBase)
	writeFile(t, exe, "binary")
	writeFile(t, filepath.Join(home, ".cursor", "mcp.json"), `{"mcpServers": {"seelegent": {"command": "other"}}}`)
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"setup", "--exe", exe}, &out, &errOut); code != ExitError {
		t.Errorf("conflict: exit %d", code)
	}
	if !strings.Contains(out.String(), "conflict") {
		t.Errorf("output:\n%s", out.String())
	}
	if _, _, err := execute(t, "setup", "--host", "vim", "--exe", exe); err == nil ||
		!strings.Contains(err.Error(), "unknown host") {
		t.Errorf("unknown host: %v", err)
	}
	// With --json a report with a conflict is the only document on stdout: exit 1 and no error document after it.
	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"setup", "--dry-run", "--json", "--exe", exe}, &out, &errOut); code != ExitError {
		t.Errorf("conflict --json: exit %d", code)
	}
	dec := json.NewDecoder(&out)
	var rep setup.Report
	if err := dec.Decode(&rep); err != nil || !rep.Failed() {
		t.Fatalf("report (%v): %+v", err, rep)
	}
	if dec.More() {
		t.Errorf("a second JSON document follows the report: %s", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr: %s", errOut.String())
	}

	// Usage errors exit 2.
	for _, args := range [][]string{{"setup", "extra"}, {"migrate", "extra"}, {"skill", "extra"}} {
		out.Reset()
		errOut.Reset()
		if code := Run(context.Background(), args, &out, &errOut); code != ExitUsage {
			t.Errorf("%v: exit %d (%s)", args, code, errOut.String())
		}
	}

	// Inside a worker setup and migrate are refused the M7 way; skill and the hook stay allowed.
	t.Setenv(account.EnvWorker, "1")
	for _, args := range [][]string{{"setup", "--dry-run", "--json", "--exe", exe}, {"migrate", "--dry-run", "--json"}} {
		out.Reset()
		errOut.Reset()
		if code := Run(context.Background(), args, &out, &errOut); code != ExitError {
			t.Errorf("%v in a worker: exit %d", args, code)
		}
		var e errorOut
		if err := json.Unmarshal(out.Bytes(), &e); err != nil || e.Code != "forbidden" || e.Error != ops.WorkerMessage ||
			e.Details["caller"] != "worker" {
			t.Errorf("%v in a worker (%v): %s", args, err, out.String())
		}
	}
	for _, args := range [][]string{{"skill"}, {"hook", setup.HookCommand}} {
		out.Reset()
		if code := Run(context.Background(), args, &out, &errOut); code != ExitOK {
			t.Errorf("%v in a worker: exit %d", args, code)
		}
	}
	if setup.EnvLead != ops.EnvLead {
		t.Error("setup and ops name the lead variable differently")
	}
}

func TestSkillCommand(t *testing.T) {
	fakeHome(t)
	out, _, err := execute(t, "skill")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "---\nname: seelegent\n") || !strings.Contains(out, "v1.2.3") {
		t.Errorf("skill:\n%s", out)
	}
	out, _, err = execute(t, "skill", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(out), &got); err != nil || !strings.Contains(got["content"], "`delegate`") {
		t.Errorf("skill --json (%v): %s", err, out)
	}
}

func TestHookCommand(t *testing.T) {
	run := func(in string) string {
		var out, errOut bytes.Buffer
		root := NewRootCommand(testInfo)
		root.SetArgs([]string{"hook", setup.HookCommand})
		root.SetIn(strings.NewReader(in))
		root.SetOut(&out)
		root.SetErr(&errOut)
		if err := root.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("hook: %v", err)
		}
		return out.String()
	}
	if out := run(`{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose"}}`); !strings.Contains(out, "additionalContext") {
		t.Errorf("coding subagent: %q", out)
	}
	if out := run(`{"tool_name":"Agent","tool_input":{"subagent_type":"Explore"}}`); out != "" {
		t.Errorf("Explore: %q", out)
	}
	if out := run("garbage"); out != "" {
		t.Errorf("garbage: %q", out)
	}
	help, _, _ := execute(t, "--help")
	for _, line := range strings.Split(help, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "hook" {
			t.Error("the hook command is listed in help")
		}
	}
}
