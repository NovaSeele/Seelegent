package task

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// fakeToolchains pins "1.80.0" for a folder holding Cargo.toml and rust-toolchain.toml (the decision itself is
// tested in internal/account) and records the folders it was asked about.
type fakeToolchains struct {
	mu   sync.Mutex
	dirs []string
}

func (f *fakeToolchains) Env(_ context.Context, cwd string, _ []string) map[string]string {
	f.mu.Lock()
	f.dirs = append(f.dirs, cwd)
	f.mu.Unlock()
	for _, name := range []string{"Cargo.toml", "rust-toolchain.toml"} {
		if _, err := os.Stat(filepath.Join(cwd, name)); err != nil {
			return map[string]string{}
		}
	}
	return map[string]string{"RUSTUP_TOOLCHAIN": "1.80.0"}
}

func envValues(env []string, name string) []string {
	var out []string
	for _, kv := range env {
		if len(kv) > len(name) && kv[:len(name)+1] == name+"=" {
			out = append(out, kv[len(name)+1:])
		}
	}
	return out
}

// TestToolchainPin: the worker and the gate of a Rust repository get the same RUSTUP_TOOLCHAIN from the daemon's
// one Toolchains; a repository without a toolchain file gets none (old bug B2: the pin was never wired).
func TestToolchainPin(t *testing.T) {
	t.Setenv("RUSTUP_TOOLCHAIN", "")
	if err := os.Unsetenv("RUSTUP_TOOLCHAIN"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakecli.EnvRecord, "RUSTUP_TOOLCHAIN")
	h := newHarness(t)
	h.agy("a", 1)
	tc := &fakeToolchains{}
	h.s.o.Toolchains = tc

	rust := filepath.Join(h.dir, "rust")
	plain := filepath.Join(h.dir, "plain")
	for _, f := range []string{"Cargo.toml", "rust-toolchain.toml"} {
		writeTestFile(t, filepath.Join(rust, f), "")
	}
	writeTestFile(t, filepath.Join(plain, "README.md"), "")

	for i, c := range []struct {
		cwd  string
		want []string
	}{{rust, []string{"1.80.0"}}, {plain, nil}} {
		h.queue.Push(t, okScript("conv-tc", "Done."))
		sum := h.delegate(api.DelegateRequest{Title: "toolchain", Cwd: c.cwd})
		h.waitStatus(sum.ID, StatusDone)
		rec := h.queue.Records(t)[i]
		if got, ok := rec.Env["RUSTUP_TOOLCHAIN"]; (len(c.want) == 0) == ok || (ok && got != c.want[0]) {
			t.Errorf("%s: worker RUSTUP_TOOLCHAIN = %q (set %v), want %v", c.cwd, got, ok, c.want)
		}
		gateEnv := h.s.gateEnv(t.Context(), &Task{ID: sum.ID}, c.cwd, nil)
		if got := envValues(gateEnv, "RUSTUP_TOOLCHAIN"); !slices.Equal(got, c.want) {
			t.Errorf("%s: gate RUSTUP_TOOLCHAIN = %q, want %q", c.cwd, got, c.want)
		}
	}
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if !slices.Contains(tc.dirs, rust) || !slices.Contains(tc.dirs, plain) {
		t.Errorf("toolchains asked about %q", tc.dirs)
	}

	// Without a Toolchains nothing is pinned.
	h.s.o.Toolchains = nil
	if got := envValues(h.s.gateEnv(t.Context(), &Task{ID: "t1"}, rust, nil), "RUSTUP_TOOLCHAIN"); got != nil {
		t.Errorf("no toolchains: gate RUSTUP_TOOLCHAIN = %q", got)
	}
}

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
