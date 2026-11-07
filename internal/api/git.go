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

// TaskGit is GET /tasks/{id}/git: the repository of a task and the files the task changed in it.
type TaskGit struct {
	Repo   bool   `json:"repo"`
	Root   string `json:"root,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty"`
	// Files are the task's uncommitted changes; a commit may only name these.
	Files []GitFile `json:"files"`
	// Preexisting lists files that were already dirty when the task started.
	Preexisting []string    `json:"preexisting"`
	Suggestion  *Suggestion `json:"suggestion"`
}

// Suggestion is a suggested commit message.
type Suggestion struct {
	State   string `json:"state" ts:"'idle' | 'running' | 'ready' | 'failed'"`
	Message string `json:"message,omitempty"`
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
