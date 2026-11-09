package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestUnknownArguments: a call with an argument its tool does not have fails, naming the argument and the valid
// ones, and reaches no daemon (field report 3, B3: `tier: "strong"` on delegate was dropped without a word and the
// task ran on the standard tier).
func TestUnknownArguments(t *testing.T) {
	st, c := newStub(t, map[string]http.HandlerFunc{
		"POST /tasks": answer(http.StatusCreated, api.TaskSummary{ID: "t1", Status: api.StatusRunning}),
		"POST /wait":  waitAnswer("done"),
	})
	cs := stubSession(t, c, nil)

	res := callTool(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": "x", "tier": "strong"})
	msg := text(res, 0)
	if !res.IsError || !strings.Contains(msg, `delegate has no argument "tier"`) ||
		!strings.Contains(msg, `tiers go in "model", e.g. model: "strong"`) ||
		!strings.Contains(msg, "Valid arguments: after, after_mode, allow_git, backend, cwd,") ||
		!strings.Contains(msg, " model, ") {
		t.Fatalf("tier = %s", msg)
	}
	var structured struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Unknown []string `json:"unknown"`
				Valid   []string `json:"valid"`
			} `json:"details"`
		} `json:"error"`
	}
	data, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(data, &structured); err != nil || structured.Error.Code != string(api.CodeInvalidArgument) ||
		strings.Join(structured.Error.Details.Unknown, ",") != "tier" || len(structured.Error.Details.Valid) != 25 {
		t.Fatalf("structured = %s", data)
	}
	st.mu.Lock()
	_, sent := st.bodies["POST /tasks"]
	st.mu.Unlock()
	if sent {
		t.Fatal("the refused delegate reached the daemon")
	}

	// Several unknown arguments are all named, sorted; the tier hint only where the tool has a model.
	msg = fails(t, cs, "wait", map[string]any{"task_ids": []string{"t1"}, "timeout": 5, "tier": "fast"})
	if !strings.HasPrefix(msg, `Seelegent error: wait has no arguments "tier", "timeout". Valid arguments: any, `+
		`task_ids, wait_seconds, wake_on_blocked.`) || strings.Contains(msg, "model") {
		t.Fatalf("wait = %s", msg)
	}
	if msg = fails(t, cs, "followup", map[string]any{"task_id": "t1", "tier": "max"}); !strings.Contains(msg,
		`tiers go in "model"`) {
		t.Fatalf("followup = %s", msg)
	}
	if msg = fails(t, cs, "members", map[string]any{"all": true}); !strings.HasSuffix(msg,
		`members has no argument "all". It takes no arguments.`) {
		t.Fatalf("members = %s", msg)
	}

	// Known arguments still work, the model tier among them.
	var rep api.TaskReport
	ok(t, cs, "delegate", map[string]any{"prompt": "p", "cwd": "x", "model": "strong", "wait_seconds": 0}, &rep)
	var req map[string]any
	st.body(t, "POST /tasks", &req)
	if req["model"] != "strong" {
		t.Fatalf("delegate body = %v", req)
	}

	// The refusal is the server's alone: no schema says additionalProperties. Hosts that pass tool schemas through
	// the Gemini API's OpenAPI schema (agy, Antigravity, Gemini CLI, all registered by setup) have rejected that
	// keyword, and one refused schema hides every Seelegent tool on that host.
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		data, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(data), "additionalProperties") {
			t.Errorf("%s schema says additionalProperties: %s", tool.Name, data)
		}
	}
}
