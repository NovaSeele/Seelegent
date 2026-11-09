// Package launchbin finds the companion launcher executable (cmd/seelegent-launch) and tells whether the daemon
// may start it instead of `seelegent __launch`.
//
// The companion runs the same launcher code in about 12 MB of private memory instead of about 51 MB (it links no
// crypto package, so not the 32 MiB entropy buffer Windows charges as commit; see launch.LimitRuntime). It is used
// only when it is safe: the file sits next to the daemon's own executable, under its fixed name, and its build
// information (read from the file, nothing is run) says it is the launcher's main package built from the same source
// as the daemon: the same module version, Go version and build settings (VCS revision and time, GOOS, GOARCH, tags,
// ...), from a clean checkout. Anything else, a missing file included, falls back to `seelegent __launch`, which
// always works.
//
// The source is told by the VCS revision, so the build must name one and must not be from a modified tree: two
// builds of one revision with different uncommitted edits carry the same information, and their launchers could
// differ. -ldflags is no help there: with -trimpath (every Seelegent build) Go does not record it. The -X version
// stamp it carries does not change the launcher's code, which does not link internal/buildinfo
// (cmd/seelegent-launch's TestLinksNoCrypto checks that too).
package launchbin

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/launch"
)

// Name is the companion's file name without the extension (seelegent-launch.exe on Windows).
const Name = "seelegent-launch"

// MainPackage is the import path of the companion's main package, as its build information names it.
const MainPackage = "github.com/NovaSeele/Seelegent/cmd/seelegent-launch"

// FileName is the companion's file name on goos.
func FileName(goos string) string {
	if goos == "windows" {
		return Name + ".exe"
	}
	return Name
}

// PathNextTo is where the companion of the executable exe sits: next to it, under FileName.
func PathNextTo(exe string) string { return filepath.Join(filepath.Dir(exe), FileName(runtime.GOOS)) }

// Match returns nil when the build companion is the companion of the build main: companion is the launcher's main
// package of the same module version, Go version and build settings, and both name a VCS revision of a clean tree
// (see the package comment). Otherwise the error says what differs.
func Match(main, companion *debug.BuildInfo) error {
	switch {
	case main == nil:
		return errors.New("this executable carries no build information")
	case companion == nil:
		return errors.New("it carries no build information")
	case companion.Path != MainPackage:
		return fmt.Errorf("it is %q, not the Seelegent launcher", companion.Path)
	case companion.Main.Path != main.Main.Path || companion.Main.Version != main.Main.Version:
		return fmt.Errorf("it is module %s %s, this executable %s %s", companion.Main.Path, companion.Main.Version,
			main.Main.Path, main.Main.Version)
	case companion.GoVersion != main.GoVersion:
		return fmt.Errorf("it was built with %s, this executable with %s", companion.GoVersion, main.GoVersion)
	}
	want, got := settings(main), settings(companion)
	switch {
	case want["vcs.revision"] == "":
		return errors.New("this executable names no VCS revision (built outside a git checkout, or with " +
			"-buildvcs=false), so the source of the two cannot be compared")
	case want["vcs.modified"] != "false":
		return errors.New("this executable was built from a modified tree, so the source of the two cannot be " +
			"compared (commit, or build from a clean checkout)")
	}
	for _, k := range slices.Sorted(maps.Keys(union(want, got))) {
		if want[k] != got[k] {
			return fmt.Errorf("its build setting %s is %q, this executable's %q", k, got[k], want[k])
		}
	}
	return nil
}

// MatchFiles is Match for two executable files, read without running them (seelegent update checks a release's
// companion against its executable with it).
func MatchFiles(exe, companion string) error {
	main, err := buildinfo.ReadFile(exe)
	if err != nil {
		return fmt.Errorf("the build information of %s cannot be read: %w", filepath.Base(exe), err)
	}
	info, err := buildinfo.ReadFile(companion)
	if err != nil {
		return fmt.Errorf("its build information cannot be read: %w", err)
	}
	return Match(main, info)
}

func settings(b *debug.BuildInfo) map[string]string {
	m := make(map[string]string, len(b.Settings))
	for _, s := range b.Settings {
		m[s.Key] = s.Value
	}
	return m
}

func union(a, b map[string]string) map[string]string {
	out := maps.Clone(a)
	maps.Copy(out, b)
	return out
}

// Status is what Inspect found.
type Status struct {
	// Path is where the companion is looked for.
	Path string
	// Present is true when a file is there.
	Present bool
	// Usable is true when the daemon of this build may start it.
	Usable bool
	// Reason says why it is not usable ("" when it is).
	Reason string
}

