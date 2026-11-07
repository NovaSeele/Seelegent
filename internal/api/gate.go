package api

// Gate, lane and verifier DTOs (milestone 13, docs/gates.md).

// Gate states (of a gate run and of TaskSummary.Gate).
const (
	GateRunning = "running"
	GateGreen   = "green"
	GateRed     = "red"
	GateError   = "error"
	GateSkipped = "skipped"
)

// Gate phases of TaskSummary.Gate while the task waits for its gate.
const (
	GatePhaseRunning   = "running"
	GatePhaseSettling  = "settling"
	GatePhaseVerifying = "verifying"
)

// Gate step kinds.
const (
	// StepLane: the files the turns changed against the task's lane.
	StepLane = "lane"
	// StepHygiene: the red hygiene findings of the turns (milestone 12 checks).
	StepHygiene = "hygiene"
	// StepAuto: syntax checks of the changed .json, .go and .js files (gateAutoChecks).
	StepAuto = "auto"
	// StepDoctrine: a check of the repository's .seelegent/doctrine.json, frozen when the task started.
	StepDoctrine = "doctrine"
	// StepTask: a gate command of the task: the lead's, or the Gate: commands of its spec tasks.
	StepTask = "task"
	// StepDetect: a test, lint or build command found in the repository (gate: true).
	StepDetect = "detect"
)

// Gate step states.
const (
	StepPending = "pending"
	StepRunning = "running"
	StepGreen   = "green"
	StepRed     = "red"
	StepError   = "error"
	StepWarn    = "warn"
	StepNotRun  = "not_run"
)

// GateStep is one step of a gate run.
type GateStep struct {
	Kind  string `json:"kind" ts:"'lane' | 'hygiene' | 'auto' | 'doctrine' | 'task' | 'detect'"`
	Label string `json:"label"`
	Cmd   string `json:"cmd,omitempty"`
	// Dir is the folder the command runs in, relative to the repository root ("" for the root).
	Dir string `json:"dir,omitempty"`
	// TaskIDs are the spec tasks whose Gate: line named the command.
	TaskIDs []string `json:"taskIds,omitempty"`
	State   string   `json:"state" ts:"'pending' | 'running' | 'green' | 'red' | 'error' | 'warn' | 'not_run'"`
	// Exit is the command's exit code (null when it did not exit: timeout, start failure).
	Exit     *int  `json:"exit,omitempty"`
	Ms       int64 `json:"ms,omitempty"`
	TimedOut bool  `json:"timedOut,omitempty"`
	// Tail is the end of the command's output (gateOutputTailBytes), or the error.
	Tail string `json:"tail,omitempty"`
	// Files are the files out of lane (lane) or checked (auto).
	Files []string `json:"files,omitempty"`
	// Failures are the files an auto check refused, with why.
	Failures []GateFailure `json:"failures,omitempty"`
	// Findings are the red hygiene findings.
	Findings []Finding `json:"findings,omitempty"`
	// Foreign: a doctrine check failed only in files other active tasks changed (CausedBy). The gate stays red, but
	// no automatic fix turn is spent on it: the lead decides.
	Foreign  bool        `json:"foreign,omitempty"`
	CausedBy []GateCause `json:"causedBy,omitempty"`
}

// GateCause is another task a foreign failure may be caused by: its id for a link, its title to name it.
type GateCause struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title,omitempty"`
}

// GateFailure is a file an auto check refused.
type GateFailure struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// GateResult is one gate run of a turn.
type GateResult struct {
	State string     `json:"state" ts:"'running' | 'green' | 'red' | 'error' | 'skipped'"`
	Steps []GateStep `json:"steps"`
	// NotRun are the labels of the steps skipped after the first failing one.
	NotRun []string `json:"notRun"`
	// Warnings say what the gate could not fully check (settle timeout, lane warnings, foreign failures).
	Warnings  []string `json:"warnings"`
	StartedAt string   `json:"startedAt"`
	EndedAt   string   `json:"endedAt,omitempty"`
	// AutoFix is set when this run's failure started an automatic fix turn (its number).
	AutoFix int `json:"autoFix,omitempty"`
	// Settled: the first run failed while other tasks of the repository were running; this is the run after they
	// finished (or after gateSettleMinutes).
	Settled bool `json:"settled,omitempty"`
}

// Verifier verdicts.
const (
	VerdictAccept = "accept"
	VerdictReject = "reject"
	VerdictError  = "error"
)

