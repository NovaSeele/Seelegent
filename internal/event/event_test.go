package event

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestUsageAdd(t *testing.T) {
	a := Usage{InputTokens: 1, OutputTokens: 2, CachedTokens: 3, TotalTokens: 3, CostUSD: 0.5}
	b := Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30, CostUSD: 0.25}
	want := Usage{InputTokens: 11, OutputTokens: 22, CachedTokens: 3, TotalTokens: 33, CostUSD: 0.75}
	if got := a.Add(b); got != want {
		t.Errorf("Add = %+v", got)
	}
	if !(Usage{}).IsZero() || a.IsZero() {
		t.Error("IsZero is wrong")
	}
}

func TestJSONRoundTrip(t *testing.T) {
	events := []Event{
		{Kind: KindSession, Session: &Session{ConversationID: "c1", Model: "m"}},
		Message(2, "hello"),
		{Kind: KindToolCall, Step: 3, Tool: &Tool{ID: "t", Name: "Bash", State: ToolCalling,
			Input: json.RawMessage(`{"command":"ls"}`), Command: "ls"}},
		{Kind: KindFile, File: &File{Path: "/a.go", Op: FileEdit, Tool: "Edit"}},
		{Kind: KindReport, Report: &Report{Status: StatusSuccess, Response: "ok", Usage: &Usage{TotalTokens: 5},
			DeniedActions: []string{"x"}}},
		Fail(ClassQuotaExhausted, "out of quota"),
		Log("line"),
	}
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"conversationId":"c1"`, `"totalTokens":5`, `"deniedActions"`, `"class":"quota_exhausted"`} {
		if !strings.Contains(string(data), field) {
			t.Errorf("JSON lacks %s: %s", field, data)
		}
	}
	var back []Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, events) {
		t.Errorf("round trip changed events:\n%+v\n%+v", back, events)
	}
}
