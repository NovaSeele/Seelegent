package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// envNoAutoStart turns off starting the daemon from the MCP server and the command line.
const envNoAutoStart = "SEELEGENT_NO_AUTO_START"

// deps are what the commands get from outside the process; tests replace them.
type deps struct {
	// spawn returns how to start the daemon of paths on port (client.SpawnDaemon of this executable).
	spawn func(paths config.Paths, port int) (client.StartFunc, error)
	// openURL opens the dashboard in the browser.
	openURL func(ctx context.Context, url string) error
	// stopTimeout bounds `seelegent stop` (client.DefaultStopTimeout when 0).
	stopTimeout time.Duration
}

// defaultDeps are the real ones.
func defaultDeps() deps {
	return deps{
		spawn: func(paths config.Paths, port int) (client.StartFunc, error) {
			exe, err := os.Executable()
			if err != nil {
				return nil, err
			}
			return client.SpawnDaemon(exe, paths, port, os.Environ()), nil
		},
		openURL: platform.OpenURL,
	}
}

// startMode says whether a command may start the daemon.
type startMode int

const (
	// startAuto starts it unless SEELEGENT_NO_AUTO_START=1 or inside a worker.
	startAuto startMode = iota
	// startNever only looks for a running daemon (status, stop, doctor).
	startNever
	// startAlways starts it even with SEELEGENT_NO_AUTO_START=1 (restart); never inside a worker.
	startAlways
)

// conn is the daemon of the data directory as the settings describe it.
type conn struct {
	paths    config.Paths
	settings config.Settings
	// configErr is why config.json could not be read (the defaults are used then).
	configErr error
	port      int
	daemon    *client.Daemon
	log       *slog.Logger
}

// connect reads the settings without creating or writing anything (the daemon owns the data directory) and builds
// the daemon lookup for mode.
func (d deps) connect(mode startMode, log *slog.Logger) (*conn, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	c := &conn{paths: paths, settings: config.Defaults(), log: log}
	if cfg, err := config.Load(paths.Config); err != nil {
		c.configErr = err
	} else {
		c.settings = cfg.Settings
	}
	c.port, err = config.ResolvePort(c.settings, os.Getenv)
	if err != nil {
		c.configErr, c.port = err, c.settings.Port
	}
	var start client.StartFunc
	noStart := ""
	switch {
	case os.Getenv(account.EnvWorker) == "1":
		// Inside a worker a started daemon would live in the worker's process tree (and die with it), on the
		// worker profile's data directory, with the worker's environment: only look for the daemon running it.
		noStart = fmt.Sprintf("the Seelegent daemon running this worker cannot be reached (looked on port %d); "+
			"a worker never starts a daemon", c.port)
	case mode == startNever:
		noStart = fmt.Sprintf("the Seelegent daemon is not running (looked on port %d)", c.port)
	case mode == startAuto && os.Getenv(envNoAutoStart) == "1":
		// client.Daemon's own message names SEELEGENT_NO_AUTO_START.
	default:
		if start, err = d.spawn(paths, c.port); err != nil {
			return nil, err
		}
	}
	c.daemon = client.NewDaemon(client.DaemonOptions{Paths: paths, Port: c.port, Start: start, NoStart: noStart,
		Logger: log})
	return c, nil
}

// settingsOK fails when config.json cannot be read: commands that act through the daemon stop there, as the MCP
// tools do, instead of acting on defaults the daemon does not use.
func (c *conn) settingsOK() error {
	if c.configErr != nil {
		return fmt.Errorf("the settings cannot be read: %w", c.configErr)
	}
	return nil
}

// cliLogger logs warnings of the client to stderr (the commands print their own results).
func cliLogger(stderr io.Writer) *slog.Logger { return logx.NewConsoleLogger(stderr, slog.LevelWarn) }
