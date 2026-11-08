package api

import "github.com/NovaSeele/Seelegent/internal/event"

// TaskStatus is where a task stands. ACTIVE = queued, running, waiting_quota; PENDING = ACTIVE + waiting_deps.
type TaskStatus string

// Task statuses.
const (
	StatusWaitingDeps  TaskStatus = "waiting_deps"
	StatusBlockedDeps  TaskStatus = "blocked_deps"
	StatusQueued       TaskStatus = "queued"
	StatusRunning      TaskStatus = "running"
	StatusWaitingQuota TaskStatus = "waiting_quota"
	StatusDone         TaskStatus = "done"
	StatusFailed       TaskStatus = "failed"
	StatusCancelled    TaskStatus = "cancelled"
)

// Verdict is the lead's verdict on a task. Fields keep plain strings (the constants below are untyped); the TS
// generator emits the union from the Verdict* constants.
type Verdict string

// Review verdicts.
const (
	VerdictAccepted = "accepted"
	VerdictRejected = "rejected"
)

// TaskSummary is a task as lists and the SSE stream show it.
type TaskSummary struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      TaskStatus `json:"status"`
	Kind        string     `json:"kind"`
	Mode        string     `json:"mode" ts:"'edit' | 'plan'"`
	Cwd         string     `json:"cwd"`
	ProjectKey  string     `json:"projectKey"`
	ProjectName string     `json:"projectName"`

	Backend      string      `json:"backend"`
	Provider     string      `json:"provider,omitempty"`
	Model        string      `json:"model"`
	Effort       string      `json:"effort,omitempty"`
	Tier         string      `json:"tier,omitempty" ts:"Tier"`
	AccountID    string      `json:"accountId,omitempty"`
	AccountLabel string      `json:"accountLabel,omitempty"`
	Member       string      `json:"member,omitempty"`
	Role         string      `json:"role,omitempty"`
	Profile      *ProfileRef `json:"profile"`
	Session      *Session    `json:"session"`

	TurnCount    int   `json:"turnCount"`
	TokensUsed   int64 `json:"tokensUsed"`
	FilesChanged int   `json:"filesChanged"`

	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	LastEventAt string `json:"lastEventAt,omitempty"`
	// WaitingUntil is when an account is expected back (waiting_quota).
	WaitingUntil string `json:"waitingUntil,omitempty"`
	// Waiting says why a waiting_quota task waits: WaitingBusy (every account that could run it has no free
	// slot), WaitingResting (they rest; WaitingUntil is when one is back), WaitingNoAccount (none can run it), or
	// WaitingMemory while free memory or free commit is below minFreeMemoryMB / minFreeCommitMB (that wait never
	// raises an attention flag: the lead cannot act on it). Absent for other statuses, and from older daemons,
	// which reported only the memory wait.
	Waiting string `json:"waiting,omitempty" ts:"'memory' | 'no_account' | 'resting' | 'busy'"`
	// WaitDetail is the daemon's English explanation of Waiting, for example "acc6: 2 of 2 running" for busy.
	WaitDetail string `json:"waitDetail,omitempty"`
	// NeedsSince is since when the task waits for the lead or the user.
	NeedsSince string `json:"needsSince,omitempty"`

	Attention  *Attention  `json:"attention"`
	Review     *Review     `json:"review"`
	Gate       *Gate       `json:"gate"`
	Spec       *SpecRef    `json:"spec"`
	ActiveStep *ActiveStep `json:"activeStep"`
	// ConsultState counts the task's consults (absent while it has none); it changes whenever a consult starts,
	// ends or has its objection answered, so a reader of the full record (TaskDetail.Consults) knows to read again.
	ConsultState *ConsultState `json:"consultState,omitempty"`

	After      []string `json:"after"`
	AfterMode  string   `json:"afterMode" ts:"'accepted' | 'done'"`
	DepsReason string   `json:"depsReason,omitempty"`

	Manual    bool   `json:"manual"`
	ParentID  string `json:"parentId,omitempty"`
	ContestID string `json:"contestId,omitempty"`
	TicketID  string `json:"ticketId,omitempty"`
	MeetingID string `json:"meetingId,omitempty"`
	// AutomationID is the automation whose run started the task.
	AutomationID string `json:"automationId,omitempty"`
	// Variant is the label (V1..) of a contest variant.
	Variant string `json:"variant,omitempty"`

	// LastEventSeq is the highest event seq of the task.
	LastEventSeq int64 `json:"lastEventSeq"`

	// Warnings are set only by a review: accepting over a senior member's objection names it and its first finding.
	Warnings []string `json:"warnings,omitempty"`
	// Objections are the same warnings as values, for a text in the reader's language (F14).
	Objections []SeniorObjection `json:"objections,omitempty"`
}

