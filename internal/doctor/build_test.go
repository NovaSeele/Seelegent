package doctor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// fakeDaemon is a Daemon whose lookup is never called by daemonVersion; only its port is read.
type fakeDaemon struct{ port int }

func (fakeDaemon) Find(context.Context) (*client.Client, error) { return nil, client.ErrNotRunning }
func (fakeDaemon) LockHeld() bool                               { return false }
func (fakeDaemon) StalePortFile() bool                          { return false }
func (fakeDaemon) NotAnswering() *client.StuckError             { return nil }
func (d fakeDaemon) Port() int                                  { return d.port }

func TestDaemonVersion(t *testing.T) {
	cl := client.ForPort(47821, nil)
	o := Options{Daemon: fakeDaemon{port: 47821}, Version: "v0.9.0", Commit: "58337977a4eb"}
	health := func(version, commit string) api.Health {
		return api.Health{OK: true, Version: version, Commit: commit, APIVersion: api.APIVersion, Port: 47821}
	}

	// The same build: OK. A short commit matches its long form, in any case.
	for _, h := range []api.Health{health("v0.9.0", "58337977a4eb"), health("v0.9.0", "58337977A4EB1234"),
		health("v0.9.0", "5833797")} {
		if c, ok := daemonVersion(o, cl, h, nil); !ok || c.Status != OK ||
			c.Message != "the daemon on port 47821 runs v0.9.0 (commit "+h.Commit+"), the build of this seelegent" {
			t.Errorf("same build %+v = %+v %v", h, c, ok)
		}
	}
	// An unknown commit on either side compares the versions only.
	for _, commit := range []string{"", buildinfo.Unknown} {
		if c, _ := daemonVersion(o, cl, health("v0.9.0", commit), nil); c.Status != OK {
			t.Errorf("daemon commit %q = %+v", commit, c)
		}
		mine := o
		mine.Commit = commit
		if c, _ := daemonVersion(mine, cl, health("v0.9.0", "abcdef123456"), nil); c.Status != OK {
			t.Errorf("own commit %q = %+v", commit, c)
		}
	}

	// Another version, or the same version from another commit: a warning that names both builds.
	cases := []struct{ version, commit, want string }{
		{"v0.8.0", "58337977a4eb", "the daemon on port 47821 runs v0.8.0 (commit 58337977a4eb), this seelegent is " +
			"v0.9.0 (commit 58337977a4eb)"},
		{"v0.9.0", "969ec24aa111", "the daemon on port 47821 runs v0.9.0 (commit 969ec24aa111), this seelegent is " +
			"v0.9.0 (commit 58337977a4eb)"},
		{"v0.9.0-dirty", "", "the daemon on port 47821 runs v0.9.0-dirty, this seelegent is v0.9.0 (commit " +
			"58337977a4eb)"},
	}
	for _, tc := range cases {
		c, ok := daemonVersion(o, cl, health(tc.version, tc.commit), nil)
		if !ok || c.Status != Warn || c.Message != tc.want || c.Fix != "seelegent restart" {
			t.Errorf("%s %s = %+v %v", tc.version, tc.commit, c, ok)
		}
	}
	// A health answer without its port names the configured one.
	h := health("v0.8.0", "")
	h.Port = 0
	if c, _ := daemonVersion(Options{Daemon: fakeDaemon{port: 1234}, Version: "v0.9.0"}, cl, h, nil); c.Message !=
		"the daemon on port 1234 runs v0.8.0, this seelegent is v0.9.0" {
		t.Errorf("no port = %+v", c)
	}
}

