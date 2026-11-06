package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
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
	for _, key := range []string{`"version"`, `"commit"`, `"date"`, `"goVersion"`, `"platform"`} {
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
		{name: "unknown command", args: []string{"no-such-command"}, wantCode: ExitError, wantStderr: "Error: unknown command"},
		{name: "unknown flag", args: []string{"--no-such-flag"}, wantCode: ExitError, wantStderr: "Error: unknown flag"},
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
