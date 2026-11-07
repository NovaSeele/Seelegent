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
	AutoStarted         = "started"
	AutoSkippedPrecheck = "skipped_precheck"
	AutoSkippedQuota    = "skipped_quota"
	AutoSkippedOverlap  = "skipped_overlap"
	AutoSkippedLate     = "skipped_late"
	AutoError           = "error"
)

// AutomationRun is one attempt to run an automation.
type AutomationRun struct {
	ID      int64  `json:"id"`
	At      string `json:"at"`
	Trigger string `json:"trigger" ts:"'schedule' | 'missed' | 'manual' | 'before_reset'"`
	Outcome string `json:"outcome" ts:"'started' | 'skipped_precheck' | 'skipped_quota' | 'skipped_overlap' | 'skipped_late' | 'error'"`
	TaskID  string `json:"taskId,omitempty"`
	// Detail says why a run was skipped or failed (the precheck's output tail, the quota left, ...).
	Detail string `json:"detail,omitempty"`
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
	// Precheck is a shell command run in cwd before each run (60 s limit); a non-zero exit skips the run.
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
