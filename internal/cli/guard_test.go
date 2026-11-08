package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/guard"
)

func TestHookDecision(t *testing.T) {
	ctx := context.Background()
	in := `{"cwd":"","tool_name":"Bash","tool_input":{"command":"git status && git stash"}}`
	if r := hookDecision(ctx, guard.HookClaude, strings.NewReader(in)); !r.Deny {
		t.Fatal("claude: git stash allowed")
	}
	in = `{"toolCall":{"name":"run_command","args":{"CommandLine":"go test ./..."}}}`
	if r := hookDecision(ctx, guard.HookAgy, strings.NewReader(in)); r.Deny {
		t.Fatalf("agy: go test denied: %s", r.Reason)
	}
	if r := hookDecision(ctx, guard.HookAgy, strings.NewReader("{oops")); !r.Deny {
		t.Fatal("unreadable input allowed")
	}
	if r := hookDecision(ctx, guard.HookClaude, strings.NewReader(`{"tool_name":"Read","tool_input":{}}`)); r.Deny {
		t.Fatal("a tool without a command was denied")
	}
	t.Setenv(account.EnvGitGuard, "off")
	in = `{"tool_name":"Bash","tool_input":{"command":"git stash"}}`
	if r := hookDecision(ctx, guard.HookClaude, strings.NewReader(in)); r.Deny {
		t.Fatal("SEELEGENT_GIT_GUARD=off still denies")
	}
}

// TestHookStdinScripts runs Claude Code's Bash hook (read in the POSIX dialect only) on scripts fed to a shell
// through a here-document, a here-string or a pipe.
func TestHookStdinScripts(t *testing.T) {
	ctx := context.Background()
	input := func(command string) io.Reader {
		data, err := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(data)
	}
	for command, deny := range map[string]bool{
		"bash <<'EOF'\ngit reset --hard\nEOF":                                     true,
		"cat <<'EOF' | sh\ngit stash\nEOF":                                        true,
		"bash -s <<EOF\ngit stash\nEOF":                                           true,
		"sh -s <<< 'git stash'":                                                   true,
		"git commit --amen --no-edit":                                             true,
		"bash <<'EOF'\ngo test ./...\nEOF":                                        false,
		"cat <<'EOF'\ngit reset --hard\nEOF":                                      false,
		"git commit -m \"$(cat <<'EOF'\nfix: x\n\nnever git stash here\nEOF\n)\"": false,
	} {
		if r := hookDecision(ctx, guard.HookClaude, input(command)); r.Deny != deny {
			t.Errorf("claude hook %q: deny = %v, want %v (%s)", command, r.Deny, deny, r.Reason)
		}
	}
}

// TestHookHardening runs both hooks on the review findings G1-G6: configuration through the environment and
// help.autocorrect are refused; data in here-documents and in code that starts no program is allowed (agy on
// Windows excepted: its run_command runs PowerShell, where the lines after cat <<'EOF' run).
func TestHookHardening(t *testing.T) {
	ctx := context.Background()
	claude := func(command string) io.Reader {
		data, err := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(data)
	}
	agy := func(command string) io.Reader {
		data, err := json.Marshal(map[string]any{"toolCall": map[string]any{"name": "run_command",
			"args": map[string]string{"CommandLine": command}}})
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(data)
	}
	windows := runtime.GOOS == "windows"
	for command, want := range map[string][2]bool{ // {claude, agy}
		"git -c help.autocorrect=immediate stsh":                                     {true, true},
		`GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'" git x`:                       {true, true},
		"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0=stash git x": {true, true},
		`env GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'" git x`:                   {true, true},
		"export GIT_CONFIG_COUNT=1; git x":                                           {true, true},
		"cat <<'EOF' | tee notes.md\ngit stash\nEOF":                                 {false, windows},
		"sed -n '1,5p' <<'EOF'\ngit reset --hard\nEOF":                               {false, windows},
		"bash script.sh <<'EOF'\ngit stash\nEOF":                                     {false, windows},
		"python3 - <<'EOF'\nfrom pathlib import Path\np = Path('a.md')\n" +
			"p.write_text(p.read_text().replace('git stash', 'git stash (refused)'))\nEOF": {false, false},
		"printf 'git stash\\n' | tr -d '\\r' | bash": {true, true},
		"git status": {false, false},
	} {
		if r := hookDecision(ctx, guard.HookClaude, claude(command)); r.Deny != want[0] {
			t.Errorf("claude hook %q: deny = %v, want %v (%s)", command, r.Deny, want[0], r.Reason)
		}
		if r := hookDecision(ctx, guard.HookAgy, agy(command)); r.Deny != want[1] {
			t.Errorf("agy hook %q: deny = %v, want %v (%s)", command, r.Deny, want[1], r.Reason)
		}
	}
	in := `{"toolCall":{"name":"run_command","args":{"CommandLine":"$env:GIT_CONFIG_PARAMETERS=\"'alias.x=stash'\"; git x"}}}`
	if r := hookDecision(ctx, guard.HookAgy, strings.NewReader(in)); !r.Deny {
		t.Error("agy: $env:GIT_CONFIG_PARAMETERS allowed")
	}
	in = `{"tool_name":"PowerShell","tool_input":{"command":"$env:GIT_CONFIG_COUNT = 1; git x"}}`
	if r := hookDecision(ctx, guard.HookClaude, strings.NewReader(in)); !r.Deny {
		t.Error("claude PowerShell: $env:GIT_CONFIG_COUNT allowed")
	}
}

