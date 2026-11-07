package api

import (
	"encoding/json"
	"errors"
	"strings"
)

// The DTOs of milestone 14: department members, consults, review notes, meetings, contests and hooks
// (docs/team.md). Times are FormatTime strings.

// Rank is the standing of a department member: a worker is a witness who cannot be consulted, a senior is an
// adviser (an objection is a warning, advice not taken is recorded), a peer stands level with the lead (an
// objection blocks review accepted, may dissent formally, may judge contests).
type Rank string

// Ranks.
const (
	RankWorker = "worker"
	RankSenior = "senior"
	RankPeer   = "peer"
)

// Ranks lists every rank.
func Ranks() []string { return []string{RankWorker, RankSenior, RankPeer} }

// Member is a department member (GET /members).
type Member struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Backend  string `json:"backend" ts:"'agy' | 'claude' | 'codex'"`
	Provider string `json:"provider,omitempty"`
	Rank     string `json:"rank" ts:"Rank"`
	Tier     string `json:"tier,omitempty" ts:"Tier"`
	Model    string `json:"model,omitempty"`
	// Role is the id of the worker role (profile) the member's tasks use.
	Role              string   `json:"role,omitempty"`
	PreferredAccounts []string `json:"preferredAccounts"`
	// FallbackBackends are tried in order when no account of the member's backend can run its task.
	FallbackBackends []string `json:"fallbackBackends"`
	Enabled          bool     `json:"enabled"`
	// Invalid says why the member cannot be used now (its provider is gone); empty when it can.
	Invalid   string `json:"invalid,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// MemberInput is the body of POST /members (ID optional: made from the name when left out) and, merged onto the
// stored member, of PATCH /members/{id}. Enabled defaults to true.
type MemberInput struct {
	ID                string   `json:"id,omitempty"`
	Name              string   `json:"name"`
	Backend           string   `json:"backend,omitempty" ts:"'agy' | 'claude' | 'codex'"`
	Provider          string   `json:"provider,omitempty"`
	Rank              string   `json:"rank" ts:"Rank"`
	Tier              string   `json:"tier,omitempty" ts:"Tier"`
	Model             string   `json:"model,omitempty"`
	Role              string   `json:"role,omitempty"`
	PreferredAccounts []string `json:"preferredAccounts,omitempty"`
	FallbackBackends  []string `json:"fallbackBackends,omitempty"`
	Enabled           *bool    `json:"enabled,omitempty"`
}

// MemberTrack is the track record of one member (GET /members/track): what its meetings and consults came to.
type MemberTrack struct {
	Member         string `json:"member"`
	Meetings       int    `json:"meetings"`
	Dissents       int    `json:"dissents"`
	Concessions    int    `json:"concessions"`
	Signals        int    `json:"signals"`
	Vindicated     int    `json:"vindicated"`
	Dismissed      int    `json:"dismissed"`
	AdviceNotTaken int    `json:"adviceNotTaken"`
	Consults       int    `json:"consults"`
	Objections     int    `json:"objections"`
}

// Citation is a [path:line] reference in a member's answer, checked against the repository (proof.CheckCite).
// Variant is the contest variant (V1..) a judge's citation is about.
type Citation struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	Quote   string `json:"quote,omitempty"`
	State   string `json:"state" ts:"'verified' | 'located' | 'quote_mismatch' | 'bad_range' | 'missing_file' | 'outside_repo'"`
	Detail  string `json:"detail,omitempty"`
	Variant string `json:"variant,omitempty"`
}

// Claim is one statement of a member with the citations that support it.
type Claim struct {
	Text  string     `json:"text"`
	Cites []Citation `json:"cites"`
}

// Consult verdicts.
const (
	ConsultApprove  = "approve"
	ConsultConcerns = "concerns"
	ConsultObject   = "object"
)

// Consult is a second opinion on a finished task from a senior or peer member (POST /tasks/{id}/consult).
type Consult struct {
	ID        string   `json:"id"`
	Member    string   `json:"member"`
	Rank      string   `json:"rank" ts:"Rank"`
	RunTaskID string   `json:"runTaskId"`
	Status    string   `json:"status" ts:"'running' | 'done' | 'failed'"`
	Verdict   string   `json:"verdict,omitempty" ts:"'approve' | 'concerns' | 'object'"`
	Findings  []Claim  `json:"findings"`
	Required  []string `json:"required"`
	Focus     string   `json:"focus,omitempty"`
	At        string   `json:"at"`
	EndedAt   string   `json:"endedAt,omitempty"`
	Error     string   `json:"error,omitempty"`
	// AnsweredObjection is the lead's answer to a peer objection, given with review accepted.
	AnsweredObjection *ObjectionAnswer `json:"answeredObjection"`
}

// ObjectionAnswer is the lead's written answer to a peer objection.
type ObjectionAnswer struct {
	Text string `json:"text"`
	At   string `json:"at"`
}

// ConsultRequest is the body of POST /tasks/{id}/consult.
type ConsultRequest struct {
	Member string `json:"member"`
	Focus  string `json:"focus,omitempty"`
}

// Review note states (stored) and statuses (computed from the task and the current file).
const (
	NoteOpen     = "open"
	NoteSent     = "sent"
	NoteResolved = "resolved"
	NoteOutdated = "outdated"

	NoteStatusDraft       = "draft"
	NoteStatusOutdated    = "outdated"
	NoteStatusSent        = "sent"
	NoteStatusAddressed   = "addressed"
	NoteStatusUnaddressed = "unaddressed"
	NoteStatusResolved    = "resolved"
)

// Note is a review note on a line of a task's diff (GET /tasks/{id}/notes).
type Note struct {
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
	// File is relative to the repository root, with '/'.
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	Side    string `json:"side" ts:"'new' | 'old'"`
	// AnchorText is the text of the line when the note was written; the note follows it across turns.
	AnchorText string `json:"anchorText"`
	Body       string `json:"body"`
	By         string `json:"by" ts:"'user' | 'lead'"`
	State      string `json:"state" ts:"'open' | 'sent' | 'resolved' | 'outdated'"`
	// Status is computed: draft (sent next time), outdated (its line changed before it was sent), sent (its turn
	// runs), addressed (the turn ended and the line changed), unaddressed (the line is unchanged: sent again),
	// resolved.
	Status string `json:"status" ts:"'draft' | 'outdated' | 'sent' | 'addressed' | 'unaddressed' | 'resolved'"`
	// CurrentLine is where the note's line is now: 0 when its anchored text is gone; Line when it cannot be told
	// (a removed line, no anchor text).
	CurrentLine int    `json:"currentLine"`
	AnchorTurn  int    `json:"anchorTurn"`
	SentInTurn  int    `json:"sentInTurn,omitempty"`
	SentAt      string `json:"sentAt,omitempty"`
	CreatedAt   string `json:"createdAt"`
	ResolvedAt  string `json:"resolvedAt,omitempty"`
}

// NoteInput is the body of POST /tasks/{id}/notes. AnchorText is read from the file when left out.
type NoteInput struct {
	File       string `json:"file"`
	Line       int    `json:"line"`
	EndLine    int    `json:"endLine,omitempty"`
	Side       string `json:"side,omitempty" ts:"'new' | 'old'"`
	AnchorText string `json:"anchorText,omitempty"`
	Body       string `json:"body"`
	By         string `json:"by,omitempty" ts:"'user' | 'lead'"`
}

// NotePatch is the body of PATCH /tasks/{id}/notes/{noteId}: a new body, or state resolved / open (reopen).
type NotePatch struct {
	Body  *string `json:"body,omitempty"`
	State string  `json:"state,omitempty" ts:"'resolved' | 'open'"`
}

// NotesSendRequest is the body of POST /tasks/{id}/notes/send: the draft and unaddressed notes go to the worker
// as one followup, with Extra after them.
type NotesSendRequest struct {
	Extra string `json:"extra,omitempty"`
	By    string `json:"by,omitempty" ts:"'user' | 'lead'"`
}

// NotesSendResult is the answer of POST /tasks/{id}/notes/send.
type NotesSendResult struct {
	Sent []string    `json:"sent"`
	Task TaskSummary `json:"task"`
}

// NotesChanged is the data of notes.changed: refetch the notes of the task.
type NotesChanged struct {
	TaskID string `json:"taskId"`
}

// Meeting statuses.
const (
	MeetingStatusRound       = "round"
	MeetingStatusWaitingLead = "waiting_lead"
	MeetingStatusDeciding    = "deciding"
	MeetingStatusDissent     = "dissent"
	MeetingStatusClosed      = "closed"
	MeetingStatusCancelled   = "cancelled"
	MeetingStatusFailed      = "failed"
)

// Meeting is a structured discussion of department members before a decision (GET /meetings/{id}).
type Meeting struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Question  string          `json:"question"`
	Options   []MeetingOption `json:"options"`
	Context   []string        `json:"context"`
	Cwd       string          `json:"cwd"`
	RepoRoot  string          `json:"repoRoot"`
	Status    string          `json:"status" ts:"'round' | 'waiting_lead' | 'deciding' | 'dissent' | 'closed' | 'cancelled' | 'failed'"`
	MaxRounds int             `json:"maxRounds"`
	Round     int             `json:"round"`
	// Sealed is the lead's own position, hashed at open and shown to the attendees from round 2 under a label.
	Sealed         *SealedPosition  `json:"sealed"`
	Attendees      []Attendee       `json:"attendees"`
	Rounds         []MeetingRound   `json:"rounds"`
	LeadQA         []LeadQuestion   `json:"leadQa"`
	LeadNotes      []LeadNote       `json:"leadNotes"`
	Decision       *MeetingDecision `json:"decision"`
	Dissents       []Dissent        `json:"dissents"`
	AdviceNotTaken []AdviceNotTaken `json:"adviceNotTaken"`
	Actions        []MeetingAction  `json:"actions"`
	Estimate       *RunEstimate     `json:"estimate"`
	Usage          MeetingUsage     `json:"usage"`
	DissentRuns    []MeetingRun     `json:"dissentRuns"`
	Error          string           `json:"error,omitempty"`
	CreatedAt      string           `json:"createdAt"`
	UpdatedAt      string           `json:"updatedAt"`
	// Minutes is the record in English markdown (the MCP tool shows it to the lead); the dashboard draws the
	// structured fields instead.
	Minutes string `json:"minutes,omitempty"`
}

// MeetingOption is one candidate answer, keyed A..F.
type MeetingOption struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// SealedPosition is the lead's sealed position.
type SealedPosition struct {
	Label         string `json:"label"`
	Text          string `json:"text"`
	Hash          string `json:"hash"`
	RevealedRound int    `json:"revealedRound"`
}

// Attendee is a member at a meeting under its neutral label (P1..).
type Attendee struct {
	Label    string `json:"label"`
	Member   string `json:"member"`
	Name     string `json:"name,omitempty"`
	Rank     string `json:"rank" ts:"Rank"`
	Backend  string `json:"backend"`
	Model    string `json:"model,omitempty"`
	Tier     string `json:"tier,omitempty"`
	Provider string `json:"provider,omitempty"`
	// TaskID is the attendee's hidden read-only run (kind meeting); each round is a turn of it.
	TaskID  string `json:"taskId,omitempty"`
	Present bool   `json:"present"`
}

// MeetingRound is one round: the runs it started and the answers they gave.
type MeetingRound struct {
	N         int             `json:"n"`
	StartedAt string          `json:"startedAt"`
	EndedAt   string          `json:"endedAt,omitempty"`
	Runs      []MeetingRun    `json:"runs"`
	Answers   []MeetingAnswer `json:"answers"`
}

// MeetingRun is the turn of an attendee's run that answers a round (or the dissent question).
type MeetingRun struct {
	Label  string `json:"label"`
	TaskID string `json:"taskId"`
	Turn   int    `json:"turn"`
	Done   bool   `json:"done,omitempty"`
}

// MeetingAnswer is an attendee's parsed answer in one round; Absent when the run failed or gave no answer.
type MeetingAnswer struct {
	Label  string `json:"label"`
	Absent bool   `json:"absent,omitempty"`
	// AbsentReason says why an absent attendee gave no answer (AbsentFailed, AbsentCancelled, AbsentGone); Error
	// is the detail. Answers recorded before it existed have none.
	AbsentReason string   `json:"absentReason,omitempty" ts:"'failed' | 'cancelled' | 'gone'"`
	Error        string   `json:"error,omitempty"`
	Option       string   `json:"option,omitempty"`
	Stance       string   `json:"stance"`
	Confidence   *int     `json:"confidence"`
	Arguments    []string `json:"arguments"`
	Claims       []Claim  `json:"claims"`
	Risks        []string `json:"risks"`
	Actions      []string `json:"actions"`
	Questions    []string `json:"questions"`
	Rebuttal     string   `json:"rebuttal,omitempty"`
	Moved        string   `json:"moved,omitempty" ts:"'yes' | 'no'"`
	// EditedFiles are files the read-only run changed anyway (shown as a warning).
	EditedFiles []string `json:"editedFiles,omitempty"`
}

// Why an attendee gave no answer (MeetingAnswer.AbsentReason).
const (
	AbsentFailed    = "failed"    // the run failed or could not start
	AbsentCancelled = "cancelled" // the run was cancelled
	AbsentGone      = "gone"      // the run or its turn no longer exists
)

// LeadQuestion is a question an attendee asked the lead, with the lead's answer.
type LeadQuestion struct {
	ID         string `json:"id"`
	Round      int    `json:"round"`
	Label      string `json:"label"`
	Question   string `json:"question"`
	Answer     string `json:"answer,omitempty"`
	AnsweredAt string `json:"answeredAt,omitempty"`
}

// LeadNote is a note the lead shares with every attendee in a round.
type LeadNote struct {
	Round int    `json:"round"`
	Text  string `json:"text"`
	At    string `json:"at"`
}

// MeetingDecision is the lead's decision.
type MeetingDecision struct {
	Text      string `json:"text"`
	Rationale string `json:"rationale,omitempty"`
	Option    string `json:"option,omitempty"`
	At        string `json:"at"`
}

// Dissent is a peer's formal dissent from the decision, or its concession.
type Dissent struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	Member string  `json:"member"`
	Kind   string  `json:"kind" ts:"'dissent' | 'concede' | 'absent'"`
	Text   string  `json:"text"`
	Claims []Claim `json:"claims"`
	// WatchPaths are the files the dissent cites; later rejected tasks or red gates on them are its signals.
	WatchPaths []string        `json:"watchPaths"`
	Signals    []DissentSignal `json:"signals"`
	Outcome    string          `json:"outcome,omitempty" ts:"'vindicated' | 'dismissed'"`
	OutcomeAt  string          `json:"outcomeAt,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// Dissent kinds: a formal dissent, a concession, or a dissent turn that gave no answer (the peer was absent).
