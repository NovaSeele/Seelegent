package account

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/envx"
)

// fakeRustup puts an executable named rustup in a new folder and returns the PATH entry and its path.
func fakeRustup(t *testing.T) (dir, bin string) {
	t.Helper()
	dir = t.TempDir()
	name := "rustup"
	if runtime.GOOS == "windows" {
		name = "rustup.exe"
	}
	bin = filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}

type runRecorder struct {
	mu    sync.Mutex
	calls []string
	reply func(dir string) (int, string)
	env   []string
}

func (r *runRecorder) run(_ context.Context, bin string, args []string, dir string, env []string) (code int, stdout string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, filepath.Base(bin)+" "+strings.Join(args, " ")+" @"+filepath.Base(dir))
	r.env = env
	return r.reply(dir)
}

func cargoRepo(t *testing.T, crates ...string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")
	for _, c := range crates {
		writeFile(t, filepath.Join(root, c, "Cargo.toml"), "[package]\n")
	}
	// Never searched: build output, dependencies, dot folders.
	writeFile(t, filepath.Join(root, "target", "x", "Cargo.toml"), "")
	writeFile(t, filepath.Join(root, "node_modules", "y", "Cargo.toml"), "")
	writeFile(t, filepath.Join(root, ".git", "Cargo.toml"), "")
	return root
}

func TestToolchainsPinsSingleToolchain(t *testing.T) {
	pathDir, _ := fakeRustup(t)
	root := cargoRepo(t, filepath.Join("crates", "a"))
	rec := &runRecorder{reply: func(string) (int, string) { return 0, "1.80.0-x86_64 (overridden by rust-toolchain.toml)\n" }}
	clock := &fakeClock{t: time.Unix(0, 0)}
	tc := NewToolchains("/real/home", rec.run, clock.now)
	env := []string{"PATH=" + pathDir, "RUSTUP_TOOLCHAIN=", "HOME=/profile"}

	got := tc.Env(ctxT(t), root, env)
	if got["RUSTUP_TOOLCHAIN"] != "1.80.0-x86_64" || len(got) != 1 {
		t.Fatalf("Env = %v", got)
	}
	if len(rec.calls) != 2 || !strings.HasSuffix(rec.calls[0], "show active-toolchain @"+filepath.Base(root)) ||
		!strings.HasSuffix(rec.calls[1], "@a") {
		t.Errorf("calls = %q", rec.calls)
	}
	if v, _ := envx.Lookup(rec.env, "HOME"); v != "/real/home" {
		t.Errorf("rustup ran with HOME=%q", v)
	}
	if v, _ := envx.Lookup(rec.env, "RUSTUP_AUTO_INSTALL"); v != "0" {
		t.Errorf("RUSTUP_AUTO_INSTALL = %q", v)
	}
	if _, ok := envx.Lookup(rec.env, "RUSTUP_TOOLCHAIN"); ok {
		t.Error("rustup ran with RUSTUP_TOOLCHAIN set")
	}

	// Cached: no new calls, and the caller's map is a copy.
	got["RUSTUP_TOOLCHAIN"] = "changed"
	again := tc.Env(ctxT(t), root, env)
	if again["RUSTUP_TOOLCHAIN"] != "1.80.0-x86_64" || len(rec.calls) != 2 {
		t.Errorf("cache: %v after %d calls", again, len(rec.calls))
	}
	// Expired after ten minutes.
	clock.t = clock.t.Add(toolchainTTL)
	tc.Env(ctxT(t), root, env)
	if len(rec.calls) != 4 {
		t.Errorf("cache did not expire: %d calls", len(rec.calls))
	}
}

func TestToolchainsNoPin(t *testing.T) {
	pathDir, _ := fakeRustup(t)
	env := []string{"PATH=" + pathDir}
	ctx := ctxT(t)

	// Different toolchains in different folders.
	root := cargoRepo(t, "a")
	rec := &runRecorder{reply: func(dir string) (int, string) {
		if filepath.Base(dir) == "a" {
			return 0, "nightly\n"
		}
		return 0, "stable\n"
	}}
	if got := NewToolchains("", rec.run, nil).Env(ctx, root, env); len(got) != 0 {
		t.Errorf("mixed toolchains pinned %v", got)
	}
	// rustup fails or prints nothing.
	for _, reply := range []func(string) (int, string){
		func(string) (int, string) { return 1, "stable" },
		func(string) (int, string) { return 0, "  \n" },
	} {
		rec := &runRecorder{reply: reply}
		if got := NewToolchains("", rec.run, nil).Env(ctx, root, env); len(got) != 0 {
			t.Errorf("failed probe pinned %v", got)
		}
	}
	never := &runRecorder{reply: func(string) (int, string) { t.Error("rustup should not run"); return 0, "x" }}
	tc := NewToolchains("", never.run, nil)
	// Already pinned by the caller, no cwd, no Cargo.toml, no rustup anywhere.
	if got := tc.Env(ctx, root, append(env, "RUSTUP_TOOLCHAIN=beta")); len(got) != 0 {
		t.Errorf("pinned twice: %v", got)
	}
	if got := tc.Env(ctx, "", env); len(got) != 0 {
		t.Errorf("empty cwd: %v", got)
	}
	if got := tc.Env(ctx, t.TempDir(), env); len(got) != 0 {
		t.Errorf("no cargo: %v", got)
	}
	if got := tc.Env(ctx, root, []string{"PATH=" + t.TempDir(), "CARGO_HOME=" + t.TempDir()}); len(got) != 0 {
		t.Errorf("no rustup: %v", got)
	}
}

