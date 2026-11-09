package gate

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// sleepCmd sleeps about n seconds in the platform's shell.
func sleepCmd(n int) string {
	if runtime.GOOS == "windows" {
		return "ping -n " + strconv.Itoa(n+1) + " 127.0.0.1 >nul"
	}
	return "sleep " + strconv.Itoa(n)
}

func TestExec(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	log := filepath.Join(dir, "logs", "1.log")
	r := Exec(ctx, "echo hello && exit 3", ExecOptions{Dir: dir, Env: os.Environ(), TailBytes: 100, LogFile: log})
	if r.Exit == nil || *r.Exit != 3 || !strings.Contains(r.Tail, "hello") || r.OK() {
		t.Fatalf("exit 3: %+v", r)
	}
	if data, err := os.ReadFile(log); err != nil || !strings.Contains(string(data), "hello") {
		t.Errorf("log = %q, %v", data, err)
	}
	if r := Exec(ctx, "echo ok", ExecOptions{Dir: dir, Env: os.Environ()}); !r.OK() {
		t.Errorf("echo: %+v", r)
	}
	began := time.Now()
	r = Exec(ctx, sleepCmd(8), ExecOptions{Dir: dir, Env: os.Environ(), Timeout: 300 * time.Millisecond})
	if !r.TimedOut || r.Exit != nil || time.Since(began) > 6*time.Second {
		t.Errorf("timeout: %+v after %s", r, time.Since(began))
	}
	cctx, cancel := context.WithCancel(ctx)
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	if r := Exec(cctx, sleepCmd(8), ExecOptions{Dir: dir, Env: os.Environ()}); !r.Cancelled || r.Exit != nil {
		t.Errorf("cancel: %+v", r)
	}
	if Tail("héllo", 4) != "llo" {
		t.Errorf("Tail cut a character: %q", Tail("héllo", 4))
	}
}

func TestPlanAndRun(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "ok.json"), `{"a": 1}`)
	writeFile(t, filepath.Join(root, "bad.json"), `{"a": }`)
	writeFile(t, filepath.Join(root, "tsconfig.json"), `{/* comment */}`)
	writeFile(t, filepath.Join(root, "x.go"), "package x\nfunc (\n")
	in := Input{Files: []string{"src/a.go", "README.md"}, LaneMode: api.LaneEnforce, Lane: []string{"src/"},
		Hygienic: true, Gates: true, AutoChecks: true,
		Doctrine: []proof.Check{{ID: "vet", Run: "echo vet"}, {ID: "web", Run: "echo web", When: []string{"web/**"}}},
		Commands: []api.GateCommand{{Cmd: "echo t", TaskIDs: []string{"T001", "T002"}, Kind: api.StepTask}}}
	steps := Plan(in)
	kinds := []string{}
	for _, s := range steps {
		kinds = append(kinds, s.Kind+":"+s.State)
	}
	if !reflect.DeepEqual(kinds, []string{"lane:red", "hygiene:green", "auto:pending", "doctrine:pending", "task:pending"}) {
		t.Fatalf("plan = %v", kinds)
	}
	if steps[4].Label != "T001, T002" || !reflect.DeepEqual(steps[0].Files, []string{"README.md"}) {
		t.Errorf("steps = %+v", steps)
	}
	r := Runner{Root: root, Env: os.Environ(), Timeout: time.Minute}
	// A red lane does not stop the gate (field report 4, F4): the commands still run, so a fix turn can work on
	// their failures while the lane waits for the lead.
	res, ok := r.Run(context.Background(), steps, nil)
	if !ok || res.State != api.GateRed || len(res.NotRun) != 0 || res.Steps[3].State != api.StepGreen ||
		res.Steps[4].State != api.StepGreen || len(res.Warnings) != 0 {
		t.Fatalf("run = %+v", res)
	}
	// Warn mode: the lane only warns and the commands run.
	in.LaneMode = api.LaneWarn
	in.Files = []string{"ok.json", "bad.json", "tsconfig.json", "x.go", "gone.json"}
	in.Lane = []string{"*.json"}
	var seen []string
	res, ok = r.Run(context.Background(), Plan(in), func(_ int, s api.GateStep) { seen = append(seen, s.Label+":"+s.State) })
	if !ok || res.State != api.GateRed || len(res.Warnings) != 1 {
		t.Fatalf("warn run = %+v", res)
	}
	auto := res.Steps[2]
	if auto.Kind != api.StepAuto || auto.State != api.StepRed || len(auto.Failures) != 2 ||
		auto.Failures[0].File != "bad.json" || auto.Failures[1].File != "x.go" {
		t.Errorf("auto = %+v", auto)
	}
	if !reflect.DeepEqual(seen, []string{"syntax:running", "syntax:red"}) {
		t.Errorf("progress = %v", seen)
	}
	// Gates off: only lane and hygiene.
	in.Gates = false
	if steps := Plan(in); len(steps) != 2 {
		t.Errorf("gates off: %+v", steps)
	}
	// A green run.
	in = Input{Files: []string{"a.txt"}, Gates: true, Commands: []api.GateCommand{{Cmd: "echo fine"}}}
	if res, ok := r.Run(context.Background(), Plan(in), nil); !ok || res.State != api.GateGreen || res.Steps[0].Tail != "" {
		t.Errorf("green = %+v", res)
	}
	// An invalid doctrine file is an error step.
	in.DoctrineError = "expected version 1"
	if res, _ := r.Run(context.Background(), Plan(in), nil); res.State != api.GateError {
		t.Errorf("doctrine error = %+v", res)
	}
}

