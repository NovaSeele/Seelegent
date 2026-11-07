package cli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
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

func TestGitShim(t *testing.T) {
	ctx := context.Background()
	var out, errOut bytes.Buffer
	if code := runGitShim(ctx, "", []string{"stash"}, strings.NewReader(""), &out, &errOut); code != 1 ||
		!strings.HasPrefix(errOut.String(), guard.Marker) {
		t.Fatalf("stash: code %d, stderr %q", code, errOut.String())
	}
	if _, err := exec.LookPath("git"); err != nil {
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

func TestGitShimArgs(t *testing.T) {
	dir, rest := gitShimArgs([]string{"--shim-dir=/a b", "status", "--short"})
	if dir != "/a b" || len(rest) != 2 || rest[0] != "status" {
		t.Fatalf("%q %q", dir, rest)
	}
	if dir, rest := gitShimArgs([]string{"--version"}); dir != "" || rest[0] != "--version" {
		t.Fatalf("%q %q", dir, rest)
	}
}
