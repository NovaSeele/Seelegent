package api

// EventType names the kind of a task event; Data has one shape per type.
type EventType string

// Task event types (contract §3.2).
const (
	// EventWorker: a normalized worker event (internal/event.Event) as Data.
	EventWorker EventType = "worker"
	// EventLeadMessage: LeadMessageData (delegate is turn 1, then followups).
	EventLeadMessage EventType = "lead_message"
	// EventRoute: RouteData, the account an attempt runs on.
	EventRoute EventType = "route"
	// EventWorkerPrompt: WorkerPromptData; the full brief is fetched separately.
	EventWorkerPrompt EventType = "worker_prompt"
	// EventAttemptEnd: AttemptEndData.
	EventAttemptEnd EventType = "attempt_end"
	// EventTurnEnd: TurnEndData.
	EventTurnEnd EventType = "turn_end"
	// EventQuotaExhausted: AccountProblemData.
	EventQuotaExhausted EventType = "quota_exhausted"
	// EventAuthFailed: AccountProblemData.
	EventAuthFailed EventType = "auth_failed"
	// EventWaitingQuota: WaitingQuotaData.
	EventWaitingQuota EventType = "waiting_quota"
	// EventRetry: RetryData.
	EventRetry EventType = "retry"
	// EventHandoff: HandoffData (account or backend switch).
	EventHandoff EventType = "handoff"
	// EventReattach: RecoveryData (a live worker was found after a daemon restart).
	EventReattach EventType = "reattach"
	// EventResumed: RecoveryData (a finished stream was replayed, or the turn was resumed, after a restart).
	EventResumed EventType = "resumed"
	// EventAttention: Attention, or AttentionData{State: "cleared"}.
	EventAttention EventType = "attention"
	// EventDeps: DepsData.
	EventDeps EventType = "deps"
	// EventTakeover: TakeoverData.
	EventTakeover EventType = "takeover"
	// EventHandback: HandbackData.
	EventHandback EventType = "handback"
	// EventReview: ReviewData.
	EventReview EventType = "review"
	// EventCancel: EmptyData.
	EventCancel EventType = "cancel"
	// EventWorkAtRisk: WorkAtRiskData (milestone 12).
	EventWorkAtRisk EventType = "work_at_risk"
	// EventRestored: RestoredData, files of a turn put back from its start snapshot (milestone 12).
	EventRestored EventType = "restored"
	// EventSnapshotTruncated: SnapshotTruncatedData, a snapshot of the turn hit its limits (M18 hunt H5).
	EventSnapshotTruncated EventType = "snapshot_truncated"
	// EventGate: GateEventData, the gate after a turn (milestone 13).
	EventGate EventType = "gate"
	// EventLane: LaneEventData, a lane claim forced or warned about at delegate or followup (milestone 13).
	EventLane EventType = "lane"
	// EventVerify: VerifyEventData, the verifier's review of a green gate (milestone 13).
	EventVerify EventType = "verify"
	// EventError: ErrorData.
	EventError EventType = "error"
)

// EventTypes lists every event type.
func EventTypes() []EventType {
	return []EventType{EventWorker, EventLeadMessage, EventRoute, EventWorkerPrompt, EventAttemptEnd, EventTurnEnd,
		EventQuotaExhausted, EventAuthFailed, EventWaitingQuota, EventRetry, EventHandoff, EventReattach,
		EventResumed, EventAttention, EventDeps, EventTakeover, EventHandback, EventReview, EventCancel,
		EventWorkAtRisk, EventRestored, EventSnapshotTruncated, EventGate, EventLane, EventVerify, EventError}
}

