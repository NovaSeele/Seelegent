// Package update replaces the installed seelegent executable with a newer release (seelegent update): it reads the
// release information from GitHub (or SEELEGENT_UPDATE_URL), downloads the archive for this platform into the data
// folder, checks its SHA-256 against the release's checksums.txt, makes sure the new executable runs, stops the
// daemon, puts the new executable in place of the old one (kept next to it for --rollback), starts the daemon again
// and refreshes the lead hosts that already have a Seelegent entry. The executable's path never changes, so the
// hosts' MCP entries keep working (old C-B1).
//
// The companion launcher seelegent-launch next to the executable (package launchbin) goes with it: the one in the
// release's archive, checked to be of the new executable's build, replaces it the same way (the replaced one is kept
// for --rollback too), and a release without one removes it. A running companion (a launcher still runs a task's
// worker) is renamed aside like a running executable and deleted by a later update or rollback.
//
// A failed step leaves the old executable in place: every replacement is a rename in the executable's folder that
// is undone on failure, and a new daemon that does not start (or answers as another build) is stopped and the old
// executable put back, unless that daemon migrated the database past what the old executable knows (then the new one
// stays and the error says so). A build that knows fewer database migrations than the data folder's database has is
// never installed: migrations are not undone, and such a daemon could not open the database.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/NovaSeele/Seelegent/internal/launchbin"
)

// Daemon controls the daemon of the data folder.
type Daemon interface {
	// Running reports whether this data folder's daemon runs.
	Running(ctx context.Context) (bool, error)
	// Stop stops it gracefully (open waits are answered and survive a quick restart).
	Stop(ctx context.Context) error
	// Start starts it from the executable exe, waits until it answers and returns the version it reports.
	Start(ctx context.Context, exe string) (version string, err error)
	// Abort makes sure no daemon of the data folder runs after a Start that failed or started the wrong build: it
	// stops a daemon that answers, kills the process the last Start spawned when it is still there, and waits until
	// the data folder's daemon lock is free. It is called with a context of its own, never a cancelled one.
	Abort(ctx context.Context) error
}

// DatabaseSchemaFunc reads the schema version of the data folder's database without changing it; exists is false
// when there is no database yet.
type DatabaseSchemaFunc func(ctx context.Context) (version int, exists bool, err error)

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
	// DatabaseSchema reads the database's schema version; nil skips the schema check.
	DatabaseSchema DatabaseSchemaFunc
	// Force installs a build that does not report which database schema it knows (a build from before
	// schemaVersion). A build known to be too old for the database is refused even then.
	Force bool
	// CurrentSchema is the number of database migrations the running build knows (0: unknown). The running build is
	// the one an undo puts back, so the undo is skipped when the failed new daemon migrated the database past it.
	CurrentSchema int
	// LockDir is the folder of the executable's update lock (the user's cache folder + seelegent when empty).
	LockDir string
	// CheckCompanion checks that the companion launcher in a release's archive is of the build of the executable
	// next to it (both extracted, not run): launchbin.MatchFiles when nil.
	CheckCompanion func(exe, companion string) error
	// ResetHosts points the lead hosts' existing MCP entries back at `<exe> mcp` before a rollback refreshes them
	// with the restored build's setup: a build from before the MCP relay neither knows a `seelegent-launch mcp`
	// entry as its own nor has a companion that runs it. It returns warnings. Nil does nothing.
	ResetHosts func(ctx context.Context, exe string) []string
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
	if o.CheckCompanion == nil {
		o.CheckCompanion = launchbin.MatchFiles
	}
	return &Updater{o: o}
}

