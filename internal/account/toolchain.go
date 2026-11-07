package account

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Limits of the toolchain probe (same as the old version).
const (
	toolchainTTL        = 10 * time.Minute
	toolchainRunTimeout = 10 * time.Second
	cargoMaxDepth       = 3
	cargoMaxDirs        = 3000
	cargoMaxQueries     = 3
)

// RunFunc runs bin with args in dir with env and returns its exit code and standard output. Tests replace it.
type RunFunc func(ctx context.Context, bin string, args []string, dir string, env []string) (code int, stdout string)

// Toolchains pins RUSTUP_TOOLCHAIN for worker and gate commands in Rust repositories.
//
// A plain (non-rustup) cargo first on PATH runs rustc through rustup's proxy with the working directory of each
// dependency, so a dependency's own rust-toolchain.toml switches that crate to another compiler mid-build (error
// E0514). rustup's own cargo proxy avoids this by pinning RUSTUP_TOOLCHAIN for its children; Env does the same, so
// the repository's toolchain file (or the default) still decides and dependency files stop mattering.
//
// Results are cached per directory for ten minutes. A Toolchains is safe for concurrent use; the daemon owns one.
type Toolchains struct {
	realHome string
	run      RunFunc
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]toolchainEntry
}

type toolchainEntry struct {
	at   time.Time
	vars map[string]string
}

// NewToolchains returns a probe that runs rustup with realHome as its home. run nil runs the real command.
func NewToolchains(realHome string, run RunFunc, now func() time.Time) *Toolchains {
	if run == nil {
		run = runCommand
	}
	if now == nil {
		now = time.Now
	}
	return &Toolchains{realHome: realHome, run: run, now: now, cache: map[string]toolchainEntry{}}
}

// Env returns the extra variables for commands run in cwd with environment env: {"RUSTUP_TOOLCHAIN": name} when
// cwd holds a Cargo project whose folders all pick the same toolchain, otherwise an empty map. It never fails.
func (t *Toolchains) Env(ctx context.Context, cwd string, env []string) map[string]string {
	if cwd == "" {
		return map[string]string{}
	}
	if v, ok := envx.Lookup(env, "RUSTUP_TOOLCHAIN"); ok && v != "" {
		return map[string]string{}
	}
	key, err := fsutil.Key(cwd)
	if err != nil {
		return map[string]string{}
	}
	t.mu.Lock()
	hit, ok := t.cache[key]
	t.mu.Unlock()
	if ok && t.now().Sub(hit.at) < toolchainTTL {
		return cloneVars(hit.vars)
	}
	vars := t.compute(ctx, cwd, env)
	if ctx.Err() == nil {
		t.mu.Lock()
		t.cache[key] = toolchainEntry{at: t.now(), vars: vars}
		t.mu.Unlock()
	}
	return cloneVars(vars)
}

func (t *Toolchains) compute(ctx context.Context, cwd string, env []string) map[string]string {
	none := map[string]string{}
	dirs := cargoDirs(cwd, cargoMaxDepth)
	if len(dirs) == 0 {
		return none
	}
	rustup := t.findRustup(env)
	if rustup == "" {
		return none
	}
	// rustup must see the real home (worker profiles redirect USERPROFILE) and must never install anything.
	runEnv := newEnvList(runtime.GOOS == "windows")
	for _, kv := range env {
		if n, v, ok := strings.Cut(kv, "="); ok && n != "" && !strings.EqualFold(n, "RUSTUP_TOOLCHAIN") {
			runEnv.set(n, v)
		}
	}
	runEnv.set("USERPROFILE", t.realHome)
	runEnv.set("HOME", t.realHome)
	runEnv.set("RUSTUP_AUTO_INSTALL", "0")
	vars := runEnv.list()
	names := map[string]bool{}
	var name string
	for i, dir := range dirs {
		if i == cargoMaxQueries {
			break
		}
		code, out := t.run(ctx, rustup, []string{"show", "active-toolchain"}, dir, vars)
		fields := strings.Fields(out)
		if code != 0 || len(fields) == 0 {
			return none
		}
		name = fields[0]
		names[name] = true
	}
	// Folders that pick different toolchains (their own toolchain files): pinning one would override the others.
	if len(names) != 1 {
		return none
	}
	return map[string]string{"RUSTUP_TOOLCHAIN": name}
}

// findRustup returns rustup on PATH, or in $CARGO_HOME/bin (default ~/.cargo/bin), or "".
func (t *Toolchains) findRustup(env []string) string {
	if p := envx.ResolveCommand("rustup", env); p != "" {
		return p
	}
	cargoHome, _ := envx.Lookup(env, "CARGO_HOME")
	if cargoHome == "" {
		if t.realHome == "" {
			return ""
		}
		cargoHome = filepath.Join(t.realHome, ".cargo")
	}
	name := "rustup"
	if runtime.GOOS == "windows" {
		name = "rustup.exe"
	}
	candidate := filepath.Join(cargoHome, "bin", name)
	if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
		return candidate
	}
	return ""
}

// cargoDirs returns the folders holding a Cargo.toml within maxDepth levels of root, shallowest first. It skips
// target, node_modules and dot folders, and gives up quietly on very large trees.
func cargoDirs(root string, maxDepth int) []string {
	var found []string
	queue := []string{root}
	visited := 0
	for depth := 0; depth <= maxDepth && len(queue) > 0; depth++ {
		var next []string
		for _, dir := range queue {
			visited++
			if visited > cargoMaxDirs {
				return found
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.Type().IsRegular() && e.Name() == "Cargo.toml" {
					found = append(found, dir)
					break
				}
			}
			if depth == maxDepth {
				continue
			}
			for _, e := range entries {
				n := e.Name()
				if e.IsDir() && n != "target" && n != "node_modules" && !strings.HasPrefix(n, ".") {
					next = append(next, filepath.Join(dir, n))
				}
			}
		}
		queue = next
	}
	return found
}

// runCommand is the real RunFunc: no console window, a time limit, and at most 1 MiB of output.
func runCommand(ctx context.Context, bin string, args []string, dir string, env []string) (code int, stdout string) {
	ctx, cancel := context.WithTimeout(ctx, toolchainRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	platform.HideWindow(cmd)
	var out limitedBuffer
	out.limit = 1 << 20
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() > 0 {
			return exitErr.ExitCode(), out.String()
		}
		return 1, out.String()
	}
	return 0, out.String()
}

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

func cloneVars(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
