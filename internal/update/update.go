// Package update replaces the installed seelegent executable with a newer release (seelegent update): it reads the
// release information from GitHub (or SEELEGENT_UPDATE_URL), downloads the archive for this platform into the data
// folder, checks its SHA-256 against the release's checksums.txt, makes sure the new executable runs, stops the
// daemon, puts the new executable in place of the old one (kept next to it for --rollback), starts the daemon again
// and refreshes the lead hosts that already have a Seelegent entry. The executable's path never changes, so the
// hosts' MCP entries keep working (old C-B1).
//
// A failed step leaves the old executable in place: every replacement is a rename in the executable's folder that
// is undone on failure, and a new daemon that does not start puts the old executable back.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Daemon controls the daemon of the data folder.
type Daemon interface {
	// Running reports whether this data folder's daemon runs.
	Running(ctx context.Context) (bool, error)
	// Stop stops it gracefully (open waits are answered and survive a quick restart).
	Stop(ctx context.Context) error
	// Start starts it from the executable exe and waits until it answers.
	Start(ctx context.Context, exe string) error
}

// RunFunc runs the executable exe with args and returns its standard output (and an *exec.ExitError for a non-zero
// exit; the output is returned then too).
type RunFunc func(ctx context.Context, exe string, args ...string) ([]byte, error)

// Options configure an Updater.
type Options struct {
	// Current is the version of the running build (buildinfo).
	Current string
	// Exe is the installed executable to replace (absolute, links resolved).
	Exe string
	// WorkDir is the folder for downloads (<data folder>/update); it is created when needed.
	WorkDir string
	// MetadataURL overrides the release information address (SEELEGENT_UPDATE_URL).
	MetadataURL string
	// GOOS and GOARCH select the archive (runtime values when empty).
	GOOS, GOARCH string
	// HTTP is the client for every download (NewHTTPClient when nil).
	HTTP *http.Client
	// Daemon is the daemon of the data folder; nil means none is ever stopped or started.
	Daemon Daemon
	// Run runs an executable (the new one's version check, the host refresh).
	Run RunFunc
	// Progress, when set, gets one line per step.
	Progress func(msg string)
	// Timeout bounds a check (1 min) or an apply's downloads (10 min) when > 0.
	Timeout time.Duration
}

// Updater checks for, applies and rolls back updates.
type Updater struct{ o Options }

// New returns an Updater for o.
func New(o Options) *Updater {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.HTTP == nil {
		o.HTTP = NewHTTPClient()
	}
	return &Updater{o: o}
}

// Errors that front ends report with their own codes.
var (
	// ErrDevBuild: the running build has no release version, so it cannot tell what is newer.
	ErrDevBuild = errors.New("this is a development build")
	// ErrNoPrevious: there is no executable kept for --rollback.
	ErrNoPrevious = errors.New("no previous version is kept")
)

// CheckResult is the answer of a check.
type CheckResult struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
	// DevBuild is true when the running build has no release version (dev, between tags, dirty, snapshot).
	DevBuild bool `json:"devBuild,omitempty"`
	// ReleaseURL is the release page.
	ReleaseURL string `json:"releaseUrl,omitempty"`
	// Asset is the archive this platform would install.
	Asset   string `json:"asset,omitempty"`
	Message string `json:"message"`
}