// SeniorObjection is a senior member's standing objection to a task that was accepted anyway: the member and its
// first finding or required change (at most 300 characters).
type SeniorObjection struct {
	Member string `json:"member"`
	Text   string `json:"text,omitempty"`
}

// ProfileRef names the role profile of a task.
type ProfileRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Session is the lead session that created a task.
type Session struct {
	ID          string `json:"id"`
	HostID      string `json:"hostId,omitempty"`
	Title       string `json:"title,omitempty"`
	ParentTitle string `json:"parentTitle,omitempty"`
	// Lead is the lead host (Leads); the worker brief names it. The daemon refuses other values.
	Lead string `json:"lead,omitempty" ts:"'claude' | 'codex' | 'opencode' | 'gemini' | 'agy' | 'cli'"`
}

// Lead hosts a Session can name: the lead coding agents Seelegent registers with, and cli for the command line.
const (
	LeadClaude   = "claude"
	LeadCodex    = "codex"
	LeadOpenCode = "opencode"
	LeadGemini   = "gemini"
	LeadAgy      = "agy"
	LeadCLI      = "cli"
)

// Leads lists every lead host a Session can name.
func Leads() []string {
	return []string{LeadClaude, LeadCodex, LeadOpenCode, LeadGemini, LeadAgy, LeadCLI}
}

// Attention states.
const (
	AttentionBlocked        = "blocked"
	AttentionQuestion       = "question"
	AttentionStalled        = "stalled"
	AttentionBackgroundWait = "background_wait"
	AttentionInfo           = "info"
	// AttentionCleared is used only in attention events: the flag was lifted.
	AttentionCleared = "cleared"
)

// NeedsLead reports whether an attention state waits for the lead: blocked, question or stalled. These are the flags
// that wake a lead's wait and that status, ls --blocked and the report count; every place asks this one function so
// the set cannot drift again (M18 hunt H16).
func NeedsLead(state string) bool {
	switch state {
	case AttentionBlocked, AttentionQuestion, AttentionStalled:
		return true
	}
	return false
}

// Attention says that a task needs someone to look at it.
type Attention struct {
	State  string `json:"state" ts:"'blocked' | 'question' | 'stalled' | 'background_wait' | 'info'"`
	Reason string `json:"reason"`
	// Who is lead or user.
	Who string `json:"who" ts:"'lead' | 'user'"`
	// Detail says why in English, for the lead (MCP, CLI); for a question it is the worker's question. The
	// dashboard builds its own text from Reason and Params (F14).
	Detail string `json:"detail,omitempty"`
	// Params are the values behind Detail, for a text in the reader's language.
	Params *AttentionParams `json:"params,omitempty"`
	Since  string           `json:"since"`
}

// Kinds of AttentionParams: which case of the reason applies.
const (
	// stalled
	AttentionNotStarted = "not_started"
	AttentionNoOutput   = "no_output"
	AttentionToolQuiet  = "tool"
	// looping
	AttentionCallsFailed = "failed_calls"
	AttentionRepeated    = "repeated"
	// deps_blocked: the prerequisite is missing, rejected, failed or cancelled.
	AttentionDepMissing   = "missing"
	AttentionDepRejected  = "rejected"
	AttentionDepFailed    = "failed"
	AttentionDepCancelled = "cancelled"
)

