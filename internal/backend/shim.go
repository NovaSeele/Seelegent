package backend

import (
	"fmt"
	"path/filepath"

	"github.com/NovaSeele/Seelegent/internal/envx"
)

// checkShimArgs refuses arguments that cmd.exe would interpret when the CLI is a .cmd or .bat shim on Windows (an
// npm install of claude or codex): Windows runs such a file through cmd.exe, which treats these characters as
// operators or variables no matter how the argument is quoted. The rule is envx.BatchUnsafeArg, shared with setup.
// goos is the operating system the command runs on (runtime.GOOS outside tests).
func checkShimArgs(argv []string, goos string) error {
	if len(argv) == 0 {
		return nil
	}
	if a, bad := envx.BatchUnsafeArg(argv[0], goos, argv[1:]); bad {
		return fmt.Errorf("%s is a batch file shim and argument %q holds characters cmd.exe would interpret; "+
			"install the native binary or point %s/%s at it", filepath.Base(argv[0]), a, EnvClaudeBin, EnvCodexBin)
	}
	return nil
}
