package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// buildinfoPkg is the package the -ldflags -X stamps go into.
const buildinfoPkg = "github.com/NovaSeele/Seelegent/internal/buildinfo"

// TestLinksNoCrypto: the launcher links no crypto package on any system Seelegent ships for, nor internal/buildinfo. Importing any of them
// links the 32 MiB entropy buffer of crypto/internal/fips140/drbg (its initializer refers to it), which Windows
// charges as commit in every launcher process; that buffer is what the companion executable exists to leave out.
func TestLinksNoCrypto(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	for _, goos := range []string{"windows", "linux", "darwin"} {
		cmd := exec.CommandContext(t.Context(), gobin, "list", "-deps", ".")
		cmd.Env = append(os.Environ(), "GOOS="+goos, "CGO_ENABLED=0")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("GOOS=%s go list -deps: %v", goos, err)
		}
		for _, pkg := range strings.Fields(string(out)) {
			if pkg == "crypto" || strings.HasPrefix(pkg, "crypto/") {
				t.Errorf("GOOS=%s: the launcher links %s; see TestLinksNoCrypto", goos, pkg)
			}
			if pkg == buildinfoPkg {
				t.Errorf("GOOS=%s: the launcher links %s, so its code would depend on the -X version stamp, which "+
					"builds with -trimpath do not record and the daemon cannot compare (launchbin)", goos, pkg)
			}
		}
	}
}
