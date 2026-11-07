package task

import (
	"slices"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// Status is where a task stands (see the transition table in state.go).
type Status = api.TaskStatus

// Statuses.
const (
	StatusWaitingDeps  = api.StatusWaitingDeps
	StatusBlockedDeps  = api.StatusBlockedDeps
	StatusQueued       = api.StatusQueued
	StatusRunning      = api.StatusRunning
	StatusWaitingQuota = api.StatusWaitingQuota
	StatusDone         = api.StatusDone
	StatusFailed       = api.StatusFailed
	StatusCancelled    = api.StatusCancelled
)

// Statuses returns every status.
func Statuses() []Status {
	return []Status{StatusWaitingDeps, StatusBlockedDeps, StatusQueued, StatusRunning, StatusWaitingQuota,
		StatusDone, StatusFailed, StatusCancelled}
}

// Active reports whether a task with status s has work going on or about to start: queued, running or
// waiting_quota. Followups are refused while a task is active.
func Active(s Status) bool {
	return s == StatusQueued || s == StatusRunning || s == StatusWaitingQuota
}

// Pending reports whether a task with status s is not finished yet: Active or waiting_deps. Wait waits for
// pending tasks.
func Pending(s Status) bool { return Active(s) || s == StatusWaitingDeps }

// Finished reports whether a task with status s has ended its last turn: done, failed or cancelled.
func Finished(s Status) bool { return s == StatusDone || s == StatusFailed || s == StatusCancelled }

// ValidStatus reports whether s is a known status.
func ValidStatus(s Status) bool { return slices.Contains(Statuses(), s) }

// Modes.
const (
	ModeEdit = "edit"
	ModePlan = "plan"
)

// Dependency modes: a dependent starts when every prerequisite is accepted by the lead, or merely done.
const (
	AfterAccepted = "accepted"
	AfterDone     = "done"
)

// Who sends a turn.
const (
	ByLead     = "lead"
	ByUser     = "user"
	ByGate     = "gate"
	ByVerifier = "verifier"
)

// Turn statuses.
const (
	TurnRunning   = "running"
	TurnOK        = "ok"
	TurnFailed    = "failed"
	TurnCancelled = "cancelled"
)

// Attempt statuses.
const (
	AttemptRunning        = "running"
	AttemptOK             = "ok"
	AttemptQuota          = "quota"
	AttemptAuth           = "auth"
	AttemptTransient      = "transient"
	AttemptBackgroundWait = "background_wait"
	AttemptError          = "error"
	AttemptCancelled      = "cancelled"
)

// KindNormal is the kind of a task the lead delegated.
const KindNormal = "normal"

// Task is the stored record of a task. Turns are stored in their own rows and loaded with it.
type Task struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      Status `json:"status"`
	Kind        string `json:"kind"`
	Mode        string `json:"mode"`
	Cwd         string `json:"cwd"`
	ProjectKey  string `json:"projectKey"`
	ProjectName string `json:"projectName"`
	RepoRoot    string `json:"repoRoot,omitempty"`
	// Prompt is the first lead message.
	Prompt string `json:"prompt"`

	Backend  string `json:"backend"`
	Provider string `json:"provider,omitempty"`
	// Model is the model of the latest turn; Tier and Effort what the lead asked for.
	Model   string          `json:"model"`
	Tier    string          `json:"tier,omitempty"`
	Effort  string          `json:"effort,omitempty"`
	Member  string          `json:"member,omitempty"`
	Role    string          `json:"role,omitempty"`
	Profile *api.ProfileRef `json:"profile,omitempty"`
	Session *api.Session    `json:"session,omitempty"`
	Spec    *api.SpecRef    `json:"spec,omitempty"`
	// SpecOutside is true when the spec directory lies outside the task's folder.
	SpecOutside bool `json:"specOutside,omitempty"`
	AllowGit    bool `json:"allowGit,omitempty"`
	// Prefer lists the accounts the lead asked for, tried first.
	Prefer []string `json:"prefer,omitempty"`

	After      []string `json:"after,omitempty"`
	AfterMode  string   `json:"afterMode,omitempty"`
	DepsReason string   `json:"depsReason,omitempty"`
	// DepsContext is the context from the prerequisites, fixed when the task starts (brief.DepsContext).
	DepsContext string `json:"depsContext,omitempty"`
	// Baseline lists the files (absolute paths) that already had uncommitted changes when the task's first turn
	// started; BaselineTaken records that the capture ran, even when it found nothing.
	Baseline      []string `json:"baseline,omitempty"`
	BaselineTaken bool     `json:"baselineTaken,omitempty"`

	// AccountID owns the conversation ConversationID: a later turn on the same account resumes it.
	AccountID      string `json:"accountId,omitempty"`
	ConversationID string `json:"conversationId,omitempty"`

	// Manual is set while the user has taken the task over in a console.
	Manual  bool     `json:"manual,omitempty"`
	Console *Console `json:"console,omitempty"`
	// CancelRequested survives a daemon restart, so recovery finishes the cancel.
	CancelRequested bool `json:"cancelRequested,omitempty"`

	Error        string    `json:"error,omitempty"`
	WaitingUntil time.Time `json:"waitingUntil,omitzero"`
	// Waiting is why a waiting_quota task waits (pool reason no_account, resting or busy); WaitDetail says more.
	Waiting              string         `json:"waiting,omitempty"`
	WaitDetail           string         `json:"waitDetail,omitempty"`
	NeedsSince           time.Time      `json:"needsSince,omitzero"`
	Attention            *api.Attention `json:"attention,omitempty"`
	AttentionDismissedAt time.Time      `json:"attentionDismissedAt,omitzero"`
	Review               *api.Review    `json:"review,omitempty"`

	ParentID  string `json:"parentId,omitempty"`
	ContestID string `json:"contestId,omitempty"`
	TicketID  string `json:"ticketId,omitempty"`
	MeetingID string `json:"meetingId,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Turns are stored in the turns table.
	Turns []*Turn `json:"-"`
	// LastEventSeq and LastEventAt are kept by the event log.
	LastEventSeq int64     `json:"-"`
	LastEventAt  time.Time `json:"-"`
}

// Console is the record of a takeover: the user runs the task's conversation in a terminal window.
type Console struct {
	AccountID string    `json:"accountId"`
	Since     time.Time `json:"since"`
	// From is the task's status before the takeover; a console that never starts puts the task back to it.
	From Status `json:"from,omitempty"`
	// OpenedAt is when the console was asked to open (after the worker stopped); its start limit counts from it.
	OpenedAt time.Time `json:"openedAt,omitzero"`
	// ExitFile is where the terminal writes the exit code when the console ends (polled after a daemon restart).
	ExitFile string `json:"exitFile,omitempty"`
	// Process is the console's process once known; a restarted daemon checks that it still runs.
	Process *ConsoleProcess `json:"process,omitempty"`
}

// Turn is one message to the worker and its answer.
type Turn struct {
	N              int      `json:"n"`
	By             string   `json:"by"`
	Message        string   `json:"message"`
	Status         string   `json:"status"`
	Model          string   `json:"model,omitempty"`
	RequestedModel string   `json:"requestedModel,omitempty"`
	Tier           string   `json:"tier,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	TaskIDs        []string `json:"taskIds,omitempty"`
	// TaskIDsPassed is true when the lead named spec tasks for this turn.
	TaskIDsPassed bool      `json:"taskIdsPassed,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	EndedAt       time.Time `json:"endedAt,omitzero"`

	Response        string       `json:"response,omitempty"`
	Files           []string     `json:"files,omitempty"`
	Commands        []string     `json:"commands,omitempty"`
	BlockedCommands []string     `json:"blockedCommands,omitempty"`
	DeniedActions   []string     `json:"deniedActions,omitempty"`
	Usage           *event.Usage `json:"usage,omitempty"`
	// Handback marks the turn that summarizes a takeover; it holds the summary once the turn ended.
	Handback     bool   `json:"handback,omitempty"`
	HandbackText string `json:"handbackText,omitempty"`
	// BackgroundContinues counts the continuations after background waits (one per turn).
	BackgroundContinues int `json:"backgroundContinues,omitempty"`

	// Snap records the turn's worktree snapshots (snapshot.Store, see safety.go); nil when none was taken.
	Snap *TurnSnap `json:"snap,omitempty"`
	// Changed lists the files (relative to the repository root) whose content the turn changed; Preexisting the
	// ones of them that already had uncommitted changes when it started.
	Changed     []string `json:"changed,omitempty"`
	Preexisting []string `json:"preexisting,omitempty"`
	// Edited lists the files the worker's edit tools changed, relative to the repository root.
	Edited []string `json:"edited,omitempty"`
	// WorkAtRisk are the files whose uncommitted work the turn may have lost.
	WorkAtRisk []api.RiskFile `json:"workAtRisk,omitempty"`
	// Hygiene and Scope are the findings of the post-turn checks.
	Hygiene []api.Finding `json:"hygiene,omitempty"`
	Scope   []api.Finding `json:"scope,omitempty"`

	// Attempts are stored in the attempts table.
	Attempts []*Attempt `json:"-"`
}

// TurnSnap says which snapshots a turn has: the start one (taken before its first attempt) and the end one (taken
// when it ended, once the checks ran).
type TurnSnap struct {
	Root string `json:"root"`
	Head string `json:"head,omitempty"`
	// Truncated: the start snapshot hit its limits; files past them are not covered.
	Truncated bool `json:"truncated,omitempty"`
	// Checked is set once the end snapshot was taken and the post-turn checks ran.
	Checked bool `json:"checked,omitempty"`
}

// Attempt is one run of the worker CLI for a turn on one account.
type Attempt struct {
	N            int    `json:"n"`
	AccountID    string `json:"accountId"`
	AccountLabel string `json:"accountLabel,omitempty"`
	Backend      string `json:"backend"`
	Model        string `json:"model,omitempty"`
	// ConversationID is the conversation the attempt ran in (resumed when Continuing).
	ConversationID string    `json:"conversationId,omitempty"`
	Continuing     bool      `json:"continuing,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	EndedAt        time.Time `json:"endedAt,omitzero"`
	Status         string    `json:"status"`
	// Kind is "console" for a takeover.
	Kind  string       `json:"kind,omitempty"`
	Error string       `json:"error,omitempty"`
	Usage *event.Usage `json:"usage,omitempty"`

	// LaunchDir is the launch directory while the attempt runs (and after, for its files).
	LaunchDir string `json:"launchDir,omitempty"`
	// StreamOffset and StderrOffset are the committed offsets: every event of the bytes before them is in the log.
	StreamOffset int64 `json:"streamOffset,omitempty"`
	StderrOffset int64 `json:"stderrOffset,omitempty"`
	// Outcome is the folded result once the attempt ended.
	Outcome *backend.Outcome `json:"outcome,omitempty"`

	// Prompt is the full brief; stored in its own column and loaded only on request.
	Prompt string `json:"-"`
}

