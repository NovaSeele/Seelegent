package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// fakeRelease is one release served by a fakeServer. The "executable" in its archive is the text "seelegent <tag>",
// which fakeRun reports as its version.
type fakeRelease struct {
	tag     string
	archive []byte
	name    string
	sums    string
}

func buildArchive(t *testing.T, goos, inner string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for _, e := range []struct {
			name string
			data []byte
		}{{"README.md", []byte("readme")}, {"../" + inner, []byte("evil")}, {inner, content}} {
			w, err := zw.Create(e.name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write(e.data)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name string
		data []byte
	}{{"LICENSE", []byte("license")}, {inner, content}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.data)),
			Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(e.data)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newFakeRelease(t *testing.T, tag, goos, goarch string) *fakeRelease {
	t.Helper()
	v, err := ParseVersion(tag)
	if err != nil {
		t.Fatal(err)
	}
	inner := binaryName(goos)
	if goos != "windows" {
		inner = "seelegent_" + tag + "/" + inner // one folder down is accepted too
	}
	r := &fakeRelease{tag: tag, name: archiveName(v, goos, goarch)}
	r.archive = buildArchive(t, goos, inner, []byte("seelegent "+tag))
	sum := sha256.Sum256(r.archive)
	r.sums = hex.EncodeToString(sum[:]) + "  " + r.name + "\n" +
		strings.Repeat("0", 64) + "  seelegent_other_linux_arm64.tar.gz\n"
	return r
}

// fakeServer serves GitHub-like release metadata and downloads.
type fakeServer struct {
	*httptest.Server
	mu       sync.Mutex
	releases map[string]*fakeRelease
	latest   string
}

func newFakeServer(t *testing.T, rels ...*fakeRelease) *fakeServer {
	t.Helper()
	fs := &fakeServer{releases: map[string]*fakeRelease{}}
	for _, r := range rels {
		fs.releases[r.tag] = r
		fs.latest = r.tag
	}
	fs.Server = httptest.NewServer(http.HandlerFunc(fs.serve))
	t.Cleanup(fs.Close)
	return fs
}

func (fs *fakeServer) serve(w http.ResponseWriter, r *http.Request) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	p := r.URL.Path
	var rel *fakeRelease
	switch {
	case p == "/releases/latest":
		rel = fs.releases[fs.latest]
	case strings.HasPrefix(p, "/releases/tags/"):
		rel = fs.releases[strings.TrimPrefix(p, "/releases/tags/")]
	case strings.HasPrefix(p, "/dl/"):
		parts := strings.SplitN(strings.TrimPrefix(p, "/dl/"), "/", 2)
		if rel = fs.releases[parts[0]]; rel == nil || len(parts) < 2 {
			http.NotFound(w, r)
			return
		}
		switch parts[1] {
		case rel.name:
			_, _ = w.Write(rel.archive)
		case ChecksumsName:
			_, _ = w.Write([]byte(rel.sums))
		default:
			http.NotFound(w, r)
		}
		return
	}
	if rel == nil {
		http.NotFound(w, r)
		return
	}
	base := fs.URL + "/dl/" + rel.tag + "/"
	_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": rel.tag, "html_url": "https://example.test/" + rel.tag,
		"assets": []map[string]string{
			{"name": rel.name, "browser_download_url": base + rel.name},
			{"name": ChecksumsName, "browser_download_url": base + ChecksumsName},
		}})
}

// fakeDaemon records what the updater asks of the daemon.
type fakeDaemon struct {
	running  bool
	calls    []string
	startErr func(ctx context.Context, exe string) error
	// answers, when set, is the version the started daemon reports (else the one in the executable).
	answers  func(content string) string
	abortErr error
}

func (d *fakeDaemon) Running(context.Context) (bool, error) { return d.running, nil }
func (d *fakeDaemon) Stop(context.Context) error {
	d.calls = append(d.calls, "stop")
	d.running = false
	return nil
}

func (d *fakeDaemon) Start(ctx context.Context, exe string) (string, error) {
	data, _ := os.ReadFile(exe)
	content := string(data)
	d.calls = append(d.calls, "start "+content)
	if d.startErr != nil {
		if err := d.startErr(ctx, content); err != nil {
			return "", err
		}
	}
	d.running = true
	if d.answers != nil {
		return d.answers(content), nil
	}
	return strings.TrimPrefix(content, "seelegent "), nil
}