const (
	DissentFormal  = "dissent"
	DissentConcede = "concede"
	DissentAbsent  = "absent"
)

// DissentSignal is a later task on a watched file that went wrong.
type DissentSignal struct {
	TaskID string `json:"taskId"`
	Reason string `json:"reason" ts:"'rejected' | 'gate_red'"`
	At     string `json:"at"`
}

// AdviceNotTaken is a senior's final stance that the decision went against.
type AdviceNotTaken struct {
	Label  string `json:"label"`
	Member string `json:"member"`
	Stance string `json:"stance"`
}

// MeetingAction is a task created from the meeting's decision.
type MeetingAction struct {
	Text   string `json:"text"`
	TaskID string `json:"taskId"`
	Member string `json:"member,omitempty"`
}

// RunEstimate is how many worker runs a meeting takes at most, per backend.
type RunEstimate struct {
	Runs      int            `json:"runs"`
	ByBackend map[string]int `json:"byBackend"`
}

// MeetingUsage is what a meeting used so far.
type MeetingUsage struct {
	Runs      int            `json:"runs"`
	ByBackend map[string]int `json:"byBackend"`
	Tokens    int64          `json:"tokens"`
}

// MeetingSummary is a meeting in lists and in meeting.updated.
type MeetingSummary struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	Status           string `json:"status" ts:"'round' | 'waiting_lead' | 'deciding' | 'dissent' | 'closed' | 'cancelled' | 'failed'"`
	Round            int    `json:"round"`
	MaxRounds        int    `json:"maxRounds"`
	Attendees        int    `json:"attendees"`
	PendingQuestions int    `json:"pendingQuestions"`
	Dissents         int    `json:"dissents"`
	Cwd              string `json:"cwd"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

// AttendeeRef names an attendee in MeetingRequest: a member id, "id:rank", or {member, rank}.
type AttendeeRef struct {
	Member string `json:"member"`
	Rank   string `json:"rank,omitempty" ts:"Rank"`
}

// UnmarshalJSON accepts "id", "id:rank" or {member, rank}.
func (a *AttendeeRef) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		id, rank, _ := strings.Cut(s, ":")
		a.Member, a.Rank = strings.TrimSpace(id), strings.TrimSpace(rank)
		return nil
	}
	type plain AttendeeRef
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return errors.New("an attendee is a member id or {member, rank}")
	}
	*a = AttendeeRef(p)
	return nil
}

// MeetingRequest is the body of POST /meetings and POST /meetings/estimate.
type MeetingRequest struct {
	Question  string        `json:"question"`
	Title     string        `json:"title,omitempty"`
	Options   []string      `json:"options,omitempty"`
	Context   []string      `json:"context,omitempty"`
	Cwd       string        `json:"cwd"`
	Attendees []AttendeeRef `json:"attendees" ts:"Array<string | AttendeeRef>"`
	Sealed    string        `json:"sealed,omitempty"`
	MaxRounds int           `json:"maxRounds,omitempty"`
}

// QuestionAnswer answers a lead question (POST /meetings/{id}/post).
type QuestionAnswer struct {
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

// MeetingPostRequest is the body of POST /meetings/{id}/post.
type MeetingPostRequest struct {
	Answers []QuestionAnswer `json:"answers"`
	Note    string           `json:"note,omitempty"`
}

// MeetingDecideRequest is the body of POST /meetings/{id}/decide.
type MeetingDecideRequest struct {
	Decision  string `json:"decision"`
	Rationale string `json:"rationale,omitempty"`
	Option    string `json:"option,omitempty"`
}

// ActItem is one task to create from a decision.
type ActItem struct {
	Text    string `json:"text"`
	Member  string `json:"member,omitempty"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
}

