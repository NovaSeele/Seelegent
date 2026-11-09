package ops

import "github.com/NovaSeele/Seelegent/internal/mcpwire"

// EnvWorker marks a worker process: the daemon sets SEELEGENT_WORKER=1 in every worker's environment
// (account.EnvWorker). An MCP server or a command a worker starts inherits it.
const EnvWorker = mcpwire.EnvWorker

// WorkerMessage is the refusal a worker gets (the old worker guard's text).
const WorkerMessage = "Seelegent is not available inside a Seelegent worker."

// WorkerError is the local refusal of a mutating operation inside a worker; its text is WorkerMessage.
type WorkerError struct{}

func (WorkerError) Error() string { return WorkerMessage }

// IsWorker reports whether this process runs inside a Seelegent worker.
func IsWorker(getenv func(string) string) bool { return getenv(EnvWorker) == "1" }

// Mutating reports whether operation name with this action changes anything, so a worker may not run it (old
// mcp.js isMutating, inventory §1.3). The table is mcpwire.Mutating, which the companion's MCP relay shares to
// tell which calls it may send again after a daemon restart.
func Mutating(name, action string) bool { return mcpwire.Mutating(name, action) }

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
