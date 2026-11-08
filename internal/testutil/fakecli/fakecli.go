// Package fakecli turns a test binary into the detached launcher and a fake worker CLI, so engine tests run the
// real launcher and real processes without any real CLI, account or network.
//
// A test package calls Main from its TestMain:
//
//	func TestMain(m *testing.M) { fakecli.Main(m) }
//
// Then the test binary is, depending on how it was started:
//
//   - "<exe> __launch <dir>": the launcher (internal/launch.Run), as the daemon runs it (an interrupt or SIGTERM
//     stops it the way it stops `seelegent __launch`);
//   - with FAKECLI_QUEUE set: the fake CLI. Whatever its arguments, it takes the next script of the queue directory
//     (scripts are taken in the order they were pushed, each once), records its arguments, standard input,
//     working directory and the variables FAKECLI_RECORD_ENV names (comma-separated) next to the script, and
//     replays it;
//   - otherwise: the tests.
//
// A script is a recorded stream: every line goes to standard output, except directive lines:
//
//	#!stderr <text>      write text to standard error
//	#!sleep <ms>         pause
//	#!exit <code>        exit now with code
//	#!hang               sleep for five minutes (until cancelled)
//	#!wait-file <path>   wait (up to five minutes) until path exists
//	#!touch <path>       create path (tells the test the script got here)
//	#!write <path>\t<q> write the Go-quoted string q to path, making its folders (a change no edit tool reports)
//	#!remove <path>      remove path
//	#!mtime <path>\t<ns> set path's modification time to ns Unix nanoseconds (a write at a given time)
//	#!pid <path>         write the fake CLI's process id to path (for checks from outside, such as its priority)
//	#!alloc <mb>         allocate and touch mb MiB, kept until the CLI exits (a worker that needs that much memory;
//	                     under a job memory cap the Go runtime fails with "out of memory", exit code 2)
//
// In a line written to standard output, NowToken is replaced by the time it is written, in nanoseconds since the
// Unix epoch (the bench measures the delay from a line written to its event seen).
//
// An empty queue makes the fake CLI fail with exit code 97 and "fakecli: no script" on standard error.
package fakecli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// EnvQueue names the queue directory of the fake CLI.
const EnvQueue = "FAKECLI_QUEUE"

// NowToken in a script's output line is replaced by the time the line is written (Unix nanoseconds).
const NowToken = "{{fakecli.now}}" //nolint:gosec // a placeholder in test scripts, not a credential

// launchArg is the launcher's command name, as the daemon passes it.
const launchArg = "__launch"

// Main runs the launcher or the fake CLI when the binary was started as one, and the tests otherwise. It never
// returns.
func Main(m *testing.M) {
	switch {
	case len(os.Args) > 2 && os.Args[1] == launchArg:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err := launch.Run(ctx, os.Args[2])
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case os.Getenv(EnvQueue) != "":
		os.Exit(runCLI(os.Getenv(EnvQueue)))
	}
	os.Exit(m.Run())
}

// Exe is the test binary, which is also the fake CLI.
func Exe(t testing.TB) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// Launcher is the launcher command for launch.Options.Launcher.
func Launcher(t testing.TB) []string { return []string{Exe(t), launchArg} }

// Record is what the fake CLI saw for one script.
type Record struct {
	Script string   `json:"script"`
	Args   []string `json:"args"`
	Stdin  string   `json:"stdin"`
	Dir    string   `json:"dir"`
	// Env holds the variables named by FAKECLI_RECORD_ENV that were set.
	Env map[string]string `json:"env,omitempty"`
}

// EnvRecord names the variables (comma-separated) the fake CLI records in Record.Env.
const EnvRecord = "FAKECLI_RECORD_ENV"

// Queue is the list of scripts the fake CLI replays, one per run.
type Queue struct {
	dir string

	mu sync.Mutex
	n  int
}

// NewQueue creates an empty queue in a temporary directory of t.
func NewQueue(t testing.TB) *Queue {
	t.Helper()
	return &Queue{dir: t.TempDir()}
}

// Dir is the queue directory.
func (q *Queue) Dir() string { return q.dir }

// Push adds a script for the next run and returns its name (Record.Script).
func (q *Queue) Push(t testing.TB, script string) string {
	t.Helper()
	q.mu.Lock()
	q.n++
	name := fmt.Sprintf("%04d.script", q.n)
	q.mu.Unlock()
	tmp := filepath.Join(q.dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(q.dir, name)); err != nil {
		t.Fatal(err)
	}
	return name
}