// MeetingActRequest is the body of POST /meetings/{id}/act.
type MeetingActRequest struct {
	Items []ActItem `json:"items"`
}

// MeetingActResult is the answer of POST /meetings/{id}/act.
type MeetingActResult struct {
	Meeting Meeting  `json:"meeting"`
	Tasks   []string `json:"tasks"`
}

// DissentOutcomeRequest is the body of POST /meetings/{id}/dissents/{dissentId}/outcome.
type DissentOutcomeRequest struct {
	Outcome string `json:"outcome" ts:"'vindicated' | 'dismissed'"`
}

// TeamLogEntry is one step of a meeting or contest (GET /meetings/{id}/events, /contests/{id}/events).
type TeamLogEntry struct {
	ID   int64          `json:"id"`
	At   string         `json:"at"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// Contest statuses.
const (
	ContestStatusStarting  = "starting"
	ContestStatusRunning   = "running"
	ContestStatusJudging   = "judging"
	ContestStatusReady     = "ready"
	ContestStatusApplied   = "applied"
	ContestStatusCancelled = "cancelled"
	ContestStatusFailed    = "failed"
)

// VariantSpec is what one contest variant runs on; empty fields take the contest's (or a default that makes the
// variants differ).
type VariantSpec struct {
	Backend  string `json:"backend,omitempty" ts:"'agy' | 'claude' | 'codex'"`
	Model    string `json:"model,omitempty"`
	Member   string `json:"member,omitempty"`
	Provider string `json:"provider,omitempty"`
}

// ContestRequest is the body of POST /contests (and /contests/estimate): a delegate whose brief runs as 2..5
// variants, each in its own git worktree.
type ContestRequest struct {
	DelegateRequest
	Variants     int           `json:"variants,omitempty"`
	VariantSpecs []VariantSpec `json:"variantSpecs,omitempty"`
	// Judge is the member id of the judge (default: the first enabled peer); NoJudge turns the judge off.
	Judge           string `json:"judge,omitempty"`
	NoJudge         bool   `json:"noJudge,omitempty"`
	DeadlineMinutes int    `json:"deadlineMinutes,omitempty"`
}

// Contest is a contest (GET /contests/{id}).
type Contest struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status" ts:"'starting' | 'running' | 'judging' | 'ready' | 'applied' | 'cancelled' | 'failed'"`
	// Cwd is the user's folder, Root its repository and Sub the folder relative to the root.
	Cwd             string           `json:"cwd"`
	Root            string           `json:"root"`
	Sub             string           `json:"sub,omitempty"`
	Prompt          string           `json:"prompt"`
	Judge           string           `json:"judgeMember,omitempty"`
	NoJudge         bool             `json:"noJudge,omitempty"`
	DeadlineMinutes int              `json:"deadlineMinutes"`
	DeadlineAt      string           `json:"deadlineAt,omitempty"`
	DeadlineHit     bool             `json:"deadlineHit,omitempty"`
	Estimate        *ContestEstimate `json:"estimate"`
	// BaseSha is the commit of the snapshot of the working tree every variant starts from.
	BaseSha        string            `json:"baseSha,omitempty"`
	Variants       []ContestVariant  `json:"variants"`
	Scoreboard     []ScoreRow        `json:"scoreboard"`
	MachineRanking []string          `json:"machineRanking"`
	JudgeRun       *ContestJudge     `json:"judge"`
	Proposal       *ContestProposal  `json:"proposal"`
	Scorings       int               `json:"scorings"`
	ScoredAt       string            `json:"scoredAt,omitempty"`
	Winner         string            `json:"winner,omitempty"`
	Applied        *ContestApplied   `json:"applied"`
	LastPickError  *ContestPickError `json:"lastPickError"`
	Warnings       []string          `json:"warnings"`
	Error          string            `json:"error,omitempty"`
	CancelledAt    string            `json:"cancelledAt,omitempty"`
	CleanedAt      string            `json:"cleanedAt,omitempty"`
	CreatedAt      string            `json:"createdAt"`
	UpdatedAt      string            `json:"updatedAt"`
}

