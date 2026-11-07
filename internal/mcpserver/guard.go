package mcpserver

import (
	"encoding/json"

	"github.com/NovaSeele/Seelegent/internal/ops"
)

// isWorker reports whether this process runs inside a Seelegent worker (old worker-guard.js isWorker).
func (s *Server) isWorker() bool { return ops.IsWorker(s.o.Getenv) }

// mutating reports whether a call of tool name with these arguments changes anything, so a worker may not make it
// (ops.Mutating over the call's action argument).
func mutating(name string, args json.RawMessage) bool { return ops.Mutating(name, argAction(args)) }

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
