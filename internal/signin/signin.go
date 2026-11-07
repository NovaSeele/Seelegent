// Package signin signs worker accounts in and out (POST/GET /accounts/{id}/login, /logout). A sign-in runs the
// backend's sign-in command (backend.SignIn) in the account's isolated profile, in a terminal window the user
// works in; its progress is a LoginSession (api), published on every change for the account.login SSE event.
//
// Each sign-in has a time limit (the backend's Expires plus two minutes). Starting a new sign-in, signing out,
// removing the account and Close cancel a running one and stop its window. After a successful sign-in the account
// is enabled and OnDone runs (the composition root wakes the pool and asks for a quota read).
package signin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Errors of the service; the HTTP layer maps them to status codes.
var (
	// ErrNotFound: no such account, or no sign-in for it.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: the account cannot sign in here (API-key or system account).
	ErrInvalid = errors.New("invalid sign-in request")
	// ErrBadState: the request does not fit the sign-in's state (a code for a terminal sign-in, a sign-out while
	// the account runs a task).
	ErrBadState = errors.New("not possible now")
)

// extraTime is added to the backend's Expires: the user also has to notice the window.
const extraTime = 2 * time.Minute

// scriptName is the base name of the terminal's helper files in the profile directory.
const scriptName = "seelegent-signin"

// Terminal is an opened sign-in window.
type Terminal interface {
	// Wait returns the command's exit code once it ended.
	Wait(ctx context.Context) (int, error)
	// Identity returns the process that keeps the window's command running, so it can be stopped.
	Identity() (pid int, start uint64, ok bool)
}

// Opener opens terminal windows (platform.OpenTerminal; a fake in tests).
type Opener interface {
	Open(ctx context.Context, spec platform.TerminalSpec) (Terminal, error)
}

// PlatformOpener opens real windows.
type PlatformOpener struct{}

// Open implements Opener.
func (PlatformOpener) Open(ctx context.Context, spec platform.TerminalSpec) (Terminal, error) {
	return platform.OpenTerminal(ctx, spec)
}

// Accounts is the part of account.Manager the service uses.
type Accounts interface {
	Get(ctx context.Context, id string) (account.Account, error)
	Update(ctx context.Context, id string, p account.Patch) (account.Account, error)
	EnsureProfile(a account.Account, links []string) (account.Profile, []string, error)
	Env(ctx context.Context, a account.Account, rules account.EnvRules, opts account.EnvOptions) (account.RunEnv, error)
	Profiles() account.Profiles
}

// Options configures a Service.
type Options struct {
	Accounts Accounts
	// Backend returns the backend with the given name.
	Backend func(name string) (backend.Backend, bool)
	// Links returns the entries of the real home linked into profiles (settings.linkIntoProfiles).
	Links func() []string
	// Hold keeps the account from starting turns while it is signed out (pool.Pool.Hold). ok is false when it
	// runs a turn now; release ends the hold. Nil: no hold (tests).
	Hold func(id string) (release func(), ok bool)
	// Opener opens the windows (PlatformOpener when nil).
	Opener Opener
	// BaseEnv is the environment the CLI inherits (os.Environ when nil).
	BaseEnv func() []string
	// Publish receives every change of a session (SSE account.login).
	Publish func(api.LoginSession)
	// OnDone runs after a sign-in ended (ok) and after a sign-out (ok false).
	OnDone func(accountID string, ok bool)
	// Linger is how long a finished session stays readable (default 15 s).
	Linger time.Duration
	Logger *slog.Logger
}

// Service runs sign-ins.
type Service struct {
	o      Options
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*session
	// locks keeps a sign-in's profile preparation and a sign-out's profile replacement of one account apart.
	locks  map[string]chan struct{}
	closed bool
}

type session struct {
	info   api.LoginSession
	cancel context.CancelFunc
	done   chan struct{}
	timer  *time.Timer
}

