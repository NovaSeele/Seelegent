package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// task <id> shows what the report holds, as wait does: the failure, the attention, the question, the files, the
// gate and the link (M18 hunt H15).
func TestTaskTextShowsTheWholeReport(t *testing.T) {
	exit := 1
	r := api.TaskReport{TaskID: "t1", Status: "failed", Turn: 2, Model: "m", Response: "Tried.",
		FilesChanged: []string{"a.go"}, Error: "the worker exited with code 3",
		Attention: &api.ReportAttention{State: api.AttentionQuestion, Reason: "The worker asked a question"},
		Question:  &api.ReportQuestion{Text: "Which file?", Options: []string{"a.go", "b.go"}},
		Gate:      &api.ReportGate{State: "red", Steps: []api.ReportGateStep{{Label: "test", Cmd: "go test", Exit: &exit}}},
		Dashboard: "http://127.0.0.1:47890/tasks/t1"}
	var b bytes.Buffer
	printTaskReport(&b, r, nil)
	out := b.String()
	for _, want := range []string{"Task t1 [failed]", "Model: m", "Tried.", "Files changed: a.go",
		"Attention: QUESTION (The worker asked a question)", "Question: Which file?", "  - b.go", "Gate: red",
		"failed test: go test (exit 1)", "Error: the worker exited with code 3", "Dashboard: http://127.0.0.1:47890/tasks/t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "BLOCKED") {
		t.Errorf("a question reads BLOCKED:\n%s", out)
	}
}

// Each attention state has its own label in a report, and the ATTENTION column of ls names the state.
func TestAttentionLabels(t *testing.T) {
	for state, want := range map[string]string{
		api.AttentionBlocked:        "Attention: BLOCKED (r)",
		api.AttentionQuestion:       "Attention: QUESTION (r)",
		api.AttentionStalled:        "Attention: STALLED (r)",
		api.AttentionBackgroundWait: "Attention: WAITING ON BACKGROUND COMMANDS (r)",
	} {
		var b bytes.Buffer
		printReport(&b, api.TaskReport{TaskID: "t1", Status: "running",
			Attention: &api.ReportAttention{State: state, Reason: "r"}}, "")
		if !strings.Contains(b.String(), want) {
			t.Errorf("%s: missing %q in:\n%s", state, want, b.String())
		}
	}
	var b bytes.Buffer
	printTasks(&b, []api.TaskSummary{{ID: "t1", Status: api.StatusRunning, Title: "T",
		Attention: &api.Attention{State: api.AttentionStalled, Reason: "stalled"}}})
	if !strings.Contains(b.String(), "stalled(stalled)") {
		t.Errorf("ls does not show the stall:\n%s", b.String())
	}
}

// The wait summary line and the status list name each state the lead must look at, not BLOCKED for all of them,
// and status tags questions and stalls as well (M18 review N1).
func TestWaitAndStatusNameTheAttentionState(t *testing.T) {
	status := func(state string) string {
		var b bytes.Buffer
		printStatus(&b, statusOut{Health: &api.Health{}, ActiveTasks: []api.TaskSummary{{ID: "t1",
			Status: api.StatusRunning, Title: "T", Attention: &api.Attention{State: state, Reason: "r"}}}})
		return b.String()
	}
	for state, want := range map[string]string{
		api.AttentionBlocked:  "[BLOCKED: r]",
		api.AttentionQuestion: "[QUESTION: r]",
		api.AttentionStalled:  "[STALLED: r]",
	} {
		var b bytes.Buffer
		printWait(&b, api.WaitResult{Reports: []api.TaskReport{{TaskID: "t1", Status: "running",
			Attention: &api.ReportAttention{State: state, Reason: "r"}}}})
		if !strings.Contains(b.String(), want) {
			t.Errorf("wait, %s: missing %q in:\n%s", state, want, b.String())
		}
		if out := status(state); !strings.Contains(out, want) {
			t.Errorf("status, %s: missing %q in:\n%s", state, want, out)
		}
	}
	if out := status(api.AttentionInfo); strings.Contains(out, "[") {
		t.Errorf("status tags an info note:\n%s", out)
	}
}
