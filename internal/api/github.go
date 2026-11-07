package api

// GitHub issues to pull requests (milestone 15, docs/github.md): connected repositories, the issue inbox, tickets
// (one issue worked on in its own worktree and branch) and the write-ahead outbox of everything that leaves the
// machine.

// GitHub access tiers.
const (
	// TierGh: the gh CLI is installed and signed in; API calls go through `gh api`.
	TierGh = "gh"
	// TierToken: a token pasted in the dashboard; API calls go to the REST API of the repository's GitHub host.
	TierToken = "token"
	// TierManual: neither; reads of public data may work, every write is refused or becomes a link to open.
	TierManual = "manual"
)

// GitHubAccess is how Seelegent reaches GitHub. The token itself never leaves the daemon: only its hint.
type GitHubAccess struct {
	Tier      string `json:"tier" ts:"'gh' | 'token' | 'manual'"`
	GhVersion string `json:"ghVersion,omitempty"`
	GhUser    string `json:"ghUser,omitempty"`
	// TokenHint is "…" and the token's last four characters when a token is stored.
	TokenHint string `json:"tokenHint,omitempty"`
	// Hosts are the hosts treated as GitHub: github.com and the enterprise hosts of the settings. A remote on any
	// other host is not GitHub, and no token is ever sent to it.
	Hosts     []string `json:"hosts"`
	CheckedAt string   `json:"checkedAt"`
}

// GitHubTokenRequest is the body of PUT /github/token (dashboard only).
type GitHubTokenRequest struct {
	Token string `json:"token"`
}

// GitHubTokenResult answers PUT /github/token: only the hint.
type GitHubTokenResult struct {
	TokenHint string `json:"tokenHint"`
}

// RepoCounts are a repository's issue and ticket counts by board column.
type RepoCounts struct {
	Inbox     int `json:"inbox"`
	Working   int `json:"working"`
	WaitingPr int `json:"waitingPr"`
	Sent      int `json:"sent"`
	Skipped   int `json:"skipped"`
}

// Outbox entry kinds.
const (
	OutboxCommit       = "commit"
	OutboxPush         = "push"
	OutboxOpenPR       = "open_pr"
	OutboxComment      = "comment"
	OutboxCreateRemote = "create_remote"
)

// Outbox entry states: pending, then running (saved before the action), then done or failed; an entry left running
// by a daemon that stopped is unknown and re-checked before any retry.
const (
	OutboxPending = "pending"
	OutboxRunning = "running"
	OutboxDone    = "done"
	OutboxFailed  = "failed"
	OutboxUnknown = "unknown"
)

// OutboxEntry is one action that leaves the machine (or changes the ticket's branch), written before it runs.
type OutboxEntry struct {
	ID     int64          `json:"id"`
	Kind   string         `json:"kind" ts:"'commit' | 'push' | 'open_pr' | 'comment' | 'create_remote'"`
	Status string         `json:"status" ts:"'pending' | 'running' | 'done' | 'failed' | 'unknown'"`
	Args   map[string]any `json:"args"`
	Result map[string]any `json:"result"`
	Error  string         `json:"error,omitempty"`
	At     string         `json:"at"`
	DoneAt string         `json:"doneAt,omitempty"`
}

// Repo is a connected repository.
type Repo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	// Owner, Repo and Host name the GitHub repository of origin; empty when origin is missing or not on a GitHub
	// host (GitHub false, NotGitHub says why).
	Owner     string `json:"owner,omitempty"`
	Repo      string `json:"repo,omitempty"`
	Host      string `json:"host,omitempty"`
	GitHub    bool   `json:"github"`
	NotGitHub string `json:"notGithub,omitempty"`
	// Remote is "origin" when the repository has one; RemoteURL is its URL without credentials.
	Remote        string        `json:"remote,omitempty"`
	RemoteURL     string        `json:"remoteUrl,omitempty"`
	DefaultBranch string        `json:"defaultBranch"`
	SharedDirs    []string      `json:"sharedDirs"`
	CreatedAt     string        `json:"createdAt"`
	LastSyncAt    string        `json:"lastSyncAt,omitempty"`
	SyncError     string        `json:"syncError,omitempty"`
	Counts        RepoCounts    `json:"counts"`
	Outbox        []OutboxEntry `json:"outbox"`
}

