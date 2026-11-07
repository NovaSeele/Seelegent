package ops

import (
	"strings"
	"testing"
)

func TestGuardAndHelpers(t *testing.T) {
	for _, c := range []struct {
		name, action string
		want         bool
	}{
		{"delegate", "", true}, {"daemon", "", true}, {"memory", "read", false}, {"memory", "append", true},
		{"setup", "", true}, {"migrate", "", true}, {"update", "", true},
		{"status", "", false}, {"task", "", false}, {"wait", "", false}, {"profiles", "list", false},
		{"automations", "show", false}, {"automations", "preview", false}, {"automations", "runs", false},
		{"automations", "run", true}, {"automations", "enable", true}, {"issues", "ticket", false},
		{"issues", "pr", true}, {"issues", "retry", true},
	} {
		if got := Mutating(c.name, c.action); got != c.want {
			t.Errorf("Mutating(%s, %s) = %v", c.name, c.action, got)
		}
	}
	if !IsWorker(func(string) string { return "1" }) || IsWorker(func(string) string { return "" }) {
		t.Error("IsWorker")
	}
	e := &UnavailableError{What: "lane", Detail: "leave it out"}
	if !strings.Contains(e.Error(), "lane is not available") || !strings.HasSuffix(e.Error(), "(leave it out)") {
		t.Errorf("error = %q", e.Error())
	}
	for in, want := range map[string]string{"": "", "full": "edit", "edit": "edit", "plan": "plan"} {
		if got, err := TaskMode(in); err != nil || got != want {
			t.Errorf("TaskMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := TaskMode("fast"); err == nil {
		t.Error("TaskMode(fast) passed")
	}
	if Plural(1) != "" || Plural(2) != "s" || Truncate("héllo", 2) != "hé" {
		t.Error("Plural/Truncate")
	}
}
