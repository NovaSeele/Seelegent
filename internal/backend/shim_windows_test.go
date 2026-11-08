package backend

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestCodexSandboxOverrideThroughShim runs a codex turn through the real launcher with codex.cmd, a batch shim like
// the one npm installs (it passes its arguments on with %*), in front of the fake CLI: the override reaches the
// program behind cmd.exe as the one argument windows.sandbox=unelevated, and the whole argv arrives unchanged.
func TestCodexSandboxOverrideThroughShim(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(t.TempDir(), "codex.cmd")
	body := "@ECHO off\r\nSETLOCAL\r\n\"" + exe + "\" %*\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := New("codex", shim)
	if err != nil {
		t.Fatal(err)
	}
	turn := Turn{Prompt: "fix it", Model: "gpt-6-luna", ConversationID: "th-9", Timeout: time.Minute}
	r := startE2EOn(t, b, filepath.Join("testdata", "streams", "codex", "success.jsonl"), testProfile(t), turn, nil)
	r.finish(t)
	if r.outcome.Status != OutcomeOK {
		t.Fatalf("outcome = %+v", r.outcome)
	}
	cmd, err := b.Command(Turn{Prompt: "fix it", Model: "gpt-6-luna", ConversationID: "th-9",
		Profile: testProfile(t), AttemptDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "--json", "--sandbox", "workspace-write", "-c",
		"windows.sandbox=unelevated", "--model", "gpt-6-luna", "--skip-git-repo-check", "resume", "th-9", "-"}
	if !slices.Equal(cmd.Argv[1:], want) {
		t.Fatalf("argv = %q\nwant %q", cmd.Argv[1:], want)
	}
	if rec := r.fakeSaw(t); !slices.Equal(rec.Args, want) || rec.Stdin != "fix it" {
		t.Errorf("the CLI behind codex.cmd saw %q (stdin %q)\nwant %q", rec.Args, rec.Stdin, want)
	}

	// The command line Windows gets keeps the override as one bare word and reads back as the same argv.
	line := windows.ComposeCommandLine(cmd.Argv)
	if !strings.Contains(line, " -c windows.sandbox=unelevated ") {
		t.Errorf("command line = %s", line)
	}
	back, err := windows.DecomposeCommandLine(line)
	if err != nil || !slices.Equal(back, cmd.Argv) {
		t.Errorf("command line reads back as %q (%v)", back, err)
	}
}
