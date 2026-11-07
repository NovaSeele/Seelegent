// Package doctor checks a Seelegent installation for `seelegent doctor`: the data directory, the settings, the
// database, the daemon, the embedded dashboard, git, the enabled backend CLIs, the accounts (counts only) and PATH.
// It reads only: it never starts the daemon, never migrates the database and never prints an e-mail address or key.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// Status of a check.
type Status string

// Check statuses: a fail makes `seelegent doctor` exit 1.
const (
	OK   Status = "ok"
	Warn Status = "warn"
	Fail Status = "fail"
)

// Check is one line of the report.
type Check struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Daemon is the daemon lookup (client.Daemon), without ever starting it.
type Daemon interface {
	Find(ctx context.Context) (*client.Client, error)
	LockHeld() bool
	StalePortFile() bool
	NotAnswering() *client.StuckError
	Port() int
}

// Options are what the checks look at.
type Options struct {
	Paths config.Paths
	// Settings and SettingsErr are the loaded settings (the defaults when SettingsErr is set or no file exists).
	Settings    config.Settings
	SettingsErr error
	// Unknown are config.json keys this version does not know.
	Unknown []string
	Daemon  Daemon
	// Version is this binary's version; a daemon of another version gets a warning.
	Version string
	// Web is the embedded dashboard (nil or without index.html: not part of this build).
	Web fs.FS
	// Env is the environment (os.Environ) and RealHome the user's home folder, to find the CLIs.
	Env      []string
	RealHome string
	// GitPath finds git (gitx.Binary.Path).
	GitPath func() (string, error)
}

// Run runs every check in a fixed order.
func Run(ctx context.Context, o Options) []Check {
	out := []Check{dataDir(o.Paths), settings(o), database(ctx, o.Paths)}
	d, c := daemon(ctx, o)
	out = append(out, d, portFile(o), dashboard(o.Web), git(o.GitPath))
	out = append(out, backends(o)...)
	return append(out, accounts(ctx, o, c), pathEntries(o.Env))
}

// Failed reports whether any check failed.
func Failed(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return true
		}
	}
	return false
}

func dataDir(p config.Paths) Check {
	c := Check{Name: "data_dir"}
	st, err := os.Stat(p.Home)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Status, c.Message = Warn, fmt.Sprintf("the data directory %s does not exist yet", p.Home)
		c.Fix = "it is created when the daemon first starts (seelegent ui)"
		return c
	case err != nil:
		c.Status, c.Message = Fail, fmt.Sprintf("cannot read the data directory: %v", err)
		return c
	case !st.IsDir():
		c.Status, c.Message = Fail, fmt.Sprintf("%s is not a folder", p.Home)
		c.Fix = "move that file away or set SEELEGENT_HOME to another folder"
		return c
	}
	probe := filepath.Join(p.Home, ".doctor-write-test")
	if err := fsutil.WriteFileAtomic(probe, []byte("ok\n"), 0o600); err != nil {
		c.Status, c.Message = Fail, fmt.Sprintf("the data directory %s is not writable: %v", p.Home, err)
		c.Fix = "fix the folder's permissions or set SEELEGENT_HOME to a writable folder"
		return c
	}
	_ = os.Remove(probe)
	c.Status, c.Message = OK, "data directory "+p.Home
	return c
}

func settings(o Options) Check {
	c := Check{Name: "settings"}
	switch {
	case o.SettingsErr != nil:
		c.Status, c.Message = Fail, fmt.Sprintf("the settings cannot be read: %v", o.SettingsErr)
		c.Fix = "fix " + o.Paths.Config + " (or move it away to start from the defaults)"
	case len(o.Unknown) > 0:
		c.Status, c.Message = Warn, "the settings have keys this version does not know: "+strings.Join(o.Unknown, ", ")
		c.Fix = "they are kept but ignored; remove them if they are typos"
	default:
		c.Status, c.Message = OK, "settings load"
	}
	return c
}

func database(ctx context.Context, p config.Paths) Check {
	c := Check{Name: "database"}
	in, err := store.Inspect(ctx, p.DB)
	switch {
	case store.IsNotExist(err):
		c.Status, c.Message = OK, "no database yet (the daemon creates it on first start)"
	case errors.Is(err, store.ErrSchemaTooNew):
		c.Status, c.Message = Fail, fmt.Sprintf("the database is at schema %d, newer than this Seelegent (%d)",
			in.Version, in.Known)
		c.Fix = "update Seelegent; an older version must not run on this database"
	case err != nil:
		c.Status, c.Message = Fail, fmt.Sprintf("the database cannot be opened: %v", err)
		c.Fix = "look at " + filepath.Join(p.Logs, "daemon.log")
	case in.Version < in.Known:
		c.Status, c.Message = Warn, fmt.Sprintf("the database is at schema %d of %d", in.Version, in.Known)
		c.Fix = "the daemon applies the new migrations when it starts (seelegent restart)"
	default:
		c.Status, c.Message = OK, fmt.Sprintf("database opens, schema %d is current", in.Version)
	}
	return c
}

