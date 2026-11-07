package api

// Proof DTOs (milestone 13): the requirement matrix of a Spec Kit feature, the project index, regression runs and
// the gate statistics.

// ProofStatus is how far a requirement (FR) or success criterion (SC) is proven.
type ProofStatus string

// Proof statuses, from least to most proven, then the bad ones.
const (
	// ProofUnmapped: no spec task covers it.
	ProofUnmapped ProofStatus = "unmapped"
	// ProofInProgress: a covering task is not ticked yet.
	ProofInProgress ProofStatus = "in_progress"
	// ProofUnproven: every covering task is ticked, but one has no gate or no green gate result.
	ProofUnproven ProofStatus = "unproven"
	// ProofProven: every covering task is ticked and its last gate result is green.
	ProofProven ProofStatus = "proven"
	// ProofRegressed: a regression run failed a gate that was green before.
	ProofRegressed ProofStatus = "regressed"
	// ProofFailing: the last gate result of a covering task is red or error.
	ProofFailing ProofStatus = "failing"
)

// ProofStatuses lists every status in display order.
func ProofStatuses() []ProofStatus {
	return []ProofStatus{ProofUnmapped, ProofInProgress, ProofUnproven, ProofProven, ProofRegressed, ProofFailing}
}

// ProofCounts counts requirements per status.
type ProofCounts struct {
	Unmapped   int `json:"unmapped"`
	InProgress int `json:"in_progress"`
	Unproven   int `json:"unproven"`
	Proven     int `json:"proven"`
	Regressed  int `json:"regressed"`
	Failing    int `json:"failing"`
}

// Add counts one requirement with status s.
func (c *ProofCounts) Add(s ProofStatus) {
	switch s {
	case ProofUnmapped:
		c.Unmapped++
	case ProofInProgress:
		c.InProgress++
	case ProofUnproven:
		c.Unproven++
	case ProofProven:
		c.Proven++
	case ProofRegressed:
		c.Regressed++
	case ProofFailing:
		c.Failing++
	}
}

// Plus adds the counts of o.
func (c *ProofCounts) Plus(o ProofCounts) {
	c.Unmapped += o.Unmapped
	c.InProgress += o.InProgress
	c.Unproven += o.Unproven
	c.Proven += o.Proven
	c.Regressed += o.Regressed
	c.Failing += o.Failing
}

// ProofMatrix is the requirement matrix of one feature.
type ProofMatrix struct {
	// Feature is the feature folder: relative to the repository root with '/', or absolute when outside it.
	Feature string       `json:"feature"`
	Title   string       `json:"title"`
	Counts  ProofCounts  `json:"counts"`
	Rows    []ProofRow   `json:"rows"`
	Stories []ProofStory `json:"stories"`
	// UncoveredTasks are the spec tasks without a Covers: line.
	UncoveredTasks []string   `json:"uncoveredTasks"`
	Tasks          ProofTally `json:"tasks"`
	// FeatureGate are the commands of the tasks.md "Feature gate:" line.
	FeatureGate []string `json:"featureGate"`
}

// ProofTally counts the spec tasks of a feature.
type ProofTally struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// ProofRow is one requirement or success criterion.
type ProofRow struct {
	ID      string         `json:"id"`
	Kind    string         `json:"kind" ts:"'FR' | 'SC'"`
	Text    string         `json:"text"`
	Status  ProofStatus    `json:"status"`
	Stories []string       `json:"stories"`
	Tasks   []ProofRowTask `json:"tasks"`
}

// ProofRowTask is a spec task covering a requirement, with its last gate result.
type ProofRowTask struct {
	ID    string   `json:"id"`
	Done  bool     `json:"done"`
	Gates []string `json:"gates"`
	// State is the last gate result (green, red, error) or "" when the ledger has none.
	State string `json:"state,omitempty" ts:"'green' | 'red' | 'error'"`
	At    string `json:"at,omitempty"`
	// Source is task (a worker's gate) or regression (a regression run).
	Source string `json:"source,omitempty" ts:"'task' | 'regression'"`
	// Ref is the task id or regression run id of the last result.
	Ref string `json:"ref,omitempty"`
}

// ProofStory is a user story with the counts of the requirements its tasks cover.
type ProofStory struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Priority string      `json:"priority"`
	Counts   ProofCounts `json:"counts"`
}

