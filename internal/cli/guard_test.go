package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// TestGuardFetchConfig (review N1): the shim and the hook read the fetch refspecs of the repository's own
// configuration, so a refspec that forces into the branches (written to .git/config by any means) refuses a plain
// git fetch, while a normal remote still fetches and prunes.
func TestGuardFetchConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	run := func(dir string, args ...string) {
		t.Helper()
		c := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, b)
		}
	}
	upstream, repo := filepath.Join(root, "upstream"), filepath.Join(root, "repo")
	for _, dir := range []string{upstream, repo} {
		run(root, "init", "-q", "-b", "main", dir)
		run(dir, "config", "user.name", "T")
		run(dir, "config", "user.email", "t@example.invalid")
		run(dir, "config", "commit.gpgsign", "false")
		run(dir, "commit", "-q", "--allow-empty", "-m", "init")
	}
	run(repo, "config", "remote.origin.url", upstream)
	run(repo, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	run(repo, "config", "fetch.prune", "true")
	shim := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		code := runGitShim(t.Context(), "", append([]string{"-C", repo}, args...), strings.NewReader(""), &out, &errOut)
		return code, errOut.String()
	}
	hook := func(command string) bool {
		data, err := json.Marshal(map[string]any{"cwd": repo, "tool_name": "Bash",
			"tool_input": map[string]string{"command": command}})
		if err != nil {
			t.Fatal(err)
		}
		return hookDecision(t.Context(), guard.HookClaude, bytes.NewReader(data)).Deny
	}
	if code, stderr := shim("fetch", "origin"); code != 0 {
		t.Fatalf("fetch origin: code %d, stderr %q", code, stderr)
	}
	if code, stderr := shim("fetch"); code != 0 {
		t.Fatalf("fetch: code %d, stderr %q", code, stderr)
	}
	if hook("git fetch origin") || hook("git fetch --prune") {
		t.Fatal("hook refused a normal fetch")
	}
	run(repo, "config", "remote.old.url", upstream)
	run(repo, "config", "remote.old.fetch", "+refs/heads/*:refs/heads/*")
	for _, args := range [][]string{{"fetch", "old"}, {"fetch"}, {"fetch", "old", "main"}, {"remote", "update"}} {
		if code, stderr := shim(args...); code != 1 || !strings.HasPrefix(stderr, guard.Marker) {
			t.Errorf("%q with a forcing refspec configured: code %d, stderr %q", args, code, stderr)
		}
	}
	if !hook("git fetch old") || !hook("git fetch") {
		t.Error("hook allowed a fetch with a forcing refspec configured")
	}
	if code, stderr := shim("fetch", "origin"); code != 0 {
		t.Errorf("fetch origin next to a mirror remote: code %d, stderr %q", code, stderr)
	}
}

// TestGuardSlashRemote runs real git through the shim (review of fix/m18-open-items, item 2): a remote named a/b
// was read as a path, so its configured forcing refspec was not checked, and git fetch a/b moved main back.
func TestGuardSlashRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(dir string, args ...string) string {
		t.Helper()
		b, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	repo, upstream := filepath.Join(root, "repo"), filepath.Join(root, "upstream")
	git(root, "init", "-q", "-b", "main", repo)
	for _, kv := range [][2]string{{"user.name", "T"}, {"user.email", "t@example.invalid"}, {"commit.gpgsign", "false"}} {
		git(repo, "config", kv[0], kv[1])
	}
	git(repo, "commit", "-q", "--allow-empty", "-m", "one")
	git(root, "clone", "-q", repo, upstream) // upstream's main stays at one
	git(repo, "commit", "-q", "--allow-empty", "-m", "two")
	git(repo, "checkout", "-q", "-b", "side") // main not checked out: fetch may write it
	git(repo, "config", "remote.a/b.url", upstream)
	git(repo, "config", "remote.a/b.fetch", "+refs/heads/*:refs/heads/*")
	git(repo, "pack-refs", "--all")
	head := git(repo, "rev-parse", "main")
	for _, args := range [][]string{{"fetch", "a/b"}, {"fetch", "a/b", "main"}, {"remote", "update", "a/b"}} {
		var out, errOut bytes.Buffer
		code := runGitShim(t.Context(), "", append([]string{"-C", repo}, args...), strings.NewReader(""), &out, &errOut)
		if code != 1 || !strings.HasPrefix(errOut.String(), guard.Marker) {
			t.Errorf("git %q: code %d, stderr %q", args, code, errOut.String())
		}
		if got := git(repo, "rev-parse", "main"); got != head {
			t.Fatalf("git %q moved main from %s to %s", args, head, got)
		}
	}
	var out, errOut bytes.Buffer
	if code := runGitShim(t.Context(), "", []string{"-C", repo, "fetch", upstream, "main"}, strings.NewReader(""),
		&out, &errOut); code != 0 {
		t.Errorf("fetch from the path: code %d, stderr %q", code, errOut.String())
	}
}