func (d *fakeDaemon) Abort(ctx context.Context) error {
	d.calls = append(d.calls, "abort")
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if d.abortErr != nil {
		return d.abortErr
	}
	d.running = false
	return nil
}

// fakeRun plays the executables: `version --json` reports the version written in the file, `setup` answers with
// a report in which the hosts named in registeredHosts have an entry.
type fakeRun struct {
	registeredHosts []string
	setupCalls      [][]string
	// schemas is the schemaVersion each version reports (versions not listed report none).
	schemas map[string]int
}

func (f *fakeRun) run(_ context.Context, exe string, args ...string) ([]byte, error) {
	data, err := os.ReadFile(exe)
	if err != nil {
		return nil, err
	}
	content := string(data)
	ver, ok := strings.CutPrefix(content, "seelegent ")
	if !ok {
		return nil, errors.New("exec format error")
	}
	switch args[0] {
	case "version":
		if n, ok := f.schemas[ver]; ok {
			return fmt.Appendf(nil, `{"version":%q,"schemaVersion":%d}`, ver, n), nil
		}
		return fmt.Appendf(nil, `{"version":%q}`, ver), nil
	case "setup":
		f.setupCalls = append(f.setupCalls, args[1:])
		items := []HostItem{}
		if slices.Contains(args, "--dry-run") {
			for _, h := range []string{"claude", "codex", "cursor"} {
				action := "added"
				if slices.Contains(f.registeredHosts, h) {
					action = "unchanged"
				}
				items = append(items, HostItem{Host: h, Kind: "mcp", Action: action},
					HostItem{Host: h, Kind: "skill", Action: "updated"})
			}
		} else {
			for _, h := range f.registeredHosts {
				items = append(items, HostItem{Host: h, Kind: "mcp", Action: "unchanged"},
					HostItem{Host: h, Kind: "skill", Action: "updated"})
			}
		}
		return json.Marshal(map[string]any{"items": items})
	}
	return nil, fmt.Errorf("unexpected %v", args)
}

type env struct {
	exe    string
	daemon *fakeDaemon
	run    *fakeRun
	server *fakeServer
	goos   string
	// dbSchema, when set, is the database's schema version (the schema check runs).
	dbSchema *int
	// dbErr, when set, is what reading the database's schema returns.
	dbErr error
	// currentSchema is the schema the running build knows (Options.CurrentSchema).
	currentSchema int
	force         bool
	// lockDir is the executable lock's folder, shared by every updater of the env (as the user's cache folder is).
	lockDir string
}

