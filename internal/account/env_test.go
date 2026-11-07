package account

import (
	"errors"
	"maps"
	"runtime"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/envx"
)

// fakeRules is a backend that strips FAKE_* and points FAKE_HOME at the profile.
type fakeRules struct {
	keyErr error
}

func (fakeRules) Filter(name, value string, _ Profile) (newValue string, keep bool) {
	if name == "PATH" {
		return value + "-filtered", true
	}
	return value, !strings.HasPrefix(strings.ToUpper(name), "FAKE_")
}

func (fakeRules) ProfileEnv(p Profile) map[string]string {
	if !p.Isolated() {
		return map[string]string{"FAKE_MODE": "system"}
	}
	return map[string]string{"FAKE_HOME": p.Home, "FAKE_MODE": "isolated"}
}

func (r fakeRules) KeyEnv(p Provider, key, model string) (map[string]string, error) {
	if r.keyErr != nil {
		return nil, r.keyErr
	}
	return map[string]string{"FAKE_KEY": key, "FAKE_URL": p.BaseURL, "FAKE_MODEL": model}, nil
}

func envMapOf(vars []string) map[string]string {
	out := map[string]string{}
	for _, kv := range vars {
		n, v, _ := strings.Cut(kv, "=")
		out[n] = v
	}
	return out
}

func TestEnvIsolated(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindIsolated}, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := []string{
		"PATH=/bin", "FAKE_TOKEN=user-session", "SEELEGENT_WORKER=0", "SEELEGENT_GIT_GUARD=on",
		"SEELEGENT_PROVIDER_KEY=leak", "NOVALUE", "=C:=C:\\", "KEEP=1", "KEEP=2",
	}
	env, err := f.m.Env(ctx, a, fakeRules{}, EnvOptions{
		Base: base, AllowGit: true, Extra: map[string]string{"RUSTUP_TOOLCHAIN": "stable", "KEEP": "extra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := envMapOf(env.Vars)
	want := map[string]string{
		"PATH": "/bin-filtered", "KEEP": "extra", "RUSTUP_TOOLCHAIN": "stable", "FAKE_HOME": env.Profile.Home,
		"FAKE_MODE": "isolated", EnvWorker: "1", "NO_COLOR": "1", EnvGitGuard: "off",
	}
	if !maps.Equal(got, want) {
		t.Errorf("env = %v\nwant  %v", got, want)
	}
	if len(env.Secrets) != 0 {
		t.Errorf("secrets = %v", env.Secrets)
	}
	if v, ok := envx.Lookup(env.Vars, "PATH"); !ok || v != "/bin-filtered" {
		t.Errorf("envx.Lookup(PATH) = %q, %v", v, ok)
	}
	if _, ok := envx.Lookup(env.Vars, "MISSING"); ok {
		t.Error("envx.Lookup(MISSING) found something")
	}
	if strings.Contains(strings.Join(env.Vars, "\n"), "leak") {
		t.Error("SEELEGENT_PROVIDER_KEY from the daemon leaked into the worker")
	}

	// Without AllowGit the guard stays on (no variable).
	env, err = f.m.Env(ctx, a, fakeRules{}, EnvOptions{Base: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envx.Lookup(env.Vars, EnvGitGuard); ok {
		t.Error("git guard turned off without AllowGit")
	}
	// nil Base inherits the process environment.
	t.Setenv("SEELEGENT_TEST_INHERIT", "yes")
	env, err = f.m.Env(ctx, a, fakeRules{}, EnvOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envx.Lookup(env.Vars, "SEELEGENT_TEST_INHERIT"); v != "yes" {
		t.Error("process environment not inherited")
	}
}

func TestEnvCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("names are case-sensitive here")
	}
	f := newFixture(t)
	a, _, err := f.m.Add(ctxT(t), New{Backend: "claude", Kind: KindIsolated}, nil)
	if err != nil {
		t.Fatal(err)
	}
	env, err := f.m.Env(ctxT(t), a, fakeRules{}, EnvOptions{Base: []string{"Path=C:\\x", "no_color=0"}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, kv := range env.Vars {
		if strings.HasPrefix(strings.ToUpper(kv), "NO_COLOR=") {
			n++
			if kv != "no_color=1" {
				t.Errorf("NO_COLOR entry = %q", kv)
			}
		}
	}
	if n != 1 {
		t.Errorf("NO_COLOR appears %d times: %v", n, env.Vars)
	}
	if v, _ := envx.Lookup(env.Vars, "PATH"); v != `C:\x` {
		t.Errorf("envx.Lookup(PATH) = %q", v)
	}
}

func TestEnvAPIKey(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	p := f.provider(t, "ds", "deepseek")
	a, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindAPIKey, Provider: p.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.m.Env(ctx, a, fakeRules{}, EnvOptions{Base: []string{}}); !errors.Is(err, ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
	if _, err := f.m.SetKey(ctx, a.ID, "sk-env-12345678"); err != nil {
		t.Fatal(err)
	}
	env, err := f.m.Env(ctx, a, fakeRules{}, EnvOptions{Base: []string{"FAKE_KEY=user"}, Model: "deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	got := envMapOf(env.Vars)
	if got["FAKE_KEY"] != "sk-env-12345678" || got["FAKE_URL"] != p.BaseURL || got["FAKE_MODEL"] != "deepseek-chat" {
		t.Errorf("key env = %v", got)
	}
	if len(env.Secrets) != 1 || env.Secrets[0] != "sk-env-12345678" {
		t.Errorf("secrets = %v", env.Secrets)
	}
	boom := errors.New("boom")
	if _, err := f.m.Env(ctx, a, fakeRules{keyErr: boom}, EnvOptions{Base: []string{}}); !errors.Is(err, boom) {
		t.Errorf("KeyEnv error: %v", err)
	}
	if _, err := f.m.Env(ctx, Account{ID: "../x", Kind: KindIsolated}, fakeRules{}, EnvOptions{}); err == nil {
		t.Error("Env with a bad id succeeded")
	}
}

func TestEnvSystem(t *testing.T) {
	f := newFixture(t)
	a, _, err := f.m.Add(ctxT(t), New{Backend: "agy", Kind: KindSystem}, nil)
	if err != nil {
		t.Fatal(err)
	}
	env, err := f.m.Env(ctxT(t), a, fakeRules{}, EnvOptions{Base: []string{"HOME=/real"}})
	if err != nil {
		t.Fatal(err)
	}
	got := envMapOf(env.Vars)
	if got["FAKE_MODE"] != "system" || got["HOME"] != "/real" || env.Profile.Home != f.realHome {
		t.Errorf("system env = %v, profile %+v", got, env.Profile)
	}
}