// RepoConnectRequest is the body of POST /github/repos: connect the repository at path, or make one there first
// (init).
type RepoConnectRequest struct {
	Path string `json:"path"`
	Init bool   `json:"init,omitempty"`
	Name string `json:"name,omitempty"`
	// FirstCommit (init, default true) makes an empty first commit when the repository has none.
	FirstCommit *bool `json:"firstCommit,omitempty"`
}

// RepoPatch is the body of PATCH /github/repos/{id}: the shared folders linked into new ticket worktrees ([] or
// null clears them).
type RepoPatch struct {
	SharedDirs []string `json:"sharedDirs"`
}

// RemoteRequest is the body of POST /github/repos/{id}/remote: create the GitHub repository (gh or token tier).
type RemoteRequest struct {
	Name    string `json:"name,omitempty"`
	Private *bool  `json:"private,omitempty"`
}

// SyncResult is what an inbox sync changed.
type SyncResult struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Closed  int `json:"closed"`
	// Skipped says why nothing was fetched (no_github: origin is not a GitHub repository).
	Skipped string `json:"skipped,omitempty"`
}

// Triage actions.
const (
	TriageAssign  = "assign"
	TriageMeeting = "meeting"
	TriageSkip    = "skip"
)

// IssueSuggestion is the lead's suggestion for an issue (no side effects).
type IssueSuggestion struct {
	Action string `json:"action" ts:"'assign' | 'meeting' | 'skip'"`
	Member string `json:"member,omitempty"`
	Reason string `json:"reason"`
	At     string `json:"at"`
}

// IssueDecision is the triage decision on an issue.
type IssueDecision struct {
	Action   string `json:"action" ts:"'assign' | 'meeting' | 'skip'"`
	By       string `json:"by" ts:"'lead' | 'user'"`
	Reason   string `json:"reason,omitempty"`
	At       string `json:"at"`
	TicketID string `json:"ticketId,omitempty"`
}

// Issue is an inbox entry: a GitHub issue (key gh#<n>) or a local one (local#<n>). Its text is untrusted.
type Issue struct {
	Key        string           `json:"key"`
	Number     int              `json:"number,omitempty"`
	LocalID    int              `json:"localId,omitempty"`
	Source     string           `json:"source" ts:"'github' | 'local'"`
	Title      string           `json:"title"`
	Body       string           `json:"body"`
	Labels     []string         `json:"labels"`
	Author     string           `json:"author,omitempty"`
	URL        string           `json:"url,omitempty"`
	State      string           `json:"state" ts:"'open' | 'closed'"`
	UpdatedAt  string           `json:"updatedAt"`
	Suggestion *IssueSuggestion `json:"suggestion"`
	Decision   *IssueDecision   `json:"decision"`
	// Ticket is the issue's latest ticket (null when none).
	Ticket *TicketSummary `json:"ticket"`
}

// IssueInput is the body of POST /github/repos/{id}/issues: a local issue.
type IssueInput struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

// SuggestRequest is the body of POST /github/repos/{id}/issues/{key}/suggest.
type SuggestRequest struct {
	Action string `json:"action" ts:"'assign' | 'meeting' | 'skip'"`
	Member string `json:"member,omitempty"`
	Reason string `json:"reason"`
}

// TicketTaskOptions are the worker options of a ticket task (triage assign, add task, fix checks), as in
// DelegateRequest.
type TicketTaskOptions struct {
	Member       string   `json:"member,omitempty"`
	Backend      string   `json:"backend,omitempty"`
	Model        string   `json:"model,omitempty"`
	Gate         *bool    `json:"gate,omitempty"`
	GateCommands []string `json:"gateCommands,omitempty"`
	// Lane defaults to the whole worktree; NoLane drops it.
	Lane   []string `json:"lane,omitempty"`
	NoLane bool     `json:"noLane,omitempty"`
}

