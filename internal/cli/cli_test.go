package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/store"
)

var testInfo = buildinfo.Info{
	Version:   "v1.2.3",
	Commit:    "abc1234",
	Date:      "2026-11-06T10:00:00Z",
	GoVersion: "go1.27.3",
	Platform:  "linux/amd64",
}

func execute(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	root := NewRootCommand(testInfo)
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)
	err = root.ExecuteContext(context.Background())
	return out.String(), errOut.String(), err
}

func TestVersionFlag(t *testing.T) {
	out, _, err := execute(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if want := testInfo.String() + "\n"; out != want {
		t.Errorf("--version printed %q, want %q", out, want)
	}
}

func TestVersionCommand(t *testing.T) {
	out, _, err := execute(t, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if want := testInfo.String() + "\n"; out != want {
		t.Errorf("version printed %q, want %q", out, want)
	}
}

func TestVersionCommandJSON(t *testing.T) {
	out, _, err := execute(t, "version", "--json")
	if err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var got buildinfo.Info
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if got != testInfo {
		t.Errorf("version --json = %+v, want %+v", got, testInfo)
	}
	var schema struct {
		SchemaVersion int  `json:"schemaVersion"`
		Database      *int `json:"databaseSchemaVersion"`
	}
	_ = json.Unmarshal([]byte(out), &schema)
	if known, _ := store.KnownSchemaVersion(); schema.SchemaVersion != known || known == 0 || schema.Database != nil {
		t.Errorf("schemaVersion = %d (database %v), want %d without a database field", schema.SchemaVersion,
			schema.Database, known)
	}
	for _, key := range []string{`"version"`, `"commit"`, `"date"`, `"goVersion"`, `"platform"`, `"schemaVersion"`} {
		if !strings.Contains(out, key) {
			t.Errorf("JSON output lacks key %s", key)
		}
	}
}

func TestVersionCommandRejectsArgs(t *testing.T) {
	if _, _, err := execute(t, "version", "extra"); err == nil {
		t.Fatal("version extra: want an error")
	}
}

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "version", args: []string{"version"}, wantCode: ExitOK, wantStdout: "seelegent "},
		{name: "unknown command", args: []string{"no-such-command"}, wantCode: ExitUsage, wantStderr: "Error: unknown command"},
		{name: "unknown flag", args: []string{"--no-such-flag"}, wantCode: ExitUsage, wantStderr: "Error: unknown flag"},
		{name: "unknown subcommand flag", args: []string{"status", "--nope"}, wantCode: ExitUsage, wantStderr: "Error: unknown flag"},
		{name: "missing argument", args: []string{"cancel"}, wantCode: ExitUsage, wantStderr: "Error: usage: seelegent cancel"},
		{name: "json usage error", args: []string{"wait", "--json"}, wantCode: ExitUsage, wantStdout: "{\n  \"error\": \"usage: seelegent wait"},
		{name: "run unknown flag", args: []string{"run", "--bogus", "x"}, wantCode: ExitUsage, wantStderr: "Error: unknown flag: --bogus"},
		{name: "no command", args: []string{}, wantCode: ExitOK, wantStdout: "Seelegent runs a local daemon"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(context.Background(), tt.args, &out, &errOut)
			if code != tt.wantCode {
				t.Errorf("Run(%v) = %d, want %d (stderr %q)", tt.args, code, tt.wantCode, errOut.String())
			}
			if !strings.HasPrefix(out.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want prefix %q", out.String(), tt.wantStdout)
			}
			if !strings.HasPrefix(errOut.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want prefix %q", errOut.String(), tt.wantStderr)
			}
		})
	}
}

func TestLaunchCommandIsHidden(t *testing.T) {
	out, _, err := execute(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if strings.Contains(out, launchCommandName) {
		t.Errorf("help lists the internal %s command:\n%s", launchCommandName, out)
	}
}

func TestLaunchCommandWithoutSpec(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := execute(t, launchCommandName, dir); err == nil {
		t.Fatal("__launch without a spec should fail")
	}
	data, err := os.ReadFile(filepath.Join(dir, launch.ExitFile))
	if err != nil {
		t.Fatalf("exit record: %v", err)
	}
	var ex launch.Exit
	if err := json.Unmarshal(data, &ex); err != nil || ex.Reason != launch.ReasonStartFailed {
		t.Fatalf("exit record %s (%v), want start_failed", data, err)
	}
}