func newEnv(t *testing.T, goos, installed string, rels ...string) *env {
	t.Helper()
	e := &env{goos: goos, daemon: &fakeDaemon{running: true}, run: &fakeRun{registeredHosts: []string{"claude"}},
		lockDir: filepath.Join(t.TempDir(), "cache", "seelegent")}
	var frs []*fakeRelease
	for _, tag := range rels {
		frs = append(frs, newFakeRelease(t, tag, goos, "amd64"))
	}
	e.server = newFakeServer(t, frs...)
	bin := t.TempDir()
	e.exe = filepath.Join(bin, binaryName(goos))
	if err := os.WriteFile(e.exe, []byte("seelegent "+installed), 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) updater(t *testing.T, current string) *Updater {
	t.Helper()
	o := Options{Current: current, Exe: e.exe, WorkDir: filepath.Join(t.TempDir(), "update"),
		MetadataURL: e.server.URL + "/releases/latest", GOOS: e.goos, GOARCH: "amd64", Daemon: e.daemon,
		Run: e.run.run, Force: e.force, LockDir: e.lockDir, CurrentSchema: e.currentSchema}
	if e.dbSchema != nil {
		o.DatabaseSchema = func(context.Context) (int, bool, error) {
			if e.dbErr != nil {
				return 0, true, e.dbErr
			}
			return *e.dbSchema, true, nil
		}
	}
	return New(o)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplyAndRollback(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			e := newEnv(t, goos, "v0.0.1", "v0.0.1", "v0.0.2")
			u := e.updater(t, "v0.0.1")
			ctx := context.Background()

			chk, err := u.Check(ctx, "")
			if err != nil || !chk.UpdateAvailable || chk.LatestVersion != "v0.0.2" || chk.Asset == "" {
				t.Fatalf("Check = %+v, %v", chk, err)
			}
			res, err := u.Apply(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			if !res.Applied || res.Version != "v0.0.2" || !res.DaemonRestarted || res.Previous != PreviousPath(e.exe) {
				t.Fatalf("Apply = %+v", res)
			}
			if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
				t.Errorf("installed = %q", got)
			}
			if got := readFile(t, PreviousPath(e.exe)); got != "seelegent v0.0.1" {
				t.Errorf("previous = %q", got)
			}
			if want := []string{"stop", "start seelegent v0.0.2"}; !slices.Equal(e.daemon.calls, want) {
				t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
			}
			// The refresh ran setup for the registered host only.
			if len(e.run.setupCalls) != 2 || !slices.Equal(e.run.setupCalls[1], []string{"--json", "--host", "claude"}) {
				t.Errorf("setup calls = %v", e.run.setupCalls)
			}
			if len(res.Hosts) != 2 || res.Hosts[0].Host != "claude" {
				t.Errorf("hosts = %+v", res.Hosts)
			}
			assertNoLeftovers(t, e.exe)

			// The release build v0.0.2 now runs; a second apply is up to date.
			u2 := e.updater(t, "v0.0.2")
			again, err := u2.Apply(ctx, "")
			if err != nil || again.Applied || again.UpdateAvailable {
				t.Fatalf("second Apply = %+v, %v", again, err)
			}

			e.daemon.calls = nil
			rb, err := u2.Rollback(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !rb.OK || rb.CurrentVersion != "v0.0.1" || rb.PreviousVersion != "v0.0.2" || !rb.DaemonRestarted {
				t.Fatalf("Rollback = %+v", rb)
			}
			if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
				t.Errorf("after rollback = %q", got)
			}
			if _, err := os.Stat(PreviousPath(e.exe)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("previous still there after rollback: %v", err)
			}
			// Old C-B15: a second rollback must not roll forward.
			if _, err := u2.Rollback(ctx); !errors.Is(err, ErrNoPrevious) {
				t.Errorf("second rollback err = %v, want ErrNoPrevious", err)
			}
			if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
				t.Errorf("after second rollback = %q", got)
			}
			assertNoLeftovers(t, e.exe)
		})
	}
}

// assertNoLeftovers checks that only the executable (and the kept previous one) are in its folder: no lock either.
func assertNoLeftovers(t *testing.T, exe string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if n := en.Name(); n != filepath.Base(exe) && n != filepath.Base(PreviousPath(exe)) {
			t.Errorf("leftover %s", n)
		}
	}
}

func TestApplyChecksumMismatch(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	e.server.releases["v0.0.2"].archive = append(e.server.releases["v0.0.2"].archive, 0) // tampered
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
	if len(e.daemon.calls) != 0 {
		t.Errorf("daemon touched: %v", e.daemon.calls)
	}
	assertNoLeftovers(t, e.exe)
}

func TestApplyChecksumMissing(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.1", "v0.0.2")
	e.server.releases["v0.0.2"].sums = strings.Repeat("a", 64) + "  other.zip\n"
	if _, err := e.updater(t, "v0.0.1").Apply(context.Background(), ""); !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
}

func TestApplyWrongVersionInArchive(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	r := e.server.releases["v0.0.2"]
	r.archive = buildArchive(t, "linux", "seelegent", []byte("seelegent v0.0.9"))
	sum := sha256.Sum256(r.archive)
	r.sums = hex.EncodeToString(sum[:]) + "  " + r.name + "\n"
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "reports version") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
}

func TestApplyDevBuild(t *testing.T) {
	e := newEnv(t, "linux", "dev", "v0.0.1", "v0.0.2")
	u := e.updater(t, "dev")
	chk, err := u.Check(context.Background(), "")
	if err != nil || !chk.DevBuild || chk.UpdateAvailable {
		t.Fatalf("Check = %+v, %v", chk, err)
	}
	if _, err := u.Apply(context.Background(), ""); !errors.Is(err, ErrDevBuild) {
		t.Fatalf("err = %v, want ErrDevBuild", err)
	}
	res, err := u.Apply(context.Background(), "v0.0.1")
	if err != nil || !res.Applied || res.Version != "v0.0.1" {
		t.Fatalf("Apply(v0.0.1) = %+v, %v", res, err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
}

func TestApplyDowngradeWithVersion(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.2", "v0.0.1", "v0.0.2")
	e.daemon.running = false
	u := e.updater(t, "v0.0.2")
	res, err := u.Apply(context.Background(), "v0.0.1")
	if err != nil || !res.Applied || res.DaemonRestarted {
		t.Fatalf("Apply = %+v, %v", res, err)
	}
	if len(e.daemon.calls) != 0 {
		t.Errorf("a daemon that did not run was touched: %v", e.daemon.calls)
	}
	if _, err := u.Apply(context.Background(), "v9.9.9"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown release err = %v", err)
	}
}

func TestApplyStartFailurePutsOldBack(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.1", "v0.0.2")
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			return errors.New("exit status 1")
		}
		return nil
	}
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "v0.0.1 is back in place and its daemon runs") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
	want := []string{"stop", "start seelegent v0.0.2", "abort", "start seelegent v0.0.1"}
	if !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
	assertNoLeftovers(t, e.exe)
}

