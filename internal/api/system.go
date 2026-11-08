package api

// APIVersion is the version of this API (the /api/v1 prefix).
const APIVersion = 1

// Tier is a model tier: an alias the lead passes instead of a model id. Each backend maps it to its own model.
type Tier string

// Tiers.
const (
	TierFast     Tier = "fast"
	TierStandard Tier = "standard"
	TierStrong   Tier = "strong"
	TierMax      Tier = "max"
)

// Health is GET /health: the daemon is up, and which build it runs.
type Health struct {
	OK      bool   `json:"ok" ts:"true"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
	// Build identifies the embedded web bundle; the dashboard reloads when hello.build changes.
	Build      string `json:"build"`
	APIVersion int    `json:"apiVersion" ts:"1"`
	PID        int    `json:"pid"`
	Port       int    `json:"port"`
	StartedAt  string `json:"startedAt"`
	// HomeID identifies the data directory the daemon serves (config.HomeID: a hash, never the path). A client of
	// another data directory refuses this daemon instead of using or stopping it.
	HomeID string `json:"homeId"`
	// OS is the daemon's operating system (Go's runtime.GOOS: windows, linux, darwin, ...), so the Settings page
	// can mark the settings that only work on Windows.
	OS string `json:"os"`
	// Memory is the machine's free memory, read when the answer was made.
	Memory MemoryStatus `json:"memory"`
	// LowMemory is true while the memory watchdog holds every new start because free memory or free commit is
	// below criticalFreeMemoryMB. resources.changed tells when it flips.
	LowMemory bool `json:"lowMemory"`
}

// MemoryStatus is the free memory of the machine (platform.MemoryStatus). Known is false when the system gave no
// reading (the numbers are 0 then); AvailCommitMB is 0 where commit is unknown (everywhere but Windows).
type MemoryStatus struct {
	AvailPhysMB   int64 `json:"availPhysMB"`
	AvailCommitMB int64 `json:"availCommitMB"`
	Known         bool  `json:"known"`
}

// DaemonStop is the answer (202) of POST /daemon/stop: the daemon accepted the request and shuts down right after
// answering, as on Ctrl+C. The caller waits for its lock to be free and its port to go quiet.
type DaemonStop struct {
	Stopping bool `json:"stopping" ts:"true"`
	PID      int  `json:"pid"`
}

// BootState is GET /state: everything the dashboard needs for its first paint, in one request. LastEventID is the
// SSE id at the moment of the snapshot; the client opens the stream with it, so no change falls between the two.
type BootState struct {
	Health   Health        `json:"health"`
	Settings Settings      `json:"settings"`
	Accounts []Account     `json:"accounts"`
	Backends []Backend     `json:"backends"`
	Tasks    []TaskSummary `json:"tasks"`
	Projects []Project     `json:"projects"`
	// LastEventID is the id of the last SSE frame before the snapshot was read.
	LastEventID string `json:"lastEventId"`
}

// Backend is one worker CLI (GET /backends).
type Backend struct {
	ID        string `json:"id" ts:"'agy' | 'claude' | 'codex'"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
	// Accounts counts the backend's accounts; Active the enabled ones.
	Accounts     int                 `json:"accounts"`
	Active       int                 `json:"active"`
	Capabilities BackendCapabilities `json:"capabilities"`
}

// BackendCapabilities says what a worker CLI supports.
type BackendCapabilities struct {
	Resume     bool `json:"resume"`
	APIKey     bool `json:"apiKey"`
	Takeover   bool `json:"takeover"`
	ListModels bool `json:"listModels"`
}

// Model is a model a worker may run (GET /models lists the allowed ones only).
type Model struct {
	ID      string `json:"id"`
	Backend string `json:"backend"`
	Label   string `json:"label"`
	// Tiers are the tiers that map to this model.
	Tiers []Tier `json:"tiers"`
}

// Project is a folder tasks ran in (GET /projects, newest activity first).
type Project struct {
	Key            string `json:"key"`
	Cwd            string `json:"cwd"`
	Name           string `json:"name"`
	RepoRoot       string `json:"repoRoot,omitempty"`
	TaskCount      int    `json:"taskCount"`
	ActiveCount    int    `json:"activeCount"`
	LastActivityAt string `json:"lastActivityAt"`
}

// SpecView is a Spec Kit feature's tasks.md, parsed (GET /spec).
type SpecView struct {
	Dir     string     `json:"dir"`
	Feature string     `json:"feature"`
	Tasks   []SpecTask `json:"tasks"`
}

// SpecTask is one task of a tasks.md.
type SpecTask struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Done     bool   `json:"done"`
	Phase    string `json:"phase,omitempty"`
	Parallel bool   `json:"parallel"`
}

// Log names of GET /logs.
const (
	LogDaemon = "daemon"
	LogHooks  = "hooks"
)

// LogLines is GET /logs: the last lines of a log, already redacted.
type LogLines struct {
	Name  string   `json:"name" ts:"'daemon' | 'hooks'"`
	Lines []string `json:"lines"`
}

// PromptText is GET /tasks/{id}/prompt: the full brief of one attempt.
type PromptText struct {
	Prompt string `json:"prompt"`
}

// MemoryRequest replaces (PUT /memory) or appends to (POST /memory) a project's notes.
type MemoryRequest struct {
	Cwd  string `json:"cwd"`
	Text string `json:"text"`
}

// RoleRequest creates or replaces a role (POST /roles).
type RoleRequest struct {
	Role  Role   `json:"role"`
	Scope string `json:"scope" ts:"'global' | 'project'"`
	Cwd   string `json:"cwd,omitempty"`
}
