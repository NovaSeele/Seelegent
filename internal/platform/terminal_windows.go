//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// openTerminal writes a batch file and runs it in a new console window through `start /wait`. The `start` builtin
// gives the window real console handles, which interactive sign-ins need (a piped or redirected stdin is ignored by
// some CLIs). The hidden outer cmd.exe lives until the window closes, so Terminal.Wait can follow it.
func openTerminal(_ context.Context, spec TerminalSpec, base, exitFile string) (*Terminal, error) {
	script, err := windowsScript(spec, exitFile)
	if err != nil {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	scriptPath := base + ".cmd"
	if strings.ContainsAny(scriptPath, `"%`) {
		return nil, fmt.Errorf("open terminal: unsupported characters in script path %q", scriptPath)
	}
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	// The window must stay open after the caller's context ends, so the command is not bound to it.
	cmd := exec.Command(comspec) //nolint:gosec,noctx // ComSpec is the system shell; the window outlives ctx
	cmd.Env = spec.Env
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	HideWindow(cmd)
	// /s strips the outer quotes and runs the rest as written.
	sysProcAttr(cmd).CmdLine = quoted(comspec) + ` /d /s /c "start ` + quoted(spec.Title) + " /wait " + quoted(comspec) +
		" /d /c " + quoted(scriptPath) + `"`
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	return &Terminal{exitFile: exitFile, process: cmd}, nil
}
