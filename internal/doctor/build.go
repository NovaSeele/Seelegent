package doctor

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// daemonVersion compares the build of the daemon that answers with this binary's: the version, and the commit
// when both builds know it (two builds of one version from different commits run different code, as a
// development build beside the installed release does). Something on the port that is no Seelegent v1 daemon,
// such as the old Node version's daemon (its API is under /api, not /api/v1), is a warning too. ok is false when
// nothing answers, when the probe timed out, or when a Seelegent v1 daemon answers that this client may not use
// (the daemon check says why).
func daemonVersion(o Options, cl *client.Client, h api.Health, findErr error) (c Check, ok bool) {
	c = Check{Name: "daemon_version"}
	if cl == nil {
		var fe *client.ForeignError
		switch {
		case !errors.As(findErr, &fe) || fe.Timeout > 0 || fe.OtherHome || fe.OlderBuild:
			return c, false
		case fe.APIVersion != 0:
			c.Status, c.Message = Warn, fmt.Sprintf("the daemon on port %d speaks Seelegent API v%d, this "+
				"seelegent speaks v%d", fe.Port, fe.APIVersion, api.APIVersion)
			c.Fix = "stop that daemon with the seelegent that started it, then run: seelegent ui"
		default:
			c.Status, c.Message = Warn, fmt.Sprintf("port %d answers, but not as a Seelegent v%d daemon (no "+
				"/api/v%d/health): it is the old Node version's daemon (its API is under /api) or another program",
				fe.Port, api.APIVersion, api.APIVersion)
			c.Fix = "stop that program (for the old Node version, see old_node), then run: seelegent ui"
		}
		return c, true
	}
	port := h.Port
	if port == 0 {
		port = o.Daemon.Port()
	}
	theirs, ours := build(h.Version, h.Commit), build(o.Version, o.Commit)
	if sameBuild(h.Version, h.Commit, o.Version, o.Commit) {
		c.Status, c.Message = OK, fmt.Sprintf("the daemon on port %d runs %s, the build of this seelegent", port, theirs)
		return c, true
	}
	c.Status, c.Message = Warn, fmt.Sprintf("the daemon on port %d runs %s, this seelegent is %s", port, theirs, ours)
	c.Fix = "seelegent restart"
	return c, true
}

// build names a version and, when known, its commit: "v0.9.0 (commit 58337977a4eb)".
func build(version, commit string) string {
	if version == "" {
		version = "an unknown version"
	}
	if knownCommit(commit) {
		return version + " (commit " + commit + ")"
	}
	return version
}

func knownCommit(c string) bool { return c != "" && c != buildinfo.Unknown }

// sameBuild compares two builds. An unknown version matches any; the commits are compared only when both are
// known, by prefix (a 7-character commit matches its 12-character form).
func sameBuild(v1, c1, v2, c2 string) bool {
	if v1 != "" && v2 != "" && v1 != v2 {
		return false
	}
	if !knownCommit(c1) || !knownCommit(c2) {
		return true
	}
	c1, c2 = strings.ToLower(c1), strings.ToLower(c2)
	if len(c1) > len(c2) {
		c1, c2 = c2, c1
	}
	return strings.HasPrefix(c2, c1)
}

// IsNode reports whether an executable file name is node's (node.exe on Windows): the filter for
// platform.ListProcesses that Options.Processes uses.
func IsNode(name string) bool {
	return strings.EqualFold(name, "node.exe") || strings.EqualFold(name, "node")
}

// oldNode looks for processes of the old Node version (node <...>\seelegent\src\cli.js). Its MCP servers, kept
// by lead hosts that were not restarted after the update, can start the old daemon on the migrated data folder.
// It never stops anything. ok is false when processes cannot be listed on this system.
func oldNode(ctx context.Context, list func(context.Context) ([]platform.Process, error)) (c Check, ok bool) {
	c = Check{Name: "old_node"}
	if list == nil {
		return c, false
	}
	procs, err := list(ctx)
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		return c, false
	case err != nil:
		c.Status, c.Message = Warn, fmt.Sprintf("the running processes could not be listed: %v", err)
		return c, true
	}
	var pids []string
	mcp := 0
	for _, p := range procs {
		old, isMCP := oldNodeArgs(p.Args)
		if !old {
			continue
		}
		pids = append(pids, strconv.Itoa(p.PID))
		if isMCP {
			mcp++
		}
	}
	if len(pids) == 0 {
		c.Status, c.Message = OK, "no process of the old Node version runs"
		return c, true
	}
	n := len(pids)
	verb, noun := "runs", "process"
	if n > 1 {
		verb, noun = "run", "processes"
	}
	c.Status = Warn
	c.Message = fmt.Sprintf("%d %s of the old Node version %s (pid %s): %d MCP server%s (cli.js mcp), %d other. "+
		"An old MCP server can start the old daemon on the migrated data folder", n, noun, verb,
		strings.Join(pids, ", "), mcp, plural(mcp), n-mcp)
	c.Fix = "restart the lead hosts (Claude Code, Claude Desktop, Codex, Antigravity, ...) so they load the new " +
		"MCP entry; doctor stops nothing"
	return c, true
}

// oldNodeArgs reports whether args run the old Node version and whether they run it as the MCP server (cli.js
// mcp). Only the old install layout counts: a script path ending in seelegent/src/cli.js, or
// seelegent/versions/<version>/src/cli.js (in any case, with either slash); any other cli.js, such as a
// node_modules tool inside a checkout of Seelegent, is not the old version.
func oldNodeArgs(args []string) (old, mcp bool) {
	for i, a := range args {
		if oldNodeScript(a) {
			return true, i+1 < len(args) && strings.EqualFold(args[i+1], "mcp")
		}
	}
	return false, false
}

func oldNodeScript(arg string) bool {
	n := strings.ToLower(strings.ReplaceAll(arg, `\`, "/"))
	dir, ok := strings.CutSuffix(n, "/src/cli.js")
	if !ok {
		return false
	}
	if strings.HasSuffix(dir, "/seelegent") || dir == "seelegent" {
		return true
	}
	// <...>/seelegent/versions/<version>
	parent, version := path.Split(dir)
	return version != "" && (strings.HasSuffix(parent, "/seelegent/versions/") || parent == "seelegent/versions/")
}
