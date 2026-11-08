package api

// Git paths (GitFile.Path and OldPath, FileDiff.Path and OldPath, commit files, TaskGit.Preexisting) are relative to
// the repository root with '/' separators (contract §3.3).

// Git file statuses.
const (
	GitAdded     = "added"
	GitModified  = "modified"
	GitDeleted   = "deleted"
	GitRenamed   = "renamed"
	GitUntracked = "untracked"
)

// Commit message suggestion states.
const (
	SuggestionIdle    = "idle"
	SuggestionRunning = "running"
	SuggestionReady   = "ready"
	SuggestionFailed  = "failed"
)

// Start states of a task's git view (TaskGit.StartState).
const (
	// GitStartRecorded: the commit the task started from is known and is an ancestor of HEAD, so the commits
	// since then are the task's and the task's diffs compare against it.
	GitStartRecorded = "recorded"
	// GitStartUnknown: the task has no start (a task from before starts were recorded and without a start
	// snapshot, or a folder that was not in git when the task started). The view shows the uncommitted changes
	// against HEAD only.
	GitStartUnknown = "unknown"
	// GitStartUnreachable: the start commit is gone or no longer an ancestor of HEAD (a rebase, a reset or a
	// switch to another line of history). The view falls back to the uncommitted changes against HEAD.
	GitStartUnreachable = "unreachable"
)

// MaxTaskCommits caps TaskGit.Commits; TaskGit.CommitsMore counts the rest.
const MaxTaskCommits = 50

// GitHistoryUnavailable (TaskGit.HistoryState): the commits since the start could not be read this time (git timed
// out on a large repository, or an object is missing). Commits and CommittedFiles are empty; Files are as usual.
const GitHistoryUnavailable = "history_unavailable"

// TaskGit is GET /tasks/{id}/git: the repository of a task and the files the task changed in it.
type TaskGit struct {
	Repo   bool   `json:"repo"`
	Root   string `json:"root,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty"`
	// Files are the task's uncommitted changes, counted against HEAD; a commit may only name these. The files of
	// UntrackedDirs are not among them.
	Files []GitFile `json:"files"`
	// UntrackedDirs are the untracked folders (no tracked file inside) holding more than GitCollapseOver of the
	// task's untracked files, each shown as one row; their files are left out of Files. A commit may still name
	// them, one by one.
	UntrackedDirs []GitDir `json:"untrackedDirs"`
	// Preexisting lists files that were already dirty when the task started.
	Preexisting []string    `json:"preexisting"`
	Suggestion  *Suggestion `json:"suggestion"`
	// Head is the current HEAD commit ("" without commits).
	Head string `json:"head,omitempty"`
	// StartState says whether the task's start can be used (GitStart*).
	StartState string `json:"startState" ts:"'recorded' | 'unknown' | 'unreachable'"`
	// StartHead is the commit the task started from ("" when the repository had no commit yet, or unknown);
	// StartBranch is the branch it was on ("" when detached or unknown).
	StartHead   string `json:"startHead,omitempty"`
	StartBranch string `json:"startBranch,omitempty"`
	// BranchChanged: the task started on StartBranch and the repository is now on another branch or detached.
	BranchChanged bool `json:"branchChanged"`
	// Commits are the commits since the start (start..HEAD), newest first, at most MaxTaskCommits; CommitsMore
	// counts the older ones left out. Empty unless StartState is recorded. A commit made outside the task's turns
	// (someone else's, or between turns) is listed with OutsideTurns set.
	Commits     []GitCommit `json:"commits"`
	CommitsMore int         `json:"commitsMore"`
	// OwnCommits counts the commits of the range made during the task's turns (listed or among the older ones),
	// the task's own by time; the others are Commits plus CommitsMore minus OwnCommits.
	OwnCommits int `json:"ownCommits,omitempty"`
	// CommittedFiles are the task's files among those the commits since the start changed: files the task's turns
	// touched, and files under the task's folder that a commit made during one of its turns changed. A file only
	// a commit outside the turns changed is left out. Each is counted from the start to the working tree (a file
	// committed and then edited again shows its whole change, other commits to the same file included). View
	// only: a commit takes Files.
	CommittedFiles []GitFile `json:"committedFiles"`
	// HistoryState is GitHistoryUnavailable when the commits could not be read; empty otherwise.
	HistoryState string `json:"historyState,omitempty" ts:"'history_unavailable'"`
}