// TestDaemonVersionForeign: what answers on the port is no Seelegent v1 daemon (the old Node version's daemon
// answers its own /api, not /api/v1): a warning. Nothing answering, a timeout and a v1 daemon of another data
// directory give no line (the daemon check covers them).
func TestDaemonVersionForeign(t *testing.T) {
	o := Options{Daemon: fakeDaemon{port: 47821}, Version: "v0.9.0"}
	node := &client.ForeignError{Port: 47821, Detail: "404 Not Found"}
	c, ok := daemonVersion(o, nil, api.Health{}, fmt.Errorf("find: %w", node))
	if !ok || c.Status != Warn || !strings.Contains(c.Message, "port 47821 answers, but not as a Seelegent v1 "+
		"daemon (no /api/v1/health)") || !strings.Contains(c.Message, "old Node") ||
		!strings.Contains(c.Fix, "old_node") {
		t.Errorf("not Seelegent = %+v %v", c, ok)
	}
	c, ok = daemonVersion(o, nil, api.Health{}, &client.ForeignError{Port: 47821, APIVersion: 2})
	if !ok || c.Status != Warn || c.Message != "the daemon on port 47821 speaks Seelegent API v2, this seelegent "+
		"speaks v1" {
		t.Errorf("API v2 = %+v %v", c, ok)
	}
	for _, err := range []error{client.ErrNotRunning, errors.New("health check failed"),
		&client.ForeignError{Port: 47821, Timeout: time.Second}, &client.ForeignError{Port: 47821, OtherHome: true},
		&client.ForeignError{Port: 47821, OlderBuild: true}} {
		if c, ok := daemonVersion(o, nil, api.Health{}, err); ok {
			t.Errorf("%v gave a line: %+v", err, c)
		}
	}
}

func TestOldNode(t *testing.T) {
	list := func(procs ...platform.Process) func(context.Context) ([]platform.Process, error) {
		return func(context.Context) ([]platform.Process, error) { return procs, nil }
	}
	cli := `C:\Users\me\AppData\Local\Programs\seelegent\versions\0.3.0\src\cli.js`
	node := `C:\Program Files\nodejs\node.exe`

	if c, ok := oldNode(t.Context(), nil); ok {
		t.Errorf("no lister = %+v", c)
	}
	unsupported := func(context.Context) ([]platform.Process, error) {
		return nil, fmt.Errorf("list processes: %w", errors.ErrUnsupported)
	}
	if c, ok := oldNode(t.Context(), unsupported); ok {
		t.Errorf("unsupported = %+v", c)
	}
	failing := func(context.Context) ([]platform.Process, error) { return nil, errors.New("access denied") }
	if c, ok := oldNode(t.Context(), failing); !ok || c.Status != Warn || !strings.Contains(c.Message, "access denied") {
		t.Errorf("failing = %+v %v", c, ok)
	}

	// Other node programs, an unreadable command line and a cli.js outside a seelegent folder do not count.
	others := []platform.Process{
		{PID: 1, Name: "node.exe", Args: []string{node, `C:\work\app\server.js`}},
		{PID: 2, Name: "node.exe"},
		{PID: 3, Name: "node.exe", Args: []string{node, `C:\tools\app\src\cli.js`, "mcp"}},
		{PID: 4, Name: "node.exe", Args: []string{node, `C:\tools\seelegent-fork.js`, "mcp"}},
	}
	if c, ok := oldNode(t.Context(), list(others...)); !ok || c.Status != OK ||
		c.Message != "no process of the old Node version runs" {
		t.Errorf("no old process = %+v %v", c, ok)
	}

	c, ok := oldNode(t.Context(), list(append(others,
		platform.Process{PID: 4120, Name: "node.exe", Args: []string{node, cli, "mcp"}},
		platform.Process{PID: 5230, Name: "node.exe", Args: []string{"node", strings.ToUpper(cli), "MCP"}},
		platform.Process{PID: 6340, Name: "node", Args: []string{"node", "/home/me/.local/seelegent/src/cli.js",
			"daemon"}})...))
	want := "3 processes of the old Node version run (pid 4120, 5230, 6340): 2 MCP servers (cli.js mcp), 1 other. " +
		"An old MCP server can start the old daemon on the migrated data folder"
	if !ok || c.Status != Warn || c.Message != want || !strings.Contains(c.Fix, "restart the lead hosts") ||
		!strings.Contains(c.Fix, "Claude Desktop") {
		t.Errorf("old processes = %+v %v", c, ok)
	}
	c, _ = oldNode(t.Context(), list(platform.Process{PID: 7, Args: []string{node, cli, "mcp"}}))
	if c.Message != "1 process of the old Node version runs (pid 7): 1 MCP server (cli.js mcp), 0 other. An old "+
		"MCP server can start the old daemon on the migrated data folder" {
		t.Errorf("one old process = %+v", c)
	}

	for name, want := range map[string]bool{"node.exe": true, "NODE.EXE": true, "node": true, "nodemon.exe": false,
		"seelegent.exe": false} {
		if IsNode(name) != want {
			t.Errorf("IsNode(%q) = %v", name, !want)
		}
	}
}

