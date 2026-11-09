package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/doctor"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/launchbin"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/web"
)

func newDoctorCommand(info buildinfo.Info, d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the installation (never starts the daemon); exit 1 when a check fails",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			log := cliLogger(cmd.ErrOrStderr())
			cn, err := d.connect(startNever, log)
			if err != nil {
				return err
			}
			o := doctor.Options{Paths: cn.paths, Settings: cn.settings, SettingsErr: cn.configErr, Daemon: cn.daemon,
				Version: info.Version, Commit: info.Commit, Web: web.FS(), Env: os.Environ(),
				GitPath: gitx.NewBinary(nil, log).Path, GitWrapper: gitx.FirstWrapper(os.Environ()),
				Processes: d.processes, Memory: platform.Memory, Launcher: ownLauncher}
			if cn.configErr == nil {
				if cfg, err := config.Load(cn.paths.Config); err == nil {
					o.Unknown = cfg.Unknown()
				}
			}
			if home, err := os.UserHomeDir(); err == nil {
				o.RealHome = home
			}
			checks := doctor.Run(cmd.Context(), o)
			if err := emit(cmd, checks, func(w io.Writer) { printChecks(w, checks) }); err != nil {
				return err
			}
			if doctor.Failed(checks) {
				return &quietError{msg: "doctor: at least one check failed"}
			}
			return nil
		},
	}
}

// printChecks is the text table of doctor.
func printChecks(w io.Writer, checks []doctor.Check) {
	for _, c := range checks {
		tag := "[OK]  "
		switch c.Status {
		case doctor.Warn:
			tag = "[WARN]"
		case doctor.Fail:
			tag = "[FAIL]"
		}
		_, _ = fmt.Fprintf(w, "%s %-16s %s\n", tag, c.Name, c.Message)
		if c.Fix != "" {
			_, _ = fmt.Fprintf(w, "       %-16s Fix: %s\n", "", c.Fix)
		}
	}
}

// ownLauncher looks at the companion launcher next to this executable, as the daemon started from it would.
func ownLauncher() launchbin.Status {
	exe, err := os.Executable()
	if err != nil {
		return launchbin.Status{Reason: fmt.Sprintf("this executable cannot be found: %v", err)}
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	self, _ := debug.ReadBuildInfo()
	return launchbin.Inspect(exe, self)
}