// daemon checks the running daemon and returns its client when it answers.
func daemon(ctx context.Context, o Options) (Check, *client.Client) {
	c := Check{Name: "daemon"}
	cl, err := o.Daemon.Find(ctx)
	if err == nil {
		h, herr := cl.Health(ctx)
		if herr != nil {
			c.Status, c.Message = Fail, fmt.Sprintf("the daemon answers but its health check fails: %v", herr)
			return c, nil
		}
		c.Status, c.Message = OK, fmt.Sprintf("the daemon %s runs at %s/ (pid %d)", h.Version, cl.BaseURL(), h.PID)
		if o.Version != "" && h.Version != "" && h.Version != o.Version {
			c.Status = Warn
			c.Message += fmt.Sprintf(", but this command is %s", o.Version)
			c.Fix = "seelegent restart"
		}
		return c, cl
	}
	switch {
	case errors.Is(err, client.ErrNotRunning) && o.Daemon.LockHeld():
		c.Status, c.Message = Fail, o.Daemon.NotAnswering().Error()
	case errors.Is(err, client.ErrNotRunning):
		c.Status, c.Message = Warn, fmt.Sprintf("the daemon is not running (port %d)", o.Daemon.Port())
		c.Fix = "it starts on first use, or run: seelegent ui"
	default:
		c.Status, c.Message = Fail, err.Error()
		var foreign *client.ForeignError
		if errors.As(err, &foreign) && !client.TimedOut(err) {
			c.Fix = "stop that program, or set another port with SEELEGENT_PORT or the settings' port"
		}
	}
	return c, nil
}

func portFile(o Options) Check {
	if o.Daemon.StalePortFile() {
		return Check{Name: "port_file", Status: Warn,
			Message: "a port file is left from a daemon that is gone (" + o.Paths.PortFile + ")",
			Fix:     "harmless; seelegent stop removes it"}
	}
	return Check{Name: "port_file", Status: OK, Message: "no stale port file"}
}

func dashboard(web fs.FS) Check {
	if web != nil {
		if st, err := fs.Stat(web, "index.html"); err == nil && !st.IsDir() {
			return Check{Name: "dashboard", Status: OK, Message: "the dashboard is part of this build"}
		}
	}
	return Check{Name: "dashboard", Status: Warn, Message: "the dashboard is not part of this build",
		Fix: "build with web/dist (make build, or scripts/build.ps1)"}
}

func git(path func() (string, error)) Check {
	p, err := path()
	if err != nil {
		return Check{Name: "git", Status: Fail, Message: fmt.Sprintf("git not found: %v", err),
			Fix: "install git and make sure it is on PATH"}
	}
	return Check{Name: "git", Status: OK, Message: "git " + p}
}

// backends checks the CLI of every enabled backend.
func backends(o Options) []Check {
	enabled := map[string]bool{config.BackendAgy: o.Settings.Backends.Agy.Enabled,
		config.BackendClaude: o.Settings.Backends.Claude.Enabled, config.BackendCodex: o.Settings.Backends.Codex.Enabled}
	var out []Check
	for _, name := range backend.Names() {
		c := Check{Name: "backend_" + name}
		if !enabled[name] {
			c.Status, c.Message = OK, name+" is disabled"
			out = append(out, c)
			continue
		}
		p, err := backend.Find(name, o.Env, o.RealHome)
		if err != nil {
			c.Status, c.Message = Fail, fmt.Sprintf("%s is enabled but its CLI was not found: %v", name, err)
			c.Fix = fmt.Sprintf("install the %s CLI, set its path in %s, or disable the backend in the settings", name,
				envFor(name))
		} else {
			c.Status, c.Message = OK, name+" "+p
		}
		out = append(out, c)
	}
	return out
}

func envFor(name string) string {
	switch name {
	case config.BackendClaude:
		return backend.EnvClaudeBin
	case config.BackendCodex:
		return backend.EnvCodexBin
	}
	return backend.EnvAgyBin
}

// accounts counts the accounts, through the daemon when it runs, else from the database (never names or e-mails).
func accounts(ctx context.Context, o Options, c *client.Client) Check {
	out := Check{Name: "accounts"}
	total, enabled, usable := 0, 0, -1
	if c != nil {
		list, err := c.Accounts(ctx)
		if err != nil {
			out.Status, out.Message = Warn, fmt.Sprintf("the accounts could not be read: %v", err)
			return out
		}
		usable = 0
		for i := range list {
			a := &list[i]
			total++
			if a.Enabled {
				enabled++
				if a.Status == api.AccountOK || a.Status == api.AccountError || a.Status == api.AccountNew {
					usable++
				}
			}
		}
	} else {
		in, err := store.Inspect(ctx, o.Paths.DB)
		switch {
		case store.IsNotExist(err):
		case err != nil || in.Version != in.Known:
			out.Status, out.Message = Warn, "the accounts are counted once the daemon runs"
			return out
		default:
			total, enabled = in.Accounts, in.EnabledAccounts
		}
	}
	msg := fmt.Sprintf("%d account%s, %d enabled", total, plural(total), enabled)
	if usable >= 0 {
		msg += fmt.Sprintf(", %d usable", usable)
	}
	out.Message = msg
	if enabled == 0 {
		out.Status, out.Fix = Warn, "add or enable an account in the dashboard (seelegent ui)"
		return out
	}
	out.Status = OK
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pathEntries warns about relative PATH entries: a worker started in a project folder would run programs from it.
func pathEntries(env []string) Check {
	path := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			path = v
		}
	}
	var rel []string
	for _, e := range filepath.SplitList(path) {
		if e != "" && !filepath.IsAbs(e) {
			rel = append(rel, e)
		}
	}
	if len(rel) > 0 {
		return Check{Name: "path", Status: Warn, Message: "PATH has relative entries: " + strings.Join(rel, ", "),
			Fix: "make them absolute or remove them; a worker resolves them inside the project folder"}
	}
	return Check{Name: "path", Status: OK, Message: "PATH has no relative entries"}
}
