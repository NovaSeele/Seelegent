package backend

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

func testProfile(t *testing.T) account.Profile {
	t.Helper()
	dir := t.TempDir()
	return account.Profile{
		AccountID: "acc7", Kind: account.KindIsolated, Dir: filepath.Join(dir, "acc7"),
		Home: filepath.Join(dir, "acc7", "home"), RealHome: filepath.Join(dir, "real"),
	}
}

func TestNewAndNames(t *testing.T) {
	for _, n := range Names() {
		b, err := New(n, "")
		if err != nil || b.Name() != n || b.Bin() != "" || b.QuotaReader() != nil {
			t.Fatalf("New(%s) = %v, %v", n, b, err)
		}
		if _, err := b.Command(Turn{Model: "m", Profile: testProfile(t)}); !errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: Command without binary = %v", n, err)
		}
		if _, err := b.SignIn(testProfile(t)); !errors.Is(err, ErrNotInstalled) {
			t.Errorf("%s: SignIn without binary = %v", n, err)
		}
	}
	if _, err := New("gpt", ""); !errors.Is(err, ErrUnknownBackend) {
		t.Errorf("New(gpt) = %v", err)
	}
	caps := map[string]Capabilities{
		"agy":    {ActiveQuota: true, NativePreToolGuard: true, ResumeFlag: "--conversation"},
		"claude": {ActiveQuota: true, NativePreToolGuard: true, StdinPrompt: true, ResumeFlag: "--resume"},
		"codex":  {ActiveQuota: true, StdinPrompt: true, ResumeFlag: "resume", PlanShell: true},
	}
	for n, want := range caps {
		if got := mustBackend(t, n).Capabilities(); got != want {
			t.Errorf("%s capabilities = %+v", n, got)
		}
	}
}

func TestAgyCommand(t *testing.T) {
	b := mustBackend(t, "agy")
	prof := testProfile(t)
	cmd, err := b.Command(Turn{Prompt: "fix it", Model: "gemini-3.8-flash-low", Effort: "high", ConversationID: "c1",
		Timeout: 89 * time.Second, Profile: prof})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/agy", "-p", "fix it", "--output-format", "stream-json", "--model", "gemini-3.8-flash-low",
		"--effort", "high", "--conversation", "c1", "--dangerously-skip-permissions", "--print-timeout", "2m"}
	if !slices.Equal(cmd.Argv, want) || cmd.HasStdin {
		t.Errorf("argv = %q", cmd.Argv)
	}
	cmd, err = b.Command(Turn{Prompt: "plan", Model: "m", Plan: true, Profile: prof})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cmd.Argv[len(cmd.Argv)-2:], []string{"--mode", "plan"}) {
		t.Errorf("plan argv = %q", cmd.Argv)
	}
	if _, err := b.Command(Turn{Prompt: "x", Profile: prof}); !errors.Is(err, ErrModel) {
		t.Errorf("no model: %v", err)
	}

	long := strings.Repeat("brief ", agyMaxArgvPrompt/5)
	if _, err := b.Command(Turn{Prompt: long, Model: "m"}); err == nil {
		t.Error("long prompt without attempt dir accepted")
	}
	attempt := filepath.Join(t.TempDir(), "a1")
	cmd, err = b.Command(Turn{Prompt: long, Model: "m", AttemptDir: attempt})
	if err != nil {
		t.Fatal(err)
	}
	brief := filepath.Join(attempt, agyBriefName)
	if !strings.HasSuffix(cmd.Argv[2], brief) || len(cmd.Argv[2]) > 500 {
		t.Errorf("prompt pointer = %q", cmd.Argv[2])
	}
	if data, err := os.ReadFile(brief); err != nil || string(data) != long {
		t.Errorf("brief file: %v", err)
	}
}

func TestClaudeCommand(t *testing.T) {
	prof := testProfile(t)
	cmd, err := mustBackend(t, "claude").Command(Turn{Prompt: "do it", Model: "claude-sonnet-5", ConversationID: "s1",
		Profile: prof})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/claude", "-p", "--output-format", "stream-json", "--verbose", "--input-format", "text",
		"--strict-mcp-config", "--mcp-config", filepath.Join(prof.Home, claudeEmptyMCP), "--setting-sources", "user",
		"--permission-mode", "bypassPermissions", "--model", "claude-sonnet-5", "--resume", "s1"}
	if !slices.Equal(cmd.Argv, want) || !cmd.HasStdin || cmd.Stdin != "do it" {
		t.Errorf("command = %+v", cmd)
	}
	cmd, _ = mustBackend(t, "claude").Command(Turn{Prompt: "p", Plan: true, Profile: prof})
	if !slices.Contains(cmd.Argv, "plan") || slices.Contains(cmd.Argv, "--model") || slices.Contains(cmd.Argv, "--resume") {
		t.Errorf("plan argv = %q", cmd.Argv)
	}
	if _, err := mustBackend(t, "claude").Command(Turn{Prompt: "p"}); err == nil {
		t.Error("command without profile accepted")
	}
}

func TestCodexCommand(t *testing.T) {
	b := mustBackend(t, "codex")
	cmd, err := b.Command(Turn{Prompt: "go", Model: "gpt-5.5-codex", ConversationID: "th1", Sandbox: config.SandboxFullAccess})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/codex", "exec", "--json", "--sandbox", "danger-full-access", "--model",
		"gpt-5.5-codex", "--skip-git-repo-check", "resume", "th1", "-"}
	if !slices.Equal(cmd.Argv, want) || cmd.Stdin != "go" || !cmd.HasStdin {
		t.Errorf("command = %+v", cmd)
	}
	cmd, _ = (&Codex{bin: "/usr/bin/codex", goos: "linux"}).Command(Turn{Prompt: "go"})
	want = []string{"/usr/bin/codex", "exec", "--json", "--sandbox", "workspace-write", "--skip-git-repo-check", "-"}
	if !slices.Equal(cmd.Argv, want) {
		t.Errorf("default argv = %q", cmd.Argv)
	}
	cmd, _ = b.Command(Turn{Prompt: "go", Plan: true, Sandbox: config.SandboxFullAccess})
	if !slices.Contains(cmd.Argv, "read-only") {
		t.Errorf("plan argv = %q", cmd.Argv)
	}
	if _, err := b.Command(Turn{Sandbox: "yolo"}); err == nil {
		t.Error("unknown sandbox accepted")
	}
	if c := b.(*Codex); c.goos != runtime.GOOS {
		t.Errorf("New gave codex goos %q, want %q", c.goos, runtime.GOOS)
	}
}

