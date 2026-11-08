package backend

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/NovaSeele/Seelegent/internal/envx"
)

// checkShimArgs refuses arguments that cmd.exe would interpret when the CLI is a .cmd or .bat shim on Windows (an
// npm install of claude or codex): Windows runs such a file through cmd.exe, which treats these characters as
// operators or variables no matter how the argument is quoted. The rule is envx.BatchUnsafeArg, shared with setup.
func checkShimArgs(argv []string) error {
	if len(argv) == 0 {
		return nil
	}
	if a, bad := envx.BatchUnsafeArg(argv[0], runtime.GOOS, argv[1:]); bad {
		return fmt.Errorf("%s is a batch file shim and argument %q holds characters cmd.exe would interpret; "+
			"install the native binary or point %s/%s at it", filepath.Base(argv[0]), a, EnvClaudeBin, EnvCodexBin)
	}
	return nil
}
