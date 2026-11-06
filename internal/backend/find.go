package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// Environment variables that point Seelegent at a specific CLI binary.
const (
	EnvAgyBin    = "SEELEGENT_AGY"
	EnvClaudeBin = "SEELEGENT_CLAUDE"
	EnvCodexBin  = "SEELEGENT_CODEX"
)

// Find locates the CLI of the backend: the override variable first (SEELEGENT_AGY, SEELEGENT_CLAUDE,
// SEELEGENT_CODEX), then the places its installer uses, then PATH. env is the environment to read (usually
// os.Environ()) and realHome the user's home directory. It returns ErrNotInstalled when nothing is found.
func Find(name string, env []string, realHome string) (string, error) {
	var override string
	switch name {
	case config.BackendAgy:
		override = EnvAgyBin
	case config.BackendClaude:
		override = EnvClaudeBin
	case config.BackendCodex:
		override = EnvCodexBin
	default:
		return "", fmt.Errorf("%w %q", ErrUnknownBackend, name)
	}
	if v, _ := account.Lookup(env, override); v != "" {
		if isFile(v) {
			return v, nil
		}
		return "", fmt.Errorf("%s=%s: %w", override, v, ErrNotInstalled)
	}
	for _, c := range candidates(name, env, realHome, runtime.GOOS) {
		if isFile(c) {
			return c, nil
		}
	}
	if p := account.ResolveCommand(name, env); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("%s: %w", name, ErrNotInstalled)
}

// candidates lists the install locations of a CLI, in the order the old version checked them.
func candidates(name string, env []string, realHome, goos string) []string {
	localAppData, _ := account.Lookup(env, "LOCALAPPDATA")
	appData, _ := account.Lookup(env, "APPDATA")
	var out []string
	switch name {
	case config.BackendAgy:
		if goos == "windows" && localAppData != "" {
			out = append(out, filepath.Join(localAppData, "agy", "bin", "agy.exe"))
			// The MSIX build keeps its files under the package's private LocalCache.
			matches, _ := filepath.Glob(filepath.Join(localAppData, "Packages", "*", "LocalCache", "Local", "agy",
				"bin", "agy.exe"))
			slices.Sort(matches)
			out = append(out, matches...)
		}
	case config.BackendClaude:
		if realHome != "" {
			if goos == "windows" {
				out = append(out, filepath.Join(realHome, ".local", "bin", "claude.exe"))
			}
			out = append(out, filepath.Join(realHome, ".local", "bin", "claude"))
		}
		if goos == "windows" {
			if localAppData != "" {
				out = append(out, filepath.Join(localAppData, "Programs", "claude", "claude.exe"))
			}
			if appData != "" {
				out = append(out, filepath.Join(appData, "npm", "claude.cmd"))
			}
		}
	}
	return out
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}