// Errors that front ends report with their own codes.
var (
	// ErrDevBuild: the running build has no release version, so it cannot tell what is newer.
	ErrDevBuild = errors.New("this is a development build")
	// ErrNoPrevious: there is no executable kept for --rollback.
	ErrNoPrevious = errors.New("no previous version is kept")
	// ErrSchemaTooNew: the build to install knows fewer database migrations than the database has.
	ErrSchemaTooNew = errors.New("the database is newer than that build")
	// ErrSchemaUnknown: the build to install does not report which database schema it knows (Options.Force).
	ErrSchemaUnknown = errors.New("that build does not report which database schema it knows")
	// ErrMigratedThenFailed: the new daemon migrated the database and then did not start; the build an undo would put
	// back cannot open the database any more, so the new one stays installed.
	ErrMigratedThenFailed = errors.New("the new daemon migrated the database and then did not start")
)

// migratedThenFailedError is an error with its own message that reports itself as ErrMigratedThenFailed.
type migratedThenFailedError struct{ err error }

func (e *migratedThenFailedError) Error() string        { return e.err.Error() }
func (e *migratedThenFailedError) Unwrap() error        { return e.err }
func (e *migratedThenFailedError) Is(target error) bool { return target == ErrMigratedThenFailed }

// undoTimeout bounds the undo after a failed start. The undo gets a context of its own: a cancelled command (Ctrl+C
// while the new daemon starts) must still stop that daemon and put the old executable back.
const undoTimeout = 3 * time.Minute

// lockSuffix names the executable's update lock next to it, used only when the user has no cache folder.
const lockSuffix = ".update.lock"

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

// lock takes the update lock of the data folder (its downloads and its daemon) and the one of the executable (its
// .new and .previous files), so two updates never run on either at once. The returned function releases both.
func (u *Updater) lock() (func(), error) {
	if err := os.MkdirAll(u.o.WorkDir, 0o700); err != nil {
		return nil, err
	}
	exeLock, err := u.exeLockPath()
	if err != nil {
		return nil, err
	}
	home, err := fsutil.TryLock(filepath.Join(u.o.WorkDir, "update.lock"))
	if errors.Is(err, fsutil.ErrLocked) {
		return nil, errors.New("another seelegent update of this data folder is running")
	}
	if err != nil {
		return nil, err
	}
	exe, err := fsutil.TryLock(exeLock)
	if err != nil {
		_ = home.Unlock()
		if errors.Is(err, fsutil.ErrLocked) {
			return nil, fmt.Errorf("another seelegent update of %s is running", u.o.Exe)
		}
		return nil, err
	}
	return func() { _ = exe.Unlock(); _ = home.Unlock() }, nil
}

// exeLockPath is the executable's update lock. It lives in the user's cache folder (the executable's folder is often
// one on PATH, where a lock file would stay forever), named after the executable's path key, so every data folder
// that updates the same executable takes the same lock. Without a usable cache folder it falls back to a lock next to
// the executable.
func (u *Updater) exeLockPath() (string, error) {
	key, err := fsutil.Key(u.o.Exe)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	name := "update-" + hex.EncodeToString(sum[:8]) + ".lock"
	if u.o.LockDir != "" {
		if err := os.MkdirAll(u.o.LockDir, 0o700); err != nil {
			return "", err
		}
		return filepath.Join(u.o.LockDir, name), nil
	}
	if dir, ok := cacheLockDir(); ok {
		return filepath.Join(dir, name), nil
	}
	return u.o.Exe + lockSuffix, nil
}

// cacheLockDir returns <user cache folder>/seelegent, created when needed; ok is false when there is no user cache
// folder (no HOME, XDG_CACHE_HOME or LocalAppData) or it cannot be created.
func cacheLockDir() (dir string, ok bool) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	dir = filepath.Join(cache, "seelegent")
	return dir, os.MkdirAll(dir, 0o700) == nil
}

// companion is where the executable's companion launcher sits.
func (u *Updater) companion() string {
	return filepath.Join(filepath.Dir(u.o.Exe), launchbin.FileName(u.o.GOOS))
}