// Left is how many pushed scripts no run has taken yet.
func (q *Queue) Left(t testing.TB) int {
	t.Helper()
	names, err := scripts(q.dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

// Records returns what the runs saw, in the order the scripts were pushed.
func (q *Queue) Records(t testing.TB) []Record {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(q.dir, "*.record.json"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(matches)
	out := make([]Record, 0, len(matches))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		var r Record
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// Env returns base (with GOCOVERDIR replaced) plus the queue variable, for a worker's environment. Under
// `go test -cover` every child writes coverage data on exit; each call gets its own GOCOVERDIR so children that run
// at the same time never collide (they did on Windows).
func (q *Queue) Env(base []string) []string {
	out := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if !strings.HasPrefix(kv, "GOCOVERDIR=") && !strings.HasPrefix(kv, EnvQueue+"=") {
			out = append(out, kv)
		}
	}
	if cover, err := os.MkdirTemp(q.dir, "cover-"); err == nil {
		out = append(out, "GOCOVERDIR="+cover)
	}
	return append(out, EnvQueue+"="+q.dir)
}

// scripts lists the scripts not taken yet, oldest first.
func scripts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".script") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// take claims the oldest script that no other run claimed. The claim is a "<name>.claim" file created with O_EXCL:
// a rename is no claim on Windows, where two runs that start together can both rename the same file and replay the
// same script (bugs F23). The script is then read and renamed to "<name>.taken" (Left counts what is not).
func take(dir string) (name string, data []byte, err error) {
	names, err := scripts(dir)
	if err != nil {
		return "", nil, err
	}
	for _, n := range names {
		//nolint:gosec // the test's own queue folder
		claim, err := os.OpenFile(filepath.Join(dir, n+".claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			continue // another run claimed it
		}
		_ = claim.Close()
		path := filepath.Join(dir, n)
		var data []byte
		err = retry(func() error {
			var rerr error
			data, rerr = os.ReadFile(path) //nolint:gosec // a file of the test's own queue
			return rerr
		})
		if err != nil {
			return n, nil, err
		}
		//nolint:gosec // the test's own queue folder
		if err := retry(func() error { return os.Rename(path, path+".taken") }); err != nil {
			return n, nil, err
		}
		return n, data, nil
	}
	return "", nil, nil
}

// retry runs fn until it succeeds, for up to two seconds: a fresh file can be held for a moment by a scanner on
// Windows (a sharing violation).
func retry(fn func() error) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := fn()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func runCLI(dir string) int {
	name, script, err := take(dir)
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "fakecli:", err)
		return 98
	case name == "":
		fmt.Fprintln(os.Stderr, "fakecli: no script")
		return 97
	}
	stdin := readStdin()
	rec := Record{Script: name, Args: os.Args[1:], Stdin: stdin}
	rec.Dir, _ = os.Getwd()
	for _, n := range strings.Split(os.Getenv(EnvRecord), ",") {
		if v, ok := os.LookupEnv(n); ok && n != "" {
			if rec.Env == nil {
				rec.Env = map[string]string{}
			}
			rec.Env[n] = v
		}
	}
	if data, err := json.Marshal(rec); err == nil {
		// Written aside and renamed, so Records never reads half a record of a run that is still writing it or
		// was stopped while writing it.
		path := filepath.Join(dir, strings.TrimSuffix(name, ".script")+".record.json")
		if os.WriteFile(path+".tmp", data, 0o600) == nil { //nolint:gosec // the test's own queue folder
			_ = retry(func() error { return os.Rename(path+".tmp", path) }) //nolint:gosec // the test's own queue folder
		}
	}
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	sc := bufio.NewScanner(strings.NewReader(string(script)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var held [][]byte // the memory of #!alloc
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "#!") {
			if strings.Contains(line, NowToken) {
				line = strings.ReplaceAll(line, NowToken, strconv.FormatInt(time.Now().UnixNano(), 10))
			}
			_, _ = out.WriteString(line + "\n")
			_ = out.Flush()
			continue
		}
		op, arg, _ := strings.Cut(strings.TrimPrefix(line, "#!"), " ")
		switch op {
		case "stderr":
			fmt.Fprintln(os.Stderr, arg)
		case "sleep":
			ms, _ := strconv.Atoi(arg)
			time.Sleep(time.Duration(ms) * time.Millisecond)
		case "exit":
			code, _ := strconv.Atoi(arg)
			_ = out.Flush()
			return code
		case "hang":
			_ = out.Flush()
			time.Sleep(5 * time.Minute)
		case "wait-file":
			_ = out.Flush()
			deadline := time.Now().Add(5 * time.Minute)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(arg); err == nil { //nolint:gosec // a path the test's own script names
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		case "touch":
			_ = out.Flush()
			_ = os.WriteFile(arg, nil, 0o600) //nolint:gosec // a path the test's own script names
		case "write":
			_ = out.Flush()
			p, q, _ := strings.Cut(arg, "\t")
			if text, err := strconv.Unquote(q); err == nil {
				_ = os.MkdirAll(filepath.Dir(p), 0o700)  //nolint:gosec // a path the test's own script names
				_ = os.WriteFile(p, []byte(text), 0o600) //nolint:gosec // a path the test's own script names
			}
		case "remove":
			_ = out.Flush()
			_ = os.Remove(arg) //nolint:gosec // a path the test's own script names
		case "mtime":
			_ = out.Flush()
			p, ns, _ := strings.Cut(arg, "\t")
			if n, err := strconv.ParseInt(ns, 10, 64); err == nil {
				at := time.Unix(0, n)
				_ = os.Chtimes(p, at, at)
			}
		case "pid":
			_ = out.Flush()
			_ = os.WriteFile(arg, []byte(strconv.Itoa(os.Getpid())), 0o600) //nolint:gosec // a path the test's own script names
		case "alloc":
			_ = out.Flush()
			mb, _ := strconv.Atoi(arg)
			held = append(held, touched(mb))
		}
	}
	runtime.KeepAlive(held)
	return 0
}

// touched allocates mb MiB and writes to every page, so the memory is committed and resident.
func touched(mb int) []byte {
	b := make([]byte, max(mb, 0)<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	return b
}

// readStdin reads standard input; the launcher gives the CLI a file or the null device, so it never blocks.
func readStdin() string {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return string(data)
}