// VerifierResult is the optional LLM review of a green gate.
type VerifierResult struct {
	Verdict string `json:"verdict" ts:"'running' | 'accept' | 'reject' | 'error'"`
	// ChildID is the hidden verify task that ran the review.
	ChildID string   `json:"childId,omitempty"`
	Reasons []string `json:"reasons"`
	// Cites are the file:line citations of the reasons, checked against the repository.
	Cites  []CiteCheck `json:"cites"`
	Detail string      `json:"detail,omitempty"`
	Tokens int64       `json:"tokens,omitempty"`
}

// CiteCheck is a checked file:line citation.
type CiteCheck struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	State   string `json:"state" ts:"'verified' | 'located' | 'quote_mismatch' | 'bad_range' | 'missing_file' | 'outside_repo'"`
	Detail  string `json:"detail,omitempty"`
}

// Gate event phases.
const (
	GateEventStart    = "start"
	GateEventStep     = "step"
	GateEventResult   = "result"
	GateEventSettling = "settling"
)

// GateEventData is the data of a gate event; the event's turn is the turn the gate is about.
type GateEventData struct {
	Phase string `json:"phase" ts:"'start' | 'step' | 'result' | 'settling'"`
	// Steps are the step labels (start).
	Steps []string `json:"steps,omitempty"`
	// Plan are the planned steps with their kind and command (start), so a view can name them in its language.
	Plan []GatePlanStep `json:"plan,omitempty"`
	// Index (from 0) and Step are the step that started or ended (step).
	Index int       `json:"index,omitempty"`
	Step  *GateStep `json:"step,omitempty"`
	// Result is the finished run (result); its AutoFix names the fix turn it started.
	Result *GateResult `json:"result,omitempty"`
	// BarChanged lists what defines "done" that changed since the task started (result).
	BarChanged []string `json:"barChanged,omitempty"`
}

// GatePlanStep is a step of a gate as planned: what it checks, without its result.
type GatePlanStep struct {
	Kind    string   `json:"kind" ts:"'lane' | 'hygiene' | 'auto' | 'doctrine' | 'task' | 'detect'"`
	Label   string   `json:"label"`
	Cmd     string   `json:"cmd,omitempty"`
	Dir     string   `json:"dir,omitempty"`
	TaskIDs []string `json:"taskIds,omitempty"`
}

// Lane event kinds.
const (
	LaneForced    = "forced"
	LaneWarning   = "warning"
	LaneViolation = "violation"
)

// LaneEventData is the data of a lane event: a claim the lead forced (forceLane), an overlap only warned about
// (lanes: warn), or files a turn changed outside the lane.
type LaneEventData struct {
	Kind      string         `json:"kind" ts:"'forced' | 'warning' | 'violation'"`
	Paths     []string       `json:"paths"`
	Conflicts []LaneConflict `json:"conflicts,omitempty"`
	Files     []string       `json:"files,omitempty"`
}

// VerifyEventData is the data of a verify event.
type VerifyEventData struct {
	Phase   string          `json:"phase" ts:"'start' | 'result'"`
	ChildID string          `json:"childId,omitempty"`
	Result  *VerifierResult `json:"result,omitempty"`
}

// Lane modes (setting lanes).
const (
	LaneOff     = "off"
	LaneWarn    = "warn"
	LaneEnforce = "enforce"
)

// TaskLane is the file lane of a task: the files it may change.
type TaskLane struct {
	Paths []string `json:"paths"`
	// Source is lead (passed with delegate or followup) or spec (the path tokens of its spec tasks).
	Source string `json:"source" ts:"'lead' | 'spec'"`
	// Forced lists the tasks whose claims the lead overrode with forceLane.
	Forced []string `json:"forced,omitempty"`
}

// GateCommand is a command of a task's gate.
type GateCommand struct {
	Cmd string `json:"cmd"`
	// Dir is the folder the command runs in, relative to the repository root ("" for the root).
	Dir     string   `json:"dir,omitempty"`
	TaskIDs []string `json:"taskIds,omitempty"`
	// Kind is task (the lead's or a spec task's) or detect (found in the repository).
	Kind string `json:"kind" ts:"'task' | 'detect'"`
}

