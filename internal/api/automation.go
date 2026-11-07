package api

// Automations (milestone 15, docs/automations.md): saved delegations run on a schedule, subject to the quota, an
// optional precheck command and an overlap guard. Every automation is created disabled; turning one on takes a
// confirmation in the dashboard (old bug D8).

// Schedule presets.
const (
	EveryHourly   = "hourly"
	EveryDaily    = "daily"
	EveryWeekdays = "weekdays"
	EveryWeekly   = "weekly"
)

// Schedule is a preset (Every with At, and Weekday for weekly) or a 5-field cron expression (Cron).
type Schedule struct {
	Every string `json:"every,omitempty" ts:"'hourly' | 'daily' | 'weekdays' | 'weekly'"`
	// At is "HH:MM" (hourly uses only the minutes).
	At string `json:"at,omitempty"`
	// Weekday is 0-6, 0 = Sunday (weekly).
	Weekday *int `json:"weekday,omitempty"`
	// Cron is "minute hour day-of-month month day-of-week".
	Cron string `json:"cron,omitempty"`
}

// AutomationQuota are the quota rules of an automation.
type AutomationQuota struct {
	// MinRemaining skips a run when the best account of the target backend has less than this percentage left
	// (0-100) in the bucket that limits it.
	MinRemaining float64 `json:"minRemaining"`
	// UseBeforeReset also starts one run when a weekly bucket resets within 24 h with at least 40 % left.
	UseBeforeReset bool `json:"useBeforeReset"`
}

// Automation run triggers.
const (
	TriggerSchedule    = "schedule"
	TriggerMissed      = "missed"
	TriggerManual      = "manual"
	TriggerBeforeReset = "before_reset"
)

// Automation run outcomes.
const (
	AutoStarted = "started"
	// AutoSkippedPrecheck: the precheck ran and said skip (exit code 1).
	AutoSkippedPrecheck = "skipped_precheck"
	// AutoPrecheckFailed: the precheck itself broke (any other non-zero exit code, killed, timed out or could not
	// start); the run is skipped too.
	AutoPrecheckFailed = "precheck_failed"
	AutoSkippedQuota   = "skipped_quota"
	AutoSkippedOverlap = "skipped_overlap"
	AutoSkippedLate    = "skipped_late"
	// AutoSkippedChanged: the automation was edited, turned off, or the master switch went off while its precheck
	// ran; nothing was started (an edit is not run before the user confirms it).
	AutoSkippedChanged = "skipped_changed"
	AutoError          = "error"
	// AutoPending is a scheduled run the scheduler decided on that has not started yet. It is written together with
	// the automation's next run time, so a stop in between leaves a trace (resolved at the next start).
	AutoPending = "pending"
)

// AutomationRun is one attempt to run an automation.
type AutomationRun struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Trigger string `json:"trigger" ts:"'schedule' | 'missed' | 'manual' | 'before_reset'"`
	Outcome string `json:"outcome" ts:"'started' | 'skipped_precheck' | 'precheck_failed' | 'skipped_quota' | 'skipped_overlap' | 'skipped_late' | 'skipped_changed' | 'error' | 'pending'"`
	TaskID  string `json:"taskId,omitempty"`
	// Detail says why a run was skipped or failed (the precheck's output tail, the quota left, ...), in English for
	// the lead's tools.
	Detail string `json:"detail,omitempty"`
	// Info is the same as facts, for the dashboard to say in the user's language. Absent on older rows.
	Info *AutomationRunInfo `json:"info,omitempty"`
}

// Reasons of an automation run (AutomationRunInfo.Reason).
const (
	// RunStarting, RunTaskRunning: skipped_overlap, another run is starting or its task is still active.
	RunStarting    = "starting"
	RunTaskRunning = "task_running"
	// RunChanged, RunUnconfirmed, RunDisabled, RunMasterOff: skipped_changed, what changed during the precheck.
	RunChanged     = "changed"
	RunUnconfirmed = "unconfirmed"
	RunDisabled    = "disabled"
	RunMasterOff   = "master_off"
	// RunInterrupted: error, the daemon stopped while the run was starting.
	RunInterrupted = "interrupted"
	// RunMemberMissing, RunMemberDisabled: error, the automation's member is gone or turned off.
	RunMemberMissing  = "member_missing"
	RunMemberDisabled = "member_disabled"
)