func TestPathUnexpanded(t *testing.T) {
	abs, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	env := func(entries ...string) []string {
		return []string{"APPDATA=C:\\Users\\me\\AppData\\Roaming", "LOCALAPPDATA=C:\\Users\\me\\AppData\\Local",
			"PATH=" + strings.Join(entries, string(filepath.ListSeparator))}
	}
	// An unexpanded variable on Windows: the REG_SZ message, with the expanded path in the fix.
	c := pathEntries(env(abs, `%APPDATA%\npm`, `%LOCALAPPDATA%\Programs\x\%APPDATA%`), "windows")
	if c.Status != Warn || !strings.Contains(c.Message, `never expanded: %APPDATA%\npm, `+
		`%LOCALAPPDATA%\Programs\x\%APPDATA%`) || !strings.Contains(c.Message, "REG_SZ instead of REG_EXPAND_SZ") ||
		!strings.Contains(c.Message, "literal text") || strings.Contains(c.Message, "relative") ||
		!strings.Contains(c.Fix, "store PATH as REG_EXPAND_SZ") ||
		!strings.Contains(c.Fix, `(C:\Users\me\AppData\Roaming\npm, `+
			`C:\Users\me\AppData\Local\Programs\x\C:\Users\me\AppData\Roaming)`) ||
		strings.Contains(c.Fix, "project folder") {
		t.Errorf("unexpanded = %+v", c)
	}
	// A variable that is not set: the fix names no expansion.
	c = pathEntries(env(`%NOPE%\bin`), "windows")
	if c.Status != Warn || !strings.Contains(c.Message, `%NOPE%\bin`) || strings.Contains(c.Fix, "expanded path (") {
		t.Errorf("unset variable = %+v", c)
	}
	// Both kinds: each gets its own message and fix.
	c = pathEntries(env(`%APPDATA%\npm`, "bin"), "windows")
	if c.Status != Warn || !strings.Contains(c.Message, `never expanded: %APPDATA%\npm.`) ||
		!strings.Contains(c.Message, "PATH has relative entries: bin") ||
		!strings.Contains(c.Fix, "REG_EXPAND_SZ") || !strings.Contains(c.Fix, "inside the project folder") {
		t.Errorf("both = %+v", c)
	}
	// A lone % or a %...% around a path separator is no variable: a relative entry as before.
	for _, e := range []string{`100%`, `a%\b%`} {
		c = pathEntries(env(e), "windows")
		if c.Status != Warn || !strings.Contains(c.Message, "relative entries: "+e) ||
			strings.Contains(c.Message, "expanded") {
			t.Errorf("%s = %+v", e, c)
		}
	}
	// Outside Windows there is no REG_SZ: a %NAME% entry is judged as a path (here a relative one).
	c = pathEntries([]string{"PATH=%APPDATA%/npm"}, "linux")
	if c.Status != Warn || !strings.Contains(c.Message, "relative entries: %APPDATA%/npm") ||
		strings.Contains(c.Message, "REG_SZ") {
		t.Errorf("linux = %+v", c)
	}
}