// A file name with shell syntax in it is never part of a command line (old bug D1): node gets the path as its own
// argument, so the file is checked as a name and nothing in it runs. Without node on PATH (POSIX only) a fake node
// that fails every file stands in: a shell between the daemon and node would still have run the name.
func TestAutoCheckNoShell(t *testing.T) {
	env := os.Environ()
	if envx.ResolveCommand("node", env) == "" {
		if runtime.GOOS == "windows" {
			t.Skip("node is not on PATH")
		}
		bin := t.TempDir()
		writeFile(t, filepath.Join(bin, "node"), "#!/bin/sh\necho \"SyntaxError in $2\" >&2\nexit 1\n")
		if err := os.Chmod(filepath.Join(bin, "node"), 0o755); err != nil {
			t.Fatal(err)
		}
		env = append(env, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	root := t.TempDir()
	name := "x$(mkdir pwned1)`mkdir pwned2`;mkdir pwned3;.js"
	if runtime.GOOS == "windows" {
		name = "x&mkdir pwned1&.js" // '$', '`' and ';' mean nothing to cmd; '&' chains commands there.
	}
	writeFile(t, filepath.Join(root, name), "function (\n")
	got := Checker{Env: env}.Check(context.Background(), root, []string{name})
	if len(got) != 1 || got[0].File != name || got[0].Message == "" {
		t.Fatalf("check = %+v", got)
	}
	for _, p := range []string{"pwned1", "pwned2", "pwned3"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("the file name ran a command: %s exists", p)
		}
	}
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module x\n")
	writeFile(t, filepath.Join(root, "web", "package.json"),
		`{"scripts":{"build":"vite build","test":"vitest","lint":"eslint .","dev":"vite"}}`)
	writeFile(t, filepath.Join(root, "web", "pnpm-lock.yaml"), "")
	if err := os.MkdirAll(filepath.Join(root, "web", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := Detect(root, filepath.Join(root, "web", "src"))
	var cmds []string
	for _, c := range got {
		cmds = append(cmds, c.Dir+"|"+c.Cmd+"|"+c.Kind)
	}
	if !reflect.DeepEqual(cmds, []string{"web|pnpm run lint|detect", "web|pnpm run test|detect", "web|pnpm run build|detect"}) {
		t.Errorf("web = %v", cmds)
	}
	if got := Detect(root, root); len(got) != 3 || got[0].Cmd != "go build ./..." || got[0].Dir != "" {
		t.Errorf("root = %+v", got)
	}
	if got := Detect(t.TempDir(), t.TempDir()); got != nil {
		t.Errorf("nothing = %+v", got)
	}
}

// A fix turn ends with the worker brief's data-folder rule, the same exception included: the spec folder or the
// task's folder when it lies in the data folder (a Spec Kit workspace).
func TestFixPromptDataFolder(t *testing.T) {
	exit := 1
	res := api.GateResult{State: api.GateRed, Steps: []api.GateStep{
		{Kind: api.StepTask, Label: "T001", Cmd: "go test ./...", State: api.StepRed, Exit: &exit, Tail: "FAIL"},
	}}
	data := "/home/u/.seelegent"
	ws := data + "/specs/app-spec"
	for where, want := range map[Folders]string{
		{Data: data, Cwd: "/work/app", Spec: ws + "/specs/001-x"}: "read only " + ws + "/specs/001-x (your spec folder)",
		{Data: data, Cwd: ws}: "work only in " + ws + ".",
		{Data: data, Cwd: "/work/app", Spec: "/work/specs/001-x"}: "read only the paths this brief names",
	} {
		p := FixPrompt(res, nil, 1, 2, nil, where)
		rule := brief.DataRule(where.Data, where.Cwd, where.Spec)
		if !strings.HasSuffix(p, "\n"+rule) || !strings.HasPrefix(rule, "Do not explore "+data+" ") ||
			!strings.Contains(rule, want) {
			t.Errorf("%+v: the fix prompt does not end with the rule naming %q:\n%s", where, want, p)
		}
	}
}

func TestFixPromptAndForeign(t *testing.T) {
	exit := 1
	res := api.GateResult{State: api.GateRed, Steps: []api.GateStep{
		{Kind: api.StepLane, Label: "lane", State: api.StepGreen},
		{Kind: api.StepTask, Label: "T001", Cmd: "go test ./...", State: api.StepRed, Exit: &exit, Tail: "FAIL x_test.go:3"},
	}}
	noLooking := brief.DataRule("", "/repo", "")
	p := FixPrompt(res, nil, 1, 2, []Other{{ID: "t2", Title: "Other", Lane: []string{"web/"}}}, Folders{Cwd: "/repo"})
	for _, want := range []string{"automatic fix 1/2", "Command: go test ./...", "Exit: 1", "FAIL x_test.go:3",
		"Do not weaken", "t2 (Other) on web/", noLooking} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt misses %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, NoDeleteLine) || strings.Contains(p, "outside it") {
		t.Errorf("a green lane is named:\n%s", p)
	}
	// A lane alone is never a fix turn's work (F4).
	lane := api.GateResult{Steps: []api.GateStep{{Kind: api.StepLane, Label: "lane", State: api.StepRed, Files: []string{"x.go"}}}}
	if Fixable(lane) != nil || Failing(lane) == nil {
		t.Error("a red lane is fixable")
	}
	// Lane and command red: the fix turn works on the command; the lane is information with the no-delete line.
	both := api.GateResult{State: api.GateRed, Steps: []api.GateStep{
		{Kind: api.StepLane, Label: "lane", State: api.StepRed, Files: []string{"src/xy.ts", "README.md"}},
		{Kind: api.StepTask, Label: "T001", Cmd: "go test ./...", State: api.StepRed, Exit: &exit, Tail: "FAIL"},
	}}
	if s := Fixable(both); s == nil || s.Kind != api.StepTask {
		t.Fatalf("fixable = %+v", s)
	}
	p = FixPrompt(both, []string{"src/x/"}, 1, 2, nil, Folders{Cwd: "/repo"})
	for _, want := range []string{"Step: T001 (task)", "your lane is src/x/", "outside it: src/xy.ts, README.md",
		NoDeleteLine, noLooking} {
		if !strings.Contains(p, want) {
			t.Errorf("lane and command prompt misses %q:\n%s", want, p)
		}
	}
	for _, bad := range []string{"Undo", "Stay inside your lane", "Step: lane"} {
		if strings.Contains(p, bad) {
			t.Errorf("lane and command prompt has %q:\n%s", bad, p)
		}
	}
	hyg := api.GateResult{Steps: []api.GateStep{{Kind: api.StepHygiene, Label: "hygiene", State: api.StepRed,
		Findings: []api.Finding{{Kind: api.FindingEOLChanged, Path: "a.txt"}, {Kind: api.FindingJunkFile, Path: "x.log"}}}}}
	if p := FixPrompt(hyg, nil, 1, 1, nil, Folders{}); !strings.Contains(p, "line endings") || !strings.Contains(p, "a.txt (eol_changed)") ||
		!strings.Contains(p, "Remove one only when you are sure your own commands left it") {
		t.Errorf("hygiene prompt:\n%s", p)
	}

	step := api.GateStep{Kind: api.StepDoctrine, State: api.StepRed, Tail: "web/src/a.ts:3:1 error\nweb/src/b.ts(4,2): bad"}
	others := []Other{{ID: "t2", Title: "Web", Lane: []string{"web/"}, Files: []string{"web/src/a.ts", "web/src/b.ts"}}}
	if by := ForeignCause(step, []string{"internal/x.go"}, others); !slices.Equal(by, []api.GateCause{{TaskID: "t2", Title: "Web"}}) {
		t.Errorf("a doctrine failure only in files t2 changed: caused by %v", by)
	}
	if by := ForeignCause(step, []string{"web/src/a.ts"}, others); by != nil {
		t.Errorf("a failure in an own file is not foreign: %v", by)
	}
	if by := ForeignCause(step, nil, nil); by != nil {
		t.Errorf("no other tasks: not foreign: %v", by)
	}
	// Another task's lane is not evidence: only files it really changed are.
	if by := ForeignCause(step, nil, []Other{{ID: "t2", Lane: []string{"web/"}}}); by != nil {
		t.Errorf("a lane alone made the failure foreign: %v", by)
	}
	step.Kind = api.StepTask
	if by := ForeignCause(step, nil, others); by != nil {
		t.Error("the task's own gate commands are never foreign")
	}
}

// TestForeignNeverGreen is the API rename of the review: task A renames a.Foo, the detected build fails in b/b.go,
// which task B owns. The gate of A must stay red.
func TestForeignNeverGreen(t *testing.T) {
	build := api.GateStep{Kind: api.StepDetect, Label: "go build ./...", Cmd: "go build ./...", State: api.StepRed,
		Tail: "b/b.go:7:9: undefined: a.Foo"}
	b := []Other{{ID: "B", Lane: []string{"b/"}, Files: []string{"b/b.go"}}}
	if by := ForeignCause(build, []string{"a/a.go"}, b); by != nil {
		t.Errorf("a detected command is never foreign: %v", by)
	}
	doctrine := build
	doctrine.Kind = api.StepDoctrine
	by := ForeignCause(doctrine, []string{"a/a.go"}, b)
	if !slices.Equal(by, []api.GateCause{{TaskID: "B"}}) {
		t.Fatalf("caused by %v", by)
	}
	doctrine.Foreign, doctrine.CausedBy = true, by
	steps := []api.GateStep{{Kind: api.StepLane, Label: "lane", State: api.StepGreen}, doctrine}
	if got := Overall(steps); got != api.GateRed {
		t.Errorf("overall with a foreign red step = %s, want red", got)
	}
	if Failing(api.GateResult{Steps: steps}) != nil {
		t.Error("a foreign failure is no fix turn's work")
	}
}

// TestLaneStepTouched: only a file a worker touched counts against the lane (F6); the others are a warning.
func TestLaneStepTouched(t *testing.T) {
	touched := func(f string) bool { return f == "src/xy.ts" }
	st := LaneStep([]string{"src/x"}, []string{"src/x/a.vue", "src/xy.ts", "notes.md"}, api.LaneEnforce, touched, nil, nil)
	if st.State != api.StepRed || !slices.Equal(st.Files, []string{"src/xy.ts"}) || !slices.Equal(st.Unseen, []string{"notes.md"}) {
		t.Fatalf("step = %+v", st)
	}
	if !strings.Contains(st.Tail, "Out of lane:\nsrc/xy.ts") || !strings.Contains(st.Tail, UnseenNote) {
		t.Errorf("tail = %q", st.Tail)
	}
	if got := LaneWarnings(st); !slices.Equal(got, []string{"outside the lane, " + UnseenNote + ": notes.md"}) {
		t.Errorf("warnings = %q", got)
	}
	// Only unseen files: a warning, never red.
	st = LaneStep([]string{"src/x"}, []string{"notes.md"}, api.LaneEnforce, touched, nil, nil)
	if st.State != api.StepWarn || len(st.Files) != 0 || Overall([]api.GateStep{st}) != api.GateGreen {
		t.Errorf("unseen only = %+v", st)
	}
	// Lanes: warn keeps the touched files as a warning too.
	st = LaneStep([]string{"src/x"}, []string{"src/xy.ts"}, api.LaneWarn, touched, nil, nil)
	if st.State != api.StepWarn || !slices.Equal(LaneWarnings(st), []string{"outside the lane (lanes: warn): src/xy.ts"}) {
		t.Errorf("warn mode = %+v %q", st, LaneWarnings(st))
	}
	// A command that wrote where Seelegent could not read makes an unexplained file count, and the tail says why.
	st = LaneStep([]string{"src/x"}, []string{"notes.md"}, api.LaneEnforce, touched, nil, []string{"rm -rf $OUT"})
	if st.State != api.StepRed || !slices.Equal(st.Files, []string{"notes.md"}) || len(st.Unseen) != 0 ||
		!strings.Contains(st.Tail, UnknownNote+": rm -rf $OUT") {
		t.Fatalf("unknown write: %+v", st)
	}
	// A file counted by time says which command it changed during (review G1: a red lane has no fix turn).
	why := func(f string) string {
		if f == "src/xy.ts" {
			return "changed while `go test ./...` ran"
		}
		return ""
	}
	st = LaneStep([]string{"src/x"}, []string{"src/xy.ts", "notes.md"}, api.LaneEnforce, touched, why, nil)
	if st.State != api.StepRed ||
		!strings.Contains(st.Tail, "Of these, "+TimedNote+":\nsrc/xy.ts: changed while `go test ./...` ran") {
		t.Fatalf("timed: %+v", st)
	}
	// A plain entry covers its folder; without a touch set every changed file counts.
	if st = LaneStep([]string{"src/x"}, []string{"src/x/a.vue"}, api.LaneEnforce, nil, nil, nil); st.State != api.StepGreen {
		t.Errorf("in lane = %+v", st)
	}
}
