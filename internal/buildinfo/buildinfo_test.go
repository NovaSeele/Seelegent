package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

func TestResolveDefaults(t *testing.T) {
	got := resolve("", "", "", nil)
	if got.Version != DefaultVersion || got.Commit != Unknown || got.Date != Unknown {
		t.Fatalf("resolve() = %+v, want defaults", got)
	}
	if got.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", got.GoVersion, runtime.Version())
	}
	if want := runtime.GOOS + "/" + runtime.GOARCH; got.Platform != want {
		t.Errorf("Platform = %q, want %q", got.Platform, want)
	}
}

func TestResolveLinkerValuesWin(t *testing.T) {
	bi := &debug.BuildInfo{
		Main: debug.Module{Version: "v9.9.9"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "ffffffffffffffffffff"},
			{Key: "vcs.time", Value: "2000-01-01T00:00:00Z"},
		},
	}
	got := resolve("v1.2.3", "abc1234", "2026-11-06T10:00:00Z", bi)
	want := Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-11-06T10:00:00Z"}
	if got.Version != want.Version || got.Commit != want.Commit || got.Date != want.Date {
		t.Fatalf("resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveFromBuildInfo(t *testing.T) {
	tests := []struct {
		name        string
		mainVersion string
		wantVersion string
	}{
		{name: "module version", mainVersion: "v0.4.0", wantVersion: "v0.4.0"},
		{name: "devel build", mainVersion: "(devel)", wantVersion: DefaultVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bi := &debug.BuildInfo{
				Main: debug.Module{Version: tt.mainVersion},
				Settings: []debug.BuildSetting{
					{Key: "vcs.revision", Value: "0123456789abcdef0123"},
					{Key: "vcs.time", Value: "2026-11-01T08:00:00Z"},
				},
			}
			got := resolve("", "", "", bi)
			if got.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", got.Version, tt.wantVersion)
			}
			if got.Commit != "0123456789ab" {
				t.Errorf("Commit = %q, want the 12-character prefix", got.Commit)
			}
			if got.Date != "2026-11-01T08:00:00Z" {
				t.Errorf("Date = %q, want the VCS time", got.Date)
			}
		})
	}
}

func TestInfoString(t *testing.T) {
	s := Info{Version: "v1.2.3", Commit: "abc1234", Date: "2026-11-06", GoVersion: "go1.27.3", Platform: "linux/amd64"}.String()
	want := "seelegent v1.2.3 (commit abc1234, built 2026-11-06, go1.27.3 linux/amd64)"
	if s != want {
		t.Errorf("String() = %q, want %q", s, want)
	}
}

func TestGet(t *testing.T) {
	got := Get()
	if got.Version == "" || got.Commit == "" || got.Date == "" {
		t.Fatalf("Get() = %+v, want no empty fields", got)
	}
	if !strings.HasPrefix(got.String(), "seelegent ") {
		t.Errorf("String() = %q", got.String())
	}
}
