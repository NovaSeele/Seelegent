package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/app"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/web"
)

// daemonCloseTimeout bounds the shutdown after Ctrl+C.
const daemonCloseTimeout = 30 * time.Second

// daemonFlags are the options of `seelegent daemon`.
type daemonFlags struct {
	port       int
	devOrigins []string
}

func newDaemonCommand(info buildinfo.Info) *cobra.Command {
	var f daemonFlags
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run the daemon in the foreground until Ctrl+C",
		Long: "Run the daemon in the foreground: the task engine, the HTTP API and the dashboard on 127.0.0.1.\n" +
			"The data directory is SEELEGENT_HOME (default ~/.seelegent); logs go to its logs/daemon.log and\n" +
			"to this terminal.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if f.port < 0 || f.port > 65535 {
				return fmt.Errorf("--port %d: must be 1..65535", f.port)
			}
			return runDaemon(cmd.Context(), info, f, cmd.ErrOrStderr())
		},
	}
	cmd.Flags().IntVar(&f.port, "port", 0, "port on 127.0.0.1 (default: $SEELEGENT_PORT, else the settings' port)")
	cmd.Flags().StringSliceVar(&f.devOrigins, "dev-origin", nil,
		"development only: also accept this loopback browser origin, e.g. http://localhost:5173 for the Vite dev server")
	return cmd
}

// warnDevOrigins logs one Warn line naming the extra origins of --dev-origin: they widen the Host and same-origin
// checks, so a daemon started with them must be easy to spot in the log.
func warnDevOrigins(log *slog.Logger, origins []string) {
	if len(origins) == 0 {
		return
	}
	log.Warn("daemon: --dev-origin accepts extra browser origins; use it for development only",
		"origins", strings.Join(origins, ", "))
}

// daemonLockWait bounds the wait for the single-instance lock: a daemon that is restarting releases it once it has
// closed. A starter that sees this daemon exit for the lock (client.ExitDaemonLocked) finds the running daemon or
// says the previous one is still shutting down.
const daemonLockWait = 3 * time.Second

// daemonRunningError says another daemon holds the data directory's lock.
type daemonRunningError struct {
	home string
	port int
}

func (e *daemonRunningError) Error() string {
	s := "another Seelegent daemon is already running for " + e.home
	if e.port > 0 {
		s += fmt.Sprintf(" (port %d)", e.port)
	}
	return s
}

// lockDaemon takes the single-instance lock of the data directory, waiting up to daemonLockWait. Nothing else (log
// file, store, recovery) is opened before it is held, so a second daemon touches nothing.
func lockDaemon(ctx context.Context, paths config.Paths) (*fsutil.FileLock, error) {
	lctx, cancel := context.WithTimeout(ctx, daemonLockWait)
	defer cancel()
	lock, err := fsutil.Lock(lctx, paths.LockFile)
	if err == nil {
		return lock, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if lctx.Err() != nil {
		port, _ := client.ReadPortFile(paths)
		return nil, &daemonRunningError{home: paths.Home, port: port}
	}
	return nil, err
}

// writePortFile records the listening address's port in run/daemon.port for discovery (client.Daemon.Find), and
// this process's id on a second line so `seelegent stop` and `doctor` can say which process holds the lock when
// the daemon does not answer. Nothing ever kills a process by that id (old B3/B14).
func writePortFile(paths config.Paths, addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(paths.PortFile, []byte(port+"\n"+strconv.Itoa(os.Getpid())+"\n"), 0o600)
}

// runDaemon runs the daemon until ctx ends. It holds run/daemon.lock for its whole life, so one data directory
// never has two daemons, and records its port in run/daemon.port while it listens.
func runDaemon(ctx context.Context, info buildinfo.Info, f daemonFlags, stderr io.Writer) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	lock, err := lockDaemon(ctx, paths)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Unlock() }()
	log, closer, err := logx.NewFileLogger(logx.FileOptions{Dir: paths.Logs, Console: stderr})
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	warnDevOrigins(log, f.devOrigins)
	a, err := app.New(ctx, app.Options{Paths: paths, Logger: log, Info: info, Web: web.FS(), DevOrigins: f.devOrigins})
	if err != nil {
		return err
	}
	closeApp := func() error {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), daemonCloseTimeout)
		defer cancel()
		return a.Close(cctx)
	}
	if err := a.Start(ctx); err != nil {
		_ = closeApp()
		return err
	}
	port := f.port
	if port == 0 {
		if port, err = a.Port(); err != nil {
			_ = closeApp()
			return err
		}
	}
	addr, err := a.ListenHTTP(ctx, port)
	if err != nil {
		_ = closeApp()
		return err
	}
	if err := writePortFile(paths, addr); err != nil {
		_ = closeApp()
		return fmt.Errorf("record the daemon port: %w", err)
	}
	log.Info("daemon: running", "version", info.Version, "home", paths.Home, "addr", addr)
	_, _ = fmt.Fprintf(stderr, "Seelegent %s listening on http://%s/ (Ctrl+C or `seelegent stop` stops it)\n",
		info.Version, addr)
	select {
	case <-ctx.Done():
	case <-a.StopRequested(): // POST /daemon/stop (`seelegent stop`, `restart`): the same clean shutdown
	}
	log.Info("daemon: stopping")
	// The port file goes first, so discovery stops sending callers here; the lock is released after the close.
	if err := os.Remove(paths.PortFile); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("daemon: remove the port file", "err", err)
	}
	return closeApp()
}
