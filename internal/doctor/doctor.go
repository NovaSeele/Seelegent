// Package doctor checks a Seelegent installation for `seelegent doctor`: the data directory, the settings, the
// database, the daemon and the build it runs, processes and the pid file of the old Node version, the embedded
// dashboard, git, the enabled backend CLIs, the accounts (counts only), PATH, free memory against the start
// thresholds and, when rustup is installed, whether the first cargo on PATH is rustup's proxy.
// It reads only: it never starts or stops the daemon or any other process, never migrates the database and never
// prints an e-mail address or key.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/launchbin"
	"github.com/NovaSeele/Seelegent/internal/platform"
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
	// Version and Commit are this binary's build (buildinfo); a daemon of another build gets a warning.
	Version string
	Commit  string
	// Processes lists the running node processes with their command lines (platform.ListProcesses with a node
	// filter); nil, or an error matching errors.ErrUnsupported, skips the old_node check.
	Processes func(ctx context.Context) ([]platform.Process, error)
	// Web is the embedded dashboard (nil or without index.html: not part of this build).
	Web fs.FS
	// Env is the environment (os.Environ) and RealHome the user's home folder, to find the CLIs.
	Env      []string
	RealHome string
	// GitPath finds git (gitx.Binary.Path). GitWrapper is the batch file (git.cmd, git.bat) that comes first on
	// PATH, which Seelegent never runs ("" when the first git on PATH is no batch file; gitx.FirstWrapper).
	GitPath    func() (string, error)
	GitWrapper string
	// Memory reads the free memory now (platform.Memory); nil skips the resources check.
	Memory func() platform.MemoryStatus
	// Launcher looks at the companion launcher next to this executable (launchbin.Inspect); nil skips the launcher
	// line.
	Launcher func() launchbin.Status
}