// TriageRequest is the body of POST /github/repos/{id}/issues/{key}/triage.
type TriageRequest struct {
	Action string `json:"action" ts:"'assign' | 'meeting' | 'skip'"`
	By     string `json:"by,omitempty" ts:"'lead' | 'user'"`
	Reason string `json:"reason,omitempty"`
	TicketTaskOptions
	// Prompt is the lead's brief (assign); Seelegent adds the issue as fenced untrusted text and the worktree rules.
	Prompt string `json:"prompt,omitempty"`
	// Question, Attendees, Options and Context open the meeting (meeting).
	Question  string        `json:"question,omitempty"`
	Attendees []AttendeeRef `json:"attendees,omitempty" ts:"Array<string | AttendeeRef>"`
	Options   []string      `json:"options,omitempty"`
	Context   []string      `json:"context,omitempty"`
}

// TriageResult answers a triage: the issue, its new ticket and the task or meeting started in it.
type TriageResult struct {
	Issue     Issue          `json:"issue"`
	Ticket    *TicketSummary `json:"ticket"`
	TaskID    string         `json:"taskId,omitempty"`
	MeetingID string         `json:"meetingId,omitempty"`
}

// Ticket statuses. working, review and ready follow the ticket's tasks.
const (
	TicketWorking = "working"
	TicketReview  = "review"
	TicketReady   = "ready"
	TicketPROpen  = "pr_open"
	TicketDone    = "done"
	TicketDropped = "dropped"
	TicketFailed  = "failed"
)

// TicketIssue names the issue of a ticket.
type TicketIssue struct {
	Number int    `json:"number,omitempty"`
	Title  string `json:"title"`
	URL    string `json:"url,omitempty"`
}

// TicketPR is the pull request of a ticket. ManualURL is the compare link of the manual tier.
type TicketPR struct {
	Number    int    `json:"number,omitempty"`
	URL       string `json:"url,omitempty"`
	State     string `json:"state,omitempty" ts:"'open' | 'closed' | 'merged'"`
	Draft     bool   `json:"draft"`
	HeadSha   string `json:"headSha,omitempty"`
	ManualURL string `json:"manualUrl,omitempty"`
}

// Check states of a commit.
const (
	ChecksPending = "pending"
	ChecksSuccess = "success"
	ChecksFailure = "failure"
	ChecksUnknown = "unknown"
)