// New returns a Service. Close it on shutdown.
func New(o Options) *Service {
	if o.Opener == nil {
		o.Opener = PlatformOpener{}
	}
	if o.BaseEnv == nil {
		o.BaseEnv = os.Environ
	}
	if o.Links == nil {
		o.Links = func() []string { return nil }
	}
	if o.Hold == nil {
		o.Hold = func(string) (func(), bool) { return func() {}, true }
	}
	if o.Publish == nil {
		o.Publish = func(api.LoginSession) {}
	}
	if o.OnDone == nil {
		o.OnDone = func(string, bool) {}
	}
	if o.Linger <= 0 {
		o.Linger = 15 * time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{o: o, ctx: ctx, cancel: cancel, sessions: map[string]*session{}, locks: map[string]chan struct{}{}}
}

// lockAccount takes the profile lock of account id. It gives up when ctx ends, so cancelling a sign-in that waits
// for the lock frees it at once.
func (s *Service) lockAccount(ctx context.Context, id string) (unlock func(), err error) {
	s.mu.Lock()
	ch, ok := s.locks[id]
	if !ok {
		ch = make(chan struct{}, 1)
		s.locks[id] = ch
	}
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Start begins the sign-in of account id, cancelling one that runs, and returns its first state. The window
// opens in the background; progress arrives through Publish and Get.
func (s *Service) Start(ctx context.Context, id string) (api.LoginSession, error) {
	a, err := s.o.Accounts.Get(ctx, id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return api.LoginSession{}, fmt.Errorf("%w: account %s", ErrNotFound, id)
		}
		return api.LoginSession{}, err
	}
	switch a.Kind {
	case account.KindAPIKey:
		return api.LoginSession{}, fmt.Errorf("%w: API-key accounts have no sign-in; set or remove the key instead",
			ErrInvalid)
	case account.KindSystem:
		return api.LoginSession{}, fmt.Errorf("%w: the system account signs in through the CLI itself", ErrInvalid)
	}
	b, ok := s.o.Backend(a.Backend)
	if !ok {
		return api.LoginSession{}, fmt.Errorf("%w: unknown backend %s", ErrInvalid, a.Backend)
	}
	runCtx, cancel := context.WithCancel(s.ctx)
	sess := &session{info: api.LoginSession{AccountID: id, State: api.LoginStarting}, cancel: cancel,
		done: make(chan struct{})}
	// Replacing the running sign-in and registering this one is one critical section: of two concurrent Starts,
	// the second always finds the first and stops it, so no window is left without a session that can close it.
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return api.LoginSession{}, errors.New("the daemon is shutting down")
	}
	old := s.sessions[id]
	s.sessions[id] = sess
	info := sess.info
	s.wg.Add(1)
	s.mu.Unlock()
	// The old window closes before this one opens (outside the lock: its goroutine takes the lock to report).
	if old != nil {
		old.cancel()
		<-old.done
	}
	s.o.Publish(info)
	go func() {
		defer s.wg.Done()
		defer close(sess.done)
		ok := s.run(runCtx, sess, a, b)
		cancel()
		s.o.OnDone(id, ok)
	}()
	return info, nil
}

// run drives one sign-in and reports whether it worked.
func (s *Service) run(ctx context.Context, sess *session, a account.Account, b backend.Backend) bool {
	fail := func(msg string) bool {
		s.update(sess, api.LoginFailed, msg)
		return false
	}
	prof, err := s.prepare(ctx, a, b)
	if err != nil {
		return fail(err.Error())
	}
	si, err := b.SignIn(prof)
	if err != nil {
		return fail(err.Error())
	}
	env, err := s.o.Accounts.Env(ctx, a, b, account.EnvOptions{Base: s.o.BaseEnv()})
	if err != nil {
		return fail(err.Error())
	}
	if ctx.Err() != nil { // replaced or cancelled before its window opened
		return fail("sign-in cancelled")
	}
	term, err := s.o.Opener.Open(ctx, platform.TerminalSpec{Title: si.Title, Banner: si.Banner, Argv: si.Argv,
		Dir: prof.Home, Env: env.Vars, StdoutPath: si.StdoutPath, ScriptDir: prof.Dir, Name: scriptName})
	if err != nil {
		var nt *platform.NoTerminalError
		if errors.As(err, &nt) {
			return fail("no terminal window could be opened (" + nt.Reason + "); run this in a terminal: " +
				nt.Command)
		}
		return fail("could not open the sign-in window: " + err.Error())
	}
	s.update(sess, api.LoginWaitingCode, "Finish signing in in the terminal window that opened.")
	limit := si.Expires + extraTime
	if si.Expires <= 0 {
		limit = 10 * time.Minute
	}
	wctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	code, err := term.Wait(wctx)
	if err != nil {
		s.stop(term)
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			return fail("sign-in cancelled")
		case wctx.Err() != nil:
			return fail("the sign-in took too long; start it again")
		}
		return fail("the sign-in window was closed before it finished")
	}
	if si.Succeeded != nil && !si.Succeeded(code) {
		return fail(fmt.Sprintf("the sign-in did not finish (exit code %d); start it again", code))
	}
	if !a.Enabled {
		enabled := true
		if _, err := s.o.Accounts.Update(context.WithoutCancel(ctx), a.ID, account.Patch{Enabled: &enabled}); err != nil {
			s.o.Logger.Warn("enable account after sign-in", "account", a.ID, "err", err)
		}
	}
	s.update(sess, api.LoginDone, "Signed in.")
	return true
}

