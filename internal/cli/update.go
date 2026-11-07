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

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/update"
)

// Error codes of update (printed with --json).
const (
	codeChecksum   = "checksum_mismatch"
	codeDevBuild   = "dev_build"
	codeNoPrevious = "no_previous"
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
	}
	return err
}

func newUpdateCommand(info buildinfo.Info, d deps) *cobra.Command {
	var check, apply, rollback bool
	var version string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a new release, install it, or go back to the previous version",
		Long: "--check (the default) compares this build with the latest release. --apply downloads the release for\n" +
			"this platform, checks its SHA-256, stops the daemon, replaces this executable in place (the old one is\n" +
			"kept next to it), starts the daemon again and refreshes the lead hosts that have a Seelegent entry.\n" +
			"--rollback puts back the executable the last update replaced. --version vX.Y.Z picks a release (also\n" +
			"an older one); a development build needs it to apply. Releases come from GitHub, or from\n" +
			update.EnvUpdateURL + ".",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "update"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (check && apply) || (check && rollback) || (apply && rollback) {
				return usagef("use only one of --check, --apply and --rollback")
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
			u, err := newUpdater(cmd, info, d)
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
	return cmd
}

// newUpdater builds the updater for this executable and data folder.
func newUpdater(cmd *cobra.Command, info buildinfo.Info, d deps) (*update.Updater, error) {
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
	return update.New(update.Options{
		Current:     info.Version,
		Exe:         exe,
		WorkDir:     filepath.Join(paths.Home, "update"),
		MetadataURL: os.Getenv(update.EnvUpdateURL),
		Daemon:      &updateDaemon{d: d, log: stderr},
		Run:         runExecutable,
		Progress: func(msg string) {
			if !quiet {
				_, _ = fmt.Fprintln(stderr, msg)
			}
		},
	}), nil
}

// updateDaemon is the daemon of the data folder for the updater, over the client.
type updateDaemon struct {
	d   deps
	log io.Writer
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

// Start starts the daemon from exe (the executable now installed) and waits until it answers.
func (u *updateDaemon) Start(ctx context.Context, exe string) error {
	cn, err := u.conn()
	if err != nil {
		return err
	}
	dm := client.NewDaemon(client.DaemonOptions{Paths: cn.paths, Port: cn.port,
		Start: client.SpawnDaemon(exe, cn.paths, cn.port, os.Environ()), Logger: cn.log})
	_, err = dm.Ensure(ctx)
	return err
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
