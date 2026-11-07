package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/ops"
)

// TestMutating pins the old isMutating table (inventory §1.3).
func TestMutating(t *testing.T) {
	if ops.EnvWorker != account.EnvWorker {
		t.Fatalf("worker marker %s, the daemon sets %s", ops.EnvWorker, account.EnvWorker)
	}
	cases := []struct {
		name, args string
		want       bool
	}{
		{"delegate", `{}`, true},
		{"followup", `{"task_id":"t"}`, true},
		{"review", `{}`, true},
		{"cancel", `{}`, true},
		{"consult", `{"action":"status"}`, true},
		{"wait", `{}`, false},
		{"task", `{}`, false},
		{"status", `{"full":true}`, false},
		{"members", `{"action":"upsert"}`, false},
		{"meeting", `{"action":"status"}`, false},
		{"meeting", `{"action":"list"}`, false},
		{"meeting", `{"action":"wait"}`, false},
		{"meeting", `{"action":"open"}`, true},
		{"meeting", `{}`, true},
		{"issues", `{"action":"repos"}`, false},
		{"issues", `{"action":"ticket"}`, false},
		{"issues", `{"action":"triage"}`, true},
		{"memory", `{"action":"read"}`, false},
		{"memory", `{"action":"append"}`, true},
		{"memory", `{}`, true},
		{"memory", `{"action":["read"]}`, true},
		{"profiles", `{"action":"list"}`, false},
		{"profiles", `{}`, false},
		{"profiles", `{"action":"upsert"}`, true},
		{"profiles", `{"action":"delete"}`, true},
		{"proof", `{"action":"show"}`, false},
		{"proof", `{"action":"run"}`, true},
		{"notes", `{"action":"list"}`, false},
		{"notes", `{"action":"send"}`, true},
		{"automations", `{"action":"runs"}`, false},
		{"automations", `{"action":"run"}`, true},
		{"contest", `{"action":"wait"}`, false},
		{"contest", `{"action":"pick"}`, true},
		{"memory", `not json`, true},
		{"status", ``, false},
	}
	for _, c := range cases {
		if got := mutating(c.name, json.RawMessage(c.args)); got != c.want {
			t.Errorf("mutating(%s, %s) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}
