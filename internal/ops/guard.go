package ops

import "slices"

// EnvWorker marks a worker process: the daemon sets SEELEGENT_WORKER=1 in every worker's environment
// (account.EnvWorker). An MCP server or a command a worker starts inherits it.
const EnvWorker = "SEELEGENT_WORKER"

// WorkerMessage is the refusal a worker gets (the old worker guard's text).
const WorkerMessage = "Seelegent is not available inside a Seelegent worker."

// WorkerError is the local refusal of a mutating operation inside a worker; its text is WorkerMessage.
type WorkerError struct{}

func (WorkerError) Error() string { return WorkerMessage }

// IsWorker reports whether this process runs inside a Seelegent worker.
func IsWorker(getenv func(string) string) bool { return getenv(EnvWorker) == "1" }

// Mutating reports whether operation name with this action changes anything, so a worker may not run it (old
// mcp.js isMutating, inventory §1.3). The daemon refuses workers too (contract §1.3); this is the second layer, which
// also covers operations whose daemon side is not built yet. Names are the MCP tool names; the command line maps
// its commands onto them (run is delegate; stop, restart and ui are "daemon"). setup, migrate and restore are
// command-line only: they change the host configs, the data folder and the user's files.
func Mutating(name, action string) bool {
	notIn := func(read ...string) bool { return !slices.Contains(read, action) }
	switch name {
	case "delegate", "followup", "review", "cancel", "consult", "daemon", "setup", "migrate", "restore":
		return true
	case "meeting":
		return notIn("status", "list", "wait")
	case "issues":
		return notIn("repos", "list", "tickets", "ticket")
	case "memory":
		return action != "read"
	case "profiles":
		return action == "upsert" || action == "delete"
	case "proof":
		return action == "run"
	case "notes":
		return action != "list"
	case "automations":
		return notIn("list", "runs")
	case "contest":
		return notIn("status", "wait")
	}
	return false
}

// UnavailableError is a feature this version of Seelegent does not have yet. Front ends report it with the code
// not_available.
type UnavailableError struct {
	// What names the feature ("lane enforcement (lane)").
	What string
	// Detail says what to do instead (optional).
	Detail string
}

func (e *UnavailableError) Error() string {
	msg := e.What + " is not available in this version of Seelegent yet; nothing was done"
	if e.Detail != "" {
		msg += " (" + e.Detail + ")"
	}
	return msg
}