// TestGitShimAutocorrectOverride (G1): a later -c help.autocorrect would win over the shim's own; it is refused.
func TestGitShimAutocorrectOverride(t *testing.T) {
	var out, errOut bytes.Buffer
	for _, args := range [][]string{
		{"-c", "help.autocorrect=immediate", "stsh"}, {"--config-env=help.autocorrect=AC", "stsh"},
		{"config", "help.autocorrect", "immediate"},
	} {
		errOut.Reset()
		if code := runGitShim(t.Context(), "", args, strings.NewReader(""), &out, &errOut); code != 1 ||
			!strings.HasPrefix(errOut.String(), guard.Marker) {
			t.Errorf("%q: code %d, stderr %q", args, code, errOut.String())
		}
	}
}

func TestGitShim(t *testing.T) {
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if code := runGitShim(ctx, "", []string{"stash"}, strings.NewReader(""), &out, &errOut); code != 1 ||
		!strings.HasPrefix(errOut.String(), guard.Marker) {
		t.Fatalf("stash: code %d, stderr %q", code, errOut.String())
	}
	if _, err := gitx.NewBinary(nil, nil).Path(); err != nil {
		t.Skip("git not installed")
	}
	out.Reset()
	errOut.Reset()
	if code := runGitShim(ctx, t.TempDir(), []string{"--version"}, strings.NewReader(""), &out, &errOut); code != 0 ||
		!strings.HasPrefix(out.String(), "git version") {
		t.Fatalf("--version: code %d, out %q, err %q", code, out.String(), errOut.String())
	}
	// git's own exit code comes back.
	if code := runGitShim(ctx, "", []string{"no-such-command-x"}, strings.NewReader(""), &out, &errOut); code == 0 {
		t.Fatal("a failing git exited 0")
	}
}

// TestGitShimIgnoresGuardVariable: the worker's shell can set SEELEGENT_GIT_GUARD (env SEELEGENT_GIT_GUARD=off git
// stash); the shim must not obey it. The daemon leaves the shim off PATH when the guard is off for a turn.
func TestGitShimIgnoresGuardVariable(t *testing.T) {
	t.Setenv(account.EnvGitGuard, "off")
	var out, errOut bytes.Buffer
	for _, args := range [][]string{{"stash"}, {"reset", "--hard"}, {"commit", "--amen", "--no-edit"}} {
		errOut.Reset()
		if code := runGitShim(t.Context(), "", args, strings.NewReader(""), &out, &errOut); code != 1 ||
			!strings.HasPrefix(errOut.String(), guard.Marker) {
			t.Errorf("%q with %s=off: code %d, stderr %q", args, account.EnvGitGuard, code, errOut.String())
		}
	}
}

// TestGitShimNoAutocorrect: with help.autocorrect=immediate git runs the command it guesses from a typo; the shim
// turns that off, so git stsh does not stash.
func TestGitShimNoAutocorrect(t *testing.T) {
	exe, err := gitx.NewBinary(nil, nil).Path() // never a Windows batch wrapper
	if err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(repo, "no-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(args ...string) {
		t.Helper()
		c := exec.CommandContext(t.Context(), exe, append([]string{"-C", repo}, args...)...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, b)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@example.invalid")
	run("config", "commit.gpgsign", "false")
	run("config", "help.autocorrect", "immediate")
	file := filepath.Join(repo, "a.txt")
	if err := os.WriteFile(file, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "init")
	if err := os.WriteFile(file, []byte("one\nthe user's line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runGitShim(t.Context(), "", []string{"-C", repo, "stsh"}, strings.NewReader(""), &out,
		&errOut); code == 0 {
		t.Errorf("git stsh exited 0: %s %s", out.String(), errOut.String())
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "one\nthe user's line\n" {
		t.Fatalf("a.txt = %q %v (stderr %q)", data, err, errOut.String())
	}
}

func TestGitShimArgs(t *testing.T) {
	dir, rest := gitShimArgs([]string{"--shim-dir=/a b", "status", "--short"})
	if dir != "/a b" || len(rest) != 2 || rest[0] != "status" {
		t.Fatalf("%q %q", dir, rest)
	}
	if dir, rest := gitShimArgs([]string{"--version"}); dir != "" || rest[0] != "--version" {
		t.Fatalf("%q %q", dir, rest)
	}
}