// Inspect looks at the companion of the executable exe, whose build information is self.
func Inspect(exe string, self *debug.BuildInfo) Status {
	st, _ := inspect(exe, self)
	return st
}

// fileID tells two states of a file apart without reading it.
type fileID struct {
	path string
	mod  time.Time
	size int64
}

func inspect(exe string, self *debug.BuildInfo) (Status, fileID) {
	s := Status{Path: PathNextTo(exe)}
	fi, err := os.Stat(s.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.Reason = "it is not there"
		} else {
			s.Present, s.Reason = true, fmt.Sprintf("it cannot be read: %v", err)
		}
		return s, fileID{path: s.Path}
	}
	id := fileID{path: s.Path, mod: fi.ModTime(), size: fi.Size()}
	s.Present = true
	if !fi.Mode().IsRegular() {
		s.Reason = "it is not a regular file"
		return s, id
	}
	return check(s, self), id
}

// check reads the companion's build information and matches it with self.
func check(s Status, self *debug.BuildInfo) Status {
	info, err := buildinfo.ReadFile(s.Path)
	if err != nil {
		s.Reason = fmt.Sprintf("its build information cannot be read: %v", err)
		return s
	}
	if err := Match(self, info); err != nil {
		s.Reason = "it is not of this build: " + err.Error()
		return s
	}
	s.Usable = true
	return s
}

// Resolver picks the launcher command for each launch: the companion when it is usable, else `exe __launch`. It
// looks at the companion's file on every call (one stat) and reads its build information again only when the file
// changed (path, modification time and size), so a companion that appears, goes or is replaced while the daemon
// runs is noticed at the next launch. Each change of the decision is logged once.
type Resolver struct {
	exe  string
	self *debug.BuildInfo
	log  *slog.Logger

	mu     sync.Mutex
	id     fileID
	cached *Status
	logged string // the decision last logged
}

// NewResolver returns a Resolver for the daemon's executable exe (absolute, links resolved), whose build
// information is self (debug.ReadBuildInfo). log gets the decisions (use a logx logger).
func NewResolver(exe string, self *debug.BuildInfo, log *slog.Logger) *Resolver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Resolver{exe: exe, self: self, log: log}
}

// Command returns the launcher command for the next launch (launch.Options.Launcher).
func (r *Resolver) Command() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.statusLocked()
	if s.Usable {
		r.logOnce("using "+s.Path, slog.LevelInfo, "launcher: the companion executable runs the launches", "path", s.Path)
		return []string{s.Path, launch.Subcommand}
	}
	level := slog.LevelInfo
	if s.Present {
		level = slog.LevelWarn
	}
	r.logOnce("fallback "+s.Reason, level, "launcher: the companion executable is not used, launches run as "+
		"`seelegent "+launch.Subcommand+"` (about 40 MB more private memory each on Windows)", "path", s.Path,
		"reason", s.Reason)
	return []string{r.exe, launch.Subcommand}
}

// statusLocked returns the companion's status, from the cache while its file is unchanged.
func (r *Resolver) statusLocked() Status {
	s := Status{Path: PathNextTo(r.exe)}
	fi, err := os.Stat(s.Path)
	if err == nil && fi.Mode().IsRegular() && r.cached != nil &&
		r.id == (fileID{path: s.Path, mod: fi.ModTime(), size: fi.Size()}) {
		return *r.cached
	}
	st, id := inspect(r.exe, r.self)
	r.id, r.cached = id, &st
	if !st.Present || id.mod.IsZero() {
		r.cached = nil // nothing to cache: look again next time
	}
	return st
}

func (r *Resolver) logOnce(decision string, level slog.Level, msg string, args ...any) {
	if r.logged == decision {
		return
	}
	r.logged = decision
	r.log.Log(context.Background(), level, msg, args...)
}

// Describe is a one-line account of a status for people (doctor).
func Describe(s Status) string {
	switch {
	case s.Usable:
		return fmt.Sprintf("%s is of this build: launchers run in it", s.Path)
	case !s.Present:
		return fmt.Sprintf("%s is not there: launchers run as `seelegent %s` (works the same, about 40 MB more "+
			"private memory each on Windows)", s.Path, launch.Subcommand)
	default:
		return fmt.Sprintf("%s is not used (%s): launchers run as `seelegent %s`", s.Path,
			strings.TrimSuffix(s.Reason, "."), launch.Subcommand)
	}
}
