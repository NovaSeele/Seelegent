//go:build bench

package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// env is the bench's view of one run.
type env struct {
	o       *options
	self    string
	root    string
	out     string
	work    string
	home    string
	project string
	queue   string
	spawns  string
	bin     string
	api     string
	pprof   string
	http    *http.Client
	scripts int
}

func run(o *options) error {
	if err := o.normalize(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	e := &env{o: o, self: self, http: &http.Client{Timeout: 30 * time.Second},
		api:   fmt.Sprintf("http://127.0.0.1:%d/api/v1", o.port),
		pprof: fmt.Sprintf("http://127.0.0.1:%d", o.pprofPort)}
	if e.root, err = moduleRoot(); err != nil {
		return err
	}
	e.out = o.out
	if e.out == "" {
		e.out = filepath.Join(os.TempDir(), "seelegent-bench", time.Now().Format("20060102-150405")+"-"+o.scenario)
	}
	if err := os.MkdirAll(e.out, 0o750); err != nil {
		return err
	}
	if e.work, err = os.MkdirTemp("", "seelegent-bench-home-"); err != nil {
		return err
	}
	if !o.keepHome {
		defer removeAll(e.work)
	}
	e.home, e.project = filepath.Join(e.work, "data"), filepath.Join(e.work, "project")
	e.queue, e.spawns = filepath.Join(e.work, "queue"), filepath.Join(e.work, "spawns.jsonl")
	for _, d := range []string{e.home, e.project, e.queue, filepath.Join(e.work, "userhome")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	if err := e.build(ctx); err != nil {
		return err
	}
	initProject(ctx, e.project)
	fmt.Fprintf(os.Stderr, "bench: %s for %s, output %s\n", o.scenario, o.duration, e.out)
	r, err := e.scenario(ctx)
	if r != nil {
		if werr := writeResult(e.out, r); werr != nil && err == nil {
			err = werr
		}
		printResult(os.Stdout, r, o.baseline)
	}
	return err
}

// moduleRoot finds go.mod from the working directory up.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run the bench inside the Seelegent module (no go.mod found)")
		}
		dir = parent
	}
}

func removeAll(dir string) {
	for range 20 {
		if os.RemoveAll(dir) == nil {
			return
		}
		time.Sleep(500 * time.Millisecond) // Windows: files held for a moment after their process exited
	}
	fmt.Fprintln(os.Stderr, "bench: could not remove", dir)
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func (e *env) build(ctx context.Context) error {
	if e.o.bin != "" {
		e.bin = e.o.bin
		return nil
	}
	e.bin = filepath.Join(e.work, exeName("seelegent"))
	cmd := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", e.bin, "./cmd/seelegent")
	cmd.Dir, cmd.Stdout, cmd.Stderr = e.root, os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build seelegent: %w", err)
	}
	return nil
}

// initProject makes the project folder a git repository with one commit, as a real project is (best effort).
func initProject(ctx context.Context, dir string) {
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("bench project\n"), 0o600); err != nil {
		return
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "README.md"},
		{"-c", "user.name=bench", "-c", "user.email=bench@example.invalid", "commit", "-q", "-m", "init"}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if cmd.Run() != nil {
			return
		}
	}
}

// daemonEnv is the daemon's environment: this machine's minus anything Seelegent or the fake CLI reads, plus the
// temp data folder, a temp user home, the fake agy and the queue.
func (e *env) daemonEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		ku := strings.ToUpper(k)
		if strings.HasPrefix(ku, "SEELEGENT_") || strings.HasPrefix(ku, "FAKECLI_") || ku == "GOCOVERDIR" ||
			ku == envSpawnLog || ku == "HOME" || ku == "USERPROFILE" || ku == "GOMEMLIMIT" || ku == "GOGC" {
			continue
		}
		out = append(out, kv)
	}
	userHome := filepath.Join(e.work, "userhome")
	missing := filepath.Join(e.work, "not-installed")
	return append(out, "SEELEGENT_HOME="+e.home, "HOME="+userHome, "USERPROFILE="+userHome,
		"SEELEGENT_AGY="+e.self, "SEELEGENT_CLAUDE="+missing, "SEELEGENT_CODEX="+missing,
		fakecli.EnvQueue+"="+e.queue, envSpawnLog+"="+e.spawns)
}

