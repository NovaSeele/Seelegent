package mcpserver

import (
	"encoding/json"
	"slices"
)

// envWorker marks a worker process: the daemon sets SEELEGENT_WORKER=1 in every worker's environment
// (account.EnvWorker). An MCP server a worker starts inherits it.
const envWorker = "SEELEGENT_WORKER"

// workerError is the local refusal of a mutating tool inside a worker; its text is the old refusal.
type workerError struct{}

func (workerError) Error() string { return workerMessage }

// isWorker reports whether this process runs inside a Seelegent worker (old worker-guard.js isWorker).
func (s *Server) isWorker() bool { return s.o.Getenv(envWorker) == "1" }

// mutating reports whether a call of tool name with these arguments changes anything, so a worker may not make it
// (old mcp.js isMutating, inventory §1.3). The daemon refuses workers too (contract §1.3); this is the second layer,
// which also covers tools whose daemon side is not built yet.
func mutating(name string, args json.RawMessage) bool {
	action := argAction(args)
	notIn := func(read ...string) bool { return !slices.Contains(read, action) }
	switch name {
	case "delegate", "followup", "review", "cancel", "consult":
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

// argAction is the call's action argument when it is a string ("" otherwise, which the old guard treated as not a
// read action).
func argAction(args json.RawMessage) string {
	var a struct {
		Action any `json:"action"`
	}
	if len(args) == 0 || json.Unmarshal(args, &a) != nil {
		return ""
	}
	s, _ := a.Action.(string)
	return s
}
