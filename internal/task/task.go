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

// Task kinds: a task the lead delegated, and the hidden read-only runs of the engine: the review of a green gate
// (verifier.go), a consult (consult.go), a meeting attendee (internal/meeting) and a contest judge
// (internal/contest). Hidden runs are never listed; they are read through what started them.
const (
	KindNormal  = "normal"
	KindVerify  = "verify"
	KindConsult = "consult"
	KindMeeting = "meeting"
	KindJudge   = "judge"
)

// Hidden reports whether tasks of kind are hidden runs (not listed, no gate, no snapshots).
func Hidden(kind string) bool {
	return kind == KindVerify || kind == KindConsult || kind == KindMeeting || kind == KindJudge
}

// hiddenKinds lists the hidden kinds (for SQL filters).
func hiddenKinds() []string { return []string{KindVerify, KindConsult, KindMeeting, KindJudge} }

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
	// Prefer lists the accounts the lead asked for, tried first.
	Prefer []string `json:"prefer,omitempty"`

	After      []string `json:"after,omitempty"`
	AfterMode  string   `json:"afterMode,omitempty"`
	DepsReason string   `json:"depsReason,omitempty"`
	// DepsBlock names the prerequisite that blocks the task (blocked_deps), for the attention flag's params.
	DepsBlock *api.AttentionParams `json:"depsBlock,omitempty"`
	// DepsContext is the context from the prerequisites, fixed when the task starts (brief.DepsContext).
	DepsContext string `json:"depsContext,omitempty"`
	// Baseline lists the files (absolute paths) that already had uncommitted changes when the task's first turn
	// started; BaselineTaken records that the capture ran, even when it found nothing.
	Baseline      []string `json:"baseline,omitempty"`
	BaselineTaken bool     `json:"baselineTaken,omitempty"`
	// GitStart is where the repository stood when the baseline was taken (the task's first turn); nil for a folder
	// outside git, when git could not be read, and for tasks from before it was recorded. Followups keep it.
	GitStart *GitStart `json:"gitStart,omitempty"`

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
	// Waiting is why a waiting_quota task waits: a pool reason (no_account, resting or busy), or api.WaitingMemory
	// while it waits for free memory (resources.go, with MemoryWait); WaitDetail says more.
	Waiting    string `json:"waiting,omitempty"`
	WaitDetail string `json:"waitDetail,omitempty"`
	// MemoryWait is set by a memory wait: since when, and the last reading against its threshold. A pool wait in
	// between keeps it (so the wait's start survives); a launch or the end of the run clears it. Attention reads it
	// only while Waiting is api.WaitingMemory.
	MemoryWait           *MemoryWait    `json:"memoryWait,omitempty"`
	NeedsSince           time.Time      `json:"needsSince,omitzero"`
	Attention            *api.Attention `json:"attention,omitempty"`
	AttentionDismissedAt time.Time      `json:"attentionDismissedAt,omitzero"`
	Review               *api.Review    `json:"review,omitempty"`

	// Proof is what the task's gate checks (gates.go); nil for a task without gate, lane or verifier.
	Proof *Proof `json:"proof,omitempty"`
	// GatePhase is set while the gate (or the verifier) of a finished turn runs: the task stays running.
	GatePhase *GatePhase `json:"gatePhase,omitempty"`
	// Verdict is the verdict of a verify task (KindVerify) once it ended.
	Verdict *api.VerifierResult `json:"verdict,omitempty"`

	ParentID  string `json:"parentId,omitempty"`
	ContestID string `json:"contestId,omitempty"`
	TicketID  string `json:"ticketId,omitempty"`
	MeetingID string `json:"meetingId,omitempty"`
	// AutomationID is the automation whose run started the task.
	AutomationID string `json:"automationId,omitempty"`
	// Variant is the label (V1..) of a contest variant; ProjectCwd is the user's folder that the variant's worktree
	// stands for (memory, roles and review history are the project's).
	Variant    string `json:"variant,omitempty"`
	ProjectCwd string `json:"projectCwd,omitempty"`
	// Consults are the second opinions asked on this task (consult.go).
	Consults []api.Consult `json:"consults,omitempty"`

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
	TaskIDsPassed bool `json:"taskIdsPassed,omitempty"`
	// ProofChanged is true when the followup that opened the turn changed the task's lane or gate commands: the
	// continuation repeats them.
	ProofChanged bool `json:"proofChanged,omitempty"`
	// AllowGit turns the git guard off for this turn. It belongs to one lead command: the delegate or followup that
	// opened the turn sets it (a followup without allow_git turns it off), and the automatic fix turns of that
	// command (gate auto-fix, verifier reject) inherit it from the turn they fix; retries and handovers run the
	// same turn. A takeover's handback turn has it off.
	AllowGit  bool      `json:"allowGit,omitempty"`
	StartedAt time.Time `json:"startedAt"`
	EndedAt   time.Time `json:"endedAt,omitzero"`

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

	// Gate is the gate run after the turn (milestone 13); Verifier the review of its green gate.
	Gate     *api.GateResult     `json:"gate,omitempty"`
	Verifier *api.VerifierResult `json:"verifier,omitempty"`

	// Attempts are stored in the attempts table.
	Attempts []*Attempt `json:"-"`
}

// GitStart is where a task's repository stood when the task started.
type GitStart struct {
	// Head is the HEAD commit, "" when the repository had no commit yet.
	Head string `json:"head,omitempty"`
	// Branch is the current branch, "" when HEAD was detached.
	Branch string    `json:"branch,omitempty"`
	At     time.Time `json:"at"`
}

// TurnSnap says which snapshots a turn has: the start one (taken before its first attempt) and the end one (taken
// when it ended, once the checks ran).
type TurnSnap struct {
	Root string `json:"root"`
	Head string `json:"head,omitempty"`
	// Truncated: the start snapshot hit its limits; files past them are not covered.
	Truncated bool `json:"truncated,omitempty"`
	// EndTruncated: the end snapshot hit its limits. The start snapshot's files and the edited ones are always in
	// it; other dirty files past the limits are not covered by the change lists and the hygiene check.
	EndTruncated bool `json:"endTruncated,omitempty"`
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

// projectDir is the folder whose project memory, roles and review history the task uses: the user's folder for a
// contest variant (which runs in a worktree), else the task's folder.
func (t *Task) projectDir() string {
	if t.ProjectCwd != "" {
		return t.ProjectCwd
	}
	return t.Cwd
}

// FilesChanged returns the files changed in any turn, without duplicates, in first-seen order.
func (t *Task) FilesChanged() []string {
	var out []string
	seen := map[string]bool{}
	for _, tu := range t.Turns {
		for _, f := range tu.Files {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// lastEdits lists the files the edit tools of a turn's last finished attempt changed, as display paths. The
// verifier is judged by them: its Files also hold what its test runs wrote (caches, build output), which is not
// an edit of the code it reviews.
func lastEdits(t *Task, tu *Turn) []string {
	for i := len(tu.Attempts) - 1; i >= 0; i-- {
		if o := tu.Attempts[i].Outcome; o != nil {
			return displayPaths(t.Cwd, o.FilesEdited)
		}
	}
	return nil
}
