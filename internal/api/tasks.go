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
	// NeedsSince is since when the task waits for the lead or the user.
	NeedsSince string `json:"needsSince,omitempty"`

	Attention  *Attention  `json:"attention"`
	Review     *Review     `json:"review"`
	Gate       *Gate       `json:"gate"`
	Spec       *SpecRef    `json:"spec"`
	ActiveStep *ActiveStep `json:"activeStep"`

	After      []string `json:"after"`
	AfterMode  string   `json:"afterMode" ts:"'accepted' | 'done'"`
	DepsReason string   `json:"depsReason,omitempty"`

	Manual    bool   `json:"manual"`
	ParentID  string `json:"parentId,omitempty"`
	ContestID string `json:"contestId,omitempty"`
	TicketID  string `json:"ticketId,omitempty"`
	MeetingID string `json:"meetingId,omitempty"`

	// LastEventSeq is the highest event seq of the task.
	LastEventSeq int64 `json:"lastEventSeq"`
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

// Attention says that a task needs someone to look at it.
type Attention struct {
	State  string `json:"state" ts:"'blocked' | 'question' | 'stalled' | 'background_wait' | 'info'"`
	Reason string `json:"reason"`
	// Who is lead or user.
	Who    string `json:"who" ts:"'lead' | 'user'"`
	Detail string `json:"detail,omitempty"`
	Since  string `json:"since"`
}

// Review is the lead's verdict on a task.
type Review struct {
	Verdict string `json:"verdict" ts:"Verdict"`
	Notes   string `json:"notes,omitempty"`
	At      string `json:"at"`
}

// Gate is the state of a task's proof gate (filled by milestone 13).
type Gate struct {
	State     string `json:"state" ts:"'running' | 'green' | 'red' | 'error' | 'skipped'"`
	Phase     string `json:"phase,omitempty"`
	AutoFixes int    `json:"autoFixes"`
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
}

// TurnGit lists the files a turn changed, relative to the repository root (filled by milestone 12).
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
	Gate      *bool        `json:"gate,omitempty"`
	Lane      []string     `json:"lane,omitempty"`
	Verify    *bool        `json:"verify,omitempty"`
	Session   *Session     `json:"session,omitempty"`
}

// FollowupRequest adds a turn to a task.
type FollowupRequest struct {
	Message string `json:"message"`
	// By is lead (default) or user.
	By        string   `json:"by,omitempty" ts:"'lead' | 'user'"`
	Tier      string   `json:"tier,omitempty" ts:"Tier"`
	Model     string   `json:"model,omitempty"`
	Effort    string   `json:"effort,omitempty"`
	TaskIDs   []string `json:"taskIds,omitempty"`
	Profile   string   `json:"profile,omitempty"`
	AllowGit  *bool    `json:"allowGit,omitempty"`
	Gate      *bool    `json:"gate,omitempty"`
	Lane      []string `json:"lane,omitempty"`
	ForceLane bool     `json:"forceLane,omitempty"`
	Verify    *bool    `json:"verify,omitempty"`
	// Force starts a task that waits for (or is blocked by) its prerequisites anyway.
	Force bool `json:"force,omitempty"`
}

// ReviewRequest records the lead's verdict.
type ReviewRequest struct {
	Verdict string `json:"verdict" ts:"Verdict"`
	Notes   string `json:"notes,omitempty"`
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
