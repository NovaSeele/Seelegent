package update

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// HostItem is one item of the host refresh (a line of the setup report the new executable printed).
type HostItem struct {
	Host   string `json:"host"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	Path   string `json:"path,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// setupReport is the part of `seelegent setup --json` the refresh reads.
type setupReport struct {
	Items    []HostItem `json:"items"`
	Warnings []string   `json:"warnings"`
}

// Setup actions that say a host has a Seelegent MCP entry already.
var registered = []string{"unchanged", "updated"}

// refreshHosts runs the installed executable's setup for the hosts that already have a Seelegent MCP entry (old
// C-B1): the entries point at the same stable path, so this mostly brings the lead skill to the new version. Hosts
// without an entry are left alone; the hook is not touched (its command is the same path). Problems are warnings:
// the update itself is done.
func (u *Updater) refreshHosts(ctx context.Context) (items []HostItem, warnings []string) {
	items = []HostItem{}
	if u.o.Run == nil {
		return items, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	u.progress("Refreshing the lead hosts...")
	plan, err := u.setup(ctx, "--dry-run")
	if err != nil {
		return items, []string{"could not check the lead hosts: " + err.Error() + "; run `seelegent setup`"}
	}
	var hosts []string
	for _, it := range plan.Items {
		if it.Kind == "mcp" && slices.Contains(registered, it.Action) && !slices.Contains(hosts, it.Host) {
			hosts = append(hosts, it.Host)
		}
	}
	if len(hosts) == 0 {
		return items, nil
	}
	rep, err := u.setup(ctx, "--host", strings.Join(hosts, ","))
	if err != nil {
		return items, []string{"could not refresh the lead hosts (" + strings.Join(hosts, ", ") + "): " +
			err.Error() + "; run `seelegent setup`"}
	}
	for _, it := range rep.Items {
		items = append(items, it)
		if it.Action == "error" || it.Action == "conflict" {
			warnings = append(warnings, fmt.Sprintf("setup %s %s: %s %s", it.Host, it.Kind, it.Action, it.Detail))
		}
	}
	return items, append(warnings, rep.Warnings...)
}

// setup runs `<exe> setup --json args...` and reads its report. setup exits 1 when an item failed; the report is
// printed then too.
func (u *Updater) setup(ctx context.Context, args ...string) (setupReport, error) {
	out, runErr := u.o.Run(ctx, u.o.Exe, append([]string{"setup", "--json"}, args...)...)
	var rep setupReport
	if err := json.Unmarshal(out, &rep); err != nil || rep.Items == nil {
		if runErr != nil {
			return setupReport{}, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(out)))
		}
		return setupReport{}, fmt.Errorf("unreadable setup report: %q", strings.TrimSpace(string(out)))
	}
	return rep, nil
}