// startDaemon starts `seelegent daemon` and waits until /health answers.
func (e *env) startDaemon(ctx context.Context) (*exec.Cmd, <-chan error, error) {
	logf, err := os.Create(filepath.Join(e.out, "daemon-console.log"))
	if err != nil {
		return nil, nil, err
	}
	args := []string{"daemon", "--port", strconv.Itoa(e.o.port), "--pprof-addr", "127.0.0.1:" + strconv.Itoa(e.o.pprofPort)}
	// No context: the daemon is stopped through POST /daemon/stop (stopDaemon), and killed only when that fails.
	cmd := exec.Command(e.bin, args...) //nolint:noctx // stopped through the API, see above
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.Dir = e.daemonEnv(), logf, logf, e.work
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return nil, nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); _ = logf.Close() }()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := e.call(ctx, http.MethodGet, "/health", nil, nil); err == nil {
			return cmd, exited, nil
		}
		select {
		case err := <-exited:
			return nil, nil, fmt.Errorf("the daemon exited at start (%w); see %s", err,
				filepath.Join(e.out, "daemon-console.log"))
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			return nil, nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			return nil, nil, errors.New("the daemon did not answer /health within 60 s")
		}
	}
}

// call sends one API request; out receives the JSON answer.
func (e *env) call(ctx context.Context, method, path string, in, out any) (time.Duration, error) {
	var body io.Reader = http.NoBody
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.api+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("X-Seelegent", "1")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	start := time.Now()
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, err
	}
	data, rerr := io.ReadAll(resp.Body)
	took := time.Since(start)
	_ = resp.Body.Close()
	if rerr != nil {
		return took, rerr
	}
	if resp.StatusCode >= 300 {
		return took, fmt.Errorf("%s %s: %s: %.300s", method, path, resp.Status, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return took, fmt.Errorf("%s %s: %w", method, path, err)
		}
	}
	return took, nil
}

// push queues one fake worker script.
func (e *env) push(script string) error {
	e.scripts++
	name := fmt.Sprintf("%06d.script", e.scripts)
	tmp := filepath.Join(e.queue, name+".tmp")
	if err := os.WriteFile(tmp, []byte(script), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(e.queue, name))
}

func (e *env) seedAccounts(ctx context.Context) error {
	for i := range e.o.accounts {
		in := api.NewAccount{Backend: "agy", Kind: "isolated", Label: fmt.Sprintf("bench-%d", i+1)}
		if _, err := e.call(ctx, http.MethodPost, "/accounts", in, nil); err != nil {
			return err
		}
	}
	// Each new account gets a quota read; wait for them, so the measured time starts after the setup.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, r := range readSpawnLog(e.spawns) {
			if r.Role == "quota-reader" {
				n++
			}
		}
		if n >= e.o.accounts {
			return nil
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
	return errors.New("the accounts' first quota reads did not come within 90 s")
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (e *env) delegate(ctx context.Context, title string) (string, error) {
	off := false
	in := api.DelegateRequest{Prompt: "Bench task " + title + ": edit the README.", Title: title, Cwd: e.project,
		Gate: &off, Verify: &off, NoLane: true}
	var sum api.TaskSummary
	if _, err := e.call(ctx, http.MethodPost, "/tasks", in, &sum); err != nil {
		return "", err
	}
	return sum.ID, nil
}

func terminal(s api.TaskStatus) bool {
	return s == api.StatusDone || s == api.StatusFailed || s == api.StatusCancelled
}

// waitTerminal waits until every task ended.
func (e *env) waitTerminal(ctx context.Context, ids []string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		left := 0
		for _, id := range ids {
			var sum api.TaskSummary
			if _, err := e.call(ctx, http.MethodGet, "/tasks/"+id, nil, &sum); err != nil || !terminal(sum.Status) {
				left++
			}
		}
		if left == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%d of %d tasks did not end within %s", left, len(ids), limit)
		}
		if err := sleep(ctx, time.Second); err != nil {
			return err
		}
	}
}