// TestCodexResumeArgv: a continued turn puts every exec option before the resume subcommand. `codex exec resume`
// of codex-cli 0.167.0 has no --sandbox: `codex exec resume ID --json --sandbox read-only ...` stops with
// "unexpected argument '--sandbox' found" (exit 2), so every follow-up and gate fix turn on the same account failed
// at startup. Only the session id and the prompt ("-") follow resume.
func TestCodexResumeArgv(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, sb := range []string{config.SandboxReadOnly, config.SandboxWorkspaceWrite, config.SandboxFullAccess} {
			cmd, err := (&Codex{bin: "/usr/bin/codex", goos: goos}).Command(Turn{Prompt: "more", Model: "gpt-6-luna",
				ConversationID: "0199a3f2-0000-7000-8000-00000000abcd", Sandbox: sb})
			if err != nil {
				t.Fatal(err)
			}
			i := slices.Index(cmd.Argv, "resume")
			if i < 0 || !slices.Equal(cmd.Argv[i:], []string{"resume", "0199a3f2-0000-7000-8000-00000000abcd", "-"}) {
				t.Errorf("%s %s: argv = %q, want resume <id> - at the end", goos, sb, cmd.Argv)
				continue
			}
			for _, flag := range []string{"--json", "--sandbox", "--model", "--skip-git-repo-check"} {
				if j := slices.Index(cmd.Argv, flag); j < 0 || j > i {
					t.Errorf("%s %s: %s not before resume in %q", goos, sb, flag, cmd.Argv)
				}
			}
			if j := slices.Index(cmd.Argv, "-c"); (j >= 0) != (goos == "windows" && sb != config.SandboxFullAccess) ||
				j > i {
				t.Errorf("%s %s: -c at %d in %q", goos, sb, j, cmd.Argv)
			}
		}
	}
}

// TestCodexWindowsSandbox: on Windows a sandboxed codex turn names the unelevated Windows sandbox (codex 0.167
// refuses every shell command under read-only or workspace-write without it); danger-full-access and other systems
// get no override.
func TestCodexWindowsSandbox(t *testing.T) {
	override := []string{"-c", "windows.sandbox=unelevated"}
	for _, bin := range []string{`C:\npm\codex.cmd`, `C:\bin\codex.exe`} {
		win := &Codex{bin: bin, goos: "windows"}
		cmd, err := win.Command(Turn{Prompt: "go", Model: "gpt-6-luna"})
		if err != nil {
			t.Fatalf("%s: %v", bin, err)
		}
		want := []string{bin, "exec", "--json", "--sandbox", "workspace-write", "-c", "windows.sandbox=unelevated",
			"--model", "gpt-6-luna", "--skip-git-repo-check", "-"}
		if !slices.Equal(cmd.Argv, want) {
			t.Errorf("windows argv = %q\nwant %q", cmd.Argv, want)
		}
		cmd, err = win.Command(Turn{Prompt: "go", ConversationID: "th1"})
		if err != nil {
			t.Fatal(err)
		}
		want = []string{bin, "exec", "--json", "--sandbox", "workspace-write", "-c",
			"windows.sandbox=unelevated", "--skip-git-repo-check", "resume", "th1", "-"}
		if !slices.Equal(cmd.Argv, want) {
			t.Errorf("windows resume argv = %q\nwant %q", cmd.Argv, want)
		}
		cmd, _ = win.Command(Turn{Prompt: "go", Plan: true, Sandbox: config.SandboxFullAccess})
		if !slices.Contains(cmd.Argv, "read-only") || !hasRun(cmd.Argv, override) {
			t.Errorf("windows plan argv = %q", cmd.Argv)
		}
		cmd, _ = win.Command(Turn{Prompt: "go", Sandbox: config.SandboxReadOnly})
		if !hasRun(cmd.Argv, override) {
			t.Errorf("windows read-only argv = %q", cmd.Argv)
		}
		cmd, _ = win.Command(Turn{Prompt: "go", Sandbox: config.SandboxFullAccess})
		if slices.Contains(cmd.Argv, "-c") {
			t.Errorf("windows danger-full-access argv = %q", cmd.Argv)
		}
	}
	for _, goos := range []string{"linux", "darwin"} {
		for _, sb := range []string{config.SandboxReadOnly, config.SandboxWorkspaceWrite, config.SandboxFullAccess} {
			cmd, err := (&Codex{bin: "/usr/bin/codex", goos: goos}).Command(Turn{Prompt: "go", Sandbox: sb})
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(cmd.Argv, "-c") {
				t.Errorf("%s %s argv = %q", goos, sb, cmd.Argv)
			}
		}
	}
	// The override passes the batch-shim rule (codex.cmd runs through cmd.exe); a quoted TOML value would not, which
	// is why the value is left bare: codex reads a value that is not valid TOML as a literal string.
	if err := checkShimArgs(append([]string{`C:\npm\codex.cmd`}, override...), "windows"); err != nil {
		t.Errorf("override refused for a .cmd shim: %v", err)
	}
	if err := checkShimArgs([]string{`C:\npm\codex.cmd`, "-c", `windows.sandbox="unelevated"`}, "windows"); err == nil {
		t.Error("quoted override accepted for a .cmd shim")
	}
}

