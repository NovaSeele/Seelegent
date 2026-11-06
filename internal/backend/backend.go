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

// Quota is an account's quota as a CLI reports it.
type Quota struct {
	Plan     string       `json:"plan,omitempty"`
	Email    string       `json:"email,omitempty"`
	Entitled bool         `json:"entitled"`
	Groups   []QuotaGroup `json:"groups"`
}

// QuotaGroup is the quota of a group of models that share limits.
type QuotaGroup struct {
	Name    string        `json:"name"`
	Buckets []QuotaBucket `json:"buckets"`
}

// QuotaBucket is one limit window.
type QuotaBucket struct {
	Label         string    `json:"label"`
	WindowMinutes int       `json:"windowMinutes,omitempty"`
	UsedFraction  float64   `json:"usedFraction"`
	ResetAt       time.Time `json:"resetAt,omitzero"`
}

// QuotaReader reads an account's quota. Implementations arrive with the pool (milestone 3); the reader must return
// errors (never panic) on unexpected output, see bug B2.
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
	// QuotaReader returns the quota reader, or nil when the backend has none yet.
	QuotaReader() QuotaReader
}

// Names returns the backend ids.
func Names() []string { return []string{config.BackendAgy, config.BackendClaude, config.BackendCodex} }

// New returns the backend with the given name, running the CLI at bin ("" when it is not installed: Command and
// SignIn then fail with ErrNotInstalled).
func New(name, bin string) (Backend, error) {
	switch name {
	case config.BackendAgy:
		return &Agy{bin: bin}, nil
	case config.BackendClaude:
		return &Claude{bin: bin}, nil
	case config.BackendCodex:
		return &Codex{bin: bin}, nil
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
