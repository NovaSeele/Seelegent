package cli

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/mcpserver"
)

// envNoAutoStart turns off starting the daemon from the MCP server and the command line.
const envNoAutoStart = "SEELEGENT_NO_AUTO_START"

func newMCPCommand(info buildinfo.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server for a lead host over stdin and stdout",
		Long: "Run the MCP server that a lead host (Claude Code, Codex, Antigravity, OpenCode) starts. It speaks\n" +
			"JSON-RPC on stdin and stdout and forwards every tool to the daemon, starting the daemon when it is\n" +
			"not running. Logs go to stderr.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCP(cmd.Context(), info, &mcp.StdioTransport{}, cmd.ErrOrStderr())
		},
	}
}

// runMCP serves MCP on t (stdin and stdout for the command) until the host closes it. Nothing else writes to
// stdout: logs go to stderr.
func runMCP(ctx context.Context, info buildinfo.Info, t mcp.Transport, stderr io.Writer) error {
	log := logx.NewConsoleLogger(stderr, slog.LevelInfo)
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	// Read the settings without creating or writing anything: the daemon owns the data directory.
	settings, configErr := config.Defaults(), error(nil)
	if c, err := config.Load(paths.Config); err != nil {
		configErr = err
	} else {
		settings = c.Settings
	}
	port, err := config.ResolvePort(settings, os.Getenv)
	if err != nil {
		configErr, port = err, settings.Port
	}
	var start client.StartFunc
	if os.Getenv(envNoAutoStart) != "1" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		start = client.SpawnDaemon(exe, paths, port, os.Environ())
	}
	daemon := client.NewDaemon(client.DaemonOptions{Paths: paths, Port: port, Start: start, Logger: log})
	srv, err := mcpserver.New(mcpserver.Options{Daemon: daemon, Version: info.Version, Port: port,
		Tiers: settings.Backends.Agy.Tiers, ConfigErr: configErr, Logger: log})
	if err != nil {
		return err
	}
	if configErr != nil {
		log.Error("mcp: settings", "err", configErr)
	}
	return srv.Run(ctx, t)
}