// Run runs every check in a fixed order.
func Run(ctx context.Context, o Options) []Check {
	out := []Check{dataDir(o.Paths, o.Daemon.LockHeld()), settings(o), database(ctx, o.Paths)}
	d, c, h, findErr := daemon(ctx, o)
	out = append(out, d)
	if v, ok := daemonVersion(o, c, h, findErr); ok {
		out = append(out, v)
	}
	if n, ok := oldNode(ctx, o.Processes); ok {
		out = append(out, n)
	}
	if p, ok := oldPIDFile(ctx, o.Paths.Home, o.Processes); ok {
		out = append(out, p)
	}
	out = append(out, portFile(o), dashboard(o.Web))
	if o.Launcher != nil {
		out = append(out, launcher(o.Launcher()))
	}
	out = append(out, git(o.GitPath, o.GitWrapper))
	out = append(out, backends(o)...)
	out = append(out, accounts(ctx, o, c), pathEntries(o.Env, runtime.GOOS))
	var live *api.Health
	if c != nil {
		live = &h
	}
	if r, ok := resources(o, runtime.GOOS, live); ok {
		out = append(out, r)
	}
	if c, ok := cargo(o.Env, o.RealHome, runtime.GOOS); ok {
		out = append(out, c)
	}
	return out
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

// dataDir checks the data directory. While its daemon runs (live: it holds the lock) the daemon is writing there
// already, so nothing is written; otherwise a probe file is written atomically and removed again.
func dataDir(p config.Paths, live bool) Check {
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
	if live {
		c.Status, c.Message = OK, "data directory "+p.Home+" (in use by its daemon)"
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

// daemon checks the running daemon. It returns the daemon's client and health answer when it answers, else the
// lookup's error (daemonVersion reads both).
func daemon(ctx context.Context, o Options) (Check, *client.Client, api.Health, error) {
	c := Check{Name: "daemon"}
	cl, err := o.Daemon.Find(ctx)
	if err == nil {
		h, herr := cl.Health(ctx)
		if herr != nil {
			c.Status, c.Message = Fail, fmt.Sprintf("the daemon answers but its health check fails: %v", herr)
			return c, nil, api.Health{}, herr
		}
		c.Status, c.Message = OK, fmt.Sprintf("the daemon %s runs at %s/ (pid %d)", h.Version, cl.BaseURL(), h.PID)
		return c, cl, h, nil
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
		switch {
		case errors.As(err, &foreign) && foreign.OlderBuild:
			c.Fix = "stop that daemon with the Seelegent build that started it, then run this one"
		case errors.As(err, &foreign) && foreign.OtherHome:
			c.Fix = "give each data directory its own port (SEELEGENT_PORT or the settings' port)"
		case errors.As(err, &foreign) && !client.TimedOut(err):
			c.Fix = "stop that program, or set another port with SEELEGENT_PORT or the settings' port"
		}
	}
	return c, nil, api.Health{}, err
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

// launcher says, as information (never a warning: launches work either way), whether the daemon runs the launches
// in the companion launcher seelegent-launch, which takes about 40 MB less private memory per running task on
// Windows than `seelegent __launch`.
func launcher(s launchbin.Status) Check {
	return Check{Name: "launcher", Status: OK, Message: launchbin.Describe(s)}
}

// git names the git Seelegent runs. A batch wrapper first on PATH (ServBay's or a package manager's git.cmd) is a
// warning: Seelegent skips it, but cmd.exe rewrites the arguments of everything else that runs it (^ is dropped,
// %VAR% expanded), such as the user's hooks and gate commands.
func git(path func() (string, error), wrapper string) Check {
	p, err := path()
	if err != nil {
		return Check{Name: "git", Status: Fail, Message: fmt.Sprintf("git not found: %v", err),
			Fix: "install Git for Windows (git.exe) or put the folder of a real git executable on PATH"}
	}
	if wrapper != "" && !strings.EqualFold(filepath.Clean(wrapper), filepath.Clean(p)) {
		return Check{Name: "git", Status: Warn, Message: fmt.Sprintf("git %s; the first git on PATH is the batch "+
			"wrapper %s, which Seelegent never runs, but your own hooks, gate commands and terminals do, and cmd.exe "+
			"rewrites their arguments (HEAD^ loses its ^)", p, wrapper),
			Fix: fmt.Sprintf("put %s before %s on PATH", filepath.Dir(p), filepath.Dir(wrapper))}
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

// cargo checks that the first cargo on PATH is rustup's proxy. ok is false when rustup is not installed (no check).
func cargo(env []string, realHome, goos string) (c Check, ok bool) {
	state, rustup, cargo := account.CargoOnPath(env, realHome)
	userBin := "~/.cargo/bin"
	if goos == "windows" {
		userBin = `%USERPROFILE%\.cargo\bin`
	}
	c = Check{Name: "cargo"}
	switch state {
	case account.CargoNoRustup:
		return c, false
	case account.CargoMissing:
		c.Status, c.Message = Warn, fmt.Sprintf("rustup is installed (%s) but no cargo is on PATH", rustup)
		c.Fix = "add " + userBin + " to PATH"
	case account.CargoProxy:
		c.Status, c.Message = OK, fmt.Sprintf("cargo on PATH is the rustup proxy (%s)", cargo)
	case account.CargoPlain:
		c.Status = Warn
		c.Message = fmt.Sprintf("the first cargo on PATH is %s, not the rustup proxy in %s. A plain cargo lets a "+
			"dependency's own rust-toolchain.toml switch compilers mid-build (error E0514). Seelegent pins "+
			"RUSTUP_TOOLCHAIN for workers and gates; your own shells are not covered", cargo, filepath.Dir(rustup))
		c.Fix = fmt.Sprintf("put %s before %s in PATH", userBin, filepath.Dir(cargo))
	}
	return c, true
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
// On Windows an entry with a %NAME% left in it is told apart: the variable was never expanded (a REG_SZ PATH
// value, or a per-user variable in the system PATH), so no program finds anything there.
func pathEntries(env []string, goos string) Check {
	path := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(k, "PATH") {
			path = v
		}
	}
	var rel, unexpanded, expanded []string
	for _, e := range filepath.SplitList(path) {
		switch {
		case e == "":
		case goos == "windows" && hasPercentVar(e):
			unexpanded = append(unexpanded, e)
			if x, ok := expandPercent(e, env); ok {
				expanded = append(expanded, x)
			}
		case !filepath.IsAbs(e):
			rel = append(rel, e)
		}
	}
	if len(rel) == 0 && len(unexpanded) == 0 {
		return Check{Name: "path", Status: OK, Message: "PATH has no relative entries"}
	}
	var msgs, fixes []string
	if len(unexpanded) > 0 {
		msgs = append(msgs, "PATH has entries whose variable was never expanded: "+strings.Join(unexpanded, ", ")+
			", so every program sees the literal text and finds nothing there. This usually has one of two causes: "+
			"the PATH value is REG_SZ instead of REG_EXPAND_SZ, or the entry is in the system PATH but uses a "+
			"per-user variable such as %APPDATA% or %USERPROFILE%, which does not exist when Windows expands the "+
			"system PATH")
		fix := "look at the Path value in HKCU\\Environment and in HKLM\\SYSTEM\\CurrentControlSet\\Control\\" +
			"Session Manager\\Environment: make it REG_EXPAND_SZ, move per-user entries to the user PATH, or write " +
			"the expanded path"
		if len(expanded) == len(unexpanded) {
			fix += " (" + strings.Join(expanded, ", ") + ")"
		}
		fixes = append(fixes, fix+"; then restart the programs that read PATH")
	}
	if len(rel) > 0 {
		msgs = append(msgs, "PATH has relative entries: "+strings.Join(rel, ", "))
		fixes = append(fixes, "make the relative entries absolute or remove them; a worker resolves them inside the "+
			"project folder")
	}
	return Check{Name: "path", Status: Warn, Message: strings.Join(msgs, ". "), Fix: strings.Join(fixes, "; ")}
}

// hasPercentVar reports whether a PATH entry holds a %NAME% (a name without path separators).
func hasPercentVar(e string) bool {
	_, ok := nextPercentVar(e)
	return ok
}

// nextPercentVar finds the first %NAME% in s: the indexes of its two % signs.
func nextPercentVar(s string) (span [2]int, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			return span, false
		}
		name := s[i+1 : i+1+j]
		if name != "" && !strings.ContainsAny(name, `\/;`) {
			return [2]int{i, i + 1 + j}, true
		}
		i += j // the second % may open the next variable
	}
	return span, false
}

// expandPercent expands every %NAME% in e from env, as REG_EXPAND_SZ would; ok is false when a name is not set.
func expandPercent(e string, env []string) (string, bool) {
	var b strings.Builder
	for {
		span, found := nextPercentVar(e)
		if !found {
			b.WriteString(e)
			return b.String(), true
		}
		v, set := envx.Lookup(env, e[span[0]+1:span[1]])
		if !set {
			return "", false
		}
		b.WriteString(e[:span[0]])
		b.WriteString(v)
		e = e[span[1]+1:]
	}
}

// resources compares one reading of free memory with the effective minFreeMemoryMB and minFreeCommitMB of this
// machine (auto from its size, derived from the other threshold, or set by the user): below either, new worker
// turns and gate commands would wait now. When the running daemon's health (live) reports them, the values, their
// sources, the machine and the reading are all the daemon's own (its cgroup may differ from this process's);
// else this process reads free memory (o.Memory, nil: the check is skipped) and the values are computed here from
// the settings file. The message says which. It is a single reading (free memory can swing by gigabytes within
// hours), so the suggestion is a range to consider, never a prescription.
func resources(o Options, goos string, live *api.Health) (Check, bool) {
	fromDaemon := live != nil && live.Resources.MinFreeMemoryMB.Source != ""
	if !fromDaemon && o.Memory == nil {
		return Check{}, false
	}
	c := Check{Name: "resources"}
	var m platform.MemoryStatus
	if fromDaemon {
		lm := live.Memory
		m = platform.MemoryStatus{AvailPhysMB: lm.AvailPhysMB, AvailCommitMB: lm.AvailCommitMB, Known: lm.Known,
			CgroupV1: lm.CgroupV1}
	} else {
		m = o.Memory()
	}
	if !m.Known {
		c.Status, c.Message = OK, "free memory cannot be read on this system: the memory thresholds hold nothing"
		if fromDaemon {
			c.Message = "the daemon cannot read free memory on this system: the memory thresholds hold nothing"
		}
		return c, true
	}
	r, src, from := effectiveResources(o.Settings, m, goos, live)
	commitKnown := m.AvailCommitMB > 0
	reading := fmt.Sprintf("free memory %d MB (minFreeMemoryMB %d, %s)", m.AvailPhysMB, r.MinFreeMemoryMB,
		sourceWords(src.MinFreeMemoryMB, "criticalFreeMemoryMB"))
	if commitKnown {
		reading += fmt.Sprintf(", free commit %d MB (minFreeCommitMB %d, %s)", m.AvailCommitMB, r.MinFreeCommitMB,
			sourceWords(src.MinFreeCommitMB, ""))
	}
	if m.CgroupV1 {
		reading += ", within the cgroup v1 memory limit"
	}
	reading += "; " + from
	physLow := r.MinFreeMemoryMB > 0 && m.AvailPhysMB < int64(r.MinFreeMemoryMB)
	commitLow := r.MinFreeCommitMB > 0 && commitKnown && m.AvailCommitMB < int64(r.MinFreeCommitMB)
	if !physLow && !commitLow {
		c.Status, c.Message = OK, "one reading: "+reading
		return c, true
	}
	c.Status = Warn
	c.Message = "right now " + reading + ". New worker turns and gate commands would wait for memory. This is a " +
		"single reading; free memory can swing a lot within hours, so check again when the machine is in its usual state"
	var hints []string
	if physLow {
		lo, hi := memoryRange(m.AvailPhysMB)
		h := fmt.Sprintf("minFreeMemoryMB somewhere around %d-%d MB", lo, hi)
		if commitKnown && !commitLow && r.MinFreeCommitMB > 0 {
			h += fmt.Sprintf(" (free commit, %d MB, is above its threshold: on Windows commit is the stronger guard, "+
				"so minFreeMemoryMB can be low or 0)", m.AvailCommitMB)
		}
		hints = append(hints, h)
	}
	if commitLow {
		lo, hi := memoryRange(m.AvailCommitMB)
		hints = append(hints, fmt.Sprintf("minFreeCommitMB somewhere around %d-%d MB", lo, hi))
	}
	c.Fix = "close memory-hungry programs, or, if this is the machine's usual state, consider setting " +
		strings.Join(hints, " and ") + " (about half to three quarters of what is free now) in the dashboard's " +
		"Settings, Resources; a value you set replaces the auto one, and Auto goes back to it"
	return c, true
}

// effectiveResources are the effective resource values with their sources and where they and the reading come
// from: the running daemon's health (live; nil when it does not answer) when it reports them, else computed here
// on m, this process's reading.
func effectiveResources(set config.Settings, m platform.MemoryStatus, goos string,
	live *api.Health) (r config.Resources, src config.ResourceSources, from string) {
	if live != nil && live.Resources.MinFreeMemoryMB.Source != "" {
		e, mc := live.Resources, live.Machine
		r = config.Resources{MinFreeMemoryMB: e.MinFreeMemoryMB.Value, CriticalFreeMemoryMB: e.CriticalFreeMemoryMB.Value,
			MinFreeCommitMB: e.MinFreeCommitMB.Value, TestParallelism: e.TestParallelism.Value,
			GateConcurrency: e.GateConcurrency.Value}
		src = config.ResourceSources{MinFreeMemoryMB: e.MinFreeMemoryMB.Source,
			CriticalFreeMemoryMB: e.CriticalFreeMemoryMB.Source, MinFreeCommitMB: e.MinFreeCommitMB.Source,
			TestParallelism: e.TestParallelism.Source, GateConcurrency: e.GateConcurrency.Source}
		return r, src, fmt.Sprintf("reading and values from the running daemon (machine: %d MB, %d CPUs)",
			mc.TotalPhysMB, mc.CPUs)
	}
	mc := config.NewMachine(m.TotalPhysMB, m.TotalCommitMB, platform.CPUs(), goos)
	r, src = config.EffectiveResourceSources(set, mc)
	why := "the daemon is not running"
	if live != nil {
		why = "the daemon does not report them"
	}
	return r, src, fmt.Sprintf("read by this command, values computed here from the settings file, as %s "+
		"(machine: %d MB, %d CPUs)", why, mc.TotalPhysMB, mc.CPUs)
}

// sourceWords say where an effective value comes from; other names the threshold a derived one comes from.
func sourceWords(source, other string) string {
	switch source {
	case config.SourceExplicit:
		return "set by you"
	case config.SourceDerived:
		return "derived from your " + other
	}
	return "auto for this machine"
}

// memoryRange is a threshold range to consider for a reading of free MB: half to three quarters of it, rounded down
// to 256 MB, at least 256.
func memoryRange(free int64) (lo, hi int64) {
	round := func(n int64) int64 { return max(n/256*256, 256) }
	return round(free / 2), round(free * 3 / 4)
}