// CheckRun is one CI check of a commit: a check run (Checks API) or a legacy commit status.
type CheckRun struct {
	Kind       string `json:"kind" ts:"'check' | 'status'"`
	ID         int64  `json:"id,omitempty"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
	App        string `json:"app,omitempty"`
	RunID      int64  `json:"runId,omitempty"`
	JobID      int64  `json:"jobId,omitempty"`
	// Summary is the check's own output text (untrusted).
	Summary string `json:"summary,omitempty"`
}

// TicketChecks are the CI checks of the pull request's head commit.
type TicketChecks struct {
	State   string     `json:"state" ts:"'pending' | 'success' | 'failure' | 'unknown'"`
	Runs    []CheckRun `json:"runs"`
	HeadSha string     `json:"headSha,omitempty"`
	At      string     `json:"at,omitempty"`
	Error   string     `json:"error,omitempty"`
}

// ChecksFix records the task fix_checks started.
type ChecksFix struct {
	TaskID  string   `json:"taskId"`
	HeadSha string   `json:"headSha,omitempty"`
	Runs    []string `json:"runs"`
	At      string   `json:"at"`
}

// Needs-attention reasons of a ticket: the lead (or the user) has to act.
const (
	// TicketChecksFailed: the open pull request's checks failed (fix_checks starts a task for them).
	TicketChecksFailed = "checks_failed"
	// TicketPushRefused: the last pr was refused before anything was pushed (Refusal says why).
	TicketPushRefused = "push_refused"
	// TicketOpenPR: the branch is pushed but no pull request exists yet (manual tier): open it with pr.manualUrl, then
	// link_pr.
	TicketOpenPR = "open_pr"
)

// Push refusal reasons (TicketRefusal.Reason and the details.reason of a 409 of pr or remote).
const (
	RefusalUnsafeFiles    = "unsafe_files"
	RefusalScanFailed     = "scan_failed"
	RefusalBaseNotPushed  = "base_not_pushed"
	RefusalBranchDiverged = "branch_diverged"
	RefusalHistoryUnsafe  = "history_unsafe"
)

// TicketRefusal is why the last pr of a ticket stopped before its branch was pushed, kept on the ticket so the
// dashboard explains it in the reader's language. Message is the daemon's English text for the lead (MCP, CLI); the
// dashboard never shows it.
type TicketRefusal struct {
	Reason   string        `json:"reason" ts:"'unsafe_files' | 'scan_failed' | 'base_not_pushed' | 'branch_diverged'"`
	Findings []ScanFinding `json:"findings"`
	// Branch is the ticket branch (branch_diverged) or the base branch (base_not_pushed).
	Branch string `json:"branch,omitempty"`
	// Commits is how many commits of the base the remote lacks (base_not_pushed; 0: the base branch is missing).
	Commits int `json:"commits,omitempty"`
	// Subjects are the subjects of the newest of those commits, at most 3 (base_not_pushed).
	Subjects []string `json:"subjects,omitempty"`
	// CompareURL is the GitHub page listing the commits on the branch that Seelegent did not push
	// (branch_diverged).
	CompareURL string `json:"compareUrl,omitempty"`
	// Cause of a scan that could not run (scan_failed): "changed" (the worktree changed while it was read) or "git"
	// (git failed; Detail is git's redacted reason).
	Cause   string `json:"cause,omitempty" ts:"'changed' | 'git'"`
	Detail  string `json:"detail,omitempty"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// TicketSummary is a ticket as lists and the SSE stream show it.
type TicketSummary struct {
	ID       string      `json:"id"`
	RepoID   string      `json:"repoId"`
	IssueKey string      `json:"issueKey"`
	Issue    TicketIssue `json:"issue"`
	Branch   string      `json:"branch"`
	Worktree string      `json:"worktree"`
	Status   string      `json:"status" ts:"'working' | 'review' | 'ready' | 'pr_open' | 'done' | 'dropped' | 'failed'"`
	TaskIDs  []string    `json:"taskIds"`
	// MeetingID is the meeting triage opened (meeting).
	MeetingID string        `json:"meetingId,omitempty"`
	PR        *TicketPR     `json:"pr"`
	Checks    *TicketChecks `json:"checks"`
	// NeedsAttention says what the lead has to do next: checks_failed while the open pull request's checks failed,
	// push_refused while Refusal stands (ready or pr_open), open_pr while a pushed branch waits for its pull request
	// to be opened by hand.
	NeedsAttention string `json:"needsAttention,omitempty" ts:"'checks_failed' | 'push_refused' | 'open_pr'"`
	// Refusal is why the last pr stopped before pushing (cleared by the next pr or a new task).
	Refusal *TicketRefusal `json:"refusal,omitempty"`
	// Error is a failure (a task or meeting that could not start, a GitHub or git call that failed).
	Error string `json:"error,omitempty"`
	// Failure is the known cause of a failed ticket, for the dashboard to explain in its own words (Error stays
	// the text for the lead's tools and for causes without a code).
	Failure *TicketFailure `json:"failure,omitempty"`
	// Asked is who the ticket's last task was given to: what a retry or a new task without a member or backend
	// gets ("as before"). Absent before the first task.
	Asked     *TicketAsk `json:"asked,omitempty"`
	CreatedAt string     `json:"createdAt"`
	UpdatedAt string     `json:"updatedAt"`
}

// TicketAsk is the member, backend and model a ticket's last task was asked for.
type TicketAsk struct {
	Member  string `json:"member,omitempty"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Ticket failure reasons with a code.
const (
	// FailureMemberMissing: the member the task was given to does not exist (deleted).
	FailureMemberMissing = "member_missing"
	// FailureMemberDisabled: the member the task was given to is turned off.
	FailureMemberDisabled = "member_disabled"
)

// TicketFailure is the known cause of a failed ticket.
type TicketFailure struct {
	Reason string `json:"reason" ts:"'member_missing' | 'member_disabled'"`
	Member string `json:"member,omitempty"`
}

// TicketTask is a task of a ticket as its detail shows it.
type TicketTask struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    TaskStatus `json:"status"`
	Verdict   string     `json:"verdict,omitempty" ts:"'accepted' | 'rejected'"`
	Member    string     `json:"member,omitempty"`
	Turns     int        `json:"turns"`
	UpdatedAt string     `json:"updatedAt"`
}

// TicketGate is the gate of one turn of a ticket task.
type TicketGate struct {
	TaskID string     `json:"taskId"`
	Turn   int        `json:"turn"`
	State  string     `json:"state"`
	At     string     `json:"at,omitempty"`
	Steps  []GateStep `json:"steps"`
}

// TicketFile is a file the ticket's branch changes against its base (status A, M, D, R, C or ? for untracked).
type TicketFile struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
}

// TicketLogEntry is one step of a ticket's log (token-redacted).
type TicketLogEntry struct {
	ID   int64          `json:"id"`
	At   string         `json:"at"`
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// Ticket is the full ticket (GET /github/tickets/{id}).
type Ticket struct {
	TicketSummary
	Slug            string        `json:"slug"`
	Base            string        `json:"base"`
	BaseSha         string        `json:"baseSha,omitempty"`
	Env             *WorktreeEnv  `json:"env"`
	Outbox          []OutboxEntry `json:"outbox"`
	ChecksFix       *ChecksFix    `json:"checksFix"`
	DropReason      string        `json:"dropReason,omitempty"`
	WorktreeRemoved bool          `json:"worktreeRemoved"`
	CleanedAt       string        `json:"cleanedAt,omitempty"`
	// Exposed lists what the branch already held on GitHub when Seelegent took it over from the previous version
	// (which pushed the worker's history): files that look like secrets or junk are public already. A warning, not a
	// block: the keys they hold should be rotated.
	Exposed []ScanFinding `json:"exposed,omitempty"`
	// Tasks, Gates, Files and Log are read when the ticket is shown.
	Tasks []TicketTask     `json:"tasks"`
	Gates []TicketGate     `json:"gates"`
	Files []TicketFile     `json:"files"`
	Log   []TicketLogEntry `json:"log"`
}

// TicketTaskRequest is the body of POST /github/tickets/{id}/tasks: another task in the ticket's worktree.
type TicketTaskRequest struct {
	Prompt string `json:"prompt"`
	TicketTaskOptions
}

// TicketTaskResult answers POST /github/tickets/{id}/tasks and /fix-checks.
type TicketTaskResult struct {
	TicketID string `json:"ticketId"`
	TaskID   string `json:"taskId"`
}

// PRRequest is the body of POST /github/tickets/{id}/pr: commit, push and open a pull request (a draft by
// default).
type PRRequest struct {
	Title         string `json:"title,omitempty"`
	Body          string `json:"body,omitempty"`
	Draft         *bool  `json:"draft,omitempty"`
	CommitMessage string `json:"commitMessage,omitempty"`
}

// LinkPRRequest is the body of POST /github/tickets/{id}/link-pr: a pull request the user opened by hand.
type LinkPRRequest struct {
	Number int `json:"number"`
}

// CommentRequest is the body of POST /github/tickets/{id}/comment.
type CommentRequest struct {
	Body string `json:"body"`
}

// DropRequest is the body of POST /github/tickets/{id}/drop.
type DropRequest struct {
	Reason string `json:"reason,omitempty"`
}

// FixChecksRequest is the body of POST /github/tickets/{id}/fix-checks.
type FixChecksRequest struct {
	Member  string `json:"member,omitempty"`
	Extra   string `json:"extra,omitempty"`
	Backend string `json:"backend,omitempty"`
	Model   string `json:"model,omitempty"`
}

// ScanFinding is a file the pull request scan refused (secret, key, junk or too large), with why: Kind and Detail
// for the dashboard, Reason in English for the lead.
type ScanFinding struct {
	Path string `json:"path"`
	// Kind is env (.env file), private_key (id_rsa ...), key_store (.pem, .p12 ...), credentials (.netrc ...), device
	// (a Windows device name such as nul), secret (the content holds a key or token), large (over 5 MB) or link (a
	// link to a place outside the worktree).
	Kind string `json:"kind" ts:"'env' | 'private_key' | 'key_store' | 'credentials' | 'device' | 'secret' | 'large' | 'link'"`
	// Detail is the extension of a key store, what a secret is (github_token, aws_key, private_key, slack_token,
	// google_key, anthropic_key, stored_token) or the size in bytes of a large file.
	Detail string `json:"detail,omitempty"`
	Reason string `json:"reason"`
}

// IssuesChanged is the data of issues.changed: refetch that repository's inbox (and the repository list).
type IssuesChanged struct {
	RepoID    string `json:"repoId"`
	ChangedAt string `json:"changedAt"`
}