// TaskEvent is one entry of a task's append-only event log. Seq starts at 1 and grows by one per task.
type TaskEvent struct {
	Seq     int64  `json:"seq"`
	TS      string `json:"ts"`
	TaskID  string `json:"taskId"`
	Turn    int    `json:"turn,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	// Type and Data are left out of the generated TaskEventBase: web/src/api/events.ts builds the union of
	// {type, data} pairs on top of it (contract §3.2).
	Type EventType `json:"type" ts:"-"`
	// Data has the shape of Type (raw JSON when read back from the log).
	Data any `json:"data" ts:"-"`
}

// LeadMessageData is the data of a lead_message event.
type LeadMessageData struct {
	Message string `json:"message"`
	By      string `json:"by" ts:"'lead' | 'user' | 'gate' | 'verifier'"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Tier    string `json:"tier,omitempty" ts:"Tier"`
}

// RouteData is the data of a route event. AccountLabel is never an email address.
type RouteData struct {
	AccountID    string `json:"accountId"`
	AccountLabel string `json:"accountLabel"`
	Model        string `json:"model"`
	Reason       string `json:"reason"`
	// Continuing is true when the attempt resumes the task's conversation on the same account.
	Continuing bool     `json:"continuing"`
	Remaining  *float64 `json:"remaining,omitempty"`
}

// WorkerPromptData is the data of a worker_prompt event: the length and the first 500 characters of the brief.
type WorkerPromptData struct {
	Length  int    `json:"length"`
	Preview string `json:"preview"`
}

// AttemptEndData is the data of an attempt_end event.
type AttemptEndData struct {
	Status string `json:"status" ts:"Attempt['status']"`
	Error  string `json:"error,omitempty"`
	// Warning is an error that did not fail an ok attempt (a network error the worker CLI reported after its
	// complete report, an error its conversation replayed): the work counts, but the lead should know.
	Warning string `json:"warning,omitempty"`
}

// TurnEndData is the data of a turn_end event.
type TurnEndData struct {
	Status   string   `json:"status" ts:"Turn['status']"`
	Response string   `json:"response,omitempty"`
	Files    []string `json:"files"`
	Usage    *Usage   `json:"usage,omitempty"`
}

// AccountProblemData is the data of quota_exhausted and auth_failed events.
type AccountProblemData struct {
	AccountID string `json:"accountId"`
	Group     string `json:"group,omitempty"`
	Until     string `json:"until,omitempty"`
	Error     string `json:"error"`
}

// Reasons of a waiting_quota event (WaitingQuotaData.Reason) and values of TaskSummary.Waiting. The account
// reasons are the pool's (pool.Reason).
const (
	// WaitingMemory: the task waits for free memory or free commit (minFreeMemoryMB, minFreeCommitMB), not for
	// an account.
	WaitingMemory = "memory"
	// WaitingNoAccount: no enabled, signed-in account can run the task.
	WaitingNoAccount = "no_account"
	// WaitingResting: every account that could run it rests (quota used up or a cooldown); Until is when the
	// first one is back.
	WaitingResting = "resting"
	// WaitingBusy: every account that could run it and is not resting has no free slot (maxConcurrentPerAccount,
	// or the maxConcurrentWorkers cap).
	WaitingBusy = "busy"
	// WaitingBudget: every account that could run it rests on its budget; no time ends the wait, the user raises
	// a budget, switches its stop off or changes a key. Detail names them ("acc9 reached its $200 budget: raise
	// the budget or change the key").
	WaitingBudget = "budget_reached"
)

// WaitingQuotaData is the data of a waiting_quota event. Reason says what the task waits for; events written
// before it was set for account waits have none, which reads as an account wait (Until is when one is expected
// back). Detail is the daemon's English explanation (for busy: which accounts are full, "acc6: 2 of 2 running").
// With Reason WaitingMemory, AvailableMB is what the last reading found and NeedMB the threshold it is below (of
// physical memory or of commit, whichever holds the task).
type WaitingQuotaData struct {
	Until       string `json:"until,omitempty"`
	Reason      string `json:"reason,omitempty" ts:"'memory' | 'no_account' | 'resting' | 'busy' | 'budget_reached'"`
	Detail      string `json:"detail,omitempty"`
	AvailableMB int64  `json:"availableMB,omitempty"`
	NeedMB      int64  `json:"needMB,omitempty"`
}

