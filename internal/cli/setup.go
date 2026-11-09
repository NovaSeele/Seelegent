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
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/launchbin"
	"github.com/NovaSeele/Seelegent/internal/setup"
)

// setupHookInputLimit bounds what the hook command reads from Claude Code.
const setupHookInputLimit = 4 << 20

func newSetupCommand(info buildinfo.Info) *cobra.Command {
	var req setup.Request
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Register Seelegent with the lead hosts on this machine",
		Long: "Detect the lead hosts on this machine (" + strings.Join(setup.HostIDs(), ", ") + ") and add the\n" +
			"seelegent MCP server to each, with the lead skill where the host reads skills. Every changed file is\n" +
			"backed up to <file>" + setup.BackupSuffix + " first. Running it again reports \"unchanged\".\n\n" +
			"The hosts start the executable given with --exe (default: this seelegent, with links resolved) with\n" +
			"the argument \"mcp\". An entry named seelegent that another program wrote is a conflict (exit code 1,\n" +
			"nothing written) unless --force. --remove removes only what Seelegent added.",
		Args:        noArgs,
		Annotations: map[string]string{annOp: "setup"},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if req.Exe == "" && !req.Remove {
				exe, err := setupExecutable()
				if err != nil {
					return err
				}
				req.Exe = exe
			} else if req.Exe != "" {
				abs, err := filepath.Abs(req.Exe)
				if err != nil {
					return err
				}
				req.Exe = abs
			}
			env, err := setupProcessEnv()
			if err != nil {
				return err
			}
			req.Version, req.Port = info.Version, setupSkillPort()
			paths, err := config.DefaultPaths()
			if err != nil {
				return err
			}
			req.Created = filepath.Join(paths.Home, setup.CreatedFile)
			rep, err := setup.Run(cmd.Context(), env, req)
			if err != nil {
				return err
			}
			return printSetupReport(cmd, rep, rep.WriteText, rep.Failed(), "some items failed or conflict")
		},
	}
	f := cmd.Flags()
	f.StringSliceVar(&req.Hosts, "host", nil, "only these hosts (comma separated: "+strings.Join(setup.HostIDs(), ",")+")")
	f.BoolVar(&req.DryRun, "dry-run", false, "show what would change without writing anything")
	f.BoolVar(&req.Remove, "remove", false, "remove Seelegent's MCP entries, skill and hook")
	f.BoolVar(&req.NoSkill, "no-skill", false, "leave the lead skill alone")
	f.BoolVar(&req.Hooks, "hooks", false, "also install the Claude Code reminder hook (delegate instead of subagents)")
	f.BoolVar(&req.Force, "force", false, "replace an entry or skill named seelegent that another program wrote")
	f.StringVar(&req.Exe, "exe", "", "the seelegent executable the hosts start (default: this one)")
	return cmd
}

func newSkillCommand(info buildinfo.Info) *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print the lead skill (the guide `seelegent setup` installs)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			text, err := setup.RenderSkill(info.Version, setupSkillPort())
			if err != nil {
				return err
			}
			return emit(cmd, map[string]string{"name": setup.ServerName, "version": info.Version,
				"file": setup.SkillFile, "content": text}, func(w io.Writer) { _, _ = io.WriteString(w, text) })
		},
	}
}

// newHookCommand is the hidden `seelegent hook claude-agent` that Claude Code runs before its Agent tool (installed
// by `seelegent setup --hooks`). It never fails: a hook error would only get in the lead's way.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Short: "Hooks that lead hosts run", Hidden: true, Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
	hook.AddCommand(&cobra.Command{
		Use:   setup.HookCommand,
		Short: "Claude Code PreToolUse hook for the Agent tool: reminds the lead to delegate",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Input that cannot be read is not valid JSON either, so the hook stays silent.
			in, _ := io.ReadAll(io.LimitReader(cmd.InOrStdin(), setupHookInputLimit))
			if out := setup.AgentHookOutput(in); out != nil {
				_, _ = cmd.OutOrStdout().Write(out)
			}
			return nil
		},
	})
	return hook
}

