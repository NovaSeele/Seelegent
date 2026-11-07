package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/setup"
)

// setupHookInputLimit bounds what the hook command reads from Claude Code.
const setupHookInputLimit = 4 << 20

// errSetupInWorker refuses commands that change the machine when run inside a Seelegent worker.
var errSetupInWorker = errors.New("this command does not run inside a Seelegent worker (" + account.EnvWorker + "=1)")

func newSetupCommand(info buildinfo.Info) *cobra.Command {
	var (
		req    setup.Request
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Register Seelegent with the lead hosts on this machine",
		Long: "Detect the lead hosts on this machine (" + strings.Join(setup.HostIDs(), ", ") + ") and add the\n" +
			"seelegent MCP server to each, with the lead skill where the host reads skills. Every changed file is\n" +
			"backed up to <file>" + setup.BackupSuffix + " first. Running it again reports \"unchanged\".\n\n" +
			"The hosts start the executable given with --exe (default: this seelegent, with links resolved) with\n" +
			"the argument \"mcp\". An entry named seelegent that another program wrote is a conflict (exit code 1,\n" +
			"nothing written) unless --force. --remove removes only what Seelegent added.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if os.Getenv(account.EnvWorker) == "1" {
				return errSetupInWorker
			}
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
			rep, err := setup.Run(cmd.Context(), env, req)
			if err != nil {
				return err
			}
			if err := printSetupReport(cmd.OutOrStdout(), asJSON, rep, rep.WriteText); err != nil {
				return err
			}
			if rep.Failed() {
				return errors.New("some items failed or conflict")
			}
			return nil
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
	f.BoolVar(&asJSON, "json", false, "print the report as JSON")
	return cmd
}

func newSkillCommand(info buildinfo.Info) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Print the lead skill (the guide `seelegent setup` installs)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			text, err := setup.RenderSkill(info.Version, setupSkillPort())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				return writeSetupJSON(out, map[string]string{"name": setup.ServerName, "version": info.Version,
					"file": setup.SkillFile, "content": text})
			}
			_, err = io.WriteString(out, text)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print as JSON")
	return cmd
}

// newHookCommand is the hidden `seelegent hook claude-agent` that Claude Code runs before its Agent tool (installed
// by `seelegent setup --hooks`). It never fails: a hook error would only get in the lead's way.
func newHookCommand() *cobra.Command {
	hook := &cobra.Command{Use: "hook", Short: "Hooks that lead hosts run", Hidden: true}
	hook.AddCommand(&cobra.Command{
		Use:   setup.HookCommand,
		Short: "Claude Code PreToolUse hook for the Agent tool: reminds the lead to delegate",
		Args:  cobra.NoArgs,
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
	return setup.Env{Home: home, GOOS: runtime.GOOS, Getenv: os.Getenv, LookPath: exec.LookPath, Run: runSetupHostCLI}, nil
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

// printSetupReport prints a report as indented JSON or as text.
func printSetupReport(out io.Writer, asJSON bool, v any, text func(io.Writer) error) error {
	if asJSON {
		return writeSetupJSON(out, v)
	}
	return text(out)
}

func writeSetupJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