// ApplyResult is the answer of an apply.
type ApplyResult struct {
	Applied         bool   `json:"applied"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Version         string `json:"version"`
	PreviousVersion string `json:"previousVersion"`
	// Exe is the installed executable; Previous the replaced one, kept for --rollback.
	Exe      string `json:"exe"`
	Previous string `json:"previous,omitempty"`
	// DaemonRestarted is true when the daemon ran and was started again from the new executable.
	DaemonRestarted bool `json:"daemonRestarted"`
	// Hosts are the host items the refresh changed or checked (empty when no host has a Seelegent entry).
	Hosts    []HostItem `json:"hosts"`
	Warnings []string   `json:"warnings,omitempty"`
	Message  string     `json:"message"`
}

// RollbackResult is the answer of a rollback.
type RollbackResult struct {
	OK bool `json:"ok"`
	// PreviousVersion is the version that was installed before the rollback (it is discarded); CurrentVersion the
	// one installed now.
	PreviousVersion string     `json:"previousVersion"`
	CurrentVersion  string     `json:"currentVersion"`
	Exe             string     `json:"exe"`
	DaemonRestarted bool       `json:"daemonRestarted"`
	Hosts           []HostItem `json:"hosts"`
	Warnings        []string   `json:"warnings,omitempty"`
	Message         string     `json:"message"`
}

func (u *Updater) progress(format string, a ...any) {
	if u.o.Progress != nil {
		u.o.Progress(fmt.Sprintf(format, a...))
	}
}

func (u *Updater) withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if u.o.Timeout > 0 {
		d = u.o.Timeout
	}
	return context.WithTimeout(ctx, d)
}

// Check reads the latest release (or tag, when set) and compares it with the running build.
func (u *Updater) Check(ctx context.Context, tag string) (CheckResult, error) {
	ctx, cancel := u.withTimeout(ctx, time.Minute)
	defer cancel()
	rel, err := u.fetchRelease(ctx, tag)
	if err != nil {
		return CheckResult{}, err
	}
	res := CheckResult{CurrentVersion: u.o.Current, LatestVersion: rel.Version.String(), ReleaseURL: rel.HTMLURL}
	if a, _, err := rel.assets(u.o.GOOS, u.o.GOARCH); err == nil {
		res.Asset = a.Name
	}
	cur, ok := releaseVersion(u.o.Current)
	switch {
	case !ok:
		res.DevBuild = true
		res.Message = fmt.Sprintf("this is a development build (%s); the release is %s. `seelegent update --apply "+
			"--version %s` installs it over this build", u.o.Current, rel.Version, rel.Version)
	case tag != "":
		res.UpdateAvailable = rel.Version.Compare(cur) != 0
		res.Message = fmt.Sprintf("%s is installed; %s is available", cur, rel.Version)
		if !res.UpdateAvailable {
			res.Message = fmt.Sprintf("%s is installed already", cur)
		}
	case rel.Version.Compare(cur) > 0:
		res.UpdateAvailable = true
		res.Message = fmt.Sprintf("%s is available (installed: %s); run `seelegent update --apply`", rel.Version, cur)
	default:
		res.Message = fmt.Sprintf("up to date (%s)", cur)
	}
	if res.UpdateAvailable && res.Asset == "" {
		res.Message += fmt.Sprintf(" (but it has no build for %s/%s)", u.o.GOOS, u.o.GOARCH)
	}
	return res, nil
}

// lock takes the update lock in the work folder, so two updates never run at once.
func (u *Updater) lock() (*fsutil.FileLock, error) {
	if err := os.MkdirAll(u.o.WorkDir, 0o700); err != nil {
		return nil, err
	}
	l, err := fsutil.TryLock(filepath.Join(u.o.WorkDir, "update.lock"))
	if errors.Is(err, fsutil.ErrLocked) {
		return nil, errors.New("another seelegent update is running")
	}
	return l, err
}

