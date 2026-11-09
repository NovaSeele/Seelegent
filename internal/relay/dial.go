package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/mcpwire"
)

// Discovery follows internal/client (docs/mcp.md, "Finding and starting the daemon"), reduced to what the relay
// needs and written without net/http, config or client, which link crypto. Every case the relay does not handle
// itself (another program or another data directory's daemon on the port, an unreadable config.json, a daemon that
// will not start) ends the first connection with an error, and the relay then runs `seelegent mcp`, which reports it.
const (
	envHome        = "SEELEGENT_HOME"
	envPort        = "SEELEGENT_PORT"
	envNoAutoStart = "SEELEGENT_NO_AUTO_START"
	defaultDirName = ".seelegent"
	defaultPort    = 47821
	// exitDaemonLocked is `seelegent daemon`'s exit code when another daemon holds the data directory's lock
	// (client.ExitDaemonLocked).
	exitDaemonLocked = 3
)

// errNotRunning: nothing accepts a connection on the daemon's port.
var errNotRunning = errors.New("no Seelegent daemon answers")

// unusableError: something answered, but no relayed session came of it (an older daemon without the route, another
// program, another data directory's daemon, a refused handshake, no answer in time).
type unusableError struct{ reason string }

func (e *unusableError) Error() string { return e.reason }

// daemonConn is an upgraded connection after an accepted handshake.
type daemonConn struct {
	c       net.Conn
	r       *bufio.Reader
	port    int
	version string
}

// readLine reads one line of at most limit bytes, without its end.
func (d *daemonConn) readLine(limit int) ([]byte, error) { return readLine(d.r, limit) }

func (d *daemonConn) write(line []byte) error {
	_, err := d.c.Write(append(append(make([]byte, 0, len(line)+1), line...), '\n'))
	return err
}

func (d *daemonConn) close() { _ = d.c.Close() }

// discovery finds the daemon of one data directory.
type discovery struct {
	o    *Options
	home string
	run  string
	// port is the configured port ($SEELEGENT_PORT, else config.json's port, else 47821).
	port int
	// hello is the handshake line.
	hello []byte
}

// newDiscovery reads where the daemon is. An error means the relay cannot tell (an unreadable config.json, a bad
// SEELEGENT_PORT): `seelegent mcp` then reports it.
func newDiscovery(o *Options) (*discovery, error) {
	home := strings.TrimSpace(o.Getenv(envHome))
	if home == "" {
		uh, err := o.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("the data directory cannot be found: %w (set %s)", err, envHome)
		}
		home = filepath.Join(uh, defaultDirName)
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, fmt.Errorf("the data directory %s: %w", home, err)
	}
	d := &discovery{o: o, home: home, run: filepath.Join(home, "run")}
	if d.port, err = configuredPort(home, o.Getenv); err != nil {
		return nil, err
	}
	cwd, err := o.Getwd()
	if err != nil {
		return nil, fmt.Errorf("the working directory cannot be read: %w", err)
	}
	hs := mcpwire.Handshake{Protocol: mcpwire.Protocol, Env: map[string]string{}, Cwd: cwd, PID: os.Getpid(),
		Home: home}
	for _, k := range mcpwire.SessionEnv() {
		if v, ok := o.LookupEnv(k); ok {
			hs.Env[k] = v
		}
	}
	if d.hello, err = json.Marshal(hs); err != nil {
		return nil, err
	}
	return d, nil
}