func TestApplyRefusesOtherExecutables(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	u := e.updater(t, "v0.0.1")
	u.o.Exe = filepath.Join(filepath.Dir(e.exe), "seelegent-dev")
	if _, err := u.Apply(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "installed") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyNoBuildForPlatform(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	u := e.updater(t, "v0.0.1")
	u.o.GOARCH = "riscv64"
	if _, err := u.Apply(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "no build") {
		t.Fatalf("err = %v", err)
	}
}

func TestRollbackWithoutPrevious(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.1")
	if _, err := e.updater(t, "v0.0.1").Rollback(context.Background()); !errors.Is(err, ErrNoPrevious) {
		t.Fatalf("err = %v", err)
	}
}

func TestSweepRemovesLeftovers(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	for _, n := range []string{e.exe + newSuffix, e.exe + oldInfix + "abc"} {
		if err := os.WriteFile(n, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.updater(t, "v0.0.1").Apply(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	assertNoLeftovers(t, e.exe)
}

func TestHostRefreshSkipsUnregistered(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	e.run.registeredHosts = nil
	res, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.run.setupCalls) != 1 || len(res.Hosts) != 0 {
		t.Errorf("setup calls = %v, hosts = %+v", e.run.setupCalls, res.Hosts)
	}
}

// NS1: a rollback whose daemon does not start is undone, and the kept executable stays for another try.
func TestRollbackStartFailureKeepsPrevious(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.2", "v0.0.1")
	if err := os.WriteFile(PreviousPath(e.exe), []byte("seelegent v0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.1" {
			return errors.New("database schema is newer than this version of Seelegent")
		}
		return nil
	}
	_, err := e.updater(t, "v0.0.2").Rollback(context.Background())
	if err == nil || !strings.Contains(err.Error(), "the rollback was undone") ||
		!strings.Contains(err.Error(), "kept version v0.0.1") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
		t.Errorf("installed = %q", got)
	}
	if got := readFile(t, PreviousPath(e.exe)); got != "seelegent v0.0.1" {
		t.Errorf("previous = %q", got)
	}
	want := []string{"stop", "start seelegent v0.0.1", "abort", "start seelegent v0.0.2"}
	if !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
	assertNoLeftovers(t, e.exe)
}

// NS3: a daemon that answers as another build is stopped and the old executable put back.
func TestApplyWrongDaemonVersion(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	e.daemon.answers = func(content string) string {
		if content == "seelegent v0.0.2" {
			return "v0.0.1" // the old daemon came back up instead
		}
		return strings.TrimPrefix(content, "seelegent ")
	}
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "reports version v0.0.1, not v0.0.2") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
	if want := []string{"stop", "start seelegent v0.0.2", "abort", "start seelegent v0.0.1"}; !slices.Equal(
		e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
}

// NS3: Ctrl+C while the new daemon starts still stops it and puts the old executable back.
func TestApplyCancelledDuringStart(t *testing.T) {
	e := newEnv(t, "windows", "v0.0.1", "v0.0.2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.daemon.startErr = func(sctx context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			cancel()
			return context.Canceled
		}
		return sctx.Err() // the undo must not run on the cancelled context
	}
	_, err := e.updater(t, "v0.0.1").Apply(ctx, "")
	if err == nil || !strings.Contains(err.Error(), "is back in place and its daemon runs") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" {
		t.Errorf("installed = %q", got)
	}
}

// NS3: a new daemon that cannot be stopped keeps its executable installed, and the error says so.
func TestApplyAbortFailureLeavesNewInstalled(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			return errors.New("did not answer within 2m0s")
		}
		return nil
	}
	e.daemon.abortErr = errors.New("the daemon lock is still held")
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "v0.0.2 stays installed") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
		t.Errorf("installed = %q", got)
	}
	if want := []string{"stop", "start seelegent v0.0.2", "abort"}; !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
}

