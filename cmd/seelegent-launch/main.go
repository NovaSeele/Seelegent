// Command seelegent-launch is the Seelegent launcher alone: `seelegent-launch __launch <dir>` runs exactly what
// `seelegent __launch <dir>` runs (launch.Main), in a small executable that links no crypto package. The full
// binary carries the 32 MiB entropy buffer of crypto/internal/fips140/drbg, which Windows charges as commit in every
// launcher process (one per running task); this one runs in about 12 MB. It ships next to seelegent, and the daemon
// starts it instead of `seelegent __launch` when it is of the daemon's build (package launchbin).
package main

import (
	"os"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

func main() {
	os.Exit(launch.Main(os.Args[1:], os.Stderr))
}