// prepare makes the account's profile ready for the sign-in, under the account's profile lock (a sign-out
// replacing the profile at the same time would remove what was just prepared). The lock is not held while the
// window is open.
func (s *Service) prepare(ctx context.Context, a account.Account, b backend.Backend) (account.Profile, error) {
	unlock, err := s.lockAccount(ctx, a.ID)
	if err != nil {
		return account.Profile{}, errors.New("sign-in cancelled")
	}
	defer unlock()
	prof, _, err := s.o.Accounts.EnsureProfile(a, s.o.Links())
	if err != nil {
		return account.Profile{}, fmt.Errorf("could not prepare the account's profile: %w", err)
	}
	if err := b.Prepare(prof, nil); err != nil {
		return account.Profile{}, err
	}
	return prof, nil
}

// stop closes the window of a sign-in that is given up.
func (s *Service) stop(term Terminal) {
	pid, _, ok := term.Identity()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := platform.KillTree(ctx, pid); err != nil {
		s.o.Logger.Debug("stop sign-in window", "pid", pid, "err", err)
	}
}

// update changes a session's state and publishes it. A finished session is forgotten after Linger.
func (s *Service) update(sess *session, state, msg string) {
	s.mu.Lock()
	sess.info.State, sess.info.Message = state, msg
	info := sess.info
	if state == api.LoginDone || state == api.LoginFailed {
		sess.timer = time.AfterFunc(s.o.Linger, func() {
			s.mu.Lock()
			if s.sessions[info.AccountID] == sess {
				delete(s.sessions, info.AccountID)
			}
			s.mu.Unlock()
		})
	}
	s.mu.Unlock()
	s.o.Publish(info)
}

// Get returns the sign-in of account id, if there is one.
func (s *Service) Get(id string) (api.LoginSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return api.LoginSession{}, false
	}
	return sess.info, true
}

// Active reports whether account id is signing in right now.
func (s *Service) Active(id string) bool {
	info, ok := s.Get(id)
	return ok && info.State != api.LoginDone && info.State != api.LoginFailed
}

// SubmitCode passes a code to a sign-in. Every backend signs in through its own terminal window, where the user
// types any code, so this is ErrBadState for a running sign-in and ErrNotFound without one.
func (s *Service) SubmitCode(id, code string) (api.LoginSession, error) {
	info, ok := s.Get(id)
	if !ok {
		return api.LoginSession{}, fmt.Errorf("%w: no sign-in runs for account %s; start one first", ErrNotFound, id)
	}
	_ = code
	return info, fmt.Errorf("%w: this sign-in takes its code in the terminal window", ErrBadState)
}

// Cancel stops the sign-in of account id, if one runs, and waits until it ended.
func (s *Service) Cancel(id string) {
	s.mu.Lock()
	sess, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	sess.cancel()
	<-sess.done
}

// Logout signs account id out: it cancels a running sign-in and replaces the profile with a fresh one, which
// drops the CLI's stored sign-in. The links into the real home are made again; the real home is never touched.
func (s *Service) Logout(ctx context.Context, id string) (account.Account, error) {
	a, err := s.o.Accounts.Get(ctx, id)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return account.Account{}, fmt.Errorf("%w: account %s", ErrNotFound, id)
		}
		return account.Account{}, err
	}
	if a.Kind != account.KindIsolated {
		return account.Account{}, fmt.Errorf("%w: only accounts with their own profile can be signed out here",
			ErrInvalid)
	}
	// The hold is taken only when no turn runs and keeps new turns off the account until the fresh profile is in
	// place (checking "busy" and then removing the profile let a turn start in between).
	release, ok := s.o.Hold(id)
	if !ok {
		return account.Account{}, fmt.Errorf("%w: account %s is running a task", ErrBadState, id)
	}
	defer release()
	// Cancel first and never while holding the profile lock: a cancelled sign-in may be waiting for that lock.
	s.Cancel(id)
	if err := s.replaceProfile(ctx, a); err != nil {
		return account.Account{}, fmt.Errorf("sign out %s: %w", id, err)
	}
	s.o.OnDone(id, false)
	return a, nil
}

// replaceProfile removes the account's profile and makes a fresh one, under the account's profile lock, so a
// sign-in started meanwhile prepares its profile only after the replacement.
func (s *Service) replaceProfile(ctx context.Context, a account.Account) error {
	unlock, err := s.lockAccount(ctx, a.ID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.o.Accounts.Profiles().Remove(a.ID); err != nil {
		return err
	}
	_, _, err = s.o.Accounts.EnsureProfile(a, s.o.Links())
	return err
}

// Close cancels every sign-in and waits for them.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true // no Start adds to wg once Wait may run
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	s.mu.Lock()
	for _, sess := range s.sessions {
		if sess.timer != nil {
			sess.timer.Stop()
		}
	}
	s.mu.Unlock()
}