// ProofFeature is one feature of the project index.
type ProofFeature struct {
	Dir    string      `json:"dir"`
	Title  string      `json:"title"`
	Counts ProofCounts `json:"counts"`
	Tasks  ProofTally  `json:"tasks"`
	// Error says why the feature could not be read (its counts are zero then).
	Error string `json:"error,omitempty"`
}

// ProofIndex is the proof overview of one project: every feature under specs/ and every feature folder the ledger
// knows outside the repository.
type ProofIndex struct {
	Key      string         `json:"key"`
	RepoRoot string         `json:"repoRoot"`
	Totals   ProofCounts    `json:"totals"`
	Features []ProofFeature `json:"features"`
	// Runs are the latest regression runs, newest first.
	Runs []ProofRun `json:"runs"`
}

// Regression run states.
const (
	RunRunning   = "running"
	RunGreen     = "green"
	RunRed       = "red"
	RunError     = "error"
	RunCancelled = "cancelled"
)

// ProofRunCounts counts the results of a regression run.
type ProofRunCounts struct {
	Green int `json:"green"`
	Red   int `json:"red"`
	Error int `json:"error"`
}

// ProofRun is a regression run: every gate command of a project's features (and the doctrine) run again from the
// repository root.
type ProofRun struct {
	ID        string `json:"id"`
	Feature   string `json:"feature,omitempty"`
	State     string `json:"state" ts:"'running' | 'green' | 'red' | 'error' | 'cancelled'"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt,omitempty"`
	GitHead   string `json:"gitHead,omitempty"`
	// Noisy: other tasks of the repository were active (force), so results may reflect their unfinished work.
	Noisy bool `json:"noisy,omitempty"`
	// Dirty: the worktree had uncommitted changes.
	Dirty   bool             `json:"dirty,omitempty"`
	Counts  ProofRunCounts   `json:"counts"`
	Total   int              `json:"total"`
	Results []ProofRunResult `json:"results"`
	Error   string           `json:"error,omitempty"`
}

// ProofRunResult is one command of a regression run.
type ProofRunResult struct {
	Cmd string `json:"cmd"`
	// Keys are the spec tasks (feature#Tnnn) the command proves.
	Keys  []string `json:"keys"`
	Kind  string   `json:"kind" ts:"'task' | 'feature' | 'doctrine'"`
	State string   `json:"state" ts:"'running' | 'green' | 'red' | 'error'"`
	Exit  *int     `json:"exit,omitempty"`
	Ms    int64    `json:"ms"`
	Tail  string   `json:"tail,omitempty"`
}

// ProofRunRequest starts a regression run.
type ProofRunRequest struct {
	Cwd     string `json:"cwd"`
	Feature string `json:"feature,omitempty"`
	// Force runs even while other tasks of the repository are active.
	Force bool `json:"force,omitempty"`
}

// ProofStats are the gate statistics of a project.
type ProofStats struct {
	Since                string             `json:"since,omitempty"`
	Tasks                int                `json:"tasks"`
	GatedTasks           int                `json:"gatedTasks"`
	GateRuns             int                `json:"gateRuns"`
	FirstRunRed          int                `json:"firstRunRed"`
	CaughtBeforeReview   int                `json:"caughtBeforeReview"`
	AutoFixTurns         int                `json:"autoFixTurns"`
	ExhaustedBudgets     int                `json:"exhaustedBudgets"`
	LaneRefusals         int                `json:"laneRefusals"`
	LaneForced           int                `json:"laneForced"`
	LaneViolationsCaught int                `json:"laneViolationsCaught"`
	LeadFollowups        ProofFollowupStats `json:"leadFollowups"`
	Verifier             ProofVerifierStats `json:"verifier"`
}

// ProofFollowupStats is the mean number of lead followups per task, with and without a gate.
type ProofFollowupStats struct {
	Gated   float64 `json:"gated"`
	Ungated float64 `json:"ungated"`
}

// ProofVerifierStats counts the verifier's verdicts.
type ProofVerifierStats struct {
	Runs    int `json:"runs"`
	Accepts int `json:"accepts"`
	Rejects int `json:"rejects"`
	Errors  int `json:"errors"`
	// Slipped counts accepted turns the lead then followed up or rejected.
	Slipped int   `json:"slipped"`
	Tokens  int64 `json:"tokens"`
}
