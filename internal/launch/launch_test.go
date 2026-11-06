package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// The test binary plays three parts, chosen by its first argument:
//
//	__launch <dir>          the launcher (Run)
//	worker <step>...        a fake worker CLI that runs the steps in order
//	daemon <dir> <step>...  a daemon that starts a launch and exits at once (restart survival)
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__launch":
			if err := Run(context.Background(), os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0)
		case "worker":
			os.Exit(runWorker(os.Args[2:]))
		case "daemon":
			_, err := Start(context.Background(), testOptions(os.Args[2], os.Args[3:]...))
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			os.Exit(0) // leave the launch running, like a daemon that is restarted
		}
	}
	os.Exit(m.Run())
}

// selfExe is the absolute path of this test binary. os.Args[0] can be relative ("./x.test"), and the children run
// in other directories.
func selfExe() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// runWorker runs the fake worker steps: print:<text>, partial:<text>, stderr:<text>, sleep:<ms>, env:<NAME>,
// stdin, pid:<file>, spawn:<file> (a grandchild that writes its pid to file and sleeps), escape:<file> (the same,
// daemonised: setsid + double fork on Unix, detached on Windows), escapeshort:<file> (a daemonised grandchild
// that exits after 300 ms), exit:<code>.
func runWorker(steps []string) int {
	for _, s := range steps {
		op, arg, _ := strings.Cut(s, ":")
		switch op {
		case "print":
			fmt.Println(arg)
		case "partial":
			fmt.Print(arg)
		case "stderr":
			fmt.Fprintln(os.Stderr, arg)
		case "sleep":
			ms, _ := strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "env":
			fmt.Println(arg + "=" + os.Getenv(arg))
		case "stdin":
			data, _ := io.ReadAll(os.Stdin)
			fmt.Println("stdin=" + string(data))
		case "pid":
			_ = os.WriteFile(arg, []byte(strconv.Itoa(os.Getpid())), 0o600)
		case "spawn":
			child := exec.CommandContext(context.Background(), selfExe(), "worker", "pid:"+arg, "sleep:60000")
			if err := child.Start(); err != nil {
				return 3
			}
		case "escape", "escapeshort":
			// A daemonised grandchild: a detached intermediate (new session on Unix) starts it and exits at once.
			fork := "fork:"
			if op == "escapeshort" {
				fork = "forkshort:"
			}
			mid := exec.CommandContext(context.Background(), selfExe(), "worker", fork+arg)
			detach(mid)
			if err := mid.Run(); err != nil {
				return 3
			}
			waitPidFile(arg)
		case "fork", "forkshort":
			life := "sleep:60000"
			if op == "forkshort" {
				life = "sleep:300"
			}
			g := exec.CommandContext(context.Background(), selfExe(), "worker", "pid:"+arg, life)
			detach(g)
			if err := g.Start(); err != nil {
				return 3
			}
			return 0 // without waiting: the grandchild is orphaned
		case "exit":
			code, _ := strconv.Atoi(arg)
			return code
		}
	}
	return 0
}

// testOptions starts this test binary as launcher and as worker running steps.
func testOptions(dir string, steps ...string) Options {
	return Options{
		Launcher: []string{selfExe(), "__launch"},
		Dir:      dir,
		Spec: Spec{
			Argv:      append([]string{selfExe(), "worker"}, steps...),
			Dir:       dir,
			KillGrace: time.Second,
		},
		Env:          os.Environ(),
		ReadyTimeout: 20 * time.Second,
	}
}

// line is one delivered line with its committed offset.
type line struct {
	text string
	next int64
}

// collect tails h from offset until the launch ends.
func collect(t *testing.T, h *Handle, offset int64) []line {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var got []line
	err := h.Tail(ctx, offset, func(b []byte, next int64) error {
		got = append(got, line{string(b), next})
		return nil
	})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	return got
}

func texts(lines []line) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.text
	}
	return out
}