// RetryData is the data of a retry event.
type RetryData struct {
	DelayMs int64  `json:"delayMs"`
	Error   string `json:"error"`
}

// HandoffData is the data of a handoff event: the task moved from one account to another, or (Kind backend) from
// one backend to the next fallback backend of its member.
type HandoffData struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
	Kind   string `json:"kind,omitempty" ts:"'backend'"`
}

// HandoffBackend is the Kind of a handoff between backends.
const HandoffBackend = "backend"

// RecoveryData is the data of reattach and resumed events.
type RecoveryData struct {
	AccountID string `json:"accountId"`
	Offset    int64  `json:"offset,omitempty"`
	// NoticeCode says what the lead should know about the recovery (RecoveryNotice*); the dashboard words it.
	// Notice is the same in English, the raw text behind the code (logs, API clients).
	NoticeCode RecoveryNoticeCode `json:"noticeCode,omitempty"`
	Notice     string             `json:"notice,omitempty"`
}

// RecoveryNoticeCode says what the lead should know about a recovery.
type RecoveryNoticeCode string

// RecoveryNoticeCode values.
const (
	// RecoveryNoticeMaskUnreadable: the attempt's mask list could not be read, so its output is also masked by key
	// patterns.
	RecoveryNoticeMaskUnreadable RecoveryNoticeCode = "mask_unreadable"
)

// AttentionData is the data of an attention event that clears the flag ({state: "cleared"}); a raised flag is an
// Attention.
type AttentionData struct {
	State string `json:"state" ts:"'cleared'"`
}

// Deps states.
const (
	DepsWaiting = "waiting"
	DepsReady   = "ready"
	DepsBlocked = "blocked"
)

// DepsData is the data of a deps event.
type DepsData struct {
	State  string   `json:"state" ts:"'waiting' | 'ready' | 'blocked'"`
	After  []string `json:"after,omitempty"`
	Reason string   `json:"reason,omitempty"`
	Forced bool     `json:"forced,omitempty"`
}

// TakeoverData is the data of a takeover event (state start or end).
type TakeoverData struct {
	State      string `json:"state" ts:"'start' | 'end'"`
	AccountID  string `json:"accountId"`
	DurationMs int64  `json:"durationMs,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// HandbackData is the data of a handback event.
type HandbackData struct {
	Turn     int    `json:"turn"`
	Response string `json:"response"`
}

// ReviewData is the data of a review event.
type ReviewData struct {
	Verdict string `json:"verdict" ts:"Verdict"`
	Notes   string `json:"notes,omitempty"`
}

// EmptyData is the data of events without fields (cancel).
type EmptyData struct{}

// WorkAtRiskData is the data of a work_at_risk event.
type WorkAtRiskData struct {
	Files    []string `json:"files"`
	Snapshot string   `json:"snapshot,omitempty"`
}

// SnapshotTruncatedData is the data of a snapshot_truncated event: which snapshots of the turn hit the limits of
// one snapshot (MaxFiles files, MaxBytes bytes of new content). The start snapshot's files and the files the edit
// tools changed are always read, so a truncated end snapshot still covers the work at risk; dirty files past the
// limits are missing from the turn's change lists and hygiene check, and a truncated start snapshot leaves its
// missing files out of the work at risk check.
type SnapshotTruncatedData struct {
	Snapshots []string `json:"snapshots" ts:"('start' | 'end')[]"`
	MaxFiles  int      `json:"maxFiles"`
	MaxBytes  int64    `json:"maxBytes"`
}

// RestoredData is the data of a restored event; the event's turn is the turn whose start snapshot the files came
// from.
type RestoredData struct {
	// Files are the restored files, relative to the repository root.
	Files []string `json:"files"`
	// Backup names the snapshot that keeps what the files held before the restore (t<turn>-restore-<time>, in the
	// task's snapshot folder).
	Backup string `json:"backup,omitempty"`
}

// ErrorData is the data of an error event.
type ErrorData struct {
	Message string `json:"message"`
}