// TaskProof is what a task's gate checks, fixed when the task started (or at a followup that changed it).
type TaskProof struct {
	// Gate is false when the lead turned the gate off for the task.
	Gate     bool          `json:"gate"`
	Commands []GateCommand `json:"commands"`
	Lane     *TaskLane     `json:"lane"`
	Verify   bool          `json:"verify"`
	// Doctrine are the ids of the doctrine checks frozen at the start; DoctrineError says why the file was refused.
	Doctrine      []string `json:"doctrine"`
	DoctrineError string   `json:"doctrineError,omitempty"`
	// BarChanged lists what defines "done" that changed since the task started (spec documents, contracts, gate
	// commands, its tasks.md lines, constitution, doctrine).
	BarChanged []string `json:"barChanged"`
	// AutoFixes counts the automatic fix turns since the lead's last message; Budget is the most allowed.
	AutoFixes int `json:"autoFixes"`
	Budget    int `json:"budget"`
	// Feature is the Spec Kit feature folder the ledger records the gate results under.
	Feature string `json:"feature,omitempty"`
}

// LaneConflict is a held claim a new lane overlaps.
type LaneConflict struct {
	TaskID string     `json:"taskId"`
	Title  string     `json:"title"`
	Status TaskStatus `json:"status"`
	Paths  []PathPair `json:"paths"`
}

// PathPair is one overlap: a path of the new lane and one of the held claim.
type PathPair struct {
	Mine   string `json:"mine"`
	Theirs string `json:"theirs"`
}

// LaneClaim is the lane a task holds in a repository: while it is active, and when it is done but not reviewed
// within laneHoldHours.
type LaneClaim struct {
	TaskID string     `json:"taskId"`
	Title  string     `json:"title"`
	Status TaskStatus `json:"status"`
	Paths  []string   `json:"paths"`
	Source string     `json:"source" ts:"'lead' | 'spec'"`
	// HeldUntil is when a finished, unreviewed task's claim lapses ("" while the task is active).
	HeldUntil string `json:"heldUntil,omitempty"`
}

// LanesView is GET /lanes: the claims held in one repository.
type LanesView struct {
	RepoRoot string      `json:"repoRoot"`
	Mode     string      `json:"mode" ts:"'off' | 'warn' | 'enforce'"`
	Claims   []LaneClaim `json:"claims"`
}

// ContentionFile is a file at least two tasks of a repository changed within contentionWindowHours.
type ContentionFile struct {
	File  string           `json:"file"`
	Tasks []ContentionTask `json:"tasks"`
}

// ContentionTask is one task that changed a contended file.
type ContentionTask struct {
	TaskID string     `json:"taskId"`
	Title  string     `json:"title"`
	Status TaskStatus `json:"status"`
	At     string     `json:"at"`
}

// ContentionView is GET /contention.
type ContentionView struct {
	RepoRoot    string           `json:"repoRoot"`
	WindowHours int              `json:"windowHours"`
	Files       []ContentionFile `json:"files"`
}

// ReportGate is the gate field of a report: the last gate run of the task. Steps lists only the failing steps,
// with the end of their output, and only when the gate is red or error.
type ReportGate struct {
	State        string           `json:"state"`
	AutoFixTurns int              `json:"auto_fix_turns"`
	NotRun       []string         `json:"not_run,omitempty"`
	Steps        []ReportGateStep `json:"steps,omitempty"`
	Warnings     []string         `json:"warnings,omitempty"`
}

// ReportLastGate is the last_gate field of a report while the gate runs or an automatic fix turn works: which turn
// the gate is about and whether a fix is under way.
type ReportLastGate struct {
	Turn   int    `json:"turn"`
	State  string `json:"state"`
	Fixing bool   `json:"fixing"`
	Phase  string `json:"phase,omitempty"`
}

// ReportGateStep is a failing step of a report's gate.
type ReportGateStep struct {
	Label string   `json:"label"`
	Kind  string   `json:"kind"`
	Cmd   string   `json:"cmd,omitempty"`
	Exit  *int     `json:"exit,omitempty"`
	Files []string `json:"files,omitempty"`
	Tail  string   `json:"tail,omitempty"`
	// CausedBy names the other tasks whose changed files a foreign failure points at.
	CausedBy []string `json:"caused_by,omitempty"`
}

// ReportLane is the lane field of a report.
type ReportLane struct {
	Paths     []string `json:"paths"`
	OutOfLane []string `json:"out_of_lane,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Forced    []string `json:"forced,omitempty"`
}

// ReportBar is the bar field of a report: what defines "done" changed since the task started.
type ReportBar struct {
	Changed []string `json:"changed"`
}

// ReportVerifier is the verifier field of a report.
type ReportVerifier struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons,omitempty"`
	// Unverified counts the reasons' file:line citations that do not match the repository.
	Unverified int    `json:"unverified_cites,omitempty"`
	Detail     string `json:"detail,omitempty"`
}