// AttentionParams are the values an attention flag's text is made of. Only the fields of the reason are set:
// stalled (Kind, Minutes, Tool, Command), looping (Kind, Count, Tool, Steps), no_accounts and auth (Backend),
// deps_blocked (Kind, Task, Title), no_response (Denied), hygiene and work_at_risk (Count), question (Options),
// resources_wait (Minutes, AvailableMB, NeedMB).
type AttentionParams struct {
	Kind string `json:"kind,omitempty" ts:"'not_started' | 'no_output' | 'tool' | 'failed_calls' | 'repeated' | 'missing' | 'rejected' | 'failed' | 'cancelled' | 'timeout' | 'process_canceled' | 'start_failed' | 'launcher_lost' | 'exit_error' | 'no_result'"`
	// ExitCode is the worker process's exit code of a failed flag whose kind is exit_error or no_result.
	ExitCode *int `json:"exitCode,omitempty"`
	Minutes  int  `json:"minutes,omitempty"`
	Count    int  `json:"count,omitempty"`
	// Steps is how many recent tool calls a repeat was counted in.
	Steps   int    `json:"steps,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	Backend string `json:"backend,omitempty"`
	// Task and Title name the prerequisite of deps_blocked.
	Task  string `json:"task,omitempty"`
	Title string `json:"title,omitempty"`
	// Denied are the actions the worker was refused before it ended without a report.
	Denied []string `json:"denied,omitempty"`
	// Options are the choices a worker's question lists (its "## Blocked" options), which the question card offers;
	// the question itself is the flag's detail.
	Options []string `json:"options,omitempty"`
	// AvailableMB and NeedMB are the free memory found and the threshold it is below (resources_wait, with
	// Minutes: how long the task has waited).
	AvailableMB int64 `json:"availableMB,omitempty"`
	NeedMB      int64 `json:"needMB,omitempty"`
}

// Review is the lead's verdict on a task.
type Review struct {
	Verdict string `json:"verdict" ts:"Verdict"`
	Notes   string `json:"notes,omitempty"`
	At      string `json:"at"`
}

// Gate is the state of a task's gate: the last gate run, or running while it runs (Phase running, settling or
// verifying).
type Gate struct {
	State string `json:"state" ts:"'running' | 'green' | 'red' | 'error' | 'skipped'"`
	Phase string `json:"phase,omitempty" ts:"'queued' | 'running' | 'settling' | 'verifying'"`
	// Wait says what a queued gate waits for: GateWaitSlot (gateConcurrency) or GateWaitMemory (minFreeMemoryMB,
	// minFreeCommitMB). Absent in every other phase.
	Wait      string `json:"wait,omitempty" ts:"'slot' | 'memory'"`
	AutoFixes int    `json:"autoFixes"`
	// Turn is the turn the gate is about.
	Turn int `json:"turn,omitempty"`
}

// SpecRef is the Spec Kit feature a task works on.
type SpecRef struct {
	Dir     string   `json:"dir"`
	TaskIDs []string `json:"taskIds"`
}

// ActiveStep is what the worker is doing right now.
type ActiveStep struct {
	Label string `json:"label"`
	Tool  string `json:"tool,omitempty"`
	Since string `json:"since"`
}

// TaskDetail is one task with its turns.
type TaskDetail struct {
	TaskSummary
	// Prompt is the first lead message.
	Prompt   string `json:"prompt"`
	RepoRoot string `json:"repoRoot,omitempty"`
	Error    string `json:"error,omitempty"`
	Turns    []Turn `json:"turns"`
	// FileOverlaps are the files this task and another task of the same repository changed in overlapping time.
	FileOverlaps []FileOverlap `json:"fileOverlaps"`
	// Proof is what the task's gate checks (null for a task without gate, lane or verifier).
	Proof *TaskProof `json:"proof"`
	// Consults are the second opinions asked on the task, oldest first.
	Consults []Consult `json:"consults"`
	// ProjectCwd is the user's folder a contest variant's worktree stands for.
	ProjectCwd string `json:"projectCwd,omitempty"`
}

// Turn is one lead (or user) message and the worker's answer to it.
type Turn struct {
	N               int       `json:"n"`
	By              string    `json:"by" ts:"'lead' | 'user' | 'gate' | 'verifier'"`
	Message         string    `json:"message"`
	Status          string    `json:"status" ts:"'running' | 'ok' | 'failed' | 'cancelled'"`
	Model           string    `json:"model,omitempty"`
	RequestedModel  string    `json:"requestedModel,omitempty"`
	Tier            string    `json:"tier,omitempty" ts:"Tier"`
	Effort          string    `json:"effort,omitempty"`
	StartedAt       string    `json:"startedAt"`
	EndedAt         string    `json:"endedAt,omitempty"`
	Response        string    `json:"response,omitempty"`
	Files           []string  `json:"files"`
	Commands        []string  `json:"commands"`
	BlockedCommands []string  `json:"blockedCommands"`
	DeniedActions   []string  `json:"deniedActions"`
	Usage           *Usage    `json:"usage"`
	Attempts        []Attempt `json:"attempts"`
	Git             *TurnGit  `json:"git"`
	Handback        string    `json:"handback,omitempty"`
	// WorkAtRisk, Hygiene and Scope are the post-turn checks (empty while the turn runs or without snapshots).
	WorkAtRisk []RiskFile `json:"workAtRisk"`
	Hygiene    []Finding  `json:"hygiene"`
	Scope      []Finding  `json:"scope"`
	// OutsideDiff is true when the turn has snapshots, so GET /tasks/{id}/turns/{n}/outside-diff can answer.
	OutsideDiff bool `json:"outsideDiff"`
	// SnapshotTruncated is true when the turn's start or end snapshot hit its limits (3000 files or 256 MiB of new
	// content), so its checks did not cover every dirty file (SnapshotTruncatedData says which).
	SnapshotTruncated bool `json:"snapshotTruncated"`
	// Gate is the gate run after the turn (null when none ran); Verifier the review of its green gate.
	Gate     *GateResult     `json:"gate"`
	Verifier *VerifierResult `json:"verifier"`
}

// TurnGit lists the files a turn changed, relative to the repository root (null for a turn without snapshots).
type TurnGit struct {
	Changed     []string `json:"changed"`
	Preexisting []string `json:"preexisting"`
}

// Attempt is one run of the worker CLI for a turn on one account.
type Attempt struct {
	AccountID    string `json:"accountId"`
	AccountLabel string `json:"accountLabel,omitempty"`
	Model        string `json:"model,omitempty"`
	StartedAt    string `json:"startedAt"`
	EndedAt      string `json:"endedAt,omitempty"`
	// Status is running, ok, quota, auth, transient, background_wait, error or cancelled.
	Status string `json:"status" ts:"'running' | 'ok' | 'quota' | 'auth' | 'transient' | 'background_wait' | 'error' | 'cancelled'"`
	// Kind is "console" for a takeover.
	Kind  string `json:"kind,omitempty" ts:"'console'"`
	Error string `json:"error,omitempty"`
	Usage *Usage `json:"usage"`
}

// Usage counts tokens.
type Usage struct {
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CachedTokens int64   `json:"cachedTokens,omitempty"`
	TotalTokens  int64   `json:"totalTokens"`
	CostUSD      float64 `json:"costUsd,omitempty"`
}

// UsageOf converts a normalized usage; nil and zero give nil.
func UsageOf(u *event.Usage) *Usage {
	if u == nil || u.IsZero() {
		return nil
	}
	total := u.TotalTokens
	if total <= 0 {
		total = u.InputTokens + u.OutputTokens
	}
	return &Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CachedTokens: u.CachedTokens,
		TotalTokens: total, CostUSD: u.CostUSD}
}

// SpecRequest names a Spec Kit feature directory and the tasks of it a worker implements.
type SpecRequest struct {
	Dir     string   `json:"dir"`
	TaskIDs []string `json:"taskIds,omitempty"`
}

// DelegateRequest creates a task.
type DelegateRequest struct {
	Prompt    string       `json:"prompt"`
	Title     string       `json:"title,omitempty"`
	Cwd       string       `json:"cwd"`
	Mode      string       `json:"mode,omitempty" ts:"'edit' | 'plan'"`
	Tier      string       `json:"tier,omitempty" ts:"Tier"`
	Model     string       `json:"model,omitempty"`
	Effort    string       `json:"effort,omitempty"`
	Backend   string       `json:"backend,omitempty"`
	Provider  string       `json:"provider,omitempty"`
	Member    string       `json:"member,omitempty"`
	Role      string       `json:"role,omitempty"`
	Profile   string       `json:"profile,omitempty"`
	Spec      *SpecRequest `json:"spec,omitempty"`
	After     []string     `json:"after,omitempty"`
	AfterMode string       `json:"afterMode,omitempty" ts:"'accepted' | 'done'"`
	AllowGit  bool         `json:"allowGit,omitempty"`
	// Gate: true adds the test, lint and build commands found in the repository; false turns the gate phase off
	// (no commands, no fix turns; the lane is still checked after each turn, as information). GateCommands are the lead's own gate commands; with spec task ids they
	// replace the tasks' Gate: lines and the feature gate, and prove those tasks.
	Gate         *bool    `json:"gate,omitempty"`
	GateCommands []string `json:"gateCommands,omitempty"`
	// Lane lists the files and globs (relative to the repository root) the worker may change; NoLane drops the
	// lane a spec would give. ForceLane overrides the lane claims of other tasks.
	Lane      []string `json:"lane,omitempty"`
	NoLane    bool     `json:"noLane,omitempty"`
	ForceLane bool     `json:"forceLane,omitempty"`
	// Verify asks for an LLM review of a green gate (default: the verifier setting).
	Verify  *bool    `json:"verify,omitempty"`
	Session *Session `json:"session,omitempty"`
}

// FollowupRequest adds a turn to a task.
type FollowupRequest struct {
	Message string `json:"message"`
	// By is lead (default) or user.
	By       string   `json:"by,omitempty" ts:"'lead' | 'user'"`
	Tier     string   `json:"tier,omitempty" ts:"Tier"`
	Model    string   `json:"model,omitempty"`
	Effort   string   `json:"effort,omitempty"`
	TaskIDs  []string `json:"taskIds,omitempty"`
	Profile  string   `json:"profile,omitempty"`
	AllowGit *bool    `json:"allowGit,omitempty"`
	// Gate, GateCommands, Lane, NoLane, ForceLane and Verify change the task's gate from this turn on (see
	// DelegateRequest); left out, the task keeps what it had. An empty GateCommands list (not left out) clears the
	// lead's commands, so the spec's Gate: lines (as frozen) or the detected commands run again.
	Gate         *bool     `json:"gate,omitempty"`
	GateCommands *[]string `json:"gateCommands,omitempty"`
	Lane         []string  `json:"lane,omitempty"`
	NoLane       bool      `json:"noLane,omitempty"`
	ForceLane    bool      `json:"forceLane,omitempty"`
	Verify       *bool     `json:"verify,omitempty"`
	// Force starts a task that waits for (or is blocked by) its prerequisites anyway.
	Force bool `json:"force,omitempty"`
	// Member moves the task to that department member from this turn on (its backend, provider, model or tier,
	// role and preferred accounts; a new backend starts a new conversation).
	Member string `json:"member,omitempty"`
}

// ReviewRequest records the lead's verdict. AnswerObjection is the lead's written answer to a peer's objection
// (consult); review accepted is refused without it while the objection stands.
type ReviewRequest struct {
	Verdict         string `json:"verdict" ts:"Verdict"`
	Notes           string `json:"notes,omitempty"`
	AnswerObjection string `json:"answerObjection,omitempty"`
}

// CancelRequest stops a task; WaitSeconds (0..60) is how long the call waits for the worker to stop.
type CancelRequest struct {
	WaitSeconds int `json:"waitSeconds,omitempty"`
}

// AnswerRequest answers the question a task asked.
type AnswerRequest struct {
	Answer string `json:"answer"`
}

// WaitRequest waits for tasks.
type WaitRequest struct {
	IDs []string `json:"ids"`
	// TimeoutSeconds is 0..3000.
	TimeoutSeconds int  `json:"timeoutSeconds"`
	Any            bool `json:"any,omitempty"`
	// WakeOnBlocked (default true) ends the wait early when a running task needs the lead.
	WakeOnBlocked *bool `json:"wakeOnBlocked,omitempty"`
	Reports       bool  `json:"reports,omitempty"`
}

// Woke values of WaitResult.
const (
	WokeDone      = "done"
	WokeBlocked   = "blocked"
	WokeTimeout   = "timeout"
	WokeCancelled = "cancelled"
)

// WaitResult is the outcome of a wait.
type WaitResult struct {
	Done    bool          `json:"done"`
	Woke    string        `json:"woke" ts:"'done' | 'blocked' | 'timeout' | 'cancelled'"`
	Tasks   []TaskSummary `json:"tasks"`
	Reports []TaskReport  `json:"reports,omitempty"`
}

// TaskPage is one page of the task list.
type TaskPage struct {
	Items      []TaskSummary `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// EventPage is a slice of a task's event log, ascending by seq.
type EventPage struct {
	Items   []TaskEvent `json:"items"`
	LastSeq int64       `json:"lastSeq"`
}

// LiveWorker is a worker process tree the daemon runs right now (for the worker guard, contract §1.3).
type LiveWorker struct {
	TaskID    string `json:"taskId"`
	AccountID string `json:"accountId"`
	// LaunchDir is the launch directory (internal/launch); its launch.json names the worker's process tree.
	LaunchDir string `json:"launchDir"`
}
