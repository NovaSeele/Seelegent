package brief

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/brief -update)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), []byte(got)) {
		t.Errorf("%s differs from %s (run go test ./internal/brief -update):\n%s", name, path, got)
	}
}

func TestWorkerMinimal(t *testing.T) {
	got := Worker(Input{Cwd: `D:\proj`, Message: "Add a --verbose flag."})
	golden(t, "minimal", got)
	if !strings.HasSuffix(got, "## Task\nAdd a --verbose flag.") || strings.Contains(got, "The lead for this task") {
		t.Errorf("unexpected brief:\n%s", got)
	}
}

func TestWorkerFull(t *testing.T) {
	baseline := make([]string, 32)
	for i := range baseline {
		baseline[i] = "file" + string(rune('a'+i%26)) + ".go"
	}
	in := Input{
		Lead: LeadName("claude"), Cwd: `D:\proj`, Plan: true,
		Role: &Role{ID: "backend", Name: "Backend", Description: " Owns the API. ", Rules: "Keep handlers thin.",
			OutputStyle: "Terse.", Skills: []Skill{{Name: "go-style", Description: "Go conventions",
				File: "C:/skills/go-style/SKILL.md"}, {Name: "bare", File: "C:/skills/bare/SKILL.md"}},
			Missing: []string{"nope"}},
		Memory:       "Use pnpm.",
		Constitution: "Write tests first.",
		History:      []string{`- 2026-11-06T10:00 t1 "Fix": accepted`},
		Spec:         &Spec{Dir: "C:/specs/app-spec/specs/001-x", Outside: true, TaskIDs: []string{"T001", "T002"}, ImplementSkill: "C:/specs/app-spec/.agents/skills/speckit-implement/SKILL.md"},
		Baseline:     baseline,
		Deps:         DepsContext([]Dep{{ID: "t1", Title: "Schema", Status: "done", Response: strings.Repeat("r", DepChars+5)}, {ID: "t2", Status: "done"}}),
		Earlier:      []Turn{{N: 1, Message: "First", Response: "Did it", Files: []string{"a.go", "b.go"}}, {N: 2, Message: "Second"}},
		Interrupted:  true,
		Message:      "Third request.",
	}
	got := Worker(in)
	golden(t, "full", got)
	for _, want := range []string{"The lead for this task is Claude Code.", "## Your role: Backend (backend)",
		"(Missing skills: nope)", "... and 2 more", "## Current request from the lead\nThird request.",
		"was interrupted before it finished", "### t2: (untitled) (done)\n(no report)"} {
		if !strings.Contains(got, want) {
			t.Errorf("brief lacks %q", want)
		}
	}
	long := Worker(Input{Constitution: strings.Repeat("x", ConstitutionChars+10)})
	if !strings.Contains(long, strings.Repeat("x", ConstitutionChars)) || strings.Contains(long, strings.Repeat("x", ConstitutionChars+1)) {
		t.Error("constitution not capped")
	}
	// Deterministic: the same input gives the same text.
	if Worker(in) != got {
		t.Error("brief is not deterministic")
	}
}

func TestWorkerInsideSpecAndOneBaselineFile(t *testing.T) {
	got := Worker(Input{Cwd: "/p", Spec: &Spec{Dir: "specs/002-y"}, Baseline: []string{"go.mod"}, Message: "Go."})
	golden(t, "spec-inside", got)
	if !strings.Contains(got, "1 file has uncommitted changes") || !strings.Contains(got, "the tasks the lead names below") {
		t.Errorf("unexpected brief:\n%s", got)
	}
}

func TestContinue(t *testing.T) {
	if got := Continue("go on", nil, true); got != "go on" {
		t.Errorf("Continue = %q", got)
	}
	spec := &Spec{Dir: "specs/1", TaskIDs: []string{"T3"}}
	if got := Continue("go on", spec, true); got != "Now work on tasks: T3 (from specs/1/tasks.md).\ngo on" {
		t.Errorf("Continue = %q", got)
	}
	if got := Continue("go on", &Spec{Dir: "s"}, true); !strings.Contains(got, "the tasks the lead names below") {
		t.Errorf("Continue = %q", got)
	}
	if got := Continue("go on", spec, false); got != "go on" {
		t.Errorf("Continue = %q", got)
	}
}

func TestSmallHelpers(t *testing.T) {
	if DepsContext(nil) != "" {
		t.Error("empty deps context")
	}
	for host, want := range map[string]string{"": "", "Claude": "Claude Code", "codex": "Codex", "opencode": "OpenCode",
		"agy": "Gemini", "gemini": "Gemini", "cli": "a CLI user", "other": "a CLI user"} {
		if got := LeadName(host); got != want {
			t.Errorf("LeadName(%q) = %q", host, got)
		}
	}
	if got := Preview(strings.Repeat("é", 600)); len([]rune(got)) != PreviewChars {
		t.Errorf("preview has %d runes", len([]rune(got)))
	}
	if cut("héllo", 3, "…") != "hél…" || cut("hi", 3, "…") != "hi" {
		t.Error("cut")
	}
	if Retry == "" || BackgroundWait == "" || !strings.HasPrefix(Handback, "## Hand-back") {
		t.Error("fixed prompts")
	}
}

func TestQuotedAndPreview(t *testing.T) {
	report := "## Summary\nDid it.\n```sh\n# a comment\n```\n#hashtag\n    ## indented code\n### Files changed\n- a.go"
	want := "##### Summary\nDid it.\n```sh\n# a comment\n```\n#hashtag\n    ## indented code\n###### Files changed\n- a.go"
	if got := quoted(report, 1000); got != want {
		t.Errorf("quoted =\n%s", got)
	}
	if got := quoted(strings.Repeat("x", 20), 10); got != strings.Repeat("x", 10)+"…" {
		t.Errorf("cut turn = %q", got)
	}
	a := Preview(Worker(Input{Cwd: "/a", Message: "Task A"}))
	b := Preview(Worker(Input{Cwd: "/b", Message: "Task B"}))
	if a == b || !strings.HasPrefix(a, "Working directory: /a") {
		t.Errorf("previews %q / %q", a, b)
	}
	w := Worker(Input{Cwd: "/p", Earlier: []Turn{{N: 1, Message: "m", Response: "## Summary\nok"}}, Message: "next"})
	if !strings.Contains(w, "##### Summary") || strings.Contains(w, "reported\n## Summary") {
		t.Errorf("the earlier report's heading was not demoted:\n%s", w)
	}
}
