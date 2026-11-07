package api

// TaskReport is the lead-facing report of a task (MCP tools, GET /tasks/{id}/report). It keeps the fields and the
// snake_case names of the old slim report, because leads and the lead skill depend on them; it is the only
// snake_case shape of the API. Empty fields are omitted. Gate, lane, bar, verifier, work_at_risk and file_overlaps
// arrive with milestones 12 and 13.
type TaskReport struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Turn   int    `json:"turn"`
	// Worker is the account label (never an email address).
	Worker     string      `json:"worker,omitempty"`
	Model      string      `json:"model,omitempty"`
	Effort     string      `json:"effort,omitempty"`
	Profile    string      `json:"profile,omitempty"`
	TokensUsed int64       `json:"tokens_used"`
	Spec       *ReportSpec `json:"spec,omitempty"`
	// Response is the worker's last report, with markdown links to file:// targets reduced to their text.
	Response string `json:"response,omitempty"`
	// FilesChanged are relative to the caller's folder with '/' separators (absolute when outside it).
	FilesChanged    []string         `json:"files_changed"`
	GitChanges      *ReportGit       `json:"git_changes,omitempty"`
	CommandsRun     []string         `json:"commands_run,omitempty"`
	CommandsTotal   int              `json:"commands_total,omitempty"`
	Attempts        []ReportAttempt  `json:"attempts,omitempty"`
	Attention       *ReportAttention `json:"attention,omitempty"`
	Question        *ReportQuestion  `json:"question,omitempty"`
	BlockedCommands []string         `json:"blocked_commands,omitempty"`
	// WorkAtRisk names the latest turn's files whose uncommitted work may be lost, and how to restore them.
	WorkAtRisk   *ReportRisk     `json:"work_at_risk,omitempty"`
	FileOverlaps []ReportOverlap `json:"file_overlaps,omitempty"`
	// Hygiene and ScopeWarnings are the findings of the last turn's checks.
	Hygiene       []ReportFinding `json:"hygiene,omitempty"`
	ScopeWarnings []ReportFinding `json:"scope_warnings,omitempty"`
	DeniedActions []string        `json:"denied_actions,omitempty"`
	// Gate is the gate of the latest instruction once it ended; LastGate shows a gate still running or an
	// automatic fix turn at work. Lane, Bar and Verifier come with it (docs/gates.md).
	Gate         *ReportGate     `json:"gate,omitempty"`
	LastGate     *ReportLastGate `json:"last_gate,omitempty"`
	Lane         *ReportLane     `json:"lane,omitempty"`
	Bar          *ReportBar      `json:"bar,omitempty"`
	Verifier     *ReportVerifier `json:"verifier,omitempty"`
	DepsReason   string          `json:"deps_reason,omitempty"`
	Cancelling   bool            `json:"cancelling,omitempty"`
	Error        string          `json:"error,omitempty"`
	WaitingUntil string          `json:"waiting_until,omitempty"`
	AnsweredBy   string          `json:"answered_by,omitempty"`
	// Handback is true when the last turn summarized a takeover.
	Handback bool `json:"handback,omitempty"`
	// Dashboard is the deep link to the task in the dashboard.
	Dashboard string `json:"dashboard"`
}

// ReportSpec is the Spec Kit feature of a report.
type ReportSpec struct {
	Dir     string   `json:"dir"`
	TaskIDs []string `json:"task_ids,omitempty"`
}

// ReportGit summarizes the git changes of the last turn: the count of files that were already changed before the
// task started, or else the changed files.
type ReportGit struct {
	Changed     []string `json:"changed,omitempty"`
	Preexisting int      `json:"preexisting,omitempty"`
}

// ReportAttempt is one attempt of the last turn.
type ReportAttempt struct {
	Account string `json:"account"`
	Model   string `json:"model,omitempty"`
	Result  string `json:"result"`
	Error   string `json:"error,omitempty"`
}

// ReportQuestion is the question of a worker's "## Blocked" section: its text and the options listed under it.
type ReportQuestion struct {
	Text    string   `json:"text"`
	Options []string `json:"options"`
}

// ReportAttention says why a task needs the lead.
type ReportAttention struct {
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
}