// sweepAll sweeps the leftovers of the executable and of its companion launcher.
func (u *Updater) sweepAll() {
	sweep(u.o.Exe)
	sweep(u.companion())
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
	unlock, err := u.lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	u.sweepAll()
	u.sweepDownloads()

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
	staged, build, err := u.stage(dlCtx, rel)
	if err != nil {
		return res, err
	}
	defer staged.remove() // gone after a successful swap
	if err := u.checkSchema(ctx, build, &res.Warnings); err != nil {
		return res, err
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
	u.progress("Replacing %s...", u.o.Exe)
	sw, err := replacePair(u.o.Exe, staged.exe, u.companion(), staged.companion, true)
	if err != nil {
		return res, u.restartAfterFailure(ctx, running, u.o.Current, err)
	}
	if running {
		u.progress("Starting the daemon (database migrations can take a while)...")
		if err := u.startExpect(ctx, build.Version); err != nil {
			return res, u.undoAfterStartFailure(ctx, sw, err, undoInfo{failed: build.Version, back: u.o.Current})
		}
		res.DaemonRestarted = true
	}
	res.Applied, res.Version, res.Previous = true, rel.Version.String(), sw.aside
	hosts, warnings := u.refreshHosts(ctx)
	res.Hosts, res.Warnings = hosts, append(res.Warnings, warnings...)
	u.sweepAll()
	res.Message = fmt.Sprintf("updated %s -> %s", u.o.Current, rel.Version)
	return res, nil
}

// sweepDownloads removes the download folders an interrupted update left in WorkDir (a killed process never runs
// its deferred cleanup). The caller holds the data folder's update lock, so no other update downloads into them.
func (u *Updater) sweepDownloads() {
	left, err := filepath.Glob(filepath.Join(u.o.WorkDir, "download-*"))
	if err != nil {
		return
	}
	for _, p := range left {
		_ = os.RemoveAll(p) // one that cannot go yet is tried again by the next update
	}
}

// staged is what stage put next to the installed files, ready to be renamed into place.
type staged struct {
	// exe is the new executable (<exe>.new); companion the new companion launcher (<companion>.new), "" when the
	// release has none.
	exe, companion string
}

// remove deletes what was not renamed into place.
func (s staged) remove() {
	_ = os.Remove(s.exe)
	if s.companion != "" {
		_ = os.Remove(s.companion)
	}
}

// stage downloads and checks the release's archive and puts the new executable next to the installed one
// (<exe>.new), with what it reports about itself, and the release's companion launcher, checked to be of the new
// executable's build, next to the installed one (<companion>.new).
func (u *Updater) stage(ctx context.Context, rel Release) (st staged, build buildVersion, err error) {
	archive, sums, err := rel.assets(u.o.GOOS, u.o.GOARCH)
	if err != nil {
		return st, build, err
	}
	tmp, err := os.MkdirTemp(u.o.WorkDir, "download-")
	if err != nil {
		return st, build, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	u.progress("Downloading %s...", archive.Name)
	sumData, err := u.readAll(ctx, sums.URL, "", maxChecksums)
	if err != nil {
		return st, build, err
	}
	arc := filepath.Join(tmp, archive.Name)
	if err := u.download(ctx, archive.URL, arc, maxArchive); err != nil {
		return st, build, err
	}
	if err := verify(arc, archive.Name, sumData); err != nil {
		return st, build, err
	}
	u.progress("Checksum verified (SHA-256).")
	extracted := filepath.Join(tmp, binaryName(u.o.GOOS))
	if err := extractBinary(arc, extracted, u.o.GOOS); err != nil {
		return st, build, err
	}
	if build, err = u.checkRuns(ctx, extracted, rel.Version); err != nil {
		return st, build, err
	}
	companion, err := u.extractCompanion(arc, tmp, extracted)
	if err != nil {
		return st, build, err
	}
	st.exe = u.o.Exe + newSuffix
	_ = os.Remove(st.exe)
	if err := copyExecutable(extracted, st.exe); err != nil {
		return staged{}, build, fmt.Errorf("copy the new executable next to %s: %w", u.o.Exe, err)
	}
	if companion != "" {
		st.companion = u.companion() + newSuffix
		_ = os.Remove(st.companion)
		if err := copyExecutable(companion, st.companion); err != nil {
			_ = os.Remove(st.exe)
			return staged{}, build, fmt.Errorf("copy the new companion launcher next to %s: %w", u.o.Exe, err)
		}
	}
	return st, build, nil
}

// extractCompanion extracts the companion launcher from the archive arc into dir and checks that it is of the build
// of exe, the executable extracted from the same archive. It returns "" when the archive holds none (a release from
// before the companion).
func (u *Updater) extractCompanion(arc, dir, exe string) (string, error) {
	name := launchbin.FileName(u.o.GOOS)
	out := filepath.Join(dir, name)
	err := extractFile(arc, out, name)
	if errors.Is(err, errNoEntry) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := u.o.CheckCompanion(exe, out); err != nil {
		return "", fmt.Errorf("the downloaded %s is not of the downloaded %s's build: %w; nothing was installed",
			name, binaryName(u.o.GOOS), err)
	}
	return out, nil
}

// buildVersion is what `seelegent version --json` reports about a build.
type buildVersion struct {
	Version string `json:"version"`
	// SchemaVersion is the number of database migrations the build knows (nil: a build from before the field).
	SchemaVersion *int `json:"schemaVersion"`
}

// versionOf runs `exe version --json` and returns what it reports.
func (u *Updater) versionOf(ctx context.Context, exe string) (buildVersion, error) {
	var info buildVersion
	if u.o.Run == nil {
		return info, errors.New("running executables is not available")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := u.o.Run(ctx, exe, "version", "--json")
	if err != nil {
		return info, fmt.Errorf("%s does not run: %w", exe, err)
	}
	if err := json.Unmarshal(out, &info); err != nil || info.Version == "" {
		return info, fmt.Errorf("%s does not report its version: %q", exe, strings.TrimSpace(string(out)))
	}
	return info, nil
}

// checkRuns makes sure the downloaded executable runs here and is the release it claims to be.
func (u *Updater) checkRuns(ctx context.Context, exe string, want Version) (buildVersion, error) {
	got, err := u.versionOf(ctx, exe)
	if err != nil {
		return got, fmt.Errorf("the downloaded executable: %w; nothing was installed", err)
	}
	if v, err := ParseVersion(got.Version); err != nil || v.Compare(want) != 0 {
		return got, fmt.Errorf("the downloaded executable reports version %q, not %s; nothing was installed",
			got.Version, want)
	}
	return got, nil
}

// checkSchema refuses a build that cannot open the data folder's database: one that knows fewer migrations than the
// database has (its daemon would stop with "schema too new", and migrations are never undone), or one that does not
// say how many it knows, unless Options.Force (then warn gets a warning).
func (u *Updater) checkSchema(ctx context.Context, b buildVersion, warn *[]string) error {
	if u.o.DatabaseSchema == nil {
		return nil
	}
	db, exists, err := u.o.DatabaseSchema(ctx)
	if err != nil {
		return fmt.Errorf("read the database's schema version: %w; nothing was changed", err)
	}
	if !exists {
		return nil
	}
	if b.SchemaVersion == nil {
		if u.o.Force {
			*warn = append(*warn, fmt.Sprintf("%s does not report which database schema it knows; installed anyway "+
				"(--force). If its daemon does not start, the database is newer than it", b.Version))
			return nil
		}
		return fmt.Errorf("%w (%s; the database is at schema version %d): it may not be able to open the database. "+
			"--force installs it anyway; nothing was changed", ErrSchemaUnknown, b.Version, db)
	}
	if db > *b.SchemaVersion {
		return fmt.Errorf("%w: the database is at schema version %d and %s knows only %d, so its daemon could not "+
			"open it (migrations are never undone); nothing was changed", ErrSchemaTooNew, db, b.Version,
			*b.SchemaVersion)
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

// sameVersion reports whether two version strings name the same build version.
func sameVersion(a, b string) bool {
	if a == b {
		return true
	}
	va, errA := ParseVersion(a)
	vb, errB := ParseVersion(b)
	return errA == nil && errB == nil && va.Compare(vb) == 0
}

// startExpect starts the daemon from the installed executable and makes sure the daemon that answers is version
// want (not, say, another build that came up meanwhile).
func (u *Updater) startExpect(ctx context.Context, want string) error {
	got, err := u.o.Daemon.Start(ctx, u.o.Exe)
	if err != nil {
		return err
	}
	if !sameVersion(got, want) {
		return fmt.Errorf("the daemon that answers reports version %s, not %s", got, want)
	}
	return nil
}

// restartAfterFailure starts the old daemon (version old) again after the executable could not be replaced.
func (u *Updater) restartAfterFailure(ctx context.Context, running bool, old string, cause error) error {
	if !running {
		return cause
	}
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), undoTimeout)
	defer cancel()
	if err := u.startExpect(uctx, old); err != nil {
		return fmt.Errorf("%w; starting the daemon again failed too: %w", cause, err)
	}
	return cause
}

// undoInfo names the versions of a swap that is undone.
type undoInfo struct {
	// failed is the version whose daemon did not start; back the one that is put back.
	failed, back string
	// rollback is true when the swap was a rollback (the kept executable stays kept after the undo).
	rollback bool
}

// undoAfterStartFailure handles a daemon that did not start from the executable sw put in place (or answered as
// another build): that daemon is stopped, the replaced executable put back and its daemon started. The file sw put
// in place goes back where it came from: the staged download (which Apply removes) or, for a rollback, the kept
// executable, which stays for another try (it is never deleted here). When the failed daemon migrated the database
// past what the replaced executable knows, nothing is put back (see keepAfterMigration). Every step runs on a
// context of its own, so a cancelled command still finishes the undo.
func (u *Updater) undoAfterStartFailure(ctx context.Context, sw *pair, cause error, in undoInfo) error {
	uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), undoTimeout)
	defer cancel()
	head := fmt.Sprintf("the daemon of %s did not start properly", in.failed)
	if in.rollback {
		head = fmt.Sprintf("the daemon of the kept version %s did not start properly", in.failed)
	}
	u.progress("Stopping the daemon of %s...", in.failed)
	if err := u.o.Daemon.Abort(uctx); err != nil {
		return fmt.Errorf("%s: %w; it could not be stopped either (%w), so %s stays installed: stop it with "+
			"`seelegent stop` and see logs/daemon.log", head, cause, err, in.failed)
	}
	if err := u.keepAfterMigration(uctx, head, cause, in); err != nil {
		return err
	}
	if err := sw.undo(); err != nil {
		return fmt.Errorf("%s: %w; putting %s back failed: %w", head, cause, in.back, err)
	}
	what := in.back + " is back in place"
	if in.rollback {
		what = fmt.Sprintf("the rollback was undone: %s stays installed and the kept version is still at %s", in.back,
			sw.src)
	}
	if sw.companionErr != nil {
		what += fmt.Sprintf(" (but its companion launcher could not be put back: %v; launches run as `seelegent "+
			"__launch` until the next update)", sw.companionErr)
	}
	u.progress("Starting the daemon of %s...", in.back)
	if err := u.startExpect(uctx, in.back); err != nil {
		return fmt.Errorf("%s: %w; %s, but its daemon did not start either: %w", head, cause, what, err)
	}
	return fmt.Errorf("%s: %w; %s and its daemon runs", head, cause, what)
}

// keepAfterMigration reads the database's schema again after the failed daemon was stopped. The executable an undo
// puts back is the running one (Options.CurrentSchema); when the failed daemon migrated the database past it, that
// build could not open the database, so the swap stays as it is (the failed build installed, the files around it
// as after a successful update or rollback, nothing deleted) and the error, ErrMigratedThenFailed, says how to go
// on. A schema that cannot be read is treated the same way, with its own message: putting back a build that may not
// open the database could leave no build that does. It returns nil when the undo may go ahead.
func (u *Updater) keepAfterMigration(ctx context.Context, head string, cause error, in undoInfo) error {
	if u.o.DatabaseSchema == nil || u.o.CurrentSchema <= 0 {
		return nil
	}
	db, exists, err := u.o.DatabaseSchema(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w; the database's schema version cannot be read now (%w), so %s was not put back "+
			"(it might not open the database): %s stays installed and no daemon runs. See logs/daemon.log, then run "+
			"`seelegent restart`", head, cause, err, in.back, in.failed)
	}
	if !exists || db <= u.o.CurrentSchema {
		return nil
	}
	return &migratedThenFailedError{err: fmt.Errorf("%s: %w; %s ran its database migrations (schema %d) first, and "+
		"%s knows only schema %d, so it cannot open the database any more: %s stays installed and no daemon runs. "+
		"Fix what logs/daemon.log says, then run `seelegent restart` (or install a release that knows schema %d with "+
		"`seelegent update --apply --version vX.Y.Z`)", head, cause, in.failed, db, in.back, u.o.CurrentSchema,
		in.failed, db)}
}

