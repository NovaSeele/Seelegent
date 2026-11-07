package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/app"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/config"
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

// runDaemon runs the daemon until ctx ends.
func runDaemon(ctx context.Context, info buildinfo.Info, f daemonFlags, stderr io.Writer) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
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
	log.Info("daemon: running", "version", info.Version, "home", paths.Home, "addr", addr)
	_, _ = fmt.Fprintf(stderr, "Seelegent %s listening on http://%s/ (Ctrl+C stops it)\n", info.Version, addr)
	<-ctx.Done()
	log.Info("daemon: stopping")
	return closeApp()
}