// AutomationRunInfo is what a run's outcome rests on. Which fields are set depends on the outcome: skipped_overlap
// (Reason, TaskID and TaskStatus of the task still active), skipped_quota (Backend, or Account for a before-reset
// run's weekly bucket, with Remaining and MinRemaining in percent), skipped_precheck and precheck_failed (ExitCode,
// TimedOut with TimeoutSeconds, or NotStarted; Output is the command's output tail), skipped_late (LateMinutes,
// GraceMinutes), skipped_changed (Reason), error (Reason and Member when known, else Message), pending (Due).
type AutomationRunInfo struct {
	Reason         string   `json:"reason,omitempty" ts:"'starting' | 'task_running' | 'changed' | 'unconfirmed' | 'disabled' | 'master_off' | 'interrupted' | 'member_missing' | 'member_disabled'"`
	TaskID         string   `json:"taskId,omitempty"`
	TaskStatus     string   `json:"taskStatus,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Account        string   `json:"account,omitempty"`
	Remaining      *float64 `json:"remaining,omitempty"`
	MinRemaining   float64  `json:"minRemaining,omitempty"`
	ExitCode       *int     `json:"exitCode,omitempty"`
	TimedOut       bool     `json:"timedOut,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	NotStarted     bool     `json:"notStarted,omitempty"`
	Output         string   `json:"output,omitempty"`
	LateMinutes    int      `json:"lateMinutes,omitempty"`
	GraceMinutes   int      `json:"graceMinutes,omitempty"`
	Member         string   `json:"member,omitempty"`
	Message        string   `json:"message,omitempty"`
	Due            string   `json:"due,omitempty"`
}

// Automation is a saved delegation on a schedule.
type Automation struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Prompt   string   `json:"prompt"`
	Cwd      string   `json:"cwd"`
	Schedule Schedule `json:"schedule"`
	// Timezone is "local" or an IANA zone.
	Timezone     string   `json:"timezone"`
	Member       string   `json:"member,omitempty"`
	Backend      string   `json:"backend,omitempty"`
	Model        string   `json:"model,omitempty"`
	Gate         *bool    `json:"gate"`
	GateCommands []string `json:"gateCommands"`
	Lane         []string `json:"lane"`
	NoLane       bool     `json:"noLane"`
	// Precheck is a shell command run in cwd before each run (60 s limit). Exit code 0 runs; 1 skips the run
	// (skipped_precheck); any other outcome skips it as a broken precheck (precheck_failed).
	Precheck string          `json:"precheck,omitempty"`
	Quota    AutomationQuota `json:"quota"`
	// GraceMinutes: a run missed while the daemon was off still runs if it is at most this late.
	GraceMinutes int `json:"graceMinutes"`
	// Enabled automations run on their schedule. EnableRequested says the lead (MCP or CLI) asked to turn it on:
	// only a confirmation in the dashboard does.
	Enabled           bool   `json:"enabled"`
	EnableRequested   bool   `json:"enableRequested"`
	EnableRequestedAt string `json:"enableRequestedAt,omitempty"`
	ConfirmedAt       string `json:"confirmedAt,omitempty"`
	// Confirmed is true while what a run does is what the user last confirmed in the dashboard (Revision unchanged
	// since). Run now needs it: an automation never confirmed, or changed since, is refused (not_confirmed).
	Confirmed bool `json:"confirmed"`
	// Revision names what a run does (a hash of the prompt, folder, worker, gate, lane, precheck and schedule). The
	// dashboard sends the revision it shows when it asks for a confirmation token, so it enables exactly that.
	Revision string `json:"revision"`
	// Running is true while the task of the last run is active (ActiveTaskID).
	Running      bool           `json:"running"`
	ActiveTaskID string         `json:"activeTaskId,omitempty"`
	NextRun      string         `json:"nextRun,omitempty"`
	LastRun      *AutomationRun `json:"lastRun"`
	CreatedAt    string         `json:"createdAt"`
	UpdatedAt    string         `json:"updatedAt"`
}