// NS2: a build that knows fewer migrations than the database has is never installed.
func TestSchemaCheck(t *testing.T) {
	db := 8
	e := newEnv(t, "windows", "v0.0.2", "v0.0.1", "v0.0.2")
	e.dbSchema = &db
	e.run.schemas = map[string]int{"v0.0.1": 7, "v0.0.2": 8}
	if err := os.WriteFile(PreviousPath(e.exe), []byte("seelegent v0.0.1"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := e.updater(t, "v0.0.2").Apply(ctx, "v0.0.1"); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("Apply(older) err = %v, want ErrSchemaTooNew", err)
	}
	if _, err := e.updater(t, "v0.0.2").Rollback(ctx); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("Rollback err = %v, want ErrSchemaTooNew", err)
	}
	e.force = true // --force never overrides a known schema gap
	if _, err := e.updater(t, "v0.0.2").Rollback(ctx); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("Rollback --force err = %v, want ErrSchemaTooNew", err)
	}
	if len(e.daemon.calls) != 0 || readFile(t, e.exe) != "seelegent v0.0.2" ||
		readFile(t, PreviousPath(e.exe)) != "seelegent v0.0.1" {
		t.Fatalf("something changed: calls %v", e.daemon.calls)
	}

	// A build that does not report its schema: refused, unless --force (with a warning).
	e.force = false
	delete(e.run.schemas, "v0.0.1")
	if _, err := e.updater(t, "v0.0.2").Rollback(ctx); !errors.Is(err, ErrSchemaUnknown) {
		t.Fatalf("Rollback (unknown) err = %v, want ErrSchemaUnknown", err)
	}
	e.force = true
	res, err := e.updater(t, "v0.0.2").Rollback(ctx)
	if err != nil || !res.OK || len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], "--force") {
		t.Fatalf("Rollback --force = %+v, %v", res, err)
	}

	// The same database with a build that knows enough goes ahead.
	e.force = false
	res2, err := e.updater(t, "v0.0.1").Apply(ctx, "v0.0.2")
	if err != nil || !res2.Applied {
		t.Fatalf("Apply(v0.0.2) = %+v, %v", res2, err)
	}
}

// migratingEnv is a database at schema 7 under the running v0.0.1 (schema 7); v0.0.2 knows schema 8, and its daemon
// migrates the database to 8 and then fails.
func migratingEnv(t *testing.T, installed string, rels ...string) (*env, *int) {
	t.Helper()
	db := 7
	e := newEnv(t, "windows", installed, rels...)
	e.dbSchema, e.currentSchema = &db, 7
	e.run.schemas = map[string]int{"v0.0.1": 7, "v0.0.2": 8}
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			db = 8
			return errors.New("exited before it answered")
		}
		return nil
	}
	return e, &db
}

// A new daemon that migrated the database and then failed is not undone: the old build could not open the database.
func TestApplyMigratedThenFailedKeepsNew(t *testing.T) {
	e, _ := migratingEnv(t, "v0.0.1", "v0.0.2")
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if !errors.Is(err, ErrMigratedThenFailed) {
		t.Fatalf("err = %v, want ErrMigratedThenFailed", err)
	}
	for _, want := range []string{"exited before it answered", "v0.0.2 ran its database migrations (schema 8)",
		"v0.0.1 knows only schema 7", "v0.0.2 stays installed and no daemon runs", "seelegent restart"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v\nmissing %q", err, want)
		}
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
		t.Errorf("installed = %q, want the new build", got)
	}
	if got := readFile(t, PreviousPath(e.exe)); got != "seelegent v0.0.1" {
		t.Errorf("previous = %q", got)
	}
	if want := []string{"stop", "start seelegent v0.0.2", "abort"}; !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
	assertNoLeftovers(t, e.exe)
}

