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
	"slices"
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
	job     *treeJob
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
	// With -bin the module is not needed (a bench binary cross-compiled for WSL runs from a temp folder).
	if e.root, err = moduleRoot(); err != nil && o.bin == "" {
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
	for _, d := range []string{e.home, e.project, e.queue, filepath.Join(e.work, "userhome"),
		filepath.Join(e.work, "fakebin"), filepath.Join(e.work, "appdata"), filepath.Join(e.work, "localappdata"),
		filepath.Join(e.work, "ghconfig")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return err
		}
	}
	// The bench measures the daemon's own cost: the memory thresholds are off, so a busy machine cannot hold the
	// bench's tasks (missing keys keep their defaults).
	off := []byte(`{"minFreeMemoryMB": 0, "minFreeCommitMB": 0, "criticalFreeMemoryMB": 0}` + "\n")
	if err := os.WriteFile(filepath.Join(e.home, "config.json"), off, 0o600); err != nil {
		return err
	}
	if err := e.build(ctx); err != nil {
		return err
	}
	// The fake gh: a copy of this binary named gh, first on the daemon's PATH.
	if err := copyExe(self, filepath.Join(e.work, "fakebin", exeName("gh"))); err != nil {
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

// copyExe copies an executable (a hard link when the file system allows it).
func copyExe(from, to string) error {
	if os.Link(from, to) == nil {
		return nil
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, data, 0o700)
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

// hostVars are variables the daemon's environment never takes from this machine: GitHub sign-ins and hosts, and
// the per-user folders where gh (%APPDATA%\GitHub CLI), the real CLIs and their sign-ins live.
func hostVars() []string {
	return []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GH_HOST",
		"GH_CONFIG_DIR", "GH_PATH", "APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "HOME", "USERPROFILE", "GOMEMLIMIT",
		"GOGC", "GOCOVERDIR", envSpawnLog}
}

// daemonEnv is the daemon's environment: this machine's minus anything Seelegent, the fake CLI or gh reads, plus
// the temp data folder, a temp user home and per-user folders, the fake agy, the fake gh first on PATH and the
// queue. The bench never uses the user's gh sign-in or reaches GitHub.
func (e *env) daemonEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		ku := strings.ToUpper(k)
		if strings.HasPrefix(ku, "SEELEGENT_") || strings.HasPrefix(ku, "FAKECLI_") || slices.Contains(hostVars(), ku) {
			continue
		}
		if ku == "PATH" {
			kv = k + "=" + filepath.Join(e.work, "fakebin") + string(os.PathListSeparator) + v
		}
		out = append(out, kv)
	}
	userHome := filepath.Join(e.work, "userhome")
	missing := filepath.Join(e.work, "not-installed")
	return append(out, "SEELEGENT_HOME="+e.home, "HOME="+userHome, "USERPROFILE="+userHome,
		"APPDATA="+filepath.Join(e.work, "appdata"), "LOCALAPPDATA="+filepath.Join(e.work, "localappdata"),
		"GH_CONFIG_DIR="+filepath.Join(e.work, "ghconfig"),
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
	// Windows: a Job Object accounts the whole tree, short-lived processes included.
	if job, err := newTreeJob(cmd.Process.Pid); err == nil {
		e.job = job
	} else if runtime.GOOS == "windows" {
		fmt.Fprintln(os.Stderr, "bench: no job accounting:", err)
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

// costMark is the CPU the daemon and its whole tree (the job) used up to a moment.
type costMark struct {
	at        time.Time
	daemonCPU time.Duration
	job       *jobStat
}

func (e *env) mark(pid int) costMark {
	m := costMark{at: time.Now()}
	if st, err := procStats(pid); err == nil {
		m.daemonCPU = st.CPU
	}
	if e.job != nil {
		if js, err := e.job.stats(); err == nil {
			m.job = &js
		}
	}
	return m
}

// phaseCost is what one setup phase cost: the daemon's CPU and the tree's (job) per unit.
func phaseCost(name, unit string, n int, from, to costMark) setupPhase {
	p := setupPhase{Name: name, Unit: unit, Count: n, Seconds: round(to.at.Sub(from.at).Seconds(), 1),
		DaemonCPUSeconds: round((to.daemonCPU - from.daemonCPU).Seconds(), 3)}
	if from.job != nil && to.job != nil {
		p.TreeCPUSeconds = round((to.job.CPU - from.job.CPU).Seconds(), 3)
		p.ProcessesStarted = to.job.Started - from.job.Started
	}
	if n > 0 {
		p.DaemonCPUPerUnit = round(p.DaemonCPUSeconds/float64(n), 3)
		p.TreeCPUPerUnit = round(p.TreeCPUSeconds/float64(n), 3)
	}
	return p
}

// scenario runs the setup, the warmup, the measured time and the profile window, then stops the daemon.
func (e *env) scenario(ctx context.Context) (*result, error) {
	cmd, exited, err := e.startDaemon(ctx)
	if err != nil {
		return nil, err
	}
	pid := cmd.Process.Pid
	s := newSampler(pid, e.self, filepath.Join(e.home, "seelegent.db"), e.pprof, e.job)
	sctx, stopSampler := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { s.run(sctx, e.o.interval, 250*time.Millisecond) })
	r := &result{Scenario: e.o.scenario, Started: time.Now(), Options: optionsOf(e.o), GOOS: runtime.GOOS,
		GOARCH: runtime.GOARCH, NumCPU: runtime.NumCPU(), Commit: cmpOrString(e.o.commit, gitCommit(ctx, e.root))}
	// The setup profile runs from the daemon's start; the warmup waits for it.
	var profWG sync.WaitGroup
	if e.o.setupProf > 0 {
		profWG.Go(func() {
			path := "/debug/pprof/profile?seconds=" + strconv.Itoa(int(e.o.setupProf.Seconds()))
			if err := s.saveProfile(ctx, path, filepath.Join(e.out, "setup-cpu.pprof"), e.o.setupProf+time.Minute); err != nil {
				fmt.Fprintln(os.Stderr, "bench: setup cpu profile:", err)
			}
		})
	}
	stopAll := func() {
		e.stopDaemon(ctx, cmd, exited)
		profWG.Wait()
		stopSampler()
		wg.Wait()
		if e.job != nil {
			if js, err := e.job.stats(); err == nil {
				r.TreeFinal = &js
			}
			e.job.close()
		}
		copyFile(filepath.Join(e.home, "logs", "daemon.log"), filepath.Join(e.out, "daemon.log"))
	}
	fail := func(err error) (*result, error) {
		stopAll()
		return nil, err
	}

	m0 := e.mark(pid)
	if err := e.seedAccounts(ctx); err != nil {
		return fail(err)
	}
	m1 := e.mark(pid)
	if err := e.seedTasks(ctx); err != nil {
		return fail(err)
	}
	m2 := e.mark(pid)
	r.Setup = append(r.Setup, phaseCost("accounts", "account", e.o.accounts, m0, m1),
		phaseCost("seed tasks", "task", e.o.seedTasks, m1, m2))
	s.setPhase("warmup")
	profWG.Wait()
	if err := sleep(ctx, e.o.warmup); err != nil {
		return fail(err)
	}
	_ = s.saveProfile(ctx, "/debug/pprof/allocs", filepath.Join(e.out, "allocs-start.pprof"), time.Minute)

	gen := &loadGen{e: e, lat: map[string]*latencies{"list": {}, "detail": {}}}
	lctx, stopLoad := context.WithCancel(ctx)
	var lwg sync.WaitGroup
	if e.o.scenario == "load" {
		// The load tasks' start is measured alone: the pollers and the SSE clients start once every task streams.
		m3 := e.mark(pid)
		if err := gen.delegateAll(lctx); err != nil {
			stopLoad()
			return fail(err)
		}
		if err := gen.waitStreaming(lctx, time.Minute); err != nil {
			fmt.Fprintln(os.Stderr, "bench:", err)
		}
		r.Setup = append(r.Setup, phaseCost("load task start", "task", e.o.tasks, m3, e.mark(pid)))
		gen.startPollers(lctx, &lwg)
	}
	for range e.o.sse {
		lwg.Go(func() { gen.sse(lctx) })
	}
	s.setPhase("measure")
	r.WindowStart = time.Now()
	// The base of the window's rates: without it they would start at the sample before, up to an interval earlier
	// (the load tasks' start included).
	s.sampleNow(ctx, r.WindowStart)
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
	r.Machine.OSVersion = osVersion()
	return r, err
}

func copyFile(from, to string) {
	data, err := os.ReadFile(from)
	if err == nil {
		_ = os.WriteFile(to, data, 0o600)
	}
}

func cmpOrString(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func gitCommit(ctx context.Context, root string) string {
	if root == "" {
		return ""
	}
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--short=12", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