// GitCommit is one commit made since a task started.
type GitCommit struct {
	SHA     string `json:"sha"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	// AuthorTime is when the commit was written.
	AuthorTime string `json:"authorTime"`
	// Turn is the task's turn that was running when the commit was made, when one was. Attribution is by time:
	// another task's commit made while this task's turn n ran also has Turn n.
	Turn int `json:"turn,omitempty"`
	// OutsideTurns: the commit was made while none of the task's turns ran (by someone else, or between turns).
	OutsideTurns bool `json:"outsideTurns,omitempty"`
	// FileCount counts the files the commit changed (0 for a merge in the list).
	FileCount int `json:"fileCount"`
}

// GitCommitDetail is GET /tasks/{id}/git/commits/{sha}: one of the task's commits with its files and diffs
// against its first parent (the empty tree for a root commit).
type GitCommitDetail struct {
	GitCommit
	Body string `json:"body"`
	// Parent is the first parent ("" for a root commit).
	Parent string    `json:"parent,omitempty"`
	Files  []GitFile `json:"files"`
	// Diffs are the parsed diffs of the files, in order. When the whole diff is over 1 MiB, Truncated is set: the
	// last diff is cut and the files after it have none.
	Diffs     []FileDiff `json:"diffs"`
	Truncated bool       `json:"truncated"`
}

// Suggestion is a suggested commit message.
type Suggestion struct {
	State   string `json:"state" ts:"'idle' | 'running' | 'ready' | 'failed'"`
	Message string `json:"message,omitempty"`
}

// GitCollapseOver: an untracked folder with more of a task's files than this is one row of TaskGit
// (UntrackedDirs); GitDirFiles is how many of its files the row lists.
const (
	GitCollapseOver = 20
	GitDirFiles     = 200
)

// GitDir is an untracked folder of TaskGit.UntrackedDirs.
type GitDir struct {
	// Path is relative to the repository root and ends with '/'.
	Path string `json:"path"`
	// Count counts its untracked files; Bytes is their total size.
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
	// Files are its first GitDirFiles files by path; Count minus their number are left out.
	Files []GitFile `json:"files"`
	// Others counts the folder's untracked files that are not the task's (already there when it started, or
	// outside its folder and never touched by it). The row stands for the whole folder, so the repository view
	// does not list them either.
	Others int `json:"others,omitempty"`
}

// GitFile is one changed file.
type GitFile struct {
	Path      string `json:"path"`
	Status    string `json:"status" ts:"'added' | 'modified' | 'deleted' | 'renamed' | 'untracked'"`
	OldPath   string `json:"oldPath,omitempty"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Turn is the turn that last changed the file, when known.
	Turn   int  `json:"turn,omitempty"`
	Binary bool `json:"binary"`
	// Stamp changes whenever an image's preview would (TaskGit.Files and UntrackedDirs of the Git tab view only,
	// images only): the working tree file's size and modification time, and its blob id at HEAD. The view adds it
	// to the image URLs, so a rewritten image is read again although its status and line counts stay the same.
	Stamp string `json:"stamp,omitempty"`
}

// GitImageType returns the Content-Type of an image the Git view previews by its extension (lower case, with the
// dot), or false for any other extension.
func GitImageType(ext string) (string, bool) {
	switch ext {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".avif":
		return "image/avif", true
	case ".bmp":
		return "image/bmp", true
	case ".ico":
		return "image/x-icon", true
	case ".svg":
		return "image/svg+xml", true
	}
	return "", false
}

// RepoStatus is GET /git/status. Repo false means cwd is not inside a git repository (the other fields are then
// absent).
type RepoStatus struct {
	Repo     bool      `json:"repo"`
	Root     string    `json:"root,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Detached bool      `json:"detached,omitempty"`
	Dirty    bool      `json:"dirty,omitempty"`
	Files    []GitFile `json:"files,omitempty"`
	// Upstream is the branch's upstream ("origin/main"), "" when it has none; Ahead and Behind count the commits
	// between them. UpstreamGone is set when the upstream is configured but its branch no longer exists (the
	// counts are then 0).
	Upstream     string `json:"upstream,omitempty"`
	Ahead        int    `json:"ahead,omitempty"`
	Behind       int    `json:"behind,omitempty"`
	UpstreamGone bool   `json:"upstreamGone,omitempty"`
}

// Diff line kinds.
const (
	DiffContext = "context"
	DiffAdd     = "add"
	DiffDel     = "del"
)

// FileDiff is one file's diff, parsed (the UI never parses unified diff text). Truncated is true when the diff
// was over 1 MiB and only its start is included.
type FileDiff struct {
	Path      string     `json:"path"`
	OldPath   string     `json:"oldPath,omitempty"`
	Binary    bool       `json:"binary"`
	Truncated bool       `json:"truncated"`
	Hunks     []DiffHunk `json:"hunks"`
}

// DiffHunk is one hunk of a diff.
type DiffHunk struct {
	Header   string     `json:"header"`
	OldStart int        `json:"oldStart"`
	OldLines int        `json:"oldLines"`
	NewStart int        `json:"newStart"`
	NewLines int        `json:"newLines"`
	Lines    []DiffLine `json:"lines"`
}

// DiffLine is one line of a hunk.
type DiffLine struct {
	Kind  string `json:"kind" ts:"'context' | 'add' | 'del'"`
	Text  string `json:"text"`
	OldNo int    `json:"oldNo,omitempty"`
	NewNo int    `json:"newNo,omitempty"`
}

// CommitRequest is POST /tasks/{id}/git/commit: commit exactly Files (repo-root relative) with Message.
type CommitRequest struct {
	Message string   `json:"message"`
	Files   []string `json:"files"`
}

// CommitResult is the answer of a commit.
type CommitResult struct {
	SHA     string `json:"sha"`
	Summary string `json:"summary"`
}

// BranchInfo is one entry of GET /git/branches.
type BranchInfo struct {
	Branch   string `json:"branch"`
	Detached bool   `json:"detached"`
	Dirty    bool   `json:"dirty"`
}
