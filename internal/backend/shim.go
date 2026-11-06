package backend

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// checkShimArgs refuses arguments that cmd.exe would interpret when the CLI is a .cmd or .bat shim on Windows (an
// npm install of claude or codex): Windows runs such a file through cmd.exe, which treats these characters as
// operators or variables no matter how the argument is quoted.
func checkShimArgs(argv []string) error {
	if runtime.GOOS != "windows" || len(argv) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(argv[0]))
	if ext != ".cmd" && ext != ".bat" {
		return nil
	}
	for _, a := range argv[1:] {
		if strings.ContainsAny(a, "\"%^&|<>!\r\n") {
			return fmt.Errorf("%s is a batch file shim and argument %q holds characters cmd.exe would interpret; "+
				"install the native binary or point %s/%s at it", filepath.Base(argv[0]), a, EnvClaudeBin, EnvCodexBin)
		}
	}
	return nil
}
