package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/update"
)

// Error codes of update (printed with --json).
const (
	codeChecksum   = "checksum_mismatch"
	codeDevBuild   = "dev_build"
	codeNoPrevious = "no_previous"
	// codeSchemaTooNew: the build to install (or `version --check-database`) knows fewer database migrations than
	// the data folder's database has; codeSchemaUnknown: it does not say how many it knows.
	codeSchemaTooNew  = "schema_too_new"
	codeSchemaUnknown = "schema_unknown"
	// codeMigratedThenFailed: the new daemon migrated the database and then did not start; the old build cannot open
	// the database any more, so the new one stays installed.
	codeMigratedThenFailed = "migrated_then_failed"
)

// codedError is an error with its own --json code (exit 1).
type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// updateError gives the updater's errors their codes.
func updateError(err error) error {
	switch {
	case errors.Is(err, update.ErrChecksum):
		return &codedError{code: codeChecksum, err: err}
	case errors.Is(err, update.ErrDevBuild):
		return &codedError{code: codeDevBuild, err: err}
	case errors.Is(err, update.ErrNoPrevious):
		return &codedError{code: codeNoPrevious, err: err}
	case errors.Is(err, update.ErrSchemaTooNew):
		return &codedError{code: codeSchemaTooNew, err: err}
	case errors.Is(err, update.ErrSchemaUnknown):
		return &codedError{code: codeSchemaUnknown, err: err}
	case errors.Is(err, update.ErrMigratedThenFailed):
		return &codedError{code: codeMigratedThenFailed, err: err}
	}
	return err
}

func newUpdateCommand(info buildinfo.Info, d deps) *cobra.Command {
	var check, apply, rollback, force bool
	var version string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new release, install it, or go back to the previous version",
		Long: "--check (the default) compares this build with the latest release. --apply downloads the release for\n" +
			"this platform, checks its SHA-256, stops the daemon, replaces this executable in place (the old one is\n" +
			"kept next to it), starts the daemon again and refreshes the lead hosts that have a Seelegent entry.\n" +
			"--rollback puts back the executable the last update replaced. --version vX.Y.Z picks a release (also\n" +
			"an older one); a development build needs it to apply. A build that knows fewer database migrations\n" +
			"than the database has is refused (schema_too_new); --force installs a build that does not report\n" +
			"how many it knows (schema_unknown). Releases come from GitHub, or from " + update.EnvUpdateURL + ".",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "update"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (check && apply) || (check && rollback) || (apply && rollback) {
				return usagef("use only one of --check, --apply and --rollback")
			}
			if force && !apply && !rollback {
				return usagef("--force goes with --apply or --rollback")
			}
			tag := ""
			if version != "" {
				if rollback {
					return usagef("--version does not go with --rollback")
				}
				var err error
				if tag, err = update.NormalizeTag(version); err != nil {
					return usagef("--version: %v", err)
				}
			}
			u, err := newUpdater(cmd, info, d, force)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			switch {
			case apply:
				res, err := u.Apply(ctx, tag)
				if err != nil {
					return updateError(err)
				}
				return emit(cmd, res, func(w io.Writer) { printApply(w, res) })
			case rollback:
				res, err := u.Rollback(ctx)
				if err != nil {
					return updateError(err)
				}
				return emit(cmd, res, func(w io.Writer) {
					printUpdateLines(w, res.Message, res.DaemonRestarted, res.Hosts, res.Warnings)
				})
			}
			res, err := u.Check(ctx, tag)
			if err != nil {
				return updateError(err)
			}
			return emit(cmd, res, func(w io.Writer) { _, _ = fmt.Fprintln(w, res.Message) })
		},
	}
	f := cmd.Flags()
	f.BoolVar(&check, "check", false, "compare this build with the latest release (the default)")
	f.BoolVar(&apply, "apply", false, "install the latest release (or --version)")
	f.BoolVar(&rollback, "rollback", false, "go back to the version the last update replaced")
	f.StringVar(&version, "version", "", "the release to check or install, for example v1.2.3")
	f.BoolVar(&force, "force", false,
		"with --apply or --rollback: install a build that does not report which database schema it knows")
	return cmd
}

// newUpdater builds the updater for this executable and data folder.
func newUpdater(cmd *cobra.Command, info buildinfo.Info, d deps, force bool) (*update.Updater, error) {
	exe, err := setupExecutable()
	if err != nil {
		return nil, err
	}
	paths, err := config.DefaultPaths()
	if err != nil {
		return nil, err
	}
	stderr := cmd.ErrOrStderr()
	quiet := jsonFlag(cmd)
	known, err := store.KnownSchemaVersion()
	if err != nil {
		return nil, err
	}
	return update.New(update.Options{
		Current:       info.Version,
		Exe:           exe,
		WorkDir:       filepath.Join(paths.Home, "update"),
		MetadataURL:   os.Getenv(update.EnvUpdateURL),
		Daemon:        &updateDaemon{d: d, log: stderr},
		Run:           runExecutable,
		Force:         force,
		CurrentSchema: known,
		DatabaseSchema: func(ctx context.Context) (int, bool, error) {
			in, err := store.Inspect(ctx, paths.DB)
			switch {
			case store.IsNotExist(err):
				return 0, false, nil
			case err != nil && !errors.Is(err, store.ErrSchemaTooNew):
				return 0, true, err
			}
			return in.Version, true, nil
		},
		Progress: func(msg string) {
			if !quiet {
				_, _ = fmt.Fprintln(stderr, msg)
			}
		},
	}), nil
}