// setupExecutable is the running seelegent with symbolic links resolved: the path the hosts keep starting after
// an update replaces the file in place.
func setupExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the seelegent executable (pass --exe): %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Abs(exe)
}

// setupProcessEnv is the setup environment of this process.
func setupProcessEnv() (setup.Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return setup.Env{}, fmt.Errorf("home directory: %w", err)
	}
	return setup.Env{Home: home, GOOS: runtime.GOOS, Getenv: os.Getenv, LookPath: exec.LookPath, Run: runSetupHostCLI,
		Relay: setupRelay}, nil
}

// setupRelay is the companion next to exe when it is of exe's build (launchbin.MatchFiles), so the hosts may start
// it as the MCP relay; `seelegent update` replaces both together, which keeps them paired.
func setupRelay(exe string) (string, error) {
	companion := launchbin.PathNextTo(exe)
	if _, err := os.Stat(companion); err != nil {
		return "", fmt.Errorf("%s is not there", companion)
	}
	if err := launchbin.MatchFiles(exe, companion); err != nil {
		return "", fmt.Errorf("%s is not of this build: %w", companion, err)
	}
	return companion, nil
}

// runSetupHostCLI runs a host CLI (claude mcp add ...) and captures its output.
func runSetupHostCLI(ctx context.Context, name string, args ...string) (setup.RunResult, error) {
	var out, errOut bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout, c.Stderr = &out, &errOut
	err := c.Run()
	res := setup.RunResult{Stdout: out.String(), Stderr: errOut.String()}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		res.Code = exit.ExitCode()
		return res, nil
	}
	return res, err
}

// setupSkillPort is the dashboard port the skill points at: from the settings when they can be read, else the default.
func setupSkillPort() int {
	settings := config.Defaults()
	if paths, err := config.DefaultPaths(); err == nil {
		if c, err := config.Load(paths.Config); err == nil {
			settings = c.Settings
		}
	}
	if port, err := config.ResolvePort(settings, os.Getenv); err == nil {
		return port
	}
	return settings.Port
}

// printSetupReport prints a report of setup or migrate (as JSON with --json, else as text). When the report holds
// failed items it ends with exit 1 and prints no second document: the report already says what failed.
func printSetupReport(cmd *cobra.Command, rep any, text func(io.Writer) error, failed bool, msg string) error {
	out := cmd.OutOrStdout()
	var err error
	if jsonFlag(cmd) {
		err = printJSON(out, rep)
	} else {
		err = text(out)
	}
	if err != nil {
		return err
	}
	if failed {
		return &quietError{msg: msg}
	}
	return nil
}

// resetHostsToPlainMCP points the MCP entries the lead hosts already have at `<exe> mcp` (update.Options.ResetHosts):
// a rollback may restore a build whose setup and companion do not know the relay. Hosts without an entry are left
// alone, and so is the skill.
func resetHostsToPlainMCP(ctx context.Context, exe string) []string {
	env, err := setupProcessEnv()
	if err != nil {
		return []string{"could not point the lead hosts at `seelegent mcp`: " + err.Error()}
	}
	env.Relay = nil
	plan, err := setup.Run(ctx, env, setup.Request{Exe: exe, DryRun: true, NoSkill: true})
	if err != nil {
		return []string{"could not point the lead hosts at `seelegent mcp`: " + err.Error()}
	}
	var hosts []string
	for _, it := range plan.Items {
		if it.Kind == setup.KindMCP && it.Action == setup.ActionUpdated && !slices.Contains(hosts, it.Host) {
			hosts = append(hosts, it.Host)
		}
	}
	if len(hosts) == 0 {
		return nil
	}
	rep, err := setup.Run(ctx, env, setup.Request{Exe: exe, Hosts: hosts, NoSkill: true})
	if err != nil {
		return []string{"could not point the lead hosts at `seelegent mcp`: " + err.Error()}
	}
	var warnings []string
	for _, it := range rep.Items {
		if it.Failed() {
			warnings = append(warnings, fmt.Sprintf("setup %s %s: %s %s", it.Host, it.Kind, it.Action, it.Detail))
		}
	}
	return warnings
}