// hasRun reports whether run appears in argv as consecutive elements.
func hasRun(argv, run []string) bool {
	for i := 0; i+len(run) <= len(argv); i++ {
		if slices.Equal(argv[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

func TestShimArgs(t *testing.T) {
	if err := checkShimArgs([]string{"claude.cmd", "a&b"}, "linux"); err != nil {
		t.Errorf("non-Windows: %v", err)
	}
	if err := checkShimArgs([]string{`C:\npm\claude.cmd`, "-p", `C:\Users\A B\home\mcp-empty.json`}, "windows"); err != nil {
		t.Errorf("safe args refused: %v", err)
	}
	for _, bad := range []string{"a&b", "50%", `say "hi"`, "a|b", "x^y", "line\nbreak", "!x!"} {
		if err := checkShimArgs([]string{`C:\npm\codex.CMD`, bad}, "windows"); err == nil {
			t.Errorf("arg %q accepted for a .cmd shim", bad)
		}
	}
	if err := checkShimArgs([]string{`C:\bin\codex.exe`, "a&b"}, "windows"); err != nil {
		t.Errorf(".exe refused: %v", err)
	}
	if runtime.GOOS != "windows" {
		return
	}
	if _, err := New("claude", `C:\npm\claude.cmd`); err != nil {
		t.Fatal(err)
	}
	b, _ := New("claude", `C:\npm\claude.cmd`)
	if _, err := b.Command(Turn{Profile: account.Profile{Home: `C:\p%TEMP%\home`}}); err == nil {
		t.Error("claude.cmd with a % in the profile path accepted")
	}
}

func envOf(t *testing.T, b Backend, prof account.Profile, base ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, kv := range base {
		n, v, _ := strings.Cut(kv, "=")
		if nv, keep := b.Filter(n, v, prof); keep {
			out[n] = nv
		}
	}
	for k, v := range b.ProfileEnv(prof) {
		out[k] = v
	}
	return out
}

func TestAgyEnv(t *testing.T) {
	b := mustBackend(t, "agy")
	prof := testProfile(t)
	env := envOf(t, b, prof, "ANTHROPIC_API_KEY=x", "PATH=/bin")
	if env["ANTHROPIC_API_KEY"] != "x" || env["PATH"] != "/bin" || env["AGY_CLI_DISABLE_AUTO_UPDATE"] != "true" ||
		env["SSH_CONNECTION"] != "127.0.0.1 50000 127.0.0.1 22" || env["SSH_CLIENT"] != "127.0.0.1 50000 22" {
		t.Errorf("env = %v", env)
	}
	if runtime.GOOS == "windows" {
		if env["USERPROFILE"] != prof.Home || env["HOME"] != prof.RealHome {
			t.Errorf("windows homes = %q, %q", env["USERPROFILE"], env["HOME"])
		}
	} else if env["HOME"] != prof.Home {
		t.Errorf("HOME = %q", env["HOME"])
	}
	sys := account.Profile{AccountID: "system", Kind: account.KindSystem, Home: prof.RealHome, RealHome: prof.RealHome}
	if env := envOf(t, b, sys); env["SSH_CLIENT"] != "" || env["USERPROFILE"] != "" || env["AGY_CLI_HIDE_LOGO"] != "1" {
		t.Errorf("system env = %v", env)
	}
	if _, err := b.KeyEnv(account.Provider{}, "k", "m"); !errors.Is(err, account.ErrInvalid) {
		t.Errorf("agy KeyEnv = %v", err)
	}
}

func TestClaudeEnv(t *testing.T) {
	b := mustBackend(t, "claude")
	prof := testProfile(t)
	claudeDir := filepath.Join(prof.RealHome, ".claude")
	sep := string(os.PathListSeparator)
	env := envOf(t, b, prof,
		"ANTHROPIC_API_KEY=lead", "anthropic_base_url=https://proxy", "CLAUDE_CODE_SESSION_ID=s", "CLAUDE_CONFIG_DIR=x",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=o", "SEELEGENT_PROVIDER_KEY=k",
		// The lead's Claude Code session markers (M18 hunt H25): a worker that sees them takes itself for a
		// session nested in the lead's.
		"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_SSE_PORT=1234",
		"PATH="+strings.Join([]string{"/bin", filepath.Join(claudeDir, "bin"), "/usr/bin"}, sep),
		"HOOK_FILE="+filepath.Join(claudeDir, "hooks", "x.js"), "OTHER="+claudeDir+"-router", "KEEP=1")
	want := map[string]string{
		"PATH": "/bin" + sep + "/usr/bin", "OTHER": claudeDir + "-router", "KEEP": "1", "CLAUDE_CONFIG_DIR": prof.Home,
	}
	if len(env) != len(want) {
		t.Errorf("env = %v", env)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	// Without a real home nothing is filtered by path.
	noHome := prof
	noHome.RealHome = ""
	if v, keep := b.Filter("X", claudeDir, noHome); !keep || v != claudeDir {
		t.Error("filtered without real home")
	}

	official := account.Provider{ID: "anthropic", Official: true}
	kv, err := b.KeyEnv(official, "sk-1", "claude-opus-5")
	if err != nil || kv["ANTHROPIC_API_KEY"] != "sk-1" || kv["ANTHROPIC_AUTH_TOKEN"] != "" ||
		kv["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "claude-opus-5" || kv["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] != "1" {
		t.Errorf("official env = %v, %v", kv, err)
	}
	other := account.Provider{ID: "zai", BaseURL: "https://api.z.ai/api/anthropic"}
	kv, err = b.KeyEnv(other, "sk-2", "")
	if err != nil || kv["ANTHROPIC_AUTH_TOKEN"] != "sk-2" || kv["ANTHROPIC_BASE_URL"] != other.BaseURL ||
		kv["ANTHROPIC_API_KEY"] != "" || kv["ANTHROPIC_SMALL_FAST_MODEL"] != "" {
		t.Errorf("provider env = %v, %v", kv, err)
	}
	if _, err := b.KeyEnv(account.Provider{ID: "x"}, "k", ""); !errors.Is(err, account.ErrProviderUnavailable) {
		t.Errorf("no base URL: %v", err)
	}
}

func TestPointsInto(t *testing.T) {
	dir := filepath.FromSlash("/home/u/.claude")
	for v, want := range map[string]bool{
		dir:                             true,
		dir + "/x":                      true,
		dir + `\x`:                      true,
		`"` + dir + `"`:                 true,
		dir + "-router":                 false,
		"/elsewhere":                    false,
		dir + "x;" + dir:                true,
		"prefix " + dir + ":/usr/bin":   true,
		strings.ToUpper(dir) + "/hooks": runtime.GOOS == "windows",
	} {
		if got := pointsInto(v, dir); got != want {
			t.Errorf("pointsInto(%q) = %v", v, got)
		}
	}
}

func TestCodexEnv(t *testing.T) {
	b := mustBackend(t, "codex")
	prof := testProfile(t)
	env := envOf(t, b, prof, "OPENAI_API_KEY=a", "openai_base_url=b", "OPENAI_ORG_ID=c", "OPENAI_ORGANIZATION=d",
		"CODEX_HOME=/lead", "codex_sandbox=e", "SEELEGENT_PROVIDER_KEY=f", "KEEP=1")
	if env["CODEX_HOME"] != filepath.Join(prof.Home, ".codex") || env["KEEP"] != "1" {
		t.Errorf("env = %v", env)
	}
	for _, k := range []string{"OPENAI_API_KEY", "openai_base_url", "OPENAI_ORG_ID", "OPENAI_ORGANIZATION",
		"codex_sandbox", "SEELEGENT_PROVIDER_KEY"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s not stripped", k)
		}
	}
	if (env["USERPROFILE"] == prof.Home) != (runtime.GOOS == "windows") {
		t.Errorf("USERPROFILE = %q", env["USERPROFILE"])
	}
	kv, err := b.KeyEnv(account.Provider{}, "sk-3", "m")
	if err != nil || len(kv) != 1 || kv[account.EnvProviderKey] != "sk-3" {
		t.Errorf("key env = %v, %v", kv, err)
	}
}

func TestPrepare(t *testing.T) {
	prof := testProfile(t)
	if err := mustBackend(t, "agy").Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(prof.Home); err != nil || !st.IsDir() {
		t.Fatalf("agy home: %v", err)
	}
	sys := account.Profile{Kind: account.KindSystem}
	if err := mustBackend(t, "agy").Prepare(sys, nil); err != nil {
		t.Errorf("agy system: %v", err)
	}
	if err := mustBackend(t, "claude").Prepare(sys, nil); !errors.Is(err, account.ErrInvalid) {
		t.Errorf("claude system: %v", err)
	}
	if err := mustBackend(t, "codex").Prepare(sys, nil); !errors.Is(err, account.ErrInvalid) {
		t.Errorf("codex system: %v", err)
	}

	if err := mustBackend(t, "claude").Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(prof.Home, claudeEmptyMCP)); err != nil || !strings.Contains(string(data), `"mcpServers":{}`) {
		t.Errorf("mcp-empty.json = %q, %v", data, err)
	}

	codex := mustBackend(t, "codex")
	cfg := filepath.Join(prof.Home, ".codex", "config.toml")
	// A sign-in account without a config gets the default.
	if err := codex.Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), `approval_policy = "never"`) ||
		strings.Contains(string(data), "model_provider") {
		t.Errorf("default config = %q", data)
	}
	// An API-key account gets the provider block.
	p := account.Provider{ID: "groq", Name: `Groq "fast"`, BaseURL: "https://api.groq.com/openai/v1"}
	if err := codex.Prepare(prof, &p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfg)
	for _, want := range []string{`model_provider = "seelegent"`, `[model_providers.seelegent]`,
		`name = "Groq \"fast\""`, `base_url = "https://api.groq.com/openai/v1"`, `env_key = "SEELEGENT_PROVIDER_KEY"`,
		`wire_api = "chat"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config lacks %s:\n%s", want, data)
		}
	}
	if err := codex.Prepare(prof, &account.Provider{ID: "x"}); !errors.Is(err, account.ErrProviderUnavailable) {
		t.Errorf("provider without base URL: %v", err)
	}
	// The user's own settings survive; the provider block goes when the account signs in instead.
	if err := os.WriteFile(cfg, append(data, []byte("\n[profiles.mine]\nmodel = \"x\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := codex.Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfg)
	if strings.Contains(string(data), "seelegent") || !strings.Contains(string(data), "[profiles.mine]") ||
		!strings.Contains(string(data), `approval_policy = "never"`) {
		t.Errorf("stripped config = %q", data)
	}
	// Nothing to strip: the file is left alone.
	st1, _ := os.Stat(cfg)
	if err := codex.Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(cfg); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("unchanged config rewritten")
	}
}

func TestTomlString(t *testing.T) {
	for in, want := range map[string]string{
		"plain": `"plain"`, `a"b`: `"a\"b"`, `c:\x`: `"c:\\x"`, "tab\t": `"tab\u0009"`, "ünï": `"ünï"`,
	} {
		if got := tomlString(in); got != want {
			t.Errorf("tomlString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestSignIn(t *testing.T) {
	prof := testProfile(t)
	if err := os.MkdirAll(prof.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(prof.Home, agyLoginResult)
	if err := os.WriteFile(result, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := mustBackend(t, "agy").SignIn(prof)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale result not removed")
	}
	if s.StdoutPath != result || !slices.Equal(s.Argv, []string{"/usr/bin/agy", "-p", "/usage", "--output-format", "json"}) {
		t.Errorf("agy sign-in = %+v", s)
	}
	if s.Succeeded(0) {
		t.Error("success without a result file")
	}
	if err := os.WriteFile(result, []byte(`{"status" : "SUCCESS","command":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !s.Succeeded(1) {
		t.Error("SUCCESS in the result file not seen")
	}
	for name, argv := range map[string][]string{"claude": {"/usr/bin/claude"}, "codex": {"/usr/bin/codex", "login"}} {
		s, err := mustBackend(t, name).SignIn(prof)
		if err != nil || !slices.Equal(s.Argv, argv) || !s.Succeeded(0) || s.Succeeded(1) || s.Title == "" {
			t.Errorf("%s sign-in = %+v, %v", name, s, err)
		}
	}
}

func TestQuotaGroups(t *testing.T) {
	agy := mustBackend(t, "agy")
	for model, want := range map[string]string{
		"gemini-4-argon-high": "Gemini 4 Argon", "gemini-3.8-flash-low": "Gemini Models", "claude-sonnet-5": "Claude and GPT models",
	} {
		if got := agy.QuotaGroup(model); got != want {
			t.Errorf("agy QuotaGroup(%s) = %s", model, got)
		}
	}
	if mustBackend(t, "claude").QuotaGroup("x") != "Claude" || mustBackend(t, "codex").QuotaGroup("x") != "Codex" {
		t.Error("quota groups of claude/codex")
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	exe := func(p string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	override := exe(filepath.Join(dir, "custom", "agy-dev"))
	if got, err := Find("agy", []string{EnvAgyBin + "=" + override}, ""); err != nil || got != override {
		t.Errorf("override = %q, %v", got, err)
	}
	if _, err := Find("agy", []string{EnvAgyBin + "=" + filepath.Join(dir, "missing")}, ""); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("missing override: %v", err)
	}
	if _, err := Find("nope", nil, ""); !errors.Is(err, ErrUnknownBackend) {
		t.Errorf("unknown: %v", err)
	}
	home := filepath.Join(dir, "home")
	local := exe(filepath.Join(home, ".local", "bin", "claude"))
	if got, err := Find("claude", []string{"PATH="}, home); err != nil || got != local {
		t.Errorf("claude in ~/.local/bin = %q, %v", got, err)
	}
	pathDir := filepath.Join(dir, "path")
	name := "codex"
	if runtime.GOOS == "windows" {
		name = "codex.exe"
	}
	onPath := exe(filepath.Join(pathDir, name))
	if got, err := Find("codex", []string{"PATH=" + pathDir}, home); err != nil || !strings.EqualFold(got, onPath) {
		t.Errorf("codex on PATH = %q, %v", got, err)
	}
	if _, err := Find("codex", []string{"PATH=" + t.TempDir()}, home); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no codex: %v", err)
	}
}

func TestCandidatesWindows(t *testing.T) {
	lad := t.TempDir()
	msix := filepath.Join(lad, "Packages", "Google.Agy_1", "LocalCache", "Local", "agy", "bin", "agy.exe")
	if err := os.MkdirAll(filepath.Dir(msix), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(msix, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"LOCALAPPDATA=" + lad, "APPDATA=" + filepath.Join(lad, "roaming")}
	got := candidates("agy", env, "", "windows")
	if len(got) != 2 || got[0] != filepath.Join(lad, "agy", "bin", "agy.exe") || got[1] != msix {
		t.Errorf("agy candidates = %q", got)
	}
	got = candidates("claude", env, "/h", "windows")
	if len(got) != 4 || !strings.HasSuffix(got[3], filepath.Join("npm", "claude.cmd")) {
		t.Errorf("claude candidates = %q", got)
	}
	if got := candidates("agy", env, "", "linux"); len(got) != 0 {
		t.Errorf("agy candidates on linux = %q", got)
	}
}

func TestModelPolicy(t *testing.T) {
	s := config.Defaults()
	agy, err := PolicyFor("agy", s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, err := agy.Resolve("", "", config.TierStandard); err != nil || m != s.Backends.Agy.Tiers.Standard {
		t.Errorf("default tier = %q, %v", m, err)
	}
	if m, err := agy.Resolve("", "fast", "standard"); err != nil || m != s.Backends.Agy.Tiers.Fast {
		t.Errorf("fast = %q, %v", m, err)
	}
	if m, err := agy.Resolve("FAST", "", "standard"); err != nil || m != s.Backends.Agy.Tiers.Fast {
		t.Errorf("tier name as model = %q, %v", m, err)
	}
	if m, err := agy.Resolve("gemini-3.8-pro-x", "", ""); err != nil || m != "gemini-3.8-pro-x" {
		t.Errorf("explicit allowed model = %q, %v", m, err)
	}
	for _, bad := range []struct{ model, tier string }{{"gemini-4-argon", ""}, {"", "ultra"}, {"a b", ""}} {
		if _, err := agy.Resolve(bad.model, bad.tier, "standard"); !errors.Is(err, ErrModel) {
			t.Errorf("Resolve(%q, %q) = %v", bad.model, bad.tier, err)
		}
	}

	s.Backends.Claude.Enabled = true
	s.Backends.Claude.AllowedModels = ""
	s.Backends.Claude.Tiers = config.TierModels{Standard: "claude-sonnet-5"}
	claude, _ := PolicyFor("claude", s, nil)
	if m, err := claude.Resolve("CLAUDE-SONNET-5", "", ""); err != nil || m != "CLAUDE-SONNET-5" {
		t.Errorf("tier model by id = %q, %v", m, err)
	}
	// An empty allowedModels allows any model, not only the tier models (the old version allowed none).
	if m, err := claude.Resolve("claude-opus-5", "", ""); err != nil || m != "claude-opus-5" {
		t.Errorf("empty allowedModels refused another model: %q, %v", m, err)
	}
	if _, err := claude.Resolve("", "max", ""); !errors.Is(err, ErrModel) || !strings.Contains(err.Error(), "no model") {
		t.Errorf("unmapped tier: %v", err)
	}
	s.Backends.Claude.AllowedModels = "("
	broken, _ := PolicyFor("claude", s, nil)
	if _, err := broken.Resolve("x", "", ""); !errors.Is(err, ErrModel) {
		t.Errorf("broken pattern: %v", err)
	}

	p := &account.Provider{ID: "zai", Enabled: true, AllowedModels: "^glm-", Tiers: config.TierModels{Fast: "glm-4.6"}}
	pp, _ := PolicyFor("claude", s, p)
	if m, err := pp.Resolve("", "fast", ""); err != nil || m != "glm-4.6" || pp.Source != "provider zai" {
		t.Errorf("provider policy = %q, %v (%+v)", m, err, pp)
	}
	// A provider list allows its models and, on top, the tier models; nothing else.
	ds := &account.Provider{ID: "deepseek", Enabled: true, Models: []string{"deepseek-v4", "deepseek-r2"},
		AllowedModels: account.ModelsPattern([]string{"deepseek-v4", "deepseek-r2"}),
		Tiers:         config.TierModels{Fast: "deepseek-chat"}}
	dp, _ := PolicyFor("claude", s, ds)
	for _, m := range []string{"deepseek-v4", "DeepSeek-R2", "deepseek-chat"} {
		if err := dp.Allowed(m); err != nil {
			t.Errorf("provider list refused %s: %v", m, err)
		}
	}
	if m, err := dp.Resolve("", "fast", ""); err != nil || m != "deepseek-chat" {
		t.Errorf("provider tier model = %q, %v", m, err)
	}
	for _, m := range []string{"deepseek-v4x", "glm-4.6"} {
		if err := dp.Allowed(m); !errors.Is(err, ErrModel) {
			t.Errorf("provider list allowed %s: %v", m, err)
		}
	}
	// Both empty: any model.
	anyp, _ := PolicyFor("claude", s, &account.Provider{ID: "any", Enabled: true})
	if err := anyp.Allowed("whatever-model-1"); err != nil {
		t.Errorf("provider without a list refused a model: %v", err)
	}
	p.Enabled = false
	pp, _ = PolicyFor("claude", s, p)
	if _, err := pp.Resolve("", "fast", ""); !errors.Is(err, ErrModel) {
		t.Errorf("disabled provider: %v", err)
	}
	if _, err := PolicyFor("gpt", s, nil); !errors.Is(err, ErrUnknownBackend) {
		t.Errorf("unknown backend: %v", err)
	}
}

func TestClassifyRules(t *testing.T) {
	cases := []struct {
		rules []rule
		text  string
		want  event.Class // "" for no match
	}{
		// B8 false positives of the old patterns.
		{agyRules(), "assigning the design in signing module", ""},
		{agyRules(), "open quota.go line 429", ""},
		{agyRules(), "limit reached for retries in test", ""},
		{codexRules(), "test expects the handler to answer forbidden", ""},
		{codexRules(), "unexpected status 403 Forbidden", event.ClassAuthRequired},
		{codexRules(), "unauthorized access test passed", ""},
		{claudeRules(), "error at line 403", ""},
		{agyRules(), "port 5003 busy", ""},
		// Real messages.
		{agyRules(), "RESOURCE_EXHAUSTED: quota exceeded", event.ClassQuotaExhausted},
		{agyRules(), "HTTP 429 Too Many Requests", event.ClassQuotaExhausted},
		{agyRules(), "Please sign in again", event.ClassAuthRequired},
		{agyRules(), "status: 503 UNAVAILABLE", event.ClassTransient},
		{agyRules(), "read tcp: connection reset by peer", event.ClassTransient},
		{claudeRules(), "API Error: 401 {\"type\":\"authentication_error\"}", event.ClassAuthRequired},
		{claudeRules(), "API Error: 429 rate_limit_error", event.ClassQuotaExhausted},
		{claudeRules(), "API Error: 500 Internal server error", event.ClassTransient},
		// Credits are not a quota limit: no time ends them (review N1).
		{claudeRules(), "Credit balance is too low", event.ClassCredits},
		{claudeRules(), `API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API. Please go to Plans & Billing to upgrade or purchase credits."}}`, event.ClassCredits},
		{claudeRules(), "402 Payment Required", event.ClassCredits},
		{codexRules(), "You exceeded your current quota, please check your plan and billing details.", event.ClassCredits},
		{codexRules(), `{"error":{"code":"insufficient_quota"}}`, event.ClassCredits},
		{codexRules(), "You've hit your usage limit. Try again at 3:10 PM.", event.ClassQuotaExhausted},
		{agyRules(), "out of credits", event.ClassQuotaExhausted},
		{codexRules(), "exceeded retry limit, last status: 429 Too Many Requests", event.ClassQuotaExhausted},
		{codexRules(), "401 Unauthorized: invalid token", event.ClassAuthRequired},
		{codexRules(), "Your access token could not be refreshed: refresh token was already used", event.ClassAuthRequired},
		{codexRules(), "unexpected status 502 Bad Gateway", event.ClassTransient},
		{codexRules(), "", ""},
		// Claude Code's limit messages (field report 3, B1).
		{claudeRules(), "You've hit your 5-hour limit · resets 3:10am (Asia/Ho_Chi_Minh)", event.ClassQuotaExhausted},
		{claudeRules(), "You’ve hit your weekly limit · resets Nov 10, 3pm (Europe/Paris)", event.ClassQuotaExhausted},
		{claudeRules(), "You've hit your session limit", event.ClassQuotaExhausted},
		{claudeRules(), "You've hit your Opus limit", event.ClassQuotaExhausted},
		{claudeRules(), "You've hit your sonnet limit", event.ClassQuotaExhausted},
		{claudeRules(), "You've hit your usage limit", event.ClassQuotaExhausted},
		{claudeRules(), "You've hit your limit", event.ClassQuotaExhausted},
		{claudeRules(), "5-hour limit reached", event.ClassQuotaExhausted},
		{claudeRules(), "Weekly limit reached ∙ resets Monday", event.ClassQuotaExhausted},
		{claudeRules(), "Opus limit reached, now using Sonnet", event.ClassQuotaExhausted},
		{claudeRules(), "Sonnet limit reached", event.ClassQuotaExhausted},
		{claudeRules(), "5-hour limit - resets 9pm", event.ClassQuotaExhausted},
		{claudeRules(), "Session limit · resets 4am", event.ClassQuotaExhausted},
		{claudeRules(), "Claude AI usage limit reached|1762459200", event.ClassQuotaExhausted},
		// Ordinary text in tool output or answers is not a limit.
		{claudeRules(), "--- FAIL: TestBurst: time limit exceeded", ""},
		{claudeRules(), "the rate limiter code drops the third call", ""},
		{claudeRules(), "hit your timeout limit in the test", ""},
		{claudeRules(), "the weekly limit is configurable", ""},
		{claudeRules(), "the session limiter resets every minute", ""},
		{claudeRules(), "limit-resets.go: added the weekly window", ""},
		{claudeRules(), "white-hit your limiter", ""},
	}
	for _, c := range cases {
		got, ok := classifyText(c.rules, c.text)
		if (c.want == "") == ok || got != c.want {
			t.Errorf("classify(%q) = %q, %v; want %q", c.text, got, ok, c.want)
		}
	}
	if got := lastLines("a\n\nb\r\nc\n  \nd\n", 3); got != "b\nc\nd" {
		t.Errorf("lastLines = %q", got)
	}
}

func TestFinishByExitReason(t *testing.T) {
	cases := []struct {
		reason launch.Reason
		status OutcomeStatus
		class  event.Class
	}{
		{launch.ReasonTimeout, OutcomeFailed, event.ClassFatal},
		{launch.ReasonStartFailed, OutcomeFailed, event.ClassFatal},
		{launch.ReasonLost, OutcomeFailed, event.ClassTransient},
		{launch.ReasonCanceled, OutcomeCanceled, ""},
		{launch.ReasonExited, OutcomeFailed, event.ClassFatal},
	}
	for _, c := range cases {
		p := mustBackend(t, "claude").NewParser(nil)
		p.Line([]byte(`{"type":"system","subtype":"init","session_id":"s9"}`))
		exit := launch.Exit{Reason: c.reason, Code: 3, Error: "boom"}
		var col Collector
		for _, e := range p.Finish(exit, "") {
			col.Add(e)
		}
		o := col.Outcome(exit)
		if o.Status != c.status || o.Class != c.class || o.ConversationID != "s9" || o.Error == "" {
			t.Errorf("%s: outcome = %+v", c.reason, o)
		}
	}
	// A report written just before the cancel still counts.
	p := mustBackend(t, "claude").NewParser(nil)
	var col Collector
	exit := launch.Exit{Reason: launch.ReasonCanceled}
	evs := p.Line([]byte(`{"type":"result","subtype":"success","result":"done","session_id":"s1"}`))
	for _, e := range append(evs, p.Finish(exit, "")...) {
		col.Add(e)
	}
	if o := col.Outcome(exit); o.Status != OutcomeOK || o.Response != "done" {
		t.Errorf("success then cancel = %+v", o)
	}
	// An error report without text takes the stderr tail.
	p = mustBackend(t, "codex").NewParser(nil)
	p.Line([]byte(`{"type":"turn.failed","error":{}}`))
	evs = p.Finish(launch.Exit{Reason: launch.ReasonExited, Code: 1}, "x\nERROR: 503 Service Unavailable\n")
	if last := evs[len(evs)-1]; last.Error == nil || last.Error.Class != event.ClassFatal {
		t.Errorf("turn.failed with default text = %+v", evs)
	}
}

func TestParserHelpers(t *testing.T) {
	for raw, want := range map[string]string{
		``: "", `null`: "", `"s"`: "s", `["a","b"]`: "a\nb", `[{"type":"text","text":"t"},{"x":1}]`: "t\n{\"x\":1}",
		`{"k":1}`: `{"k":1}`, `42`: "42",
	} {
		if got := textOf([]byte(raw)); got != want {
			t.Errorf("textOf(%s) = %q", raw, got)
		}
	}
	long := strings.Repeat("é", maxOutput)
	if got := capText(long); len(got) > maxOutput+20 || !strings.HasSuffix(got, "[truncated]") || !utf8Valid(got) {
		t.Errorf("capText produced %d bytes", len(got))
	}
	b := newBase("x", []string{"sec\"ret", "ab"}, nil)
	if got := string(b.maskJSON([]byte(`{"k":"sec\"ret"}`))); got != `{"k":"[REDACTED]"}` && !strings.Contains(got, redacted) {
		t.Errorf("maskJSON = %s", got)
	}
	if b.maskJSON([]byte(" null ")) != nil {
		t.Error("maskJSON(null) not nil")
	}
	if b.mask("ab") != "ab" {
		t.Error("secrets shorter than four characters must not be masked")
	}
	for raw, want := range map[string]int{`"a"`: 1, `["a","b"]`: 2, `[1,2,3]`: 3, `null`: 0, `{"x":1}`: 1, `""`: 0} {
		if got := stringList([]byte(raw)); len(got) != want {
			t.Errorf("stringList(%s) = %q", raw, got)
		}
	}
	if u, ok := parseUsage([]byte(`{"inputTokens":1,"outputTokens":2,"cost_usd":0.5}`)); !ok || u.TotalTokens != 3 || u.CostUSD != 0.5 {
		t.Errorf("parseUsage = %+v, %v", u, ok)
	}
	if _, ok := parseUsage([]byte(`[1]`)); ok {
		t.Error("parseUsage accepted an array")
	}
	// Cache writes: the total, its one-hour part from the split, the split's sum without a total, and a split
	// larger than the total clamped to it.
	for raw, want := range map[string][2]int64{
		`{"input_tokens":1,"cache_creation_input_tokens":4100}`: {4100, 0},
		`{"input_tokens":1,"cache_creation_input_tokens":4100,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":4000}}`: {4100, 4000},
		`{"input_tokens":1,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":4000}}`:                                    {4100, 4000},
		`{"input_tokens":1,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_1h_input_tokens":4000}}`:                                   {10, 10},
		`{"input_tokens":1,"cache_creation_input_tokens":-5}`:                                                                                       {0, 0},
	} {
		u, ok := parseUsage([]byte(raw))
		if !ok || u.CacheWriteTokens != want[0] || u.CacheWrite1hTokens != want[1] || u.InputTokens != 1 {
			t.Errorf("parseUsage(%s) = %+v", raw, u)
		}
	}
	if stringField([]byte(`not json`), "x") != "" || stringField([]byte(`{"a":" ","b":"v"}`), "a", "b") != "v" {
		t.Error("stringField")
	}
	if errorText([]byte(`{"message":"m"}`)) != "m" || errorText([]byte(`"s"`)) != "s" {
		t.Error("errorText")
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }

func TestUnknownLinesAreLogs(t *testing.T) {
	for _, name := range Names() {
		p := mustBackend(t, name).NewParser(nil)
		evs := p.Line([]byte("not json at all"))
		evs = append(evs, p.Line([]byte(`{"event":"mystery","type":"mystery"}`))...)
		evs = append(evs, p.Line([]byte("   "))...)
		if len(evs) != 2 || evs[0].Kind != event.KindLog || evs[1].Kind != event.KindLog {
			t.Errorf("%s: %+v", name, evs)
		}
	}
	// Lines that carry nothing to report.
	agy := mustBackend(t, "agy").NewParser(nil)
	for _, l := range []string{
		`{"event":"step_update"}`,
		`{"event":"step_update","step_update":{"step_type":"planner"}}`,
		`{"event":"step_update","step_update":{"step_index":1,"step_type":"tool","state":"QUEUED","tool_name":"x"}}`,
	} {
		if evs := agy.Line([]byte(l)); len(evs) != 0 {
			t.Errorf("agy %s: %+v", l, evs)
		}
	}
	if evs := agy.Line([]byte(`{"event":"result"}`)); len(evs) != 1 || evs[0].Report.Status != event.StatusError {
		t.Errorf("empty result = %+v", evs)
	}
	claude := mustBackend(t, "claude").NewParser(nil)
	for _, l := range []string{`{"type":"assistant"}`, `{"type":"user"}`, `{"type":"user","message":{"content":[{"type":"text","text":"x"}]}}`} {
		if evs := claude.Line([]byte(l)); len(evs) != 0 {
			t.Errorf("claude %s: %+v", l, evs)
		}
	}
	if evs := claude.Line([]byte(`{"type":"system","subtype":"hook_response"}`)); len(evs) != 1 || evs[0].Kind != event.KindLog {
		t.Errorf("claude system line = %+v", evs)
	}
	if evs := claude.Line([]byte(`{"type":"result","subtype":"error_max_turns","is_error":true}`)); evs[0].Report.Error == "" {
		t.Errorf("claude error without text = %+v", evs)
	}
	codex := mustBackend(t, "codex").NewParser(nil)
	for _, l := range []string{
		`{"type":"item.started"}`,
		`{"type":"item.updated","item":{"id":"i","type":"command_execution","command":"ls"}}`,
		`{"type":"item.completed","item":{"id":"r","type":"reasoning"}}`,
		`{"type":"item.completed","item":{"id":"m","type":"agent_message"}}`,
		`{"type":"item.started","item":{"id":"f","type":"file_change","changes":[]}}`,
	} {
		if evs := codex.Line([]byte(l)); len(evs) != 0 {
			t.Errorf("codex %s: %+v", l, evs)
		}
	}
	evs := codex.Line([]byte(`{"type":"item.completed","item":{"id":"w","type":"web_search","query":"go"}}`))
	evs = append(evs, codex.Line([]byte(`{"type":"item.started","item":{"id":"t","type":"todo_list","items":[{"text":"a"}]}}`))...)
	evs = append(evs, codex.Line([]byte(`{"type":"item.completed","item":{"id":"e","type":"error","text":"warn"}}`))...)
	evs = append(evs, codex.Line([]byte(`{"type":"item.completed","item":{"id":"z","type":"novel"}}`))...)
	evs = append(evs, codex.Line([]byte(`{"type":"item.completed","item":{"id":"p","type":"file_change","status":"failed","changes":[{"path":"/x"}]}}`))...)
	evs = append(evs, codex.Line([]byte(`{"type":"item.completed","item":{"id":"q","type":"mcp_tool_call","status":"failed","error":{"message":"down"}}}`))...)
	evs = append(evs, codex.Line([]byte(`{"type":"error","error":{"message":"fatal-ish"}}`))...)
	if len(evs) != 7 || evs[0].Tool.Name != "web_search" || evs[1].Tool.Name != "todo_list" || evs[2].Kind != event.KindLog ||
		evs[3].Kind != event.KindLog || evs[4].Tool.State != event.ToolFailed || evs[5].Tool.Output != "down" ||
		evs[5].Tool.Name != "mcp_tool_call" || evs[6].Text != "fatal-ish" {
		t.Errorf("codex items = %+v", evs)
	}
	// The report waits for Finish (codexParser.held).
	if evs := codex.Line([]byte(`{"type":"turn.completed"}`)); len(evs) != 0 {
		t.Errorf("turn.completed without usage = %+v", evs)
	}
	if evs := codex.Finish(launch.Exit{Reason: launch.ReasonExited}, ""); len(evs) != 1 || evs[0].Report == nil ||
		evs[0].Report.Usage != nil {
		t.Errorf("finish after turn.completed without usage = %+v", evs)
	}
}