// Waits of the updater's daemon steps.
const (
	// updateStartTimeout bounds the start of a daemon after an update: it may run database migrations first.
	updateStartTimeout = 2 * time.Minute
	// abortLockWait bounds the wait for the data folder's daemon lock after the daemon was stopped or killed.
	abortLockWait = 30 * time.Second
	// abortExitWait bounds the wait for a killed daemon process to exit.
	abortExitWait = 10 * time.Second
)

// updateDaemon is the daemon of the data folder for the updater, over the client.
type updateDaemon struct {
	d   deps
	log io.Writer
	// spawned is the process the last Start started (Abort kills exactly that one when it does not answer).
	spawned *client.Spawned
}

func (u *updateDaemon) conn() (*conn, error) { return u.d.connect(startNever, cliLogger(u.log)) }

// Running is true when this data folder's daemon answers. Another program, or another data folder's daemon, on the
// port is not this one: it is never stopped.
func (u *updateDaemon) Running(ctx context.Context) (bool, error) {
	cn, err := u.conn()
	if err != nil {
		return false, err
	}
	_, err = cn.daemon.Find(ctx)
	var foreign *client.ForeignError
	switch {
	case err == nil:
		return true, nil
	case (errors.Is(err, client.ErrNotRunning) || errors.As(err, &foreign)) && !cn.daemon.LockHeld():
		return false, nil
	case errors.Is(err, client.ErrNotRunning):
		return false, cn.daemon.NotAnswering()
	}
	return false, err
}

func (u *updateDaemon) Stop(ctx context.Context) error {
	cn, err := u.conn()
	if err != nil {
		return err
	}
	_, err = cn.daemon.Stop(ctx, u.d.stopTimeout)
	return err
}

// Start starts the daemon from exe (the executable now installed), waits until it answers (up to
// updateStartTimeout: migrations may run first) and returns the version it reports.
func (u *updateDaemon) Start(ctx context.Context, exe string) (string, error) {
	cn, err := u.conn()
	if err != nil {
		return "", err
	}
	start, spawned := client.SpawnDaemonTracked(exe, cn.paths, cn.port, os.Environ())
	u.spawned = spawned
	dm := client.NewDaemon(client.DaemonOptions{Paths: cn.paths, Port: cn.port, Start: start, Logger: cn.log,
		StartTimeout: updateStartTimeout})
	c, err := dm.Ensure(ctx)
	if err != nil {
		return "", err
	}
	h, err := c.Health(ctx)
	if err != nil {
		return "", err
	}
	return h.Version, nil
}

// Abort stops the daemon after a start that failed or brought up the wrong build: a daemon that answers is stopped
// the normal way; the process the last Start spawned is killed when it is still there (it may be holding the lock
// while it migrates, or answer only after the start gave up); then the data folder's lock must come free.
func (u *updateDaemon) Abort(ctx context.Context) error {
	cn, err := u.conn()
	if err != nil {
		return err
	}
	var proc *os.Process
	var done <-chan struct{}
	if u.spawned != nil {
		proc, done = u.spawned.Process()
	}
	_, stopErr := cn.daemon.Stop(ctx, u.d.stopTimeout)
	if proc != nil {
		select {
		case <-done:
		default:
			_ = proc.Kill()
			select {
			case <-done:
			case <-time.After(abortExitWait):
				return fmt.Errorf("the daemon process %d did not exit after it was killed", proc.Pid)
			}
		}
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	end := time.After(abortLockWait)
	for cn.daemon.LockHeld() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-end:
			if stopErr != nil {
				return stopErr
			}
			return cn.daemon.NotAnswering()
		case <-tick.C:
		}
	}
	return nil
}

// runExecutable runs exe with args and returns its standard output; a failure carries the standard error.
func runExecutable(ctx context.Context, exe string, args ...string) ([]byte, error) {
	var out, errOut bytes.Buffer
	c := exec.CommandContext(ctx, exe, args...)
	c.Stdout, c.Stderr = &out, &errOut
	if err := c.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return out.Bytes(), fmt.Errorf("%w: %s", err, msg)
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

func printApply(w io.Writer, r update.ApplyResult) {
	printUpdateLines(w, r.Message, r.DaemonRestarted, r.Hosts, r.Warnings)
	if r.Applied {
		_, _ = fmt.Fprintf(w, "The previous version is kept as %s (seelegent update --rollback).\n", r.Previous)
		_, _ = fmt.Fprintln(w, "Lead hosts keep running the old MCP server until they restart it (or start a new session).")
	}
}

func printUpdateLines(w io.Writer, msg string, restarted bool, hosts []update.HostItem, warnings []string) {
	_, _ = fmt.Fprintln(w, msg)
	if restarted {
		_, _ = fmt.Fprintln(w, "Daemon restarted.")
	}
	for _, h := range hosts {
		_, _ = fmt.Fprintf(w, "%-15s %-6s %s\n", h.Host, h.Kind, h.Action)
	}
	for _, s := range warnings {
		_, _ = fmt.Fprintf(w, "Warning: %s\n", s)
	}
}