// ContestEstimate is what the pool can run: the variants asked for, granted and dropped.
type ContestEstimate struct {
	Requested int                `json:"requested"`
	Granted   int                `json:"granted"`
	Reduced   bool               `json:"reduced"`
	Dropped   []DroppedVariant   `json:"dropped"`
	Pools     map[string]PoolCap `json:"pools"`
	// Refused is why the contest cannot start (estimate only).
	Refused string `json:"refused,omitempty"`
}

// PoolCap is the capacity of one account pool (a backend, or provider:<id>).
type PoolCap struct {
	Accounts int `json:"accounts"`
	Usable   int `json:"usable"`
	Slots    int `json:"slots"`
	// Best is the best remaining quota fraction of the pool's accounts (null when unknown).
	Best *float64 `json:"best"`
}

// DroppedVariant is a variant the pool cannot run in parallel.
type DroppedVariant struct {
	Label  string `json:"label"`
	Pool   string `json:"pool"`
	Reason string `json:"reason"`
}

// ContestVariant is one variant: its spec, task and worktree.
type ContestVariant struct {
	Label    string      `json:"label"`
	Spec     VariantSpec `json:"spec"`
	TaskID   string      `json:"taskId,omitempty"`
	Worktree string      `json:"worktree,omitempty"`
	// Cwd is the variant's folder inside its worktree (the user's folder mirrored).
	Cwd               string       `json:"cwd,omitempty"`
	Env               *WorktreeEnv `json:"env"`
	Removed           bool         `json:"removed"`
	Commit            string       `json:"commit,omitempty"`
	DeadlineCancelled bool         `json:"deadlineCancelled,omitempty"`
	CleanupError      string       `json:"cleanupError,omitempty"`
	// Status, Backend, Model and Member come from the variant's task.
	Status  string `json:"status,omitempty"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
	Member  string `json:"member,omitempty"`
}

// WorktreeEnv is what a new worktree got from the main checkout: gitignored files copied from .worktreeinclude,
// shared folders linked, and what was skipped and why.
type WorktreeEnv struct {
	Copied  []string  `json:"copied"`
	Linked  []string  `json:"linked"`
	Skipped []EnvSkip `json:"skipped"`
}

// EnvSkip is a path the worktree environment left out.
type EnvSkip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ScoreRow is the machine score of a variant.
type ScoreRow struct {
	Label         string   `json:"label"`
	TaskID        string   `json:"taskId"`
	Status        string   `json:"status"`
	Backend       string   `json:"backend,omitempty"`
	Model         string   `json:"model,omitempty"`
	Member        string   `json:"member,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	Gate          string   `json:"gate"`
	AutoFixTurns  int      `json:"autoFixTurns"`
	Files         int      `json:"files"`
	FileList      []string `json:"fileList"`
	Added         int      `json:"added"`
	Removed       int      `json:"removed"`
	OutOfLane     int      `json:"outOfLane"`
	LaneViolation bool     `json:"laneViolation"`
	Tokens        int64    `json:"tokens"`
	DurationMs    *int64   `json:"durationMs"`
	Verifier      string   `json:"verifier,omitempty"`
	// Disqualified says why the variant cannot win: task_missing, failed, deadline, cancelled, rejected,
	// lane_violation, gate_red or no_changes.
	Disqualified string `json:"disqualified,omitempty"`
}

