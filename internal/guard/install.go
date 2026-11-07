package guard

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Names of what the guard installs.
const (
	// HookSubcommand is the hidden seelegent subcommand the pre-tool hooks run.
	HookSubcommand = "__guard"
	// ShimSubcommand is the hidden seelegent subcommand the git shim runs (on Windows the shim is a copy of the
	// executable named git.exe, recognized by its name instead).
	ShimSubcommand = "__git"
	// agyHookName is the key of Seelegent's entry in agy's hooks.json.
	agyHookName = "seelegent-guard"
	// agyScript is the Windows batch file the agy hook runs (agy runs hook commands with cmd /c there).
	agyScript = "seelegent-guard.cmd"
	// claudeMatcher is the Claude Code tools the hook watches: both shell tools.
	claudeMatcher = "Bash|PowerShell"
	// hookTimeout is the hook timeout in seconds given to the CLIs.
	hookTimeout = 30
)

// agyShellTools are the agy tools that run shell input.
var agyShellTools = []string{"run_command", "send_command_input"}

// commandLine is how a hook command names exe: double quotes with forward slashes on Windows (cmd and bash read it
// the same way), POSIX single quotes elsewhere.
func commandLine(exe string, args ...string) string {
	var q string
	if runtime.GOOS == "windows" {
		q = `"` + strings.ReplaceAll(exe, `\`, "/") + `"`
	} else {
		q = posixQuote(exe)
	}
	return strings.Join(append([]string{q}, args...), " ")
}

// posixQuote quotes s for a POSIX shell.
func posixQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ClaudeSettingsPath is the settings.json Claude Code reads with --setting-sources user in a profile home.
func ClaudeSettingsPath(home string) string { return filepath.Join(home, "settings.json") }

// InstallClaude adds (on) or removes (off) the guard hook in the settings.json of a Claude Code profile home.
// Other settings and hooks are kept; a settings file that cannot be read is an error, never reset.
func InstallClaude(home, exe string, on bool) error {
	path := ClaudeSettingsPath(home)
	doc, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("guard: claude settings: %w", err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if doc["hooks"] != nil && hooks == nil {
		return fmt.Errorf("guard: claude settings %s: hooks is not an object", path)
	}
	if hooks == nil {
		hooks = map[string]any{}
	}
	var groups []any
	if raw, ok := hooks["PreToolUse"]; ok && raw != nil {
		if groups, ok = raw.([]any); !ok {
			return fmt.Errorf("guard: claude settings %s: hooks.PreToolUse is not a list", path)
		}
	}
	kept := make([]any, 0, len(groups)+1)
	for _, g := range groups {
		if !isOurClaudeGroup(g) {
			kept = append(kept, g)
		}
	}
	if on {
		kept = append(kept, map[string]any{
			"matcher": claudeMatcher,
			"hooks": []any{map[string]any{
				"type": "command", "command": commandLine(exe, HookSubcommand, HookClaude), "timeout": hookTimeout,
			}},
		})
	}
	if len(kept) > 0 {
		hooks["PreToolUse"] = kept
	} else {
		delete(hooks, "PreToolUse")
	}
	if len(hooks) > 0 {
		doc["hooks"] = hooks
	} else {
		delete(doc, "hooks")
	}
	return writeJSONIfChanged(path, doc)
}

// isOurClaudeGroup reports a PreToolUse group whose hooks include Seelegent's guard.
func isOurClaudeGroup(g any) bool {
	m, _ := g.(map[string]any)
	list, _ := m["hooks"].([]any)
	for _, h := range list {
		hm, _ := h.(map[string]any)
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, HookSubcommand+" "+HookClaude) {
			return true
		}
	}
	return false
}

// AgyConfigDir is agy's global customization folder inside a profile home (hooks.json lives there).
func AgyConfigDir(home string) string { return filepath.Join(home, ".gemini", "config") }

// InstallAgy adds (on) or removes (off) the guard hook in the hooks.json of an agy profile home. agy runs hook
// commands with cmd /c (Windows) or sh -c from the folder of hooks.json; on Windows the command is a small batch
// file next to it, so a path with spaces needs no cmd quoting rules.
func InstallAgy(home, exe string, on bool) error {
	dir := AgyConfigDir(home)
	path := filepath.Join(dir, "hooks.json")
	doc, err := readJSONObject(path)
	if err != nil {
		return fmt.Errorf("guard: agy hooks: %w", err)
	}
	script := filepath.Join(dir, agyScript)
	if !on {
		if err := os.Remove(script); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("guard: agy hooks: %w", err)
		}
		if _, ok := doc[agyHookName]; !ok {
			return nil
		}
		delete(doc, agyHookName)
		return writeJSONIfChanged(path, doc)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("guard: agy hooks: %w", err)
	}
	command := commandLine(exe, HookSubcommand, HookAgy)
	if runtime.GOOS == "windows" {
		body := "@\"" + strings.ReplaceAll(exe, "%", "%%") + "\" " + HookSubcommand + " " + HookAgy + "\r\n"
		if err := writeIfChanged(script, []byte(body), 0o700); err != nil {
			return fmt.Errorf("guard: agy hooks: %w", err)
		}
		command = agyScript
	}
	groups := make([]any, 0, len(agyShellTools))
	for _, tool := range agyShellTools {
		groups = append(groups, map[string]any{
			"matcher": "^" + tool + "$",
			"hooks":   []any{map[string]any{"type": "command", "command": command, "timeout": hookTimeout}},
		})
	}
	doc[agyHookName] = map[string]any{"PreToolUse": groups}
	return writeJSONIfChanged(path, doc)
}

