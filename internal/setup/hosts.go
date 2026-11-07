package setup

import (
	"encoding/json"
	"path/filepath"
	"strconv"
)

// GeminiTimeoutMs is the per-server timeout setup writes for Gemini CLI (milliseconds): its default of 10 minutes
// equals Seelegent's default wait and is below the longest one (3000 s).
const GeminiTimeoutMs = 3_600_000

// allHosts lists the lead hosts setup knows, in the order it handles them. See docs/setup.md for where each one
// keeps its MCP servers and skills, and what is certain about each.
func allHosts() []host {
	return []host{
		claudeCode{},
		jsonHost{hostID: "claude-desktop", hostName: "Claude Desktop", container: "mcpServers",
			path:    func(e Env) string { return filepath.Join(claudeDesktopDir(e), "claude_desktop_config.json") },
			detects: detectFolder(claudeDesktopDir)},
		codex{},
		jsonHost{hostID: "opencode", hostName: "OpenCode", container: "mcp", style: styleOpenCode,
			path: openCodeConfig,
			detects: detectAny(detectCLI("opencode"),
				detectFolder(func(e Env) string { return filepath.Join(e.xdgConfig(), "opencode") })),
			skills: agentsSkills},
		jsonHost{hostID: "agy", hostName: "Antigravity CLI", container: "mcpServers",
			path: func(e Env) string { return e.home(".gemini", "config", "mcp_config.json") },
			detects: detectAny(detectCLI("agy"), detectAgyBin, detectFolder(func(e Env) string {
				return e.home(".gemini", "antigravity-cli")
			})),
			skills: func(e Env) string { return e.home(".gemini", "config", "skills") }},
		jsonHost{hostID: "antigravity", hostName: "Antigravity", container: "mcpServers",
			path:    func(e Env) string { return e.home(".gemini", "antigravity", "mcp_config.json") },
			detects: detectFolder(func(e Env) string { return e.home(".gemini", "antigravity") })},
		jsonHost{hostID: "gemini", hostName: "Gemini CLI", container: "mcpServers",
			path:    func(e Env) string { return e.home(".gemini", "settings.json") },
			detects: detectCLI("gemini"),
			extra:   []jmember{{key: "timeout", val: json.RawMessage(strconv.Itoa(GeminiTimeoutMs))}}},
		jsonHost{hostID: "cursor", hostName: "Cursor", container: "mcpServers",
			path:    func(e Env) string { return e.home(".cursor", "mcp.json") },
			detects: detectFolder(func(e Env) string { return e.home(".cursor") })},
		jsonHost{hostID: "windsurf", hostName: "Windsurf", container: "mcpServers",
			path:    func(e Env) string { return e.home(".codeium", "windsurf", "mcp_config.json") },
			detects: detectFolder(func(e Env) string { return e.home(".codeium", "windsurf") })},
	}
}

// agentsSkills is ~/.agents/skills, which Codex and OpenCode read (kept from the Node version).
func agentsSkills(e Env) string { return e.home(".agents", "skills") }

// claudeDesktopDir is Claude Desktop's settings folder. The Windows Store (MSIX) build sees a private copy of
// AppData when one exists, so that copy wins over %APPDATA%\Claude.
func claudeDesktopDir(e Env) string {
	if e.goos() == "windows" {
		pattern := filepath.Join(e.localAppData(), "Packages", "Claude_*", "LocalCache", "Roaming", "Claude")
		if dirs, _ := filepath.Glob(pattern); len(dirs) > 0 {
			for _, d := range dirs {
				if exists(filepath.Join(d, "claude_desktop_config.json")) {
					return d
				}
			}
		}
	}
	return filepath.Join(e.appConfigDir(), "Claude")
}

// openCodeConfig is opencode.json in OpenCode's config folder, or opencode.jsonc when only that exists.
func openCodeConfig(e Env) string {
	dir := filepath.Join(e.xdgConfig(), "opencode")
	plain := filepath.Join(dir, "opencode.json")
	if jsonc := filepath.Join(dir, "opencode.jsonc"); !exists(plain) && exists(jsonc) {
		return jsonc
	}
	return plain
}

// detectAgyBin finds agy where its installer puts it when it is not on PATH: $SEELEGENT_AGY, %LOCALAPPDATA%\agy, or
// the private AppData of a Store app (agy installed from inside Claude Desktop).
func detectAgyBin(e Env) (found bool, how string) {
	candidates := []string{e.getenv("SEELEGENT_AGY")}
	if e.goos() == "windows" {
		candidates = append(candidates, filepath.Join(e.localAppData(), "agy", "bin", "agy.exe"))
		if m, _ := filepath.Glob(filepath.Join(e.localAppData(), "Packages", "*", "LocalCache", "Local", "agy", "bin",
			"agy.exe")); len(m) > 0 {
			candidates = append(candidates, m...)
		}
	}
	for _, c := range candidates {
		if c != "" && exists(c) {
			return true, "agy at " + c
		}
	}
	return false, ""
}

// detectCLI finds a host by its command on PATH.
func detectCLI(name string) func(Env) (bool, string) {
	return func(e Env) (bool, string) {
		if p := e.lookPath(name); p != "" {
			return true, name + " CLI at " + p
		}
		return false, "no " + name + " CLI on PATH"
	}
}

// detectFolder finds a host by its config folder.
func detectFolder(dir func(Env) string) func(Env) (bool, string) {
	return func(e Env) (bool, string) {
		d := dir(e)
		if isDir(d) {
			return true, "config folder " + d
		}
		return false, "no " + d
	}
}

// detectAny is the first check that finds the host; otherwise the reasons of all checks.
func detectAny(checks ...func(Env) (bool, string)) func(Env) (bool, string) {
	return func(e Env) (bool, string) {
		var why []string
		for _, c := range checks {
			ok, reason := c(e)
			if ok {
				return true, reason
			}
			if reason != "" {
				why = append(why, reason)
			}
		}
		out := ""
		for i, w := range why {
			if i > 0 {
				out += ", "
			}
			out += w
		}
		return false, out
	}
}
