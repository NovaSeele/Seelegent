package setup

import (
	"fmt"
	"io"
	"strings"
)

// WriteText prints the report for a terminal: the command line, one line per item with its path and detail, then
// warnings and what to do next.
func (r Report) WriteText(w io.Writer) error {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("Dry run: nothing was written.\n")
	}
	if !r.Remove {
		fmt.Fprintf(&b, "MCP server command: %s %s\n", quoteArg(r.Command), strings.Join(r.Args, " "))
	}
	detected := 0
	for _, h := range r.Hosts {
		if h.Detected {
			detected++
		}
	}
	if len(r.Items) == 0 {
		if detected == 0 {
			b.WriteString("No lead host was found. Known hosts: ")
			ids := make([]string, 0, len(r.Hosts))
			for _, h := range r.Hosts {
				ids = append(ids, h.ID)
			}
			b.WriteString(strings.Join(ids, ", ") + ".\n")
		} else {
			b.WriteString("Nothing to do.\n")
		}
	}
	for _, it := range r.Items {
		fmt.Fprintf(&b, "%-15s %-5s %-14s %s\n", it.Host, it.Kind, r.verb(it.Action), it.Path)
		if it.Detail != "" {
			fmt.Fprintf(&b, "%22s%s\n", "", it.Detail)
		}
		if it.Backup != "" {
			fmt.Fprintf(&b, "%22sbackup: %s\n", "", it.Backup)
		}
		if it.Snippet != "" {
			for line := range strings.SplitSeq(it.Snippet, "\n") {
				fmt.Fprintf(&b, "%26s%s\n", "", line)
			}
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	if r.Failed() {
		b.WriteString("Some items failed or conflict; see above. Nothing was written for them.\n")
	} else if !r.DryRun && r.changed() {
		b.WriteString("Restart the lead hosts (or start a new session) so they load the change.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// verb is the action as the text output shows it ("would add" in a dry run).
func (r Report) verb(a Action) string {
	if !r.DryRun {
		return string(a)
	}
	switch a {
	case ActionAdded:
		return "would add"
	case ActionUpdated:
		return "would update"
	case ActionRemoved:
		return "would remove"
	}
	return string(a)
}

// changed reports whether any item changed something.
func (r Report) changed() bool {
	for _, it := range r.Items {
		switch it.Action {
		case ActionAdded, ActionUpdated, ActionRemoved:
			return true
		}
	}
	return false
}

// quoteArg quotes a path with spaces for display.
func quoteArg(s string) string {
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}
