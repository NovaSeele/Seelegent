//go:build bench

package bench

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"slices"
	"time"

	"testing"

	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

// envSpawnLog names the file the fake CLI appends one JSON line to per run (role, pid, own CPU and peak memory).
const envSpawnLog = "BENCH_SPAWN_LOG"

// TestMain is the whole bench. The binary is started four ways:
//   - by the daemon as gh (a copy named gh on the daemon's PATH): it answers the access check offline;
//   - by the daemon's quota readers as `agy -p /usage ...` (FAKECLI_QUEUE set): it answers a healthy quota;
//   - by the daemon's launchers as the worker CLI (FAKECLI_QUEUE set): fakecli replays the next queued script;
//   - by the developer: it parses the bench flags and runs a scenario;
//   - by `go test -run`: it runs the unit tests of the report instead.
func TestMain(m *testing.M) {
	if exeBase(os.Args[0]) == "gh" {
		os.Exit(fakeGh(os.Args[1:]))
	}
	if os.Getenv(fakecli.EnvQueue) != "" {
		if slices.Contains(os.Args[1:], "/usage") {
			os.Exit(fakeUsage())
		}
		logSpawn("worker", time.Now(), false)
		fakecli.Main(m) // never returns
	}
	if len(os.Args) > 2 && os.Args[1] == "__launch" {
		fakecli.Main(m)
	}
	o := registerFlags(flag.CommandLine)
	flag.Parse()
	if f := flag.Lookup("test.run"); f != nil && f.Value.String() != "" {
		// `go test -tags bench -run Test ./scripts/bench`: the bench's own unit tests (report_unit_test.go), no bench.
		os.Exit(m.Run())
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// usageAnswer is a healthy `agy -p /usage --output-format json` answer.
const usageAnswer = `{"status":"SUCCESS","command":{"data":{"groups":[{"name":"Gemini","buckets":[` +
	`{"id":"gemini-weekly","name":"Weekly Limit Remaining","window":"weekly","remaining_fraction":0.8,` +
	`"reset_time":"2030-01-06T00:00:00Z"},` +
	`{"id":"gemini-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":0.9,` +
	`"reset_time":"2030-01-01T05:00:00Z"}]}]}}}`

func fakeUsage() int {
	start := time.Now()
	_, _ = fmt.Fprintln(os.Stdout, usageAnswer)
	logSpawn("quota-reader", start, true)
	return 0
}

// fakeGh is the gh CLI of the bench daemon: the daemon never reaches GitHub or the user's gh sign-in. It answers
// what the access check asks (`gh --version`, `gh api user --jq .login`) and refuses everything else.
func fakeGh(args []string) int {
	start := time.Now()
	defer logSpawn("gh", start, true)
	switch {
	case slices.Equal(args, []string{"--version"}):
		_, _ = fmt.Fprintln(os.Stdout, "gh version 2.99.0 (2026-01-01)\nhttps://github.com/cli/cli/releases/tag/v2.99.0")
		return 0
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		_, _ = fmt.Fprintln(os.Stdout, "bench-user")
		return 0
	}
	_, _ = fmt.Fprintf(os.Stderr, "bench gh: %q is not supported offline\n", args)
	return 1
}

// spawnRecord is one line of the spawn log.
type spawnRecord struct {
	Role   string    `json:"role"`
	PID    int       `json:"pid"`
	At     time.Time `json:"at"`
	Ms     float64   `json:"ms,omitempty"`
	CPUMs  float64   `json:"cpuMs,omitempty"`
	PeakWS uint64    `json:"peakWS,omitempty"`
	Exited bool      `json:"exited"`
}

// logSpawn appends this run to the spawn log; exited records the run's own CPU time and peak working set.
func logSpawn(role string, start time.Time, exited bool) {
	path := os.Getenv(envSpawnLog)
	if path == "" {
		return
	}
	rec := spawnRecord{Role: role, PID: os.Getpid(), At: start, Exited: exited}
	if exited {
		rec.Ms = float64(time.Since(start).Microseconds()) / 1000
		if st, err := selfStats(); err == nil {
			rec.CPUMs, rec.PeakWS = float64(st.CPU.Microseconds())/1000, st.PeakWS
		}
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(data, '\n'))
	_ = f.Close()
}

// options are the bench flags.
type options struct {
	scenario   string
	duration   time.Duration
	warmup     time.Duration
	cpuProfile time.Duration
	setupProf  time.Duration
	interval   time.Duration
	out        string
	baseline   string
	bin        string
	commit     string
	port       int
	pprofPort  int
	accounts   int
	tasks      int
	rate       float64
	sse        int
	poll       time.Duration
	seedTasks  int
	keepHome   bool
}

func registerFlags(fs *flag.FlagSet) *options {
	o := &options{}
	fs.StringVar(&o.scenario, "scenario", "idle", "idle, idle-sse (a dashboard tab left open) or load")
	fs.DurationVar(&o.duration, "duration", 0,
		"measured time (default: idle 22m, at least two quota cycles of 10 minutes; load 5m)")
	fs.DurationVar(&o.warmup, "warmup", 30*time.Second, "time after the setup that is not measured")
	fs.DurationVar(&o.cpuProfile, "cpuprofile", 60*time.Second,
		"CPU profile taken after the measured time (0 = none); kept out of the CPU numbers")
	fs.DurationVar(&o.setupProf, "setup-profile", 90*time.Second,
		"CPU profile from the daemon's start, covering the accounts and the seed tasks (0 = none); the warmup "+
			"waits for it")
	fs.DurationVar(&o.interval, "interval", 5*time.Second, "sampling interval")
	fs.StringVar(&o.out, "out", "", "output folder (default: <temp>/seelegent-bench/<time>)")
	fs.StringVar(&o.baseline, "baseline", "", "an earlier result.json: print the deltas")
	fs.StringVar(&o.bin, "bin", "", "seelegent binary to run (default: build ./cmd/seelegent)")
	fs.StringVar(&o.commit, "commit", "", "commit recorded in the result (default: git rev-parse HEAD of the module)")
	fs.IntVar(&o.port, "port", 0, "daemon port (default: a free port picked for this run)")
	fs.IntVar(&o.pprofPort, "pprof-port", 0, "daemon --pprof-addr port on 127.0.0.1 (default: a free port)")
	fs.IntVar(&o.accounts, "accounts", 6, "fake agy accounts (quota readers on)")
	fs.IntVar(&o.tasks, "tasks", 8, "load: tasks running at once")
	fs.Float64Var(&o.rate, "rate", 100, "load: worker stream lines per second, all tasks together")
	fs.IntVar(&o.sse, "sse", -1, "SSE clients (default: idle 0, idle-sse 1, load 3)")
	fs.DurationVar(&o.poll, "poll", 250*time.Millisecond, "load: interval of the list and the detail requests")
	fs.IntVar(&o.seedTasks, "seed-tasks", 20, "finished tasks created before the warmup")
	fs.BoolVar(&o.keepHome, "keep-home", false, "keep the temp data folder")
	return o
}

func (o *options) normalize() error {
	switch o.scenario {
	case "idle", "idle-sse":
		// At least two quota cycles (quotaRefreshMinutes 10, ±10 % jitter) fall inside the window.
		o.duration = cmpOr(o.duration, 22*time.Minute)
		if o.sse < 0 {
			o.sse = 0
			if o.scenario == "idle-sse" {
				o.sse = 1
			}
		}
	case "load":
		o.duration = cmpOr(o.duration, 5*time.Minute)
		if o.sse < 0 {
			o.sse = 3
		}
	default:
		return fmt.Errorf("-scenario %q: want idle, idle-sse or load", o.scenario)
	}
	if o.port == 47821 || o.pprofPort == 47821 {
		return fmt.Errorf("port 47821 belongs to the installed daemon; the bench uses its own (default: a free port)")
	}
	// A free port per run, so benches that run at the same time never share one (one bench's cleanup once
	// stopped another bench's daemon on the shared default port).
	for _, p := range []*int{&o.port, &o.pprofPort} {
		if *p == 0 {
			free, err := freePort()
			if err != nil {
				return err
			}
			*p = free
		}
	}
	if o.port < 0 || o.pprofPort < 0 || o.port == o.pprofPort {
		return fmt.Errorf("-port and -pprof-port must be two different ports")
	}
	if o.interval <= 0 || o.tasks < 1 || o.rate <= 0 || o.accounts < 1 || o.poll <= 0 {
		return fmt.Errorf("-interval, -tasks, -rate, -accounts and -poll must be positive")
	}
	if o.perAccount() > maxPerAccount {
		return fmt.Errorf("-tasks %d on -accounts %d needs %d turns per account; at most %d: add accounts", o.tasks,
			o.accounts, o.perAccount(), maxPerAccount)
	}
	return nil
}

// defaultPerAccount is the daemon's default maxConcurrentPerAccount; maxPerAccount the most it accepts.
const defaultPerAccount, maxPerAccount = 2, 64

// perAccount is the maxConcurrentPerAccount the bench daemon gets: the default, or more when the load tasks would
// not all fit on the accounts at once (-tasks 16 on 6 accounts needs 3), so every load task runs in parallel.
func (o *options) perAccount() int {
	return max(defaultPerAccount, (o.tasks+o.accounts-1)/o.accounts)
}

// freePort asks the system for a free loopback port. Another program can take it before the daemon listens; the
// daemon then fails to start, and the identity check (env.ours) keeps the bench away from that program.
func freePort() (int, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pick a free port: %w", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("pick a free port: unexpected address %v", ln.Addr())
	}
	return addr.Port, nil
}

func cmpOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}
