package migrate

import (
	"fmt"
	"io"
	"strings"
)

// WriteText prints the report for a terminal: one line per item, then a summary.
func (r Report) WriteText(w io.Writer) error {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("Dry run: nothing was written.\n")
	}
	fmt.Fprintf(&b, "From %s\nInto %s\n", r.From, r.To)
	counts := map[Action]int{}
	for _, it := range r.Items {
		counts[it.Action]++
		verb := string(it.Action)
		if r.DryRun && it.Action == Imported {
			verb = "would import"
		}
		id := it.ID
		if id == "" {
			id = "-"
		}
		fmt.Fprintf(&b, "%-10s %-28s %-13s %s\n", it.Kind, id, verb, it.Detail)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n", w)
	}
	fmt.Fprintf(&b, "%d imported, %d unchanged, %d kept, %d skipped, %d errors.\n", counts[Imported], counts[Unchanged],
		counts[Kept], counts[Skipped], counts[Error])
	if !r.DryRun && counts[Imported] > 0 {
		b.WriteString("The old folder was not changed. Start the daemon to use the imported data.\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