// ContestJudge is the judge's run and verdict. Letters maps the anonymous letters (A..C) to variant labels.
type ContestJudge struct {
	Status        string            `json:"status" ts:"'running' | 'done' | 'failed'"`
	Member        string            `json:"member"`
	RunTaskID     string            `json:"runTaskId,omitempty"`
	Letters       map[string]string `json:"letters"`
	Winner        string            `json:"winner,omitempty"`
	WinnerLabel   string            `json:"winnerLabel,omitempty"`
	Ranking       []string          `json:"ranking"`
	RankingLabels []string          `json:"rankingLabels"`
	Reasons       []Claim           `json:"reasons"`
	Verified      int               `json:"verified"`
	Error         string            `json:"error,omitempty"`
	At            string            `json:"at"`
	EndedAt       string            `json:"endedAt,omitempty"`
}

// ContestProposal is the winner Seelegent proposes: the judge's pick when it is qualified and backed by a
// verified citation, else the machine's.
type ContestProposal struct {
	Winner        string `json:"winner"`
	Source        string `json:"source" ts:"'judge' | 'machine'"`
	MachineWinner string `json:"machineWinner,omitempty"`
	// Reason says in English why the machine's winner is proposed; ReasonCode is the same for the dashboard.
	Reason     string `json:"reason,omitempty"`
	ReasonCode string `json:"reasonCode,omitempty" ts:"'unverified' | 'judge_failed' | 'judge_running'"`
}