// checkExe makes sure the executable to replace is an installed seelegent (not a `go run` or test build).
func (u *Updater) checkExe() error {
	want := binaryName(u.o.GOOS)
	if !filepath.IsAbs(u.o.Exe) || !strings.EqualFold(filepath.Base(u.o.Exe), want) {
		return fmt.Errorf("update replaces an installed %s, but this one is %s (a development or test build?): "+
			"install a release with the installer instead", want, u.o.Exe)
	}
	st, err := os.Stat(u.o.Exe)
	if err != nil {
		return fmt.Errorf("the installed executable: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("the installed executable %s is not a regular file", u.o.Exe)
	}
	return nil
}

// Apply installs the latest release (or tag, when set: that exact version, older ones too).
func (u *Updater) Apply(ctx context.Context, tag string) (ApplyResult, error) {
	res := ApplyResult{PreviousVersion: u.o.Current, Version: u.o.Current, Exe: u.o.Exe, Hosts: []HostItem{}}
	cur, isRelease := releaseVersion(u.o.Current)
	if !isRelease && tag == "" {
		return res, fmt.Errorf("%w (%s): it cannot tell which release is newer; pass --version vX.Y.Z to install "+
			"that release over it", ErrDevBuild, u.o.Current)
	}
	if err := u.checkExe(); err != nil {
		return res, err
	}
	lock, err := u.lock()
	if err != nil {
		return res, err
	}
	defer func() { _ = lock.Unlock() }()
	sweep(u.o.Exe)

	dlCtx, cancel := u.withTimeout(ctx, 10*time.Minute)
	defer cancel()
	u.progress("Reading the release information...")
	rel, err := u.fetchRelease(dlCtx, tag)
	if err != nil {
		return res, err
	}
	if isRelease && (rel.Version.Compare(cur) == 0 || (tag == "" && rel.Version.Compare(cur) < 0)) {
		res.Message = fmt.Sprintf("already up to date (%s)", cur)
		return res, nil
	}
	res.UpdateAvailable = true
	staged, err := u.stage(dlCtx, rel)
	if err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(staged) }() // gone after a successful swap

	running, err := u.daemonRunning(ctx)
	if err != nil {
		return res, err
	}
	if running {
		u.progress("Stopping the daemon...")
		if err := u.o.Daemon.Stop(ctx); err != nil {
			return res, fmt.Errorf("stop the daemon: %w; nothing was changed", err)
		}
	}
	u.progress("Replacing %s...", u.o.Exe)
	sw, err := replaceExe(u.o.Exe, staged, PreviousPath(u.o.Exe))
	if err != nil {
		return res, u.restartAfterFailure(ctx, running, err)
	}
	if running {
		u.progress("Starting the daemon...")
		if err := u.o.Daemon.Start(ctx, u.o.Exe); err != nil {
			return res, u.undoAfterStartFailure(ctx, sw, err)
		}
		res.DaemonRestarted = true
	}
	res.Applied, res.Version, res.Previous = true, rel.Version.String(), sw.aside
	res.Hosts, res.Warnings = u.refreshHosts(ctx)
	sweep(u.o.Exe)
	res.Message = fmt.Sprintf("updated %s -> %s", u.o.Current, rel.Version)
	return res, nil
}