// AutomationInput is the body of POST /automations. Enabled true only records an enable request.
type AutomationInput struct {
	Name         string           `json:"name"`
	Prompt       string           `json:"prompt"`
	Cwd          string           `json:"cwd"`
	Schedule     Schedule         `json:"schedule"`
	Timezone     string           `json:"timezone,omitempty"`
	Member       string           `json:"member,omitempty"`
	Backend      string           `json:"backend,omitempty"`
	Model        string           `json:"model,omitempty"`
	Gate         *bool            `json:"gate,omitempty"`
	GateCommands []string         `json:"gateCommands,omitempty"`
	Lane         []string         `json:"lane,omitempty"`
	NoLane       bool             `json:"noLane,omitempty"`
	Precheck     string           `json:"precheck,omitempty"`
	Quota        *AutomationQuota `json:"quota,omitempty"`
	GraceMinutes *int             `json:"graceMinutes,omitempty"`
	Enabled      bool             `json:"enabled,omitempty"`
}

// AutomationPatch is the body of PATCH /automations/{id}: the fields to change. Enabled false turns it off at
// once; Enabled true only records an enable request. A change of what a run does (prompt, cwd, worker, gate, lane,
// precheck) turns an enabled automation off until it is confirmed again.
type AutomationPatch struct {
	Name         *string          `json:"name,omitempty"`
	Prompt       *string          `json:"prompt,omitempty"`
	Cwd          *string          `json:"cwd,omitempty"`
	Schedule     *Schedule        `json:"schedule,omitempty"`
	Timezone     *string          `json:"timezone,omitempty"`
	Member       *string          `json:"member,omitempty"`
	Backend      *string          `json:"backend,omitempty"`
	Model        *string          `json:"model,omitempty"`
	Gate         *bool            `json:"gate,omitempty"`
	GateCommands *[]string        `json:"gateCommands,omitempty"`
	Lane         *[]string        `json:"lane,omitempty"`
	NoLane       *bool            `json:"noLane,omitempty"`
	Precheck     *string          `json:"precheck,omitempty"`
	Quota        *AutomationQuota `json:"quota,omitempty"`
	GraceMinutes *int             `json:"graceMinutes,omitempty"`
	Enabled      *bool            `json:"enabled,omitempty"`
}

// AutomationEnableResult answers an enable request (POST /automations/{id}/enable, PATCH enabled true, a create
// with enabled true): the automation stays off until the user confirms it in the dashboard at ConfirmURL.
type AutomationEnableResult struct {
	Automation Automation `json:"automation"`
	// Confirm is true: the user has to confirm in the dashboard.
	Confirm    bool   `json:"confirm"`
	ConfirmURL string `json:"confirmUrl"`
	Message    string `json:"message"`
}

// AutomationConfirmToken answers POST /automations/{id}/confirm-token (dashboard only): a one-time token that
// enables exactly the automation as shown, for a few minutes.
type AutomationConfirmToken struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
}

// AutomationConfirmTokenRequest is the body of POST /automations/{id}/confirm-token (dashboard only): the
// revision of the automation as the dashboard shows it. A token is refused when the automation has changed since.
type AutomationConfirmTokenRequest struct {
	Revision string `json:"revision"`
}

// AutomationConfirmRequest is the body of POST /automations/{id}/confirm (dashboard only).
type AutomationConfirmRequest struct {
	Token string `json:"token"`
}

// SchedulePreviewRequest is the body of POST /automations/preview: the next run times of a schedule.
type SchedulePreviewRequest struct {
	Schedule Schedule `json:"schedule"`
	Timezone string   `json:"timezone,omitempty"`
	Count    int      `json:"count,omitempty"`
}

// SchedulePreview answers POST /automations/preview.
type SchedulePreview struct {
	// Runs are the next run times (UTC); Local are the same in the schedule's zone ("2026-11-07 09:00 Mon").
	Runs     []string `json:"runs"`
	Local    []string `json:"local"`
	Timezone string   `json:"timezone"`
}
