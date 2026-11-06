package backend

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// stdinFileName is the prompt file in the attempt directory for CLIs that read the prompt on standard input.
const stdinFileName = "stdin.txt"

// LaunchSpec turns a turn's command into a launch spec: the worker runs in dir with the given time limit, and a
// prompt passed on standard input is written to attemptDir/stdin.txt (mode 0600) first.
func LaunchSpec(cmd Command, dir, attemptDir string, timeout time.Duration) (launch.Spec, error) {
	spec := launch.Spec{Argv: cmd.Argv, Dir: dir, Timeout: timeout}
	if cmd.HasStdin {
		if attemptDir == "" {
			return launch.Spec{}, fmt.Errorf("launch spec: no attempt directory for the prompt")
		}
		if err := os.MkdirAll(attemptDir, 0o700); err != nil {
			return launch.Spec{}, fmt.Errorf("launch spec: %w", err)
		}
		path := filepath.Join(attemptDir, stdinFileName)
		if err := fsutil.WriteFileAtomic(path, []byte(cmd.Stdin), 0o600); err != nil {
			return launch.Spec{}, fmt.Errorf("launch spec: %w", err)
		}
		spec.StdinPath = path
	}
	return spec, nil
}
