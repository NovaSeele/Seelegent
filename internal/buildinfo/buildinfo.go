// Package buildinfo reports the version, commit and build date of the binary.
//
// Release builds set the values with the linker:
//
//	go build -ldflags "-X github.com/NovaSeele/Seelegent/internal/buildinfo.version=v1.2.3 \
//	  -X github.com/NovaSeele/Seelegent/internal/buildinfo.commit=abc1234 \
//	  -X github.com/NovaSeele/Seelegent/internal/buildinfo.date=2026-11-06T10:00:00Z"
//
// Builds without ldflags fall back to the VCS data that the Go toolchain embeds, then to "dev"/"unknown".
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Defaults used when neither ldflags nor embedded VCS data provide a value.
const (
	DefaultVersion = "dev"
	Unknown        = "unknown"
)

// Set at link time with -ldflags "-X ...". These are the only package-level variables and are never written at
// run time.
var (
	version string
	commit  string
	date    string
)

// Info describes one build of the binary.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build information of the running binary.
func Get() Info {
	bi, _ := debug.ReadBuildInfo()
	return resolve(version, commit, date, bi)
}

// resolve combines the linker values with the toolchain's build info; linker values win.
func resolve(version, commit, date string, bi *debug.BuildInfo) Info {
	info := Info{
		Version:   version,
		Commit:    commit,
		Date:      date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	if bi != nil {
		if info.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if info.Commit == "" {
					info.Commit = shortCommit(s.Value)
				}
			case "vcs.time":
				if info.Date == "" {
					info.Date = s.Value
				}
			}
		}
	}
	if info.Version == "" {
		info.Version = DefaultVersion
	}
	if info.Commit == "" {
		info.Commit = Unknown
	}
	if info.Date == "" {
		info.Date = Unknown
	}
	return info
}

func shortCommit(rev string) string {
	const n = 12
	if len(rev) > n {
		return rev[:n]
	}
	return rev
}

// String returns a one-line summary, for example
// "seelegent v1.2.3 (commit abc1234, built 2026-11-06T10:00:00Z, go1.27.3 windows/amd64)".
func (i Info) String() string {
	return fmt.Sprintf("seelegent %s (commit %s, built %s, %s %s)", i.Version, i.Commit, i.Date, i.GoVersion, i.Platform)
}
