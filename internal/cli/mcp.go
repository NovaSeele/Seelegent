package cli

import (
	"context"
	"io"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/mcpserver"
)

func newMCPCommand(info buildinfo.Info, d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server for a lead host over stdin and stdout",
		Long: "Run the MCP server that a lead host (Claude Code, Codex, Antigravity, OpenCode) starts. It speaks\n" +
			"JSON-RPC on stdin and stdout and forwards every tool to the daemon, starting the daemon when it is\n" +
			"not running. Logs go to stderr.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMCP(cmd.Context(), info, d, &mcp.StdioTransport{}, cmd.ErrOrStderr())
		},
	}
}

// runMCP serves MCP on t (stdin and stdout for the command) until the host closes it. Nothing else writes to
// stdout: logs go to stderr.
func runMCP(ctx context.Context, info buildinfo.Info, d deps, t mcp.Transport, stderr io.Writer) error {
	log := logx.NewConsoleLogger(stderr, slog.LevelInfo)
	c, err := d.connect(startAuto, log)
	if err != nil {
		return err
	}
	srv, err := mcpserver.New(mcpserver.Options{Daemon: c.daemon, Version: info.Version, Port: c.port,
		Tiers: c.settings.Backends.Agy.Tiers, ConfigErr: c.configErr, Logger: log})
	if err != nil {
		return err
	}
	if c.configErr != nil {
		log.Error("mcp: settings", "err", c.configErr)
	}
	return srv.Run(ctx, t)
}