// Rollback puts back the executable the last update replaced. The version rolled back from is discarded, so a
// second rollback has nothing to go back to (old C-B15: it rolled forward again).
func (u *Updater) Rollback(ctx context.Context) (RollbackResult, error) {
	res := RollbackResult{Exe: u.o.Exe, Hosts: []HostItem{}}
	if err := u.checkExe(); err != nil {
		return res, err
	}
	unlock, err := u.lock()
	if err != nil {
		return res, err
	}
	defer unlock()
	u.sweepAll()
	prev := PreviousPath(u.o.Exe)
	if _, err := os.Stat(prev); errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("%w next to %s: a rollback goes back once, to the version the last update replaced",
			ErrNoPrevious, u.o.Exe)
	}
	prevBuild, err := u.versionOf(ctx, prev)
	if err != nil {
		return res, fmt.Errorf("the kept executable: %w; nothing was changed", err)
	}
	prevVersion := prevBuild.Version
	res.PreviousVersion = u.o.Current
	if b, err := u.versionOf(ctx, u.o.Exe); err == nil {
		res.PreviousVersion = b.Version
	}
	if err := u.checkSchema(ctx, prevBuild, &res.Warnings); err != nil {
		return res, err
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
	// The kept companion goes back with the kept executable; without one, the current companion goes too.
	prevCompanion := PreviousPath(u.companion())
	if _, err := os.Lstat(prevCompanion); err != nil {
		prevCompanion = ""
	}
	sw, err := replacePair(u.o.Exe, prev, u.companion(), prevCompanion, false)
	if err != nil {
		return res, u.restartAfterFailure(ctx, running, res.PreviousVersion, err)
	}
	if running {
		u.progress("Starting the daemon...")
		if err := u.startExpect(ctx, prevVersion); err != nil {
			return res, u.undoAfterStartFailure(ctx, sw, err,
				undoInfo{failed: prevVersion, back: res.PreviousVersion, rollback: true})
		}
		res.DaemonRestarted = true
	}
	res.OK, res.CurrentVersion = true, prevVersion
	if u.o.ResetHosts != nil {
		res.Warnings = append(res.Warnings, u.o.ResetHosts(ctx, u.o.Exe)...)
	}
	hosts, warnings := u.refreshHosts(ctx)
	res.Hosts, res.Warnings = hosts, append(res.Warnings, warnings...)
	u.sweepAll()
	res.Message = fmt.Sprintf("rolled back %s -> %s", res.PreviousVersion, prevVersion)
	return res, nil
}