// A failed start that left the database's schema as it was is undone exactly as without the schema check.
func TestApplyStartFailureWithoutMigrationStillUndoes(t *testing.T) {
	e, db := migratingEnv(t, "v0.0.1", "v0.0.2")
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			return errors.New("exited before it answered") // no migration: the database stays at 7
		}
		return nil
	}
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || errors.Is(err, ErrMigratedThenFailed) ||
		!strings.Contains(err.Error(), "v0.0.1 is back in place and its daemon runs") {
		t.Fatalf("err = %v", err)
	}
	if *db != 7 || readFile(t, e.exe) != "seelegent v0.0.1" {
		t.Errorf("db %d, installed %q", *db, readFile(t, e.exe))
	}
	want := []string{"stop", "start seelegent v0.0.2", "abort", "start seelegent v0.0.1"}
	if !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
	if _, err := os.Stat(PreviousPath(e.exe)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a kept executable appeared: %v", err)
	}
	assertNoLeftovers(t, e.exe)
}

// A schema that cannot be read after the failed start keeps the new build too, with its own message.
func TestApplySchemaUnreadableAfterFailureKeepsNew(t *testing.T) {
	e, _ := migratingEnv(t, "v0.0.1", "v0.0.2")
	e.daemon.startErr = func(_ context.Context, content string) error {
		if content == "seelegent v0.0.2" {
			e.dbErr = errors.New("file is not a database")
			return errors.New("exited before it answered")
		}
		return nil
	}
	_, err := e.updater(t, "v0.0.1").Apply(context.Background(), "")
	if err == nil || errors.Is(err, ErrMigratedThenFailed) || !strings.Contains(err.Error(), "cannot be read now") ||
		!strings.Contains(err.Error(), "v0.0.2 stays installed") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
		t.Errorf("installed = %q", got)
	}
}

// The same holds for a rollback to a kept build that knows more migrations than the running one.
func TestRollbackMigratedThenFailedKeepsKept(t *testing.T) {
	e, _ := migratingEnv(t, "v0.0.1", "v0.0.1")
	if err := os.WriteFile(PreviousPath(e.exe), []byte("seelegent v0.0.2"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := e.updater(t, "v0.0.1").Rollback(context.Background())
	if !errors.Is(err, ErrMigratedThenFailed) || !strings.Contains(err.Error(), "kept version v0.0.2") ||
		!strings.Contains(err.Error(), "v0.0.2 stays installed") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.2" {
		t.Errorf("installed = %q", got)
	}
	if want := []string{"stop", "start seelegent v0.0.2", "abort"}; !slices.Equal(e.daemon.calls, want) {
		t.Errorf("daemon calls = %v, want %v", e.daemon.calls, want)
	}
}

// Suggestion 5: two updates of the same executable from different data folders never run at once.
func TestExecutableLock(t *testing.T) {
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	path, err := e.updater(t, "v0.0.1").exeLockPath()
	if err != nil {
		t.Fatal(err)
	}
	l, err := fsutil.TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.updater(t, "v0.0.1").Apply(context.Background(), "") // another data folder (WorkDir)
	_ = l.Unlock()
	if err == nil || !strings.Contains(err.Error(), "another seelegent update of") {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, e.exe); got != "seelegent v0.0.1" || len(e.daemon.calls) != 0 {
		t.Errorf("installed = %q, calls %v", got, e.daemon.calls)
	}
}

// G4: the executable's lock lives in the user's cache folder, never next to the executable, and falls back to the
// executable's folder only without a cache folder.
func TestExecutableLockLocation(t *testing.T) {
	cache := t.TempDir()
	for _, k := range []string{"LocalAppData", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(k, cache)
	}
	e := newEnv(t, "linux", "v0.0.1", "v0.0.2")
	e.lockDir = ""
	u := e.updater(t, "v0.0.1")
	path, err := u.exeLockPath()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.UserCacheDir()
	if filepath.Dir(path) != filepath.Join(want, "seelegent") || !strings.HasPrefix(filepath.Base(path), "update-") {
		t.Fatalf("lock = %s, want one in %s", path, filepath.Join(want, "seelegent"))
	}
	if again, _ := e.updater(t, "v0.0.1").exeLockPath(); again != path {
		t.Errorf("another data folder locks %s, not %s", again, path)
	}
	if _, err := u.Apply(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	assertNoLeftovers(t, e.exe)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("lock file: %v", err)
	}

	for _, k := range []string{"LocalAppData", "XDG_CACHE_HOME", "HOME"} {
		t.Setenv(k, "")
	}
	if path, err := u.exeLockPath(); err != nil || path != e.exe+lockSuffix {
		t.Errorf("without a cache folder: lock = %s, %v", path, err)
	}
}
