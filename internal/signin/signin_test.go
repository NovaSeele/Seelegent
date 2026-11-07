package signin

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

func TestMain(m *testing.M) { fakecli.Main(m) }

// procOpener runs the sign-in command as a plain child process instead of a window.
type procOpener struct {
	env   func([]string) []string
	mu    sync.Mutex
	specs []platform.TerminalSpec
}

type procTerminal struct {
	cmd  *exec.Cmd
	done chan struct{}
	code int
}

func (o *procOpener) Open(_ context.Context, spec platform.TerminalSpec) (Terminal, error) {
	o.mu.Lock()
	o.specs = append(o.specs, spec)
	o.mu.Unlock()
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...) //nolint:gosec // the test binary
	cmd.Dir = spec.Dir
	cmd.Env = o.env(spec.Env)
	if spec.StdoutPath != "" {
		f, err := os.Create(spec.StdoutPath)
		if err != nil {
			return nil, err
		}
		cmd.Stdout = f
		defer func() { _ = f.Close() }()
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	t := &procTerminal{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		t.code = cmd.ProcessState.ExitCode()
		close(t.done)
	}()
	return t, nil
}

func (t *procTerminal) Wait(ctx context.Context) (int, error) {
	select {
	case <-t.done:
		return t.code, nil
	case <-ctx.Done():
		return -1, ctx.Err()
	}
}

func (t *procTerminal) Identity() (int, uint64, bool) { return t.cmd.Process.Pid, 0, true }

type fixture struct {
	accounts *account.Manager
	queue    *fakecli.Queue
	opener   *procOpener
	s        *Service
	mu       sync.Mutex
	states   []string
	done     []bool
}

func newFixture(t *testing.T, linger time.Duration) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{queue: fakecli.NewQueue(t)}
	f.accounts = account.NewManager(db, account.NewProfiles(filepath.Join(dir, "profiles"), filepath.Join(dir, "real")),
		time.Now)
	f.opener = &procOpener{env: func(env []string) []string { return f.queue.Env(env) }}
	codex, err := backend.New(config.BackendCodex, fakecli.Exe(t))
	if err != nil {
		t.Fatal(err)
	}
	f.s = New(Options{
		Accounts: f.accounts,
		Backend: func(name string) (backend.Backend, bool) {
			return codex, name == config.BackendCodex
		},
		Opener: f.opener,
		Publish: func(l api.LoginSession) {
			f.mu.Lock()
			f.states = append(f.states, l.State)
			f.mu.Unlock()
		},
		OnDone: func(_ string, ok bool) {
			f.mu.Lock()
			f.done = append(f.done, ok)
			f.mu.Unlock()
		},
		Linger: linger,
	})
	t.Cleanup(f.s.Close)
	return f
}

func (f *fixture) add(t *testing.T, kind account.Kind, enabled bool) account.Account {
	t.Helper()
	a, _, err := f.accounts.Add(t.Context(), account.New{Backend: config.BackendCodex, Kind: kind, Label: "c",
		Enabled: enabled}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (f *fixture) waitState(t *testing.T, id, want string) api.LoginSession {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if l, ok := f.s.Get(id); ok && l.State == want {
			return l
		}
		if time.Now().After(deadline) {
			l, _ := f.s.Get(id)
			t.Fatalf("sign-in state = %+v, want %s", l, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSignInSucceedsAndEnables(t *testing.T) {
	f := newFixture(t, time.Hour)
	a := f.add(t, account.KindIsolated, false)
	f.queue.Push(t, "Successfully logged in\n#!exit 0\n")
	l, err := f.s.Start(t.Context(), a.ID)
	if err != nil || l.State != api.LoginStarting {
		t.Fatalf("start = %+v, %v", l, err)
	}
	f.waitState(t, a.ID, api.LoginDone)
	got, err := f.accounts.Get(t.Context(), a.ID)
	if err != nil || !got.Enabled {
		t.Errorf("account after sign-in = %+v, %v", got, err)
	}
	f.s.Close()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.done) != 1 || !f.done[0] {
		t.Errorf("OnDone = %v", f.done)
	}
	if len(f.states) < 3 || f.states[1] != api.LoginWaitingCode {
		t.Errorf("states = %v", f.states)
	}
	spec := f.opener.specs[0]
	if spec.Argv[len(spec.Argv)-1] != "login" || spec.ScriptDir == "" || spec.Dir == "" {
		t.Errorf("terminal spec = %+v", spec)
	}
	if _, err := f.s.SubmitCode(a.ID, "123"); !errors.Is(err, ErrBadState) {
		t.Errorf("submit code: %v", err)
	}
}

func TestSignInFailsCancelsAndLogout(t *testing.T) {
	f := newFixture(t, 50*time.Millisecond)
	a := f.add(t, account.KindIsolated, true)
	f.queue.Push(t, "#!exit 3\n")
	if _, err := f.s.Start(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	l := f.waitState(t, a.ID, api.LoginFailed)
	if l.Message == "" {
		t.Error("a failed sign-in has no message")
	}
	// The finished session goes away after Linger.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := f.s.Get(a.ID); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the finished session stayed")
		}
		time.Sleep(10 * time.Millisecond)
	}

	f.queue.Push(t, "#!hang\n")
	if _, err := f.s.Start(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	f.waitState(t, a.ID, api.LoginWaitingCode)
	if !f.s.Active(a.ID) {
		t.Error("a running sign-in is not active")
	}
	prof, err := f.accounts.Profile(a)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(prof.Home, "auth.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Logout(t.Context(), a.ID); err != nil {
		t.Fatal(err)
	}
	if f.s.Active(a.ID) {
		t.Error("logout left the sign-in running")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Errorf("logout kept the stored sign-in: %v", err)
	}
	if _, err := os.Stat(prof.Home); err != nil {
		t.Errorf("logout did not recreate the profile: %v", err)
	}
}

func TestSignInRefusals(t *testing.T) {
	f := newFixture(t, time.Second)
	key, _, err := f.accounts.Add(t.Context(), account.New{Backend: config.BackendAgy, Kind: account.KindSystem,
		Label: "sys", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Start(t.Context(), key.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("system sign-in: %v", err)
	}
	if _, err := f.s.Logout(t.Context(), key.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("system sign-out: %v", err)
	}
	if _, err := f.s.Start(t.Context(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if _, err := f.s.SubmitCode("nope", "1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("code without sign-in: %v", err)
	}
}
