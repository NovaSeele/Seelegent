package api

// StreamEvent names an SSE frame type of GET /api/v1/events (contract §7).
type StreamEvent string

// SSE frame types and their data.
const (
	// StreamHello: Hello, the first frame after a connect or resume.
	StreamHello StreamEvent = "hello"
	// StreamReset: Reset; the client refetches /state.
	StreamReset StreamEvent = "reset"
	// StreamTaskCreated: TaskSummary.
	StreamTaskCreated StreamEvent = "task.created"
	// StreamTaskUpdated: TaskSummary (coalesced per task, 100 ms).
	StreamTaskUpdated StreamEvent = "task.updated"
	// StreamTaskDeleted: Deleted.
	StreamTaskDeleted StreamEvent = "task.deleted"
	// StreamTaskEvent: TaskEvent (never coalesced).
	StreamTaskEvent StreamEvent = "task.event"
	// StreamAccountUpdated: Account.
	StreamAccountUpdated StreamEvent = "account.updated"
	// StreamAccountDeleted: Deleted.
	StreamAccountDeleted StreamEvent = "account.deleted"
	// StreamAccountLogin: LoginSession.
	StreamAccountLogin StreamEvent = "account.login"
	// StreamSettingsUpdated: Settings.
	StreamSettingsUpdated StreamEvent = "settings.updated"
	// StreamProvidersChanged: Changed.
	StreamProvidersChanged StreamEvent = "providers.changed"
	// StreamRolesChanged: Changed.
	StreamRolesChanged StreamEvent = "roles.changed"
	// StreamMembersChanged: Changed.
	StreamMembersChanged StreamEvent = "members.changed"
	// StreamMemoryChanged: MemoryChanged.
	StreamMemoryChanged StreamEvent = "memory.changed"
	// StreamHookRan: HookRun (milestone 14).
	StreamHookRan StreamEvent = "hook.ran"
)

// Reset reasons.
const (
	ResetTooOld   = "too_old"
	ResetRestart  = "restart"
	ResetOverflow = "overflow"
)

// Hello is the data of the hello frame.
type Hello struct {
	// Instance is the id prefix of this daemon start (8 hex digits).
	Instance   string `json:"instance"`
	PID        int    `json:"pid"`
	Build      string `json:"build"`
	Version    string `json:"version"`
	ServerTime string `json:"serverTime"`
}

// Reset is the data of the reset frame.
type Reset struct {
	Reason string `json:"reason" ts:"'too_old' | 'restart' | 'overflow'"`
}

// Deleted is the data of task.deleted and account.deleted.
type Deleted struct {
	ID string `json:"id"`
}

// Changed is the data of providers.changed, roles.changed and members.changed: refetch the list.
type Changed struct {
	ChangedAt string `json:"changedAt"`
}

// MemoryChanged is the data of memory.changed.
type MemoryChanged struct {
	Cwd string `json:"cwd"`
}