// LastTurn returns the latest turn, or nil.
func (t *Task) LastTurn() *Turn {
	if len(t.Turns) == 0 {
		return nil
	}
	return t.Turns[len(t.Turns)-1]
}

// TurnN returns turn n, or nil.
func (t *Task) TurnN(n int) *Turn {
	for _, tu := range t.Turns {
		if tu.N == n {
			return tu
		}
	}
	return nil
}

// LastAttempt returns the latest attempt of the turn, or nil.
func (tu *Turn) LastAttempt() *Attempt {
	if len(tu.Attempts) == 0 {
		return nil
	}
	return tu.Attempts[len(tu.Attempts)-1]
}

// TokensUsed adds up the usage of every attempt.
func (t *Task) TokensUsed() int64 {
	var n int64
	for _, tu := range t.Turns {
		for _, a := range tu.Attempts {
			if a.Usage != nil {
				total := a.Usage.TotalTokens
				if total <= 0 {
					total = a.Usage.InputTokens + a.Usage.OutputTokens
				}
				n += total
			}
		}
	}
	return n
}

// FilesChanged returns the files edited in any turn, without duplicates, in first-seen order.
func (t *Task) FilesChanged() []string {
	var out []string
	for _, tu := range t.Turns {
		for _, f := range tu.Files {
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}
