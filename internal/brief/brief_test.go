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
		Memory:          "Use pnpm.",
		Constitution:    "Write tests first.",
		History:         []string{`- 2026-11-06T10:00 t1 "Fix": accepted`},
		Spec:            &Spec{Dir: "C:/specs/app-spec/specs/001-x", Outside: true, TaskIDs: []string{"T001", "T002"}, ImplementSkill: "C:/specs/app-spec/.agents/skills/speckit-implement/SKILL.md"},
		Baseline:        baseline,
		Deps:            DepsContext([]Dep{{ID: "t1", Title: "Schema", Status: "done", Response: strings.Repeat("r", DepChars+5)}, {ID: "t2", Status: "done"}}),
		Earlier:         []Turn{{N: 1, Message: "First", Response: "Did it", Files: []string{"a.go", "b.go"}}, {N: 2, Message: "Second"}},
		Interrupted:     true,
		Message:         "Third request.",
		TestParallelism: 4,
	}
	got := Worker(in)
	golden(t, "full", got)
	for _, want := range []string{"The lead for this task is Claude Code.", "## Your role: Backend (backend)",
		"(Missing skills: nope)", "... and 2 more", "## Current request from the lead\nThird request.",
		"was interrupted before it finished", "### t2: (untitled) (done)\n(no report)", "## Shared machine",
		"`go test -p 4`", "`pytest -n 4`"} {
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

// A Spec Kit workspace lives in Seelegent's data folder (<data>/specs/<repo>-spec/): the brief names its spec
// paths and its data-folder rule lets the worker read them, so the two never contradict each other. A task whose
// folder lies inside the data folder (speckit-specify in the workspace) gets no data-folder rule at all.
func TestWorkerDataFolder(t *testing.T) {
	data := "/home/u/.seelegent"
	feature := data + "/specs/app-spec/specs/001-x"
	got := Worker(Input{Cwd: "/work/app", DataDir: data, Message: "Go.", Spec: &Spec{Dir: feature, Outside: true,
		TaskIDs: []string{"T1"}, ImplementSkill: data + "/specs/app-spec/.agents/skills/speckit-implement/SKILL.md"}})
	golden(t, "spec-data-dir", got)
	for _, want := range []string{feature + "/spec.md", feature + "/tasks.md", "speckit-implement/SKILL.md",
		"Do not explore Seelegent's data folder (" + data + ")", "read only the paths this brief names"} {
		if !strings.Contains(got, want) {
			t.Errorf("the brief lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Never read") {
		t.Errorf("the brief forbids reading the data folder it names:\n%s", got)
	}

	inside := Worker(Input{Cwd: data + "/specs/app-spec", DataDir: data, Message: "Run speckit-specify."})
	golden(t, "cwd-in-data-dir", inside)
	if strings.Contains(inside, "data folder") {
		t.Errorf("a task inside the data folder is told not to explore it:\n%s", inside)
	}
	for _, b := range []string{got, inside} {
		if p := Preview(b); !strings.HasPrefix(p, "Working directory: ") {
			t.Errorf("the preview does not skip the rules: %q", p)
		}
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
	if Retry == "" || BackgroundWait == "" || !strings.HasPrefix(Handback, "## Hand-back") ||
		!strings.HasPrefix(HandbackElsewhere, "## Hand-back") {
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
	// A fence the cut leaves open, or the text never closes, is closed at the end with its own marker; a shorter
	// or different marker inside it does not close it.
	for in, want := range map[string]string{
		"## A\n```go\nx := 1\n## not a heading\n" + strings.Repeat("y", 50): "##### A\n```go\nx := 1\n## not a heading\n" +
			strings.Repeat("y", 5) + "…\n```",
		"~~~~\n```\n~~~\n# in code": "~~~~\n```\n~~~\n# in code\n~~~~",
		"```\ncode\n```  \n# after": "```\ncode\n```  \n#### after",
	} {
		if got := quoted(in, 40); got != want {
			t.Errorf("quoted(%q) =\n%s\nwant\n%s", in, got, want)
		}
	}
	cutReport := Worker(Input{Cwd: "/p", Message: "next",
		Earlier: []Turn{{N: 1, Message: "m", Response: "```\n" + strings.Repeat("z", TurnChars)}}})
	if !strings.Contains(cutReport, "z…\n```\n") {
		t.Errorf("the cut report's fence was not closed:\n%s", cutReport)
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

// A plan brief tells the worker how the attempt's backend reads files: codex's read-only sandbox runs shell
// commands that only read and has no file tools; agy's and claude's plan modes refuse the shell and keep their
// file tools (R4: a codex plan task answered from guesses after being told shell commands were refused).
func TestWorkerPlanRulePerBackend(t *testing.T) {
	codex := Worker(Input{Cwd: `D:\proj`, Plan: true, PlanShell: true, Message: "Explain the pool."})
	golden(t, "plan-codex", codex)
	for _, want := range []string{"This turn runs in a read-only sandbox", "`rg -n`", "`Get-Content`",
		"every write to a file is refused", "Read files with shell commands, and always end with a text answer."} {
		if !strings.Contains(codex, want) {
			t.Errorf("codex plan brief lacks %q", want)
		}
	}
	if strings.Contains(codex, "Shell commands are refused") || strings.Contains(codex, "file tools") {
		t.Errorf("codex plan brief steers to file tools:\n%s", codex)
	}
	tools := Worker(Input{Cwd: `D:\proj`, Plan: true, Message: "Explain the pool."})
	golden(t, "plan-file-tools", tools)
	if !strings.Contains(tools, "Shell commands are refused in this mode, so read files with the file tools, and "+
		"always end with a text answer.") || strings.Contains(tools, "read-only sandbox") {
		t.Errorf("agy/claude plan brief:\n%s", tools)
	}
	if full := Worker(Input{Cwd: `D:\proj`, PlanShell: true, Message: "Fix it."}); strings.Contains(full,
		"read-only sandbox") || strings.Contains(full, "Shell commands are refused") {
		t.Errorf("a full-mode brief carries a plan rule:\n%s", full)
	}
}

// A hidden read-only run starts with the plan rule of its backend; a codex run is also told to take its citation
// line numbers from rg -n, so its [path:line] citations verify.
func TestReadOnlyRun(t *testing.T) {
	const body = "You are P1, a peer.\n\n## Question\nShould the pool retry?"
	codex := ReadOnlyRun(body, true)
	golden(t, "readonly-codex", codex)
	if !strings.HasPrefix(codex, PlanRule(true)+" ") || !strings.Contains(codex, "citations from `rg -n`") ||
		!strings.HasSuffix(codex, "\n\n"+body) {
		t.Errorf("codex run:\n%s", codex)
	}
	tools := ReadOnlyRun(body, false)
	golden(t, "readonly-file-tools", tools)
	if tools != PlanRule(false)+"\n\n"+body {
		t.Errorf("agy/claude run:\n%s", tools)
	}
}
