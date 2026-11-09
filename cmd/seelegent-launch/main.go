// Command seelegent-launch is Seelegent's companion executable, which links no crypto package. The full binary
// carries the 32 MiB entropy buffer of crypto/internal/fips140/drbg, which Windows charges as commit in every
// process; this one runs in about 12 MB. It ships next to seelegent and has two roles:
//
//   - `seelegent-launch __launch <dir>` runs exactly what `seelegent __launch <dir>` runs (launch.Main), one
//     launcher per running task; the daemon starts it instead of `seelegent __launch` when it is of the daemon's
//     build (package launchbin).
//   - `seelegent-launch mcp` is the MCP relay (package relay): a lead host starts it instead of `seelegent mcp`
//     (setup registers it when it is of the same build as seelegent), and the MCP server runs in the daemon.
package main

import (
	"os"

	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/relay"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == relay.Subcommand {
		// The relay only copies lines: the launcher's runtime limits (two Ps, a small soft memory limit) fit it.
		launch.LimitRuntime(os.Getenv)
		os.Exit(relay.Main(os.Args[2:]))
	}
	os.Exit(launch.Main(os.Args[1:], os.Stderr))
}
