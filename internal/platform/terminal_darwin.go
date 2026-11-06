//go:build darwin

package platform

import (
	"context"
	"os/exec"
	"strings"
)

// terminalCommand returns the command that opens Terminal.app running script.
func terminalCommand(_ func(string) string, _ func(string) (string, error), script string) (argv []string, reason string) {
	shell := "/bin/sh " + shQuote(script)
	as := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(shell)
	return []string{
		"osascript",
		"-e", `tell application "Terminal" to do script "` + as + `"`,
		"-e", `tell application "Terminal" to activate`,
	}, ""
}

func openURLCommand(ctx context.Context, u string) *exec.Cmd {
	return exec.CommandContext(ctx, "open", u)
}
