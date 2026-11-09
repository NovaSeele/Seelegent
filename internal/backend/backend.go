// Package backend knows the worker CLIs: agy (Antigravity, Gemini models), claude (Claude Code) and codex (OpenAI
// Codex). For each one it builds the command line of a turn, the environment rules of its profile, the files its
// profile needs, its sign-in command, and a parser that turns its stream output into normalized events
// (internal/event), including the classification of failures.
//
// The package starts nothing itself: the task engine passes Command to internal/launch and feeds the launch's
// stream and stderr lines to the Parser.
package backend

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// Errors of the backend functions.
var (
	// ErrUnknownBackend: no backend with that name.
	ErrUnknownBackend = errors.New("unknown backend")
	// ErrNotInstalled: the CLI was not found.
	ErrNotInstalled = errors.New("CLI not installed")
	// ErrModel: the requested model or tier cannot be used (unmapped tier, not allowed, backend disabled).
	ErrModel = errors.New("model not available")
)

// Capabilities describe what a CLI supports.
type Capabilities struct {
	// ActiveQuota: the account's quota can be read before it runs out (sign-in accounts).
	ActiveQuota bool `json:"activeQuota"`
	// NativePreToolGuard: the CLI can run a hook before each tool call (used by the git guard).
	NativePreToolGuard bool `json:"nativePreToolGuard"`
	// StdinPrompt: the prompt is passed on standard input rather than on the command line.
	StdinPrompt bool `json:"stdinPrompt"`
	// ResumeFlag is how a conversation is resumed (informative).
	ResumeFlag string `json:"resumeFlag"`
	// PlanShell: a plan turn reads files with shell commands, which its read-only sandbox runs as long as they write
	// nothing (codex, which has no other tool to read files). Otherwise plan mode refuses shell commands and the
	// worker reads with its file tools (agy, claude). The worker brief says which (brief.PlanRule).
	PlanShell bool `json:"planShell"`
	// ReplaysErrors: the CLI keeps a run's error in the conversation and reports it again, word for word, when the
	// conversation is resumed (agy; the task engine then needs more than the same text to call an error stale).
	ReplaysErrors bool `json:"replaysErrors"`
}

// Turn is the input of one worker turn.
type Turn struct {
	// Prompt is the brief or follow-up text.
	Prompt string
	// Model is the resolved model id (see ModelPolicy.Resolve).
	Model string
	// Effort is the reasoning effort (agy only; empty for the CLI's default).
	Effort string
	// ConversationID resumes that conversation; empty starts a new one.
	ConversationID string
	// Plan runs the turn read-only (plan mode).
	Plan bool
	// Timeout is the turn's time limit (agy also gets it as --print-timeout).
	Timeout time.Duration
	// Profile is the account's profile.
	Profile account.Profile
	// AttemptDir is a directory owned by this attempt, where files the command needs (a long brief) are written.
	AttemptDir string
	// Sandbox is the codex sandbox mode for turns that are not plan turns (default workspace-write).
	Sandbox string
}

// Command is what to run for a turn.
type Command struct {
	// Argv is the program (absolute path) and its arguments.
	Argv []string
	// Stdin is written to the CLI's standard input when HasStdin is set.
	Stdin    string
	HasStdin bool
}

// Parser turns one turn's raw output into normalized events. It is not safe for concurrent use; one goroutine
// feeds it the stream lines in order.
type Parser interface {
	// Line parses one line of the stream file (standard output).
	Line(line []byte) []event.Event
	// Stderr looks at one line of standard error (only a few CLI states are recognized there).
	Stderr(line string) []event.Event
	// Finish ends the turn: it returns a synthesized report when the CLI wrote none, and an error event with its
	// class when the turn failed. stderrTail is the end of standard error (launch.Handle.StderrTail).
	Finish(exit launch.Exit, stderrTail string) []event.Event
	// MaskPatterns makes the parser also run redact over every text it emits, after the literal secrets: for a
	// turn whose secrets are not known for sure, redact masks keys by their shape (logx.Redactor.String).
	MaskPatterns(redact func(string) string)
}

// SignIn describes how an account signs in: a command run in a terminal window the user interacts with.
type SignIn struct {
	// Title and Banner are shown in the window.
	Title  string
	Banner []string
	// Argv is the command.
	Argv []string
	// StdoutPath, when set, captures the command's output (agy prints the result there).
	StdoutPath string
	// Expires is how long the user has to finish (informative; shown in the dashboard).
	Expires time.Duration
	// Succeeded tells from the command's exit code (and files it wrote) whether the sign-in worked.
	Succeeded func(exitCode int) bool
}