// seedTasks runs short finished tasks, so the lists the daemon serves are not empty.
func (e *env) seedTasks(ctx context.Context) error {
	var ids []string
	for i := range e.o.seedTasks {
		if err := e.push(shortScript(i)); err != nil {
			return err
		}
		id, err := e.delegate(ctx, fmt.Sprintf("seed %d", i+1))
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	return e.waitTerminal(ctx, ids, 5*time.Minute)
}

func (e *env) stopDaemon(ctx context.Context, cmd *exec.Cmd, exited <-chan error) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	_, err := e.call(sctx, http.MethodPost, "/daemon/stop", struct{}{}, nil)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "bench: stop the daemon:", err)
	}
	var exit error
	select {
	case exit = <-exited:
	case <-time.After(45 * time.Second):
		_, _ = fmt.Fprintln(os.Stderr, "bench: the daemon did not stop within 45 s; killing it")
		_ = cmd.Process.Kill()
		exit = <-exited
	}
	if exit != nil {
		_, _ = fmt.Fprintln(os.Stderr, "bench: the daemon exited with", exit)
	}
}

// scenario runs the setup, the warmup, the measured time and the profile window, then stops the daemon.
func (e *env) scenario(ctx context.Context) (*result, error) {
	cmd, exited, err := e.startDaemon(ctx)
	if err != nil {
		return nil, err
	}
	s := newSampler(cmd.Process.Pid, e.self, filepath.Join(e.home, "seelegent.db"), e.pprof)
	sctx, stopSampler := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { s.run(sctx, e.o.interval, 250*time.Millisecond) })
	r := &result{Scenario: e.o.scenario, Started: time.Now(), Options: optionsOf(e.o), GOOS: runtime.GOOS,
		GOARCH: runtime.GOARCH, NumCPU: runtime.NumCPU(), Commit: gitCommit(ctx, e.root)}
	stopAll := func() {
		e.stopDaemon(ctx, cmd, exited)
		stopSampler()
		wg.Wait()
		copyFile(filepath.Join(e.home, "logs", "daemon.log"), filepath.Join(e.out, "daemon.log"))
	}
	fail := func(err error) (*result, error) {
		stopAll()
		return nil, err
	}

	if err := e.seedAccounts(ctx); err != nil {
		return fail(err)
	}
	if err := e.seedTasks(ctx); err != nil {
		return fail(err)
	}
	s.setPhase("warmup")
	if err := sleep(ctx, e.o.warmup); err != nil {
		return fail(err)
	}
	_ = s.saveProfile(ctx, "/debug/pprof/allocs", filepath.Join(e.out, "allocs-start.pprof"), time.Minute)

	gen := &loadGen{e: e, lat: map[string]*latencies{"list": {}, "detail": {}}}
	lctx, stopLoad := context.WithCancel(ctx)
	var lwg sync.WaitGroup
	for range e.o.sse {
		lwg.Go(func() { gen.sse(lctx) })
	}
	if e.o.scenario == "load" {
		if err := gen.start(lctx, &lwg); err != nil {
			stopLoad()
			lwg.Wait()
			return fail(err)
		}
	}
	s.setPhase("measure")
	r.WindowStart = time.Now()
	err = sleep(ctx, e.o.duration)
	r.WindowEnd = time.Now()
	if err == nil && e.o.cpuProfile > 0 {
		s.setPhase("profile")
		if perr := s.saveProfile(ctx, "/debug/pprof/profile?seconds="+strconv.Itoa(int(e.o.cpuProfile.Seconds())),
			filepath.Join(e.out, "cpu.pprof"), e.o.cpuProfile+time.Minute); perr != nil {
			fmt.Fprintln(os.Stderr, "bench: cpu profile:", perr)
		}
	}
	s.setPhase("end")
	for name, path := range map[string]string{"allocs-end.pprof": "/debug/pprof/allocs",
		"heap.pprof": "/debug/pprof/heap", "goroutine.txt": "/debug/pprof/goroutine?debug=2"} {
		_ = s.saveProfile(context.WithoutCancel(ctx), path, filepath.Join(e.out, name), time.Minute)
	}
	s.sampleNow(context.WithoutCancel(ctx), time.Now())
	stopLoad()
	lwg.Wait()
	gen.finish(context.WithoutCancel(ctx))
	stopAll()
	r.fill(s, gen, readSpawnLog(e.spawns))
	return r, err
}

func copyFile(from, to string) {
	data, err := os.ReadFile(from)
	if err == nil {
		_ = os.WriteFile(to, data, 0o600)
	}
}

func gitCommit(ctx context.Context, root string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short=12", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
