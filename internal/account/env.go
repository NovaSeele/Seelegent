package account

import (
	"context"
	"os"
	"runtime"
	"slices"
	"strings"
)

// Environment variables Seelegent itself sets for every worker turn.
const (
	// EnvWorker marks a process as a Seelegent worker (the MCP server refuses to delegate from inside one).
	EnvWorker = "SEELEGENT_WORKER"
	// EnvGitGuard set to "off" disables the git guard for a turn that is allowed to use git freely.
	EnvGitGuard = "SEELEGENT_GIT_GUARD"
	// EnvProviderKey carries the API key to codex, whose config.toml names this variable as env_key.
	EnvProviderKey = "SEELEGENT_PROVIDER_KEY"
)

// EnvRules is what a backend contributes to the environment of a turn. internal/backend implements it per CLI; it
// is declared here, where it is used.
type EnvRules interface {
	// Filter decides what becomes of an inherited variable: keep false drops it (credentials and settings of the
	// user's own session that would override the account's); otherwise value is what the CLI gets (for example PATH
	// without entries that point into the user's own CLI configuration).
	Filter(name, value string, prof Profile) (newValue string, keep bool)
	// ProfileEnv returns the variables that point the CLI at the account's profile.
	ProfileEnv(prof Profile) map[string]string
	// KeyEnv returns the variables that make the CLI use the provider with key; model is the resolved model id of
	// the turn (some providers need it in the environment).
	KeyEnv(p Provider, key, model string) (map[string]string, error)
}

// EnvOptions are the per-turn inputs of Env.
type EnvOptions struct {
	// Base is the inherited environment ("NAME=value" entries); nil means os.Environ().
	Base []string
	// Model is the resolved model id of the turn.
	Model string
	// AllowGit turns the git guard off for this turn.
	AllowGit bool
	// Extra variables (for example RUSTUP_TOOLCHAIN from ToolchainEnv). They override inherited values but never the
	// profile, key or Seelegent variables.
	Extra map[string]string
}

// RunEnv is the environment of one turn.
type RunEnv struct {
	// Vars is the environment ("NAME=value" entries) to start the CLI with. It holds the API key of an API-key
	// account: never log it or write it to disk.
	Vars []string
	// Secrets are the values the turn's output must be masked for (the API key).
	Secrets []string
	// Profile is the account's profile.
	Profile Profile
}

// Env assembles the environment of a turn on account a: the inherited environment as the backend filters it
// (without Seelegent's own markers), then opts.Extra, then the profile variables, then for an
// API-key account the provider and key variables, and finally SEELEGENT_WORKER=1, NO_COLOR=1 and, with AllowGit,
// SEELEGENT_GIT_GUARD=off. It fails with ErrNoKey or ErrProviderUnavailable when an API-key account cannot run.
func (m *Manager) Env(ctx context.Context, a Account, rules EnvRules, opts EnvOptions) (RunEnv, error) {
	prof, err := m.profiles.For(a)
	if err != nil {
		return RunEnv{}, err
	}
	base := opts.Base
	if base == nil {
		base = os.Environ()
	}
	env := newEnvList(runtime.GOOS == "windows")
	for _, kv := range base {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if ownVariable(name) {
			continue
		}
		if value, keep := rules.Filter(name, value, prof); keep {
			env.set(name, value)
		}
	}
	env.setAll(opts.Extra)
	env.setAll(rules.ProfileEnv(prof))
	out := RunEnv{Profile: prof}
	if a.Kind == KindAPIKey {
		p, key, err := m.Credentials(ctx, a)
		if err != nil {
			return RunEnv{}, err
		}
		vars, err := rules.KeyEnv(p, key, opts.Model)
		if err != nil {
			return RunEnv{}, err
		}
		env.setAll(vars)
		out.Secrets = append(out.Secrets, key)
	}
	env.set(EnvWorker, "1")
	env.set("NO_COLOR", "1")
	if opts.AllowGit {
		env.set(EnvGitGuard, "off")
	}
	out.Vars = env.list()
	return out, nil
}

// ownVariable reports variables Seelegent sets itself and never passes through from the daemon's environment.
func ownVariable(name string) bool {
	switch strings.ToUpper(name) {
	case EnvWorker, EnvGitGuard, EnvProviderKey:
		return true
	}
	return false
}

// envList is an ordered environment with case-insensitive names on Windows.
type envList struct {
	fold  bool
	names []string
	vals  map[string]string // by key()
	orig  map[string]string // key() -> name as first written
}

func newEnvList(fold bool) *envList {
	return &envList{fold: fold, vals: map[string]string{}, orig: map[string]string{}}
}

func (e *envList) key(name string) string {
	if e.fold {
		return strings.ToUpper(name)
	}
	return name
}

func (e *envList) set(name, value string) {
	k := e.key(name)
	if _, ok := e.vals[k]; !ok {
		e.names = append(e.names, k)
		e.orig[k] = name
	}
	e.vals[k] = value
}

// setAll sets the variables in a stable (sorted) order so the result does not depend on map iteration.
func (e *envList) setAll(vars map[string]string) {
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		e.set(n, vars[n])
	}
}

func (e *envList) list() []string {
	out := make([]string, 0, len(e.names))
	for _, k := range e.names {
		out = append(out, e.orig[k]+"="+e.vals[k])
	}
	return out
}

// Lookup returns the value of name in env ("NAME=value" entries), the last one winning, with case-insensitive names
// on Windows.
func Lookup(env []string, name string) (string, bool) {
	fold := runtime.GOOS == "windows"
	var (
		val   string
		found bool
	)
	for _, kv := range env {
		n, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if n == name || (fold && strings.EqualFold(n, name)) {
			val, found = v, true
		}
	}
	return val, found
}