// stage downloads and checks the release's archive and returns the new executable next to the installed one
// (<exe>.new), ready to be renamed into place.
func (u *Updater) stage(ctx context.Context, rel Release) (string, error) {
	archive, sums, err := rel.assets(u.o.GOOS, u.o.GOARCH)
	if err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(u.o.WorkDir, "download-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	u.progress("Downloading %s...", archive.Name)
	sumData, err := u.readAll(ctx, sums.URL, "", maxChecksums)
	if err != nil {
		return "", err
	}
	arc := filepath.Join(tmp, archive.Name)
	if err := u.download(ctx, archive.URL, arc, maxArchive); err != nil {
		return "", err
	}
	if err := verify(arc, archive.Name, sumData); err != nil {
		return "", err
	}
	u.progress("Checksum verified (SHA-256).")
	extracted := filepath.Join(tmp, binaryName(u.o.GOOS))
	if err := extractBinary(arc, extracted, u.o.GOOS); err != nil {
		return "", err
	}
	if err := u.checkRuns(ctx, extracted, rel.Version); err != nil {
		return "", err
	}
	staged := u.o.Exe + newSuffix
	_ = os.Remove(staged)
	if err := copyExecutable(extracted, staged); err != nil {
		return "", fmt.Errorf("copy the new executable next to %s: %w", u.o.Exe, err)
	}
	return staged, nil
}

// versionOf runs `exe version --json` and returns the version it reports.
func (u *Updater) versionOf(ctx context.Context, exe string) (string, error) {
	if u.o.Run == nil {
		return "", errors.New("running executables is not available")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := u.o.Run(ctx, exe, "version", "--json")
	if err != nil {
		return "", fmt.Errorf("%s does not run: %w", exe, err)
	}
	var info struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &info); err != nil || info.Version == "" {
		return "", fmt.Errorf("%s does not report its version: %q", exe, strings.TrimSpace(string(out)))
	}
	return info.Version, nil
}

// checkRuns makes sure the downloaded executable runs here and is the release it claims to be.
func (u *Updater) checkRuns(ctx context.Context, exe string, want Version) error {
	got, err := u.versionOf(ctx, exe)
	if err != nil {
		return fmt.Errorf("the downloaded executable: %w; nothing was installed", err)
	}
	if v, err := ParseVersion(got); err != nil || v.Compare(want) != 0 {
		return fmt.Errorf("the downloaded executable reports version %q, not %s; nothing was installed", got, want)
	}
	return nil
}

func (u *Updater) daemonRunning(ctx context.Context) (bool, error) {
	if u.o.Daemon == nil {
		return false, nil
	}
	running, err := u.o.Daemon.Running(ctx)
	if err != nil {
		return false, fmt.Errorf("look for the daemon: %w; nothing was changed", err)
	}
	return running, nil
}

// restartAfterFailure starts the old daemon again after the executable could not be replaced.
func (u *Updater) restartAfterFailure(ctx context.Context, running bool, cause error) error {
	if running {
		if err := u.o.Daemon.Start(ctx, u.o.Exe); err != nil {
			return fmt.Errorf("%w; starting the daemon again failed too: %w", cause, err)
		}
	}
	return cause
}

// undoAfterStartFailure puts the old executable back when the new daemon does not start, and starts the old one.
func (u *Updater) undoAfterStartFailure(ctx context.Context, sw *swap, cause error) error {
	if err := sw.undo(); err != nil {
		return fmt.Errorf("the new daemon did not start: %w; putting the old executable back failed: %w", cause, err)
	}
	_ = os.Remove(sw.src)
	if err := u.o.Daemon.Start(ctx, u.o.Exe); err != nil {
		return fmt.Errorf("the new daemon did not start: %w; the old executable is back, but its daemon did not "+
			"start either: %w", cause, err)
	}
	return fmt.Errorf("the new daemon did not start: %w; the old executable is back in place and running", cause)
}

// Rollback puts back the executable the last update replaced. The version rolled back from is discarded, so a
// second rollback has nothing to go back to (old C-B15: it rolled forward again).
func (u *Updater) Rollback(ctx context.Context) (RollbackResult, error) {
	res := RollbackResult{Exe: u.o.Exe, Hosts: []HostItem{}}
	if err := u.checkExe(); err != nil {
		return res, err
	}
	lock, err := u.lock()
	if err != nil {
		return res, err
	}
	defer func() { _ = lock.Unlock() }()
	sweep(u.o.Exe)
	prev := PreviousPath(u.o.Exe)
	if _, err := os.Stat(prev); errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("%w next to %s: a rollback goes back once, to the version the last update replaced",
			ErrNoPrevious, u.o.Exe)
	}
	prevVersion, err := u.versionOf(ctx, prev)
	if err != nil {
		return res, fmt.Errorf("the kept executable: %w; nothing was changed", err)
	}
	res.PreviousVersion = u.o.Current
	if v, err := u.versionOf(ctx, u.o.Exe); err == nil {
		res.PreviousVersion = v
	}
	running, err := u.daemonRunning(ctx)
	if err != nil {
		return res, err
	}
	if running {
		u.progress("Stopping the daemon...")
		if err := u.o.Daemon.Stop(ctx); err != nil {
			return res, fmt.Errorf("stop the daemon: %w; nothing was changed", err)
		}
	}
	u.progress("Putting back %s...", prevVersion)
	sw, err := replaceExe(u.o.Exe, prev, "")
	if err != nil {
		return res, u.restartAfterFailure(ctx, running, err)
	}
	if running {
		u.progress("Starting the daemon...")
		if err := u.o.Daemon.Start(ctx, u.o.Exe); err != nil {
			return res, u.undoAfterStartFailure(ctx, sw, err)
		}
		res.DaemonRestarted = true
	}
	res.OK, res.CurrentVersion = true, prevVersion
	res.Hosts, res.Warnings = u.refreshHosts(ctx)
	sweep(u.o.Exe)
	res.Message = fmt.Sprintf("rolled back %s -> %s", res.PreviousVersion, prevVersion)
	return res, nil
}