// configuredPort is $SEELEGENT_PORT when set, else the port setting of config.json, else the default.
func configuredPort(home string, getenv func(string) string) (int, error) {
	if raw := strings.TrimSpace(getenv(envPort)); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return 0, fmt.Errorf("%s=%q is not a port", envPort, raw)
		}
		return p, nil
	}
	data, err := fsutil.ReadFile(filepath.Join(home, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return defaultPort, nil
	}
	if err != nil {
		return 0, fmt.Errorf("config.json cannot be read: %w", err)
	}
	var s struct {
		Port *int `json:"port"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), &s); err != nil {
		return 0, fmt.Errorf("config.json cannot be read: %w", err)
	}
	if s.Port == nil {
		return defaultPort, nil
	}
	if *s.Port < 1 || *s.Port > 65535 {
		return 0, fmt.Errorf("config.json: port %d is not a port", *s.Port)
	}
	return *s.Port, nil
}

// lockHeld reports whether a daemon holds the data directory's single-instance lock (an error other than "locked"
// counts as held, as in client).
func (d *discovery) lockHeld() bool {
	l, err := fsutil.TryLock(filepath.Join(d.run, "daemon.lock"))
	if err != nil {
		return true
	}
	_ = l.Unlock()
	return false
}

// ports are the ports to try: the running daemon's port file first (when a daemon holds the lock and it differs),
// then the configured one.
func (d *discovery) ports() []int {
	data, err := os.ReadFile(filepath.Join(d.run, "daemon.port"))
	if err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			if p, err := strconv.Atoi(f[0]); err == nil && p >= 1 && p <= 65535 && p != d.port && d.lockHeld() {
				return []int{p, d.port}
			}
		}
	}
	return []int{d.port}
}

// find connects to the running daemon: errNotRunning when nothing accepts on its ports, an *unusableError when
// something answered without a session.
func (d *discovery) find(ctx context.Context) (*daemonConn, error) {
	var last error
	for _, port := range d.ports() {
		c, err := d.dial(ctx, port)
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if last == nil || !errors.Is(err, errNotRunning) {
			last = err
		}
	}
	return nil, last
}

// dial opens the relay upgrade on port and sends the handshake.
func (d *discovery) dial(ctx context.Context, port int) (*daemonConn, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dl := net.Dialer{Timeout: d.o.ProbeTimeout}
	c, err := dl.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w on %s", errNotRunning, addr)
	}
	ok := false
	defer func() {
		if !ok {
			_ = c.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	_ = c.SetDeadline(time.Now().Add(d.o.HandshakeTimeout))
	req := "POST " + mcpwire.RelayPath + " HTTP/1.1\r\nHost: " + addr + "\r\nConnection: Upgrade\r\nUpgrade: " +
		mcpwire.UpgradeToken + "\r\nX-Seelegent: 1\r\nContent-Length: 0\r\n\r\n"
	if _, err := io.WriteString(c, req); err != nil {
		return nil, &unusableError{fmt.Sprintf("port %d: %v", port, err)}
	}
	br := bufio.NewReader(c)
	status, err := readStatus(br)
	if err != nil {
		return nil, &unusableError{fmt.Sprintf("port %d does not answer as a Seelegent daemon (%v)", port, err)}
	}
	if status != 101 {
		why := fmt.Sprintf("the daemon on port %d answered HTTP %d to the relay", port, status)
		switch status {
		case 404:
			why += " (a daemon of an older build, without the MCP relay)"
		case 403:
			why += " (refused)"
		case 421:
			why += " (another program on the port?)"
		case 503:
			why += " (it cannot serve this relay now)"
		}
		return nil, &unusableError{why}
	}
	if err := (&daemonConn{c: c}).write(d.hello); err != nil {
		return nil, &unusableError{fmt.Sprintf("port %d: the handshake: %v", port, err)}
	}
	line, err := readLine(br, mcpwire.MaxHandshake)
	if err != nil {
		return nil, &unusableError{fmt.Sprintf("port %d: no handshake answer (%v)", port, err)}
	}
	var reply mcpwire.Reply
	if err := json.Unmarshal(line, &reply); err != nil {
		return nil, &unusableError{fmt.Sprintf("port %d: the handshake answer is not JSON", port)}
	}
	if !reply.OK {
		return nil, &unusableError{fmt.Sprintf("the daemon on port %d refused the relay (%s: %s)", port, reply.Code,
			reply.Error)}
	}
	_ = c.SetDeadline(time.Time{})
	ok = true
	return &daemonConn{c: c, r: br, port: port, version: reply.Version}, nil
}

// readStatus reads an HTTP/1.1 status line and the headers after it, and returns the status code.
func readStatus(br *bufio.Reader) (int, error) {
	line, err := readLine(br, 8<<10)
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(line))
	if len(f) < 2 || !strings.HasPrefix(f[0], "HTTP/1.") {
		return 0, fmt.Errorf("not an HTTP answer: %.40q", line)
	}
	code, err := strconv.Atoi(f[1])
	if err != nil {
		return 0, fmt.Errorf("not an HTTP status: %.40q", line)
	}
	for range 100 {
		h, err := readLine(br, 8<<10)
		if err != nil {
			return 0, err
		}
		if len(h) == 0 {
			return code, nil
		}
	}
	return 0, errors.New("too many header lines")
}

// readLine reads one line of at most limit bytes, without its end.
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(buf)+len(chunk) > limit {
			return nil, fmt.Errorf("a line longer than %d bytes", limit)
		}
		buf = append(buf, chunk...)
		switch {
		case err == nil:
			return bytes.TrimRight(buf, "\r\n"), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(buf) > 0:
			return nil, io.ErrUnexpectedEOF
		default:
			return nil, err
		}
	}
}

// connect returns a session connection, starting the daemon when nothing answers and start is true.
func (d *discovery) connect(ctx context.Context, start bool) (*daemonConn, error) {
	c, err := d.find(ctx)
	if err == nil || !errors.Is(err, errNotRunning) || !start || d.o.Start == nil {
		return c, err
	}
	return d.start(ctx)
}

// start starts the daemon under run/start.lock, as client.Daemon.Ensure does, and waits until a session opens.
// One deadline (StartTimeout) covers the whole start. While another daemon of this home holds its lock without
// answering (starting, or shutting down) nothing is started; a started daemon that exits for the lock (code 3)
// means another one runs, and the wait goes on for it.
func (d *discovery) start(ctx context.Context) (*daemonConn, error) {
	ctx, cancel := context.WithTimeout(ctx, d.o.StartTimeout)
	defer cancel()
	if err := os.MkdirAll(d.run, 0o700); err != nil {
		return nil, err
	}
	lock, err := fsutil.Lock(ctx, filepath.Join(d.run, "start.lock"))
	if err != nil {
		if c, ferr := d.find(context.WithoutCancel(ctx)); ferr == nil {
			return c, nil
		}
		return nil, fmt.Errorf("%w: another process has been starting the daemon for %s", errNotRunning,
			d.o.StartTimeout)
	}
	defer func() { _ = lock.Unlock() }()
	var exited <-chan error
	started := false
	tick := time.NewTicker(d.o.PollInterval)
	defer tick.Stop()
	for {
		c, err := d.find(ctx)
		switch {
		case err == nil:
			return c, nil
		case ctx.Err() != nil:
			return nil, fmt.Errorf("%w: the daemon did not answer within %s", errNotRunning, d.o.StartTimeout)
		case !errors.Is(err, errNotRunning):
			return nil, err
		case !started && !d.lockHeld():
			started = true
			if exited, err = d.o.Start(ctx, d.home, d.port); err != nil {
				return nil, fmt.Errorf("start the Seelegent daemon: %w", err)
			}
		}
		select {
		case <-ctx.Done():
		case xerr, ok := <-exited:
			exited = nil
			if ok && exitCode(xerr) != exitDaemonLocked {
				return nil, fmt.Errorf("%w: the Seelegent daemon exited before it answered (%w); see %s",
					errNotRunning, xerr, filepath.Join(d.home, "logs", "daemon.log"))
			}
		case <-tick.C:
		}
	}
}