// readJSONObject reads a JSON object file; a missing or empty file is an empty object.
func readJSONObject(path string) (map[string]any, error) {
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func writeJSONIfChanged(path string, doc map[string]any) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writeIfChanged(path, append(data, '\n'), 0o600)
}

// writeIfChanged writes data atomically unless the file already holds it.
func writeIfChanged(path string, data []byte, perm fs.FileMode) error {
	if old, err := fsutil.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, perm)
}

// ShimName is the file name of the git shim in its folder.
func ShimName() string {
	if runtime.GOOS == "windows" {
		return "git.exe"
	}
	return "git"
}

// InstallShim puts a git shim into dir that runs the guard before the real git: on Windows a hard link to exe (a
// copy across volumes) named git.exe, elsewhere a sh script that runs `exe __git`. Workers whose CLI has no
// pre-tool hook get dir first on PATH.
func InstallShim(dir, exe string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("guard: shim: %w", err)
	}
	path := filepath.Join(dir, ShimName())
	if runtime.GOOS != "windows" {
		script := "#!/bin/sh\nexec " + commandLine(exe, ShimSubcommand, "--shim-dir="+posixQuote(dir)) + " \"$@\"\n"
		if err := writeIfChanged(path, []byte(script), 0o755); err != nil {
			return fmt.Errorf("guard: shim: %w", err)
		}
		return os.Chmod(path, 0o755) //nolint:gosec // the shim is a program every worker runs
	}
	if same(path, exe) {
		return nil
	}
	tmp := path + ".new"
	_ = os.Remove(tmp)
	if err := os.Link(exe, tmp); err != nil {
		data, rerr := os.ReadFile(exe)
		if rerr != nil {
			return fmt.Errorf("guard: shim: %w", rerr)
		}
		if old, err := os.ReadFile(path); err == nil && sha256.Sum256(old) == sha256.Sum256(data) {
			return nil
		}
		if err := fsutil.WriteFileAtomic(path, data, 0o755); err != nil {
			return fmt.Errorf("guard: shim: %w", err)
		}
		return nil
	}
	if err := fsutil.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("guard: shim (a worker may still run the old one): %w", err)
	}
	return nil
}

// same reports whether a and b are the same file (a hard link).
func same(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	return err == nil && os.SameFile(sa, sb)
}

// IsShim reports whether the running program is the Windows git shim (a copy of seelegent named git.exe).
func IsShim(argv0, executable string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	for _, p := range []string{executable, argv0} {
		base := strings.ToLower(filepath.Base(p))
		if base == "git.exe" || base == "git" {
			return true
		}
	}
	return false
}

// WithoutDir returns env with dir removed from PATH (so the shim finds the real git, not itself).
func WithoutDir(env []string, dir string) []string {
	key, _ := fsutil.Key(dir)
	out := make([]string, 0, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 || !strings.EqualFold(kv[:eq], "PATH") {
			out = append(out, kv)
			continue
		}
		var kept []string
		for _, d := range filepath.SplitList(kv[eq+1:]) {
			if k, err := fsutil.Key(d); err == nil && k == key {
				continue
			}
			kept = append(kept, d)
		}
		out = append(out, kv[:eq+1]+strings.Join(kept, string(os.PathListSeparator)))
	}
	return out
}