// ContestApplied records the winner's patch applied to the user's working tree.
type ContestApplied struct {
	Commit string   `json:"commit"`
	Files  []string `json:"files"`
	By     string   `json:"by" ts:"'lead' | 'user'"`
	At     string   `json:"at"`
}

// ContestPickError is the last pick that did not apply.
type ContestPickError struct {
	Winner string `json:"winner"`
	Detail string `json:"detail"`
	At     string `json:"at"`
}

// ContestSummary is a contest in lists and in contest.updated.
type ContestSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status" ts:"'starting' | 'running' | 'judging' | 'ready' | 'applied' | 'cancelled' | 'failed'"`
	Cwd        string `json:"cwd"`
	Variants   int    `json:"variants"`
	Proposal   string `json:"proposal,omitempty"`
	Winner     string `json:"winner,omitempty"`
	DeadlineAt string `json:"deadlineAt,omitempty"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// ContestPickRequest is the body of POST /contests/{id}/pick (Winner defaults to the proposal).
type ContestPickRequest struct {
	Winner string `json:"winner,omitempty"`
	By     string `json:"by,omitempty" ts:"'lead' | 'user'"`
}

// ContestDiff is GET /contests/{id}/variants/{label}/diff.
type ContestDiff struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Base  string `json:"base"`
	Diff  string `json:"diff"`
	// Files is the same diff parsed per file (contract §3.3: the dashboard never reads unified diff text).
	Files []FileDiff `json:"files"`
	// Truncated is true when the diff was cut at its size limit.
	Truncated bool `json:"truncated"`
}

// Hook events.
const (
	HookTaskCreated      = "task.created"
	HookTaskStarted      = "task.started"
	HookTaskBlocked      = "task.blocked"
	HookTaskUnblocked    = "task.unblocked"
	HookTaskDone         = "task.done"
	HookTaskFailed       = "task.failed"
	HookTaskReviewed     = "task.reviewed"
	HookTaskCancelled    = "task.cancelled"
	HookTaskTakeover     = "task.takeover"
	HookTaskHandback     = "task.handback"
	HookAccountExhausted = "account.exhausted"
	HookAccountLogin     = "account.needs_login"
	HookDaemonStarted    = "daemon.started"
	HookDaemonReattached = "daemon.reattached"
	HookTest             = "hook.test"
)

// HookEvents lists the events a hook can name.
func HookEvents() []string {
	return []string{HookTaskCreated, HookTaskStarted, HookTaskBlocked, HookTaskUnblocked, HookTaskDone,
		HookTaskFailed, HookTaskReviewed, HookTaskCancelled, HookTaskTakeover, HookTaskHandback,
		HookAccountExhausted, HookAccountLogin, HookDaemonStarted, HookDaemonReattached}
}

// Hook is a command the daemon runs on events (GET /hooks). It gets the event as JSON on stdin and
// SEELEGENT_EVENT, SEELEGENT_TASK_ID and SEELEGENT_PORT in its environment.
type Hook struct {
	// ID is stable: PUT /hooks keeps the ids it gets and gives new hooks one.
	ID   string   `json:"id,omitempty"`
	Name string   `json:"name,omitempty"`
	On   []string `json:"on"`
	Run  string   `json:"run"`
	// Project limits the hook to tasks of this folder (absolute; empty: every project).
	Project        string `json:"project,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	Enabled        bool   `json:"enabled"`
}

// HooksRequest is the body of PUT /hooks.
type HooksRequest struct {
	Hooks []Hook `json:"hooks"`
}

// HookRun is one run of a hook (POST /hooks/{id}/test, hook.ran).
type HookRun struct {
	HookID    string `json:"hookId"`
	Event     string `json:"event"`
	TaskID    string `json:"taskId,omitempty"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
	ExitCode  *int   `json:"exitCode"`
	TimedOut  bool   `json:"timedOut"`
	// Output is the start of stdout and stderr (at most 4 KiB, secrets masked).
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
	Test   bool   `json:"test"`
}