func wait(t *testing.T, h *Handle) Exit {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ex, err := h.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	return ex
}

func TestRunToCompletion(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	h, err := Start(context.Background(), testOptions(dir,
		"print:one", "sleep:150", "stderr:warning here", "print:two", "sleep:150", "partial:three", "exit:7"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r, ok := h.Ready()
	if !ok || r.WorkerPID <= 0 || r.LauncherPID <= 0 {
		t.Fatalf("ready record %+v %v", r, ok)
	}
	got := collect(t, h, 0)
	if strings.Join(texts(got), ",") != "one,two,three" {
		t.Fatalf("lines %v", texts(got))
	}
	if got[0].next != int64(len("one\n")) {
		t.Fatalf("first offset %d", got[0].next)
	}
	st, err := os.Stat(h.Files().Stream)
	if err != nil || got[len(got)-1].next != st.Size() {
		t.Fatalf("last offset %d, file size %v (%v)", got[len(got)-1].next, st, err)
	}
	ex := wait(t, h)
	if ex.Reason != ReasonExited || ex.Code != 7 || ex.StartedAt.IsZero() || ex.EndedAt.Before(ex.StartedAt) {
		t.Fatalf("exit %+v", ex)
	}
	tail, err := h.StderrTail(4000)
	if err != nil || !strings.Contains(tail, "warning here") {
		t.Fatalf("stderr tail %q, %v", tail, err)
	}
	if short, _ := h.StderrTail(4); len(short) != 4 {
		t.Fatalf("StderrTail(4) = %q", short)
	}
	var errLines []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.TailStderr(ctx, 0, func(b []byte, _ int64) error {
		errLines = append(errLines, string(b))
		return nil
	}); err != nil || len(errLines) == 0 {
		t.Fatalf("TailStderr: %v %v", errLines, err)
	}
	// The same lines come back from any committed offset.
	if again := collect(t, h, got[0].next); strings.Join(texts(again), ",") != "two,three" {
		t.Fatalf("resume from offset: %v", texts(again))
	}
	// A finished launch cannot be run again.
	if err := Run(context.Background(), dir); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("second Run: %v", err)
	}
}

func TestCancelStopsWholeTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	childPid := filepath.Join(dir, "child.pid")
	h, err := Start(context.Background(), testOptions(dir, "spawn:"+childPid, "print:working", "sleep:60000"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r, _ := h.Ready()
	grandchild := readPid(t, childPid)
	if !platform.ProcessAlive(r.WorkerPID) || !platform.ProcessAlive(grandchild) {
		t.Fatal("worker tree not running")
	}
	if _, ok, _ := h.Exited(); ok {
		t.Fatal("launch reported ended while running")
	}
	if err := h.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := h.Cancel(); err != nil {
		t.Fatalf("second Cancel: %v", err)
	}
	ex := wait(t, h)
	if ex.Reason != ReasonCanceled {
		t.Fatalf("exit %+v, want canceled", ex)
	}
	waitDead(t, r.WorkerPID, grandchild, r.LauncherPID)
	if lines := collect(t, h, 0); len(lines) != 1 || lines[0].text != "working" {
		t.Fatalf("lines %v", texts(lines))
	}
}

// TestCancelStopsDaemonisedChild: a grandchild that left the worker's session and lost its parent still belongs
// to the tree (Linux: adopted by the launcher as subreaper; Windows: in the job), the daemon side lists it through
// Handle.Tree, and cancel stops it.
func TestCancelStopsDaemonisedChild(t *testing.T) {
	if !platform.WorkerTreesExact() {
		t.Skip("worker trees are best effort on " + runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), "a1")
	childPid := filepath.Join(dir, "child.pid")
	h, err := Start(context.Background(), testOptions(dir, "escape:"+childPid, "print:working", "sleep:60000"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	r, _ := h.Ready()
	grandchild := readPid(t, childPid)
	if !platform.ProcessAlive(grandchild) {
		t.Fatal("daemonised grandchild not running")
	}
	if r.LauncherStart == 0 {
		t.Fatalf("ready record without launcher start time: %+v", r)
	}
	var sf specFile
	if err := readJSON(h.Files().Spec, &sf); err != nil || sf.Job != r.Job {
		t.Fatalf("job name in launch.json %q, in ready.json %q (%v)", sf.Job, r.Job, err)
	}
	if (runtime.GOOS == "windows") != (r.Job != "") {
		t.Fatalf("job name %q on %s", r.Job, runtime.GOOS)
	}
	tree, ok := h.Tree()
	if !ok || tree.ID != dir {
		t.Fatalf("Tree: %+v %v", tree, ok)
	}
	pids, err := tree.PIDs()
	if err != nil || !slices.Contains(pids, grandchild) || !slices.Contains(pids, r.WorkerPID) {
		t.Fatalf("tree pids %v (%v) should hold worker %d and grandchild %d", pids, err, r.WorkerPID, grandchild)
	}
	checkAdopted(t, grandchild, r.LauncherPID)

	if err := h.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if ex := wait(t, h); ex.Reason != ReasonCanceled {
		t.Fatalf("exit %+v, want canceled", ex)
	}
	waitDead(t, grandchild, r.WorkerPID, r.LauncherPID)
	if left, err := tree.PIDs(); err != nil || len(left) != 0 {
		t.Fatalf("tree of an ended launch: %v, %v", left, err)
	}
}

// TestExitCodeWithDaemonisedChild: the worker exits 7 leaving a daemonised grandchild; the exit code is exact and
// the grandchild ends with the turn.
func TestExitCodeWithDaemonisedChild(t *testing.T) {
	if !platform.WorkerTreesExact() {
		t.Skip("worker trees are best effort on " + runtime.GOOS)
	}
	dir := filepath.Join(t.TempDir(), "a1")
	childPid := filepath.Join(dir, "child.pid")
	h, err := Start(context.Background(), testOptions(dir, "escape:"+childPid, "exit:7"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ex := wait(t, h)
	if ex.Reason != ReasonExited || ex.Code != 7 {
		t.Fatalf("exit %+v, want exited with 7", ex)
	}
	waitDead(t, readPid(t, childPid))
}

func TestTreeBeforeReady(t *testing.T) {
	dir := t.TempDir()
	if err := writeJSON(FilesIn(dir).Spec, specFile{Spec: Spec{Argv: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	h, _ := Attach(dir)
	if tree, ok := h.Tree(); ok {
		t.Fatalf("no ready record and no job name: %+v", tree)
	}
	if err := writeJSON(FilesIn(dir).Spec, specFile{Spec: Spec{Argv: []string{"x"}}, Job: `Local\Seelegent-x`}); err != nil {
		t.Fatal(err)
	}
	if tree, ok := h.Tree(); !ok || tree.Job != `Local\Seelegent-x` || tree.RootPID != 0 {
		t.Fatalf("job name from launch.json: %+v %v", tree, ok)
	}
}

func TestTimeoutStopsTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	opts := testOptions(dir, "sleep:60000")
	opts.Spec.Timeout = 300 * time.Millisecond
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if ex := wait(t, h); ex.Reason != ReasonTimeout {
		t.Fatalf("exit %+v, want timeout", ex)
	}
	if tail, _ := h.StderrTail(1000); !strings.Contains(tail, "timeout") {
		t.Fatalf("stderr %q", tail)
	}
}

func TestStdinAndEnvironment(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stdin := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(stdin, []byte("do the task"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := testOptions(dir, "stdin", "env:SEELEGENT_TEST_VALUE")
	opts.Spec.StdinPath = stdin
	opts.Env = append(os.Environ(), "SEELEGENT_TEST_VALUE=secret-ish")
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := strings.Join(texts(collect(t, h, 0)), "|")
	if got != "stdin=do the task|SEELEGENT_TEST_VALUE=secret-ish" {
		t.Fatalf("worker saw %q", got)
	}
	spec, err := os.ReadFile(h.Files().Spec)
	if err != nil || strings.Contains(string(spec), "secret-ish") {
		t.Fatalf("the environment must not be written to the spec: %s", spec)
	}
}

func TestStartFailedWorker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	opts := testOptions(dir)
	opts.Spec.Argv = []string{filepath.Join(dir, "no-such-cli")}
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ex := wait(t, h)
	if ex.Reason != ReasonStartFailed || ex.Code != -1 || ex.Error == "" {
		t.Fatalf("exit %+v", ex)
	}
	if lines := collect(t, h, 0); len(lines) != 0 {
		t.Fatalf("lines %v", texts(lines))
	}
}

func TestStartValidates(t *testing.T) {
	ctx := context.Background()
	if _, err := Start(ctx, Options{Dir: t.TempDir(), Spec: Spec{Argv: []string{"x"}}}); err == nil {
		t.Fatal("missing launcher should fail")
	}
	if _, err := Start(ctx, Options{Launcher: []string{"x"}, Dir: t.TempDir()}); err == nil {
		t.Fatal("empty argv should fail")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, SpecFile), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(ctx, testOptions(dir, "exit:0")); err == nil {
		t.Fatal("a directory with a launch must be refused")
	}
	opts := testOptions(filepath.Join(t.TempDir(), "a"), "exit:0")
	opts.Launcher = []string{filepath.Join(t.TempDir(), "missing-launcher")}
	if _, err := Start(ctx, opts); err == nil {
		t.Fatal("missing launcher binary should fail")
	}
}

// TestSurvivesDaemonExit starts the launch from a separate "daemon" process that exits at once, then attaches to
// the launch from this process, as a restarted daemon would, and resumes reading from a committed offset.
func TestSurvivesDaemonExit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a1")
	daemon := exec.CommandContext(context.Background(), selfExe(), "daemon", dir,
		"print:l1", "sleep:400", "print:l2", "sleep:400", "print:l3", "exit:0")
	platform.HideWindow(daemon)
	if out, err := daemon.CombinedOutput(); err != nil {
		t.Fatalf("daemon: %v: %s", err, out)
	}
	h, err := Attach(dir)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Read the first line, then stop following, as a daemon that is stopped again.
	ctx, cancel := context.WithCancel(context.Background())
	var committed int64
	errStop := errors.New("stop")
	err = h.Tail(ctx, 0, func(_ []byte, next int64) error {
		committed = next
		return errStop
	})
	cancel()
	if !errors.Is(err, errStop) || committed == 0 {
		t.Fatalf("first Tail: %v at %d", err, committed)
	}
	h2, err := Attach(dir)
	if err != nil {
		t.Fatalf("second Attach: %v", err)
	}
	if rest := texts(collect(t, h2, committed)); strings.Join(rest, ",") != "l2,l3" {
		t.Fatalf("resumed lines %v", rest)
	}
	if ex := wait(t, h2); ex.Reason != ReasonExited || ex.Code != 0 {
		t.Fatalf("exit %+v", ex)
	}
}

func TestAttachNotALaunch(t *testing.T) {
	if _, err := Attach(t.TempDir()); !errors.Is(err, ErrNotLaunch) {
		t.Fatalf("Attach: %v", err)
	}
}

// TestLostLauncher simulates a launcher that was killed: ready record present, lock free, no exit record.
func TestLostLauncher(t *testing.T) {
	dir := t.TempDir()
	f := FilesIn(dir)
	if err := writeJSON(f.Spec, specFile{Spec: Spec{Argv: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(-time.Minute)
	if err := writeJSON(f.Ready, Ready{LauncherPID: 1, WorkerPID: 2, StartedAt: started}); err != nil {
		t.Fatal(err)
	}
	h, err := Attach(dir)
	if err != nil {
		t.Fatal(err)
	}
	ex := wait(t, h)
	if ex.Reason != ReasonLost || !ex.StartedAt.Equal(started) {
		t.Fatalf("exit %+v", ex)
	}
	if again, ok, err := readExit(f.Exit); err != nil || !ok || again.Reason != ReasonLost {
		t.Fatalf("lost record not written: %+v %v %v", again, ok, err)
	}
}

// TestStartingLaunchIsNotLost: without a ready record and within the start grace, a launch counts as starting.
func TestStartingLaunchIsNotLost(t *testing.T) {
	dir := t.TempDir()
	if err := writeJSON(FilesIn(dir).Spec, specFile{Spec: Spec{Argv: []string{"x"}}}); err != nil {
		t.Fatal(err)
	}
	h, _ := Attach(dir)
	if _, ok, err := h.Exited(); ok || err != nil {
		t.Fatalf("Exited = %v, %v; want still starting", ok, err)
	}
}

func TestRunRefusesWhileLocked(t *testing.T) {
	dir := t.TempDir()
	lock, err := fsutil.TryLock(FilesIn(dir).Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	if err := Run(context.Background(), dir); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("Run: %v", err)
	}
	h := &Handle{files: FilesIn(dir)}
	if !h.launcherAlive() {
		t.Fatal("a held lock means the launcher is alive")
	}
}

func TestRunInProcessCanceledByContext(t *testing.T) {
	dir := t.TempDir()
	f := FilesIn(dir)
	sf := specFile{
		Spec:   Spec{Argv: []string{selfExe(), "worker", "sleep:60000"}, Dir: dir, KillGrace: time.Second},
		Stream: f.Stream, Stderr: f.Stderr, Exit: f.Exit, Cancel: f.Cancel,
	}
	if err := writeJSON(f.Spec, sf); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		for !exists(f.Ready) {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	})
	if err := Run(ctx, dir); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wg.Wait()
	ex, ok, err := readExit(f.Exit)
	if err != nil || !ok || ex.Reason != ReasonCanceled {
		t.Fatalf("exit %+v %v %v", ex, ok, err)
	}
}

func TestRunWithBadSpec(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(FilesIn(dir).Spec, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), dir); err == nil {
		t.Fatal("bad spec should fail")
	}
	if ex, ok, _ := readExit(FilesIn(dir).Exit); !ok || ex.Reason != ReasonStartFailed {
		t.Fatalf("exit %+v %v", ex, ok)
	}
}

func TestDeliverLongLine(t *testing.T) {
	long := make([]byte, maxLine+10)
	for i := range long {
		long[i] = 'x'
	}
	var got []int
	rest, next, err := deliverLines(long, 5, func(b []byte, _ int64) error {
		got = append(got, len(b))
		return nil
	})
	if err != nil || len(rest) != 0 || next != int64(5+len(long)) || len(got) != 1 || got[0] != len(long) {
		t.Fatalf("got %v rest %d next %d err %v", got, len(rest), next, err)
	}
	rest, next, err = deliverLines([]byte("a\r\nb"), 0, func(b []byte, n int64) error {
		if string(b) != "a" || n != 3 {
			t.Errorf("line %q next %d", b, n)
		}
		return nil
	})
	if err != nil || string(rest) != "b" || next != 3 {
		t.Fatalf("rest %q next %d err %v", rest, next, err)
	}
}

// waitPidFile waits up to 10 s for path to hold a pid.
func waitPidFile(path string) {
	for i := 0; i < 500; i++ {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) != "" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	for i := 0; i < 500; i++ {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

func waitDead(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for _, p := range pids {
		for platform.ProcessAlive(p) {
			if time.Now().After(deadline) {
				t.Fatalf("process %d still alive", p)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
