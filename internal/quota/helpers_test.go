package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/testutil/raceenv"
)

// The test binary doubles as a fake CLI when QUOTA_FAKE is set, so the real process code (runCommand, startProc)
// is exercised without any real CLI or account:
//
//   - QUOTA_FAKE=print: print QUOTA_FAKE_STDOUT and QUOTA_FAKE_STDERR, exit with QUOTA_FAKE_CODE;
//   - QUOTA_FAKE=hang: sleep for five minutes;
//   - QUOTA_FAKE=app-server: answer JSON-RPC requests on stdin like `codex app-server` (signed in, Plus plan).
func TestMain(m *testing.M) {
	raceenv.QuietExit()
	switch os.Getenv("QUOTA_FAKE") {
	case "print":
		fmt.Fprint(os.Stdout, os.Getenv("QUOTA_FAKE_STDOUT"))
		fmt.Fprint(os.Stderr, os.Getenv("QUOTA_FAKE_STDERR"))
		code := 0
		_, _ = fmt.Sscan(os.Getenv("QUOTA_FAKE_CODE"), &code)
		os.Exit(code)
	case "hang":
		time.Sleep(5 * time.Minute)
		os.Exit(0)
	case "app-server":
		fakeAppServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeAppServer answers the requests the codex reader sends.
func fakeAppServer() {
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"userAgent": "fake"}
		case "account/read":
			result = map[string]any{"account": map[string]any{"type": "chatgpt", "email": "w@example.com", "planType": "plus"}}
		case "account/rateLimits/read":
			result = map[string]any{"rateLimits": map[string]any{
				"primary": map[string]any{"usedPercent": 25, "windowDurationMins": 300, "resetsAt": 1893456000},
			}}
		case "model/list":
			result = map[string]any{"data": []any{map[string]any{"id": "gpt-6-sol", "model": "gpt-6-sol"}}}
		}
		// A notification first, which the reader must skip.
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "method": "progress", "params": map[string]any{}})
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}
}

// selfBin returns the path of the test binary.
func selfBin(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// fakeEnv returns an environment that makes the test binary act as fake CLI mode with extra variables.
func fakeEnv(mode string, extra ...string) []string {
	env := append(os.Environ(), "QUOTA_FAKE="+mode)
	return append(env, extra...)
}

// testProfile returns a profile in a temp dir.
func testProfile(t *testing.T, kind account.Kind) account.Profile {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	return account.Profile{AccountID: "acc1", Kind: kind, Dir: dir, Home: home, RealHome: filepath.Join(dir, "real")}
}

// writeFile writes a file, creating its directory.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture is a database with an account manager.
type fixture struct {
	db *store.Store
	m  *account.Manager
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	profiles := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(profiles, 0o700); err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, m: account.NewManager(db, account.NewProfiles(profiles, filepath.Join(dir, "real")), nil)}
}

// add creates an account.
func (f *fixture) add(t *testing.T, n account.New) account.Account {
	t.Helper()
	a, _, err := f.m.Add(t.Context(), n, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

var t0 = time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