// TestGuardBranchCase runs real git through the shim and the hook (review of fix/m18-open-items, item 1): on a disk
// that ignores case, git branch Main old (or checkout -b, worktree add -b, fetch . old:Main) wrote the loose ref
// refs/heads/Main over main, packed or not, and main moved back. Those are refused and main stays; a new name
// passes. Where the disk keeps case and core.ignorecase is false (Linux), Main is just another branch.
func TestGuardBranchCase(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	git := func(dir string, args ...string) string {
		t.Helper()
		c := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, b)
		}
		return strings.TrimSpace(string(b))
	}
	newRepo := func(name string, packed bool) (repo, old, head string) {
		repo = filepath.Join(root, name)
		git(root, "init", "-q", "-b", "main", repo)
		git(repo, "config", "user.name", "T")
		git(repo, "config", "user.email", "t@example.invalid")
		git(repo, "config", "commit.gpgsign", "false")
		git(repo, "commit", "-q", "--allow-empty", "-m", "one")
		git(repo, "branch", "old")
		git(repo, "commit", "-q", "--allow-empty", "-m", "two")
		git(repo, "branch", "feature")
		if packed {
			git(repo, "pack-refs", "--all")
		}
		return repo, git(repo, "rev-parse", "old"), git(repo, "rev-parse", "main")
	}
	creates := [][]string{
		{"fetch", ".", "old:refs/heads/Main"},
		{"fetch", ".", "old:Main"},
		{"branch", "Main", "old"},
		{"branch", "Feature", "old"},
		{"checkout", "-b", "Main", "old"},
		{"worktree", "add", "-b", "Main", "WT", "old"},
		{"worktree", "add", "-b", "Feature", "WT", "old"},
	}
	folds := foldsCase(runtime.GOOS)
	for _, packed := range []bool{false, true} {
		repo, old, head := newRepo(fmt.Sprintf("repo-%v", packed), packed)
		if !folds {
			git(repo, "config", "core.ignorecase", "true") // a disk that ignores case, as git init records it there
		}
		shim := func(args ...string) (int, string) {
			var out, errOut bytes.Buffer
			code := runGitShim(t.Context(), "", append([]string{"-C", repo}, args...), strings.NewReader(""), &out,
				&errOut)
			return code, errOut.String()
		}
		hook := func(args []string) bool {
			data, err := json.Marshal(map[string]any{"cwd": repo, "tool_name": "Bash",
				"tool_input": map[string]string{"command": "git " + strings.Join(args, " ")}})
			if err != nil {
				t.Fatal(err)
			}
			return hookDecision(t.Context(), guard.HookClaude, bytes.NewReader(data)).Deny
		}
		for _, args := range creates {
			args = append([]string{}, args...)
			for i, a := range args {
				if a == "WT" {
					args[i] = filepath.Join(root, fmt.Sprintf("wt-%v", packed))
				}
			}
			if code, stderr := shim(args...); code != 1 || !strings.HasPrefix(stderr, guard.Marker) {
				t.Errorf("packed %v: shim git %q: code %d, stderr %q", packed, args, code, stderr)
			}
			if !hook(args) {
				t.Errorf("packed %v: hook allowed git %q", packed, args)
			}
			if got := git(repo, "rev-parse", "main"); got != head {
				t.Fatalf("packed %v: git %q moved main from %s to %s (old %s)", packed, args, head, got, old)
			}
		}
		if code, stderr := shim("branch", "new-work", "old"); code != 0 {
			t.Errorf("packed %v: a new branch: code %d, stderr %q", packed, code, stderr)
		}
		if code, stderr := shim("fetch", ".", "old:refs/heads/other"); code != 0 {
			t.Errorf("packed %v: fetch into a new branch: code %d, stderr %q", packed, code, stderr)
		}
		if hook([]string{"checkout", "-b", "fix-1"}) {
			t.Errorf("packed %v: hook refused a new branch", packed)
		}
	}
	if !folds {
		repo, _, head := newRepo("keeps-case", true)
		git(repo, "config", "core.ignorecase", "false")
		var out, errOut bytes.Buffer
		if code := runGitShim(t.Context(), "", []string{"-C", repo, "branch", "Main", "old"}, strings.NewReader(""),
			&out, &errOut); code != 0 {
			t.Errorf("disk that keeps case: branch Main: code %d, stderr %q", code, errOut.String())
		}
		if got := git(repo, "rev-parse", "main"); got != head {
			t.Errorf("disk that keeps case: main moved to %s", got)
		}
	}
}

// TestGitBranches: the reader lists branches when the disk ignores case (always on Windows and macOS, elsewhere by
// core.ignorecase), none outside a repository.
func TestGitBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "no-global"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo},
		{"-C", repo, "-c", "user.name=T", "-c", "user.email=t@example.invalid", "commit", "-q", "--allow-empty",
			"-m", "one"},
		{"-C", repo, "config", "core.ignorecase", "false"}} {
		if b, err := exec.CommandContext(t.Context(), "git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %q: %v\n%s", args, err, b)
		}
	}
	env := os.Environ()
	for goos, want := range map[string]string{"windows": "refs/heads/main", "darwin": "refs/heads/main", "linux": ""} {
		got, err := gitBranches(t.Context(), env, repo, goos)(nil)
		if err != nil || strings.Join(got, ",") != want {
			t.Errorf("%s with core.ignorecase=false: %q, %v; want %q", goos, got, err, want)
		}
	}
	got, err := gitBranches(t.Context(), env, root, "linux")([]string{"-C", repo, "-c", "core.ignorecase=true"})
	if err != nil || strings.Join(got, ",") != "refs/heads/main" {
		t.Errorf("linux with core.ignorecase=true: %q, %v", got, err)
	}
	if got, err := gitBranches(t.Context(), env, root, "windows")(nil); err != nil || len(got) != 0 {
		t.Errorf("outside a repository: %q, %v", got, err)
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
