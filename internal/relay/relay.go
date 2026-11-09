// Package relay is `seelegent-launch mcp`: a thin stdio relay that a lead host starts instead of `seelegent mcp`.
// It opens one relayed MCP session on the daemon (POST /api/v1/mcp/relay, an upgraded connection; see docs/api.md
// and docs/mcp.md) and copies newline-delimited JSON-RPC between the host and that session. The MCP server itself
// runs inside the daemon, so each lead host window costs a small process (no crypto, no HTTP client, no MCP SDK)
// instead of a full seelegent process with its 32 MiB entropy buffer.
//
// The relay forwards only the environment variables the MCP server reads (mcpwire.SessionEnv) and its working
// directory. It finds and starts the daemon like internal/client. When it cannot open a session at the start (an
// older daemon, a refused handshake, no daemon after the start, an unreadable config.json), it runs
// `seelegent mcp` from its own folder with the host's stdin, stdout and stderr and exits with its code, so the
// host always works.
//
// It survives a daemon restart (session.go): the host's initialize, initialized and logging/setLevel are replayed
// to the new session, read-only calls in flight are sent again, and mutating calls in flight are answered with a
// tool error, since they may already have taken effect.
//
// This package must link no crypto package (cmd/seelegent-launch TestLinksNoCrypto): it uses net, bufio and
// encoding/json, and of Seelegent only mcpwire, fsutil and platform.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/mcpwire"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Subcommand is the companion's first argument that runs the relay: `seelegent-launch mcp`.
const Subcommand = "mcp"

// Defaults of Options.
const (
	DefaultProbeTimeout     = 1500 * time.Millisecond
	DefaultHandshakeTimeout = 10 * time.Second
	DefaultStartTimeout     = 20 * time.Second
	defaultPollInterval     = 250 * time.Millisecond
	defaultMaxBackoff       = 2 * time.Second
	// maxLine bounds one JSON-RPC message (the MCP SDK's own default).
	maxLine = 16 << 20
)

// StartFunc starts `seelegent daemon` for home on port, detached. The channel gets the process's exit.
type StartFunc func(ctx context.Context, home string, port int) (exited <-chan error, err error)

// FallbackFunc runs `seelegent mcp` in place of the relay, with the host's standard streams, and returns its exit
// code. Nothing has been read from stdin when it runs.
type FallbackFunc func(ctx context.Context) int

// Options configure Run. The zero value of a field takes the process's own (os.Stdin, os.Getenv, ...).
type Options struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	LookupEnv      func(string) (string, bool)
	Getwd          func() (string, error)
	UserHomeDir    func() (string, error)
	// Dir is the folder of the relay's executable, where `seelegent[.exe]` is looked for (os.Executable's).
	Dir string
	// Start starts the daemon (SpawnDaemon of Dir's seelegent when nil). It is never called inside a worker or with
	// SEELEGENT_NO_AUTO_START=1.
	Start StartFunc
	// Fallback runs `seelegent mcp` instead (Dir's seelegent with the process's streams when nil).
	Fallback FallbackFunc
	// Grace is how long the relay reconnects after it lost the daemon while calls were open (mcpwire.StopGrace).
	Grace time.Duration
	// ProbeTimeout bounds a TCP connect, HandshakeTimeout the upgrade and handshake, StartTimeout a whole start.
	ProbeTimeout, HandshakeTimeout, StartTimeout time.Duration
	// PollInterval is the pause between probes while a daemon starts; MaxBackoff the longest pause between
	// reconnects.
	PollInterval, MaxBackoff time.Duration
}

// Main is the companion's `mcp` subcommand: args are the arguments after it (none). It returns the exit code.
func Main(args []string) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: seelegent-launch mcp (started by a lead host, like `seelegent mcp`)")
		return 2
	}
	return Run(context.Background(), Options{})
}

// Run relays until the host closes stdin, and returns the exit code.
func Run(ctx context.Context, o Options) int {
	o.defaults()
	allowStart := o.Getenv(mcpwire.EnvWorker) != "1" && o.Getenv(envNoAutoStart) != "1"
	if !allowStart {
		o.Start = nil // inside a worker, or SEELEGENT_NO_AUTO_START=1: only look for the daemon
	}
	d, err := newDiscovery(&o)
	if err != nil {
		return fallback(ctx, &o, err)
	}
	c, err := d.connect(ctx, true)
	if err != nil {
		return fallback(ctx, &o, err)
	}
	s := newSession(&o, d, c)
	return s.run(ctx)
}

func fallback(ctx context.Context, o *Options, why error) int {
	_, _ = fmt.Fprintf(o.Stderr, "seelegent-launch mcp: no relayed session (%v); running `seelegent mcp` instead\n", why)
	return o.Fallback(ctx)
}

func (o *Options) defaults() {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.LookupEnv == nil {
		o.LookupEnv = os.LookupEnv
	}
	if o.Getwd == nil {
		o.Getwd = os.Getwd
	}
	if o.UserHomeDir == nil {
		o.UserHomeDir = os.UserHomeDir
	}
	if o.Dir == "" {
		o.Dir = ownDir()
	}
	if o.Start == nil {
		o.Start = SpawnDaemon(seelegentExe(o.Dir))
	}
	if o.Fallback == nil {
		exe := seelegentExe(o.Dir)
		o.Fallback = func(ctx context.Context) int { return runMCP(ctx, exe, o.Stderr) }
	}
	if o.Grace <= 0 {
		o.Grace = mcpwire.StopGrace
	}
	if o.ProbeTimeout <= 0 {
		o.ProbeTimeout = DefaultProbeTimeout
	}
	if o.HandshakeTimeout <= 0 {
		o.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = defaultPollInterval
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = defaultMaxBackoff
	}
}

// ownDir is the folder of this executable, links resolved.
func ownDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe)
}

// seelegentExe is the seelegent executable in dir.
func seelegentExe(dir string) string {
	name := "seelegent"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// runMCP runs `exe mcp` with this process's standard streams and environment and returns its exit code.
func runMCP(ctx context.Context, exe string, stderr io.Writer) int {
	cmd := exec.CommandContext(ctx, exe, "mcp")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if code := exitCode(err); code >= 0 {
		return code
	}
	_, _ = fmt.Fprintf(stderr, "seelegent-launch mcp: %s mcp: %v\n", exe, err)
	return 1
}

// SpawnDaemon starts `exe daemon --port <port>` detached from this process (it outlives the host; on Windows it
// breaks away from the host's job when allowed and shows no console), with this process's environment plus
// SEELEGENT_HOME=<home>, in the data directory: client.SpawnDaemon.
func SpawnDaemon(exe string) StartFunc {
	return func(_ context.Context, home string, port int) (<-chan error, error) {
		cmd, err := platform.StartDetached(func() *exec.Cmd {
			//nolint:gosec,noctx // our own executable; the daemon outlives every context of this process
			cmd := exec.Command(exe, "daemon", "--port", strconv.Itoa(port))
			cmd.Dir = home
			cmd.Env = withEnv(os.Environ(), envHome, home)
			return cmd
		})
		if err != nil {
			return nil, err
		}
		exited := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			if err == nil {
				err = errors.New("exit status 0")
			}
			exited <- err
		}()
		return exited, nil
	}
}

// withEnv returns env with key set to value (case-insensitively on Windows).
func withEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if k == key || (os.PathSeparator == '\\' && strings.EqualFold(k, key)) {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}

// exitCode is the exit code in err from exec.Cmd.Wait (-1 when there is none).
func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