func TestFindRustupInCargoHome(t *testing.T) {
	home := t.TempDir()
	name := "rustup"
	if runtime.GOOS == "windows" {
		name = "rustup.exe"
	}
	bin := filepath.Join(home, ".cargo", "bin", name)
	writeFile(t, bin, "")
	tc := NewToolchains(home, nil, nil)
	if got := tc.findRustup([]string{"PATH="}); got != bin {
		t.Errorf("findRustup = %q, want %q", got, bin)
	}
	if got := NewToolchains("", nil, nil).findRustup([]string{"PATH="}); got != "" {
		t.Errorf("findRustup without home = %q", got)
	}
}

func TestCargoOnPath(t *testing.T) {
	exe := func(dir, name string) {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sep := string(os.PathListSeparator)
	proxy, _ := fakeRustup(t)
	exe(proxy, "cargo")
	plain := t.TempDir()
	exe(plain, "cargo")
	noHome := "CARGO_HOME=" + t.TempDir()

	if st, _, _ := CargoOnPath([]string{"PATH=" + plain, noHome}, ""); st != CargoNoRustup {
		t.Errorf("no rustup: %v", st)
	}
	if st, _, cargo := CargoOnPath([]string{"PATH=" + proxy + sep + plain}, ""); st != CargoProxy ||
		filepath.Dir(cargo) != proxy {
		t.Errorf("proxy first: %v %q", st, cargo)
	}
	if st, rustup, cargo := CargoOnPath([]string{"PATH=" + plain + sep + proxy}, ""); st != CargoPlain ||
		filepath.Dir(cargo) != plain || filepath.Dir(rustup) != proxy {
		t.Errorf("plain first: %v %q %q", st, rustup, cargo)
	}
	onlyRustup, _ := fakeRustup(t)
	if st, _, _ := CargoOnPath([]string{"PATH=" + onlyRustup}, ""); st != CargoMissing {
		t.Errorf("no cargo: %v", st)
	}
}

func TestCargoDirsLimits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "b", "c", "Cargo.toml"), "")
	writeFile(t, filepath.Join(root, "a", "b", "c", "d", "Cargo.toml"), "")
	got := cargoDirs(root, cargoMaxDepth)
	if len(got) != 1 || got[0] != filepath.Join(root, "a", "b", "c") {
		t.Errorf("cargoDirs = %q", got)
	}
	if got := cargoDirs(filepath.Join(root, "missing"), 3); len(got) != 0 {
		t.Errorf("missing root: %q", got)
	}
}

func TestRunCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, out := runCommand(ctxT(t), exe, []string{"-test.run=^TestHelperEcho$"}, t.TempDir(),
		append(os.Environ(), "ACCOUNT_HELPER=echo"))
	if code != 0 || !strings.Contains(out, "helper-out") {
		t.Errorf("runCommand = %d, %q", code, out)
	}
	code, _ = runCommand(ctxT(t), exe, []string{"-test.run=^TestHelperEcho$"}, t.TempDir(),
		append(os.Environ(), "ACCOUNT_HELPER=fail"))
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if code, _ := runCommand(ctxT(t), filepath.Join(t.TempDir(), "missing"), nil, "", nil); code != 1 {
		t.Errorf("missing binary: code %d", code)
	}
	var b limitedBuffer
	b.limit = 4
	n, _ := b.Write([]byte("abcdef"))
	_, _ = b.Write([]byte("gh"))
	if n != 6 || b.String() != "abcd" {
		t.Errorf("limitedBuffer = %q (n=%d)", b.String(), n)
	}
}

// TestHelperEcho is run as a child process by TestRunCommand.
func TestHelperEcho(_ *testing.T) {
	switch os.Getenv("ACCOUNT_HELPER") {
	case "echo":
		_, _ = os.Stdout.WriteString("helper-out\n")
		os.Exit(0)
	case "fail":
		os.Exit(3)
	}
}
