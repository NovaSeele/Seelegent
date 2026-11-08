package launchbin

import (
	"bytes"
	"debug/buildinfo"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

func info(path string, settings ...string) *debug.BuildInfo {
	b := &debug.BuildInfo{Path: path, GoVersion: "go1.26.1",
		Main: debug.Module{Path: "github.com/NovaSeele/Seelegent", Version: "v1.2.3"}}
	for i := 0; i+1 < len(settings); i += 2 {
		b.Settings = append(b.Settings, debug.BuildSetting{Key: settings[i], Value: settings[i+1]})
	}
	return b
}

func TestMatch(t *testing.T) {
	const daemon = "github.com/NovaSeele/Seelegent/cmd/seelegent"
	main := info(daemon, "vcs.revision", "abc", "GOOS", "windows", "-ldflags", "-X v=1.2.3")
	if err := Match(main, info(MainPackage, "GOOS", "windows", "-ldflags", "-X v=1.2.3", "vcs.revision", "abc")); err != nil {
		t.Errorf("same build: %v", err)
	}
	for name, c := range map[string]struct {
		main, companion *debug.BuildInfo
		want            string
	}{
		"no own info":      {nil, info(MainPackage), "this executable carries no build information"},
		"no info":          {main, nil, "it carries no build information"},
		"other program":    {main, info(daemon, "vcs.revision", "abc"), "not the Seelegent launcher"},
		"other revision":   {main, info(MainPackage, "vcs.revision", "abd", "GOOS", "windows", "-ldflags", "-X v=1.2.3"), "vcs.revision"},
		"other version":    {main, info(MainPackage, "vcs.revision", "abc", "GOOS", "windows", "-ldflags", "-X v=1.2.4"), "-ldflags"},
		"setting missing":  {main, info(MainPackage, "vcs.revision", "abc", "GOOS", "windows"), "-ldflags"},
		"setting extra":    {main, info(MainPackage, "vcs.revision", "abc", "GOOS", "windows", "-ldflags", "-X v=1.2.3", "vcs.modified", "true"), "vcs.modified"},
		"other go version": {main, func() *debug.BuildInfo { b := info(MainPackage); b.GoVersion = "go1.26.2"; return b }(), "built with go1.26.2"},
		"other module":     {main, func() *debug.BuildInfo { b := info(MainPackage); b.Main.Version = "v1.2.4"; return b }(), "v1.2.4"},
	} {
		err := Match(c.main, c.companion)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: Match = %v, want an error naming %q", name, err, c.want)
		}
	}
}

// records keeps the messages logged.
type records struct{ msgs []string }

func (r *records) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		r.msgs = append(r.msgs, string(p))
		return len(p), nil
	}), nil))
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func TestResolverFallsBack(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "seelegent.exe")
	self := info("github.com/NovaSeele/Seelegent/cmd/seelegent")
	var logs records
	r := NewResolver(exe, self, logs.logger())
	want := []string{exe, launch.Subcommand}
	for range 3 {
		if got := r.Command(); !slices.Equal(got, want) {
			t.Fatalf("no companion: Command = %q, want %q", got, want)
		}
	}
	if len(logs.msgs) != 1 || !strings.Contains(logs.msgs[0], "level=INFO") ||
		!strings.Contains(logs.msgs[0], "not there") {
		t.Fatalf("no companion: logged %q, want one INFO line saying it is not there", logs.msgs)
	}
	if st := Inspect(exe, self); st.Present || st.Usable || st.Path != PathNextTo(exe) {
		t.Errorf("Inspect = %+v", st)
	}

	// A file that is not a Go executable: present, not used, a warning once.
	if err := os.WriteFile(PathNextTo(exe), []byte("not a program"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if got := r.Command(); !slices.Equal(got, want) {
			t.Fatalf("broken companion: Command = %q, want %q", got, want)
		}
	}
	if len(logs.msgs) != 2 || !strings.Contains(logs.msgs[1], "level=WARN") ||
		!strings.Contains(logs.msgs[1], "build information cannot be read") {
		t.Fatalf("broken companion: logged %q, want a second line, a WARN naming the build information", logs.msgs)
	}
	st := Inspect(exe, self)
	if !st.Present || st.Usable || !strings.Contains(Describe(st), "is not used") {
		t.Errorf("Inspect = %+v, Describe %q", st, Describe(st))
	}
	if d := Describe(Status{Path: "x"}); !strings.Contains(d, "is not there") {
		t.Errorf("Describe(absent) = %q", d)
	}
}

// TestResolverUsesCompanion builds the real companion and checks that the resolver picks it when it is of the
// daemon's build, notices when it is replaced by something else, and logs each change of decision once.
func TestResolverUsesCompanion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds cmd/seelegent-launch")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "seelegent"+filepath.Ext(FileName(runtime.GOOS)))
	companion := PathNextTo(exe)
	cmd := exec.CommandContext(t.Context(), gobin, "build", "-trimpath", "-o", companion,
		"github.com/NovaSeele/Seelegent/cmd/seelegent-launch")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	built, err := buildinfo.ReadFile(companion)
	if err != nil {
		t.Fatal(err)
	}
	// The daemon of the same build: the companion's information under the daemon's package path.
	self := *built
	self.Path = "github.com/NovaSeele/Seelegent/cmd/seelegent"

	var logs records
	r := NewResolver(exe, &self, logs.logger())
	for range 2 {
		if got, want := r.Command(), []string{companion, launch.Subcommand}; !slices.Equal(got, want) {
			t.Fatalf("Command = %q, want %q", got, want)
		}
	}
	if st := Inspect(exe, &self); !st.Usable || !strings.Contains(Describe(st), "of this build") {
		t.Errorf("Inspect = %+v, Describe %q", st, Describe(st))
	}

	// Another build of the daemon: the same file no longer matches.
	other := self
	other.Settings = append(slices.Clone(self.Settings), debug.BuildSetting{Key: "vcs.revision", Value: "other"})
	if got := NewResolver(exe, &other, nil).Command(); got[0] != exe {
		t.Errorf("other build: Command = %q, want the daemon itself", got)
	}

	// The file replaced (other size and time): read again, and the fallback logged.
	data, err := os.ReadFile(companion)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(companion, bytes.ToUpper(data[:4096]), 0o700); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(companion, later, later); err != nil {
		t.Fatal(err)
	}
	if got := r.Command(); got[0] != exe {
		t.Errorf("replaced: Command = %q, want the daemon itself", got)
	}
	if len(logs.msgs) != 2 || !strings.Contains(logs.msgs[0], "runs the launches") ||
		!strings.Contains(logs.msgs[1], "level=WARN") {
		t.Errorf("logged %q, want the companion in use, then one warning", logs.msgs)
	}
}