// Quota is an account's quota as a CLI reports it (the output of a QuotaReader; internal/quota stores it per
// account).
type Quota struct {
	// Plan is the subscription plan when the CLI reports one (codex: "Plus", "Pro", ...).
	Plan string `json:"plan,omitempty"`
	// Email is the signed-in identity. internal/quota keeps only a hash of it (to find two accounts signed in to
	// the same identity); it is never logged or stored.
	Email string `json:"email,omitempty"`
	// Entitled is false when the plan cannot use this CLI at all. Readers must set it to true explicitly.
	Entitled bool `json:"entitled"`
	// EntitlementError says why the account is not entitled.
	EntitlementError string `json:"entitlementError,omitempty"`
	// ExhaustedUntil, when set, says the account cannot run before then even if no window shows 100 % used (a rate
	// or spend limit was reached).
	ExhaustedUntil time.Time `json:"exhaustedUntil,omitzero"`
	// ExhaustedReason says which limit was reached.
	ExhaustedReason string `json:"exhaustedReason,omitempty"`
	// Models lists the models this account may run when the CLI reports a per-account catalog (codex); nil means
	// unknown.
	Models []string `json:"models,omitempty"`
	// Groups are the limits, by group of models that share them.
	Groups []QuotaGroup `json:"groups"`
}

// QuotaGroup is the quota of a group of models that share limits. A group named "<group> <family>" (for example
// "Claude Opus") is a sub-limit of <group> that only applies to models whose id contains <family>.
type QuotaGroup struct {
	Name    string        `json:"name"`
	Buckets []QuotaBucket `json:"buckets"`
}

// QuotaBucket is one limit window.
type QuotaBucket struct {
	// Window is a short key of the window length: "5h", "weekly", "30d", "<n>m", or "window" when unknown.
	Window string `json:"window"`
	// Label is the CLI's name for the window.
	Label         string    `json:"label"`
	WindowMinutes int       `json:"windowMinutes,omitempty"`
	UsedFraction  float64   `json:"usedFraction"`
	ResetAt       time.Time `json:"resetAt,omitzero"`
}

// QuotaReader reads an account's quota (internal/quota implements one per backend). env is the account's run
// environment and prof its profile. A reader never panics on unexpected output: it returns an error (see bug B2).
type QuotaReader interface {
	ReadQuota(ctx context.Context, prof account.Profile, env []string) (Quota, error)
}

// Backend is one worker CLI.
type Backend interface {
	account.EnvRules

	// Name is the backend id: agy, claude or codex.
	Name() string
	// Capabilities says what the CLI supports.
	Capabilities() Capabilities
	// Bin is the CLI's path ("" when not installed).
	Bin() string
	// Command builds the command of a turn. It may write files under t.AttemptDir.
	Command(t Turn) (Command, error)
	// Prepare writes the files the profile needs before a turn (empty MCP config, codex config.toml). provider is
	// the provider of an API-key account, nil otherwise.
	Prepare(prof account.Profile, provider *account.Provider) error
	// NewParser returns a parser for one turn; secrets are masked in every text it emits.
	NewParser(secrets []string) Parser
	// SignIn returns the sign-in command of the profile.
	SignIn(prof account.Profile) (SignIn, error)
	// QuotaGroup returns the quota group a model belongs to.
	QuotaGroup(model string) string
	// QuotaReader returns the quota reader given to New (WithQuotaReader), or nil.
	QuotaReader() QuotaReader
}

// Names returns the backend ids.
func Names() []string { return []string{config.BackendAgy, config.BackendClaude, config.BackendCodex} }

// Option configures a backend made by New.
type Option func(*options)

type options struct {
	quota QuotaReader
	guard *Guard
}

// Guard is how Prepare installs Seelegent's git guard (package guard) into a worker profile.
type Guard struct {
	// Exe is the seelegent executable the pre-tool hooks run (`<Exe> __guard <backend>`).
	Exe string
	// Enabled reports the gitGuard setting; it is read at every Prepare, so turning the setting off removes the
	// hooks before the next turn.
	Enabled func() bool
}

// WithGuard makes Prepare install the git guard hook into isolated agy and claude profiles (codex has no hook; the
// task engine gives its workers the git shim instead).
func WithGuard(g Guard) Option {
	return func(o *options) { o.guard = &g }
}

// NeedsShim reports whether a worker of backend b with profile p gets the git shim on PATH because no pre-tool hook
// can guard it: codex (no hook) and system accounts (their profile is the user's real home, which Seelegent never
// writes to).
func NeedsShim(b Backend, p account.Profile) bool {
	return !b.Capabilities().NativePreToolGuard || !p.Isolated()
}

// WithQuotaReader gives the backend its quota reader. The readers live in internal/quota (which imports this
// package), so the composition root passes them in.
func WithQuotaReader(r QuotaReader) Option {
	return func(o *options) { o.quota = r }
}

// New returns the backend with the given name, running the CLI at bin ("" when it is not installed: Command and
// SignIn then fail with ErrNotInstalled).
func New(name, bin string, opts ...Option) (Backend, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	switch name {
	case config.BackendAgy:
		return &Agy{bin: bin, quota: o.quota, guard: o.guard}, nil
	case config.BackendClaude:
		return &Claude{bin: bin, quota: o.quota, guard: o.guard}, nil
	case config.BackendCodex:
		return &Codex{bin: bin, quota: o.quota, goos: runtime.GOOS}, nil
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownBackend, name)
}

// needBin returns bin or ErrNotInstalled.
func needBin(name, bin string) (string, error) {
	if bin == "" {
		return "", fmt.Errorf("%s: %w", name, ErrNotInstalled)
	}
	return bin, nil
}
