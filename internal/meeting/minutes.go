package meeting

import (
	"fmt"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// minutes writes the record of a meeting in English markdown for the lead (the MCP tool). The dashboard draws the
// structured record in the user's language instead.
func minutes(m *api.Meeting) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	attendees := slices.Clone(m.Attendees)
	slices.SortFunc(attendees, func(x, y api.Attendee) int { return labelNum(x.Label) - labelNum(y.Label) })
	w("# Minutes: %s", m.Title)
	w("Id: %s · Status: %s · Round %d/%d", m.ID, m.Status, m.Round, m.MaxRounds)
	w("\n## Question\n%s", m.Question)
	if len(m.Options) > 0 {
		w("")
		for _, o := range m.Options {
			w("- **%s.** %s", o.Key, o.Text)
		}
	}
	if len(m.Context) > 0 {
		w("\nContext files: `%s`", strings.Join(m.Context, "`, `"))
	}
	w("\n## Attendees\n| Label | Member | Rank | Backend | Model |\n|---|---|---|---|---|")
	for ai := range attendees {
		a := &attendees[ai]
		absent := ""
		if !a.Present {
			absent = " (absent)"
		}
		w("| %s | %s%s | %s | %s | %s |", a.Label, cell(a.Member), absent, a.Rank, cell(dash(a.Backend)),
			cell(dash(a.Model)))
	}
	if s := m.Sealed; s != nil {
		w("\n## The lead's sealed position\nLabel: %s (shown anonymously from round %d)\nsha256: %s\n", s.Label,
			max(s.RevealedRound, 2), s.Hash)
		for _, l := range strings.Split(s.Text, "\n") {
			w("> %s", l)
		}
	}
	w("\n## Positions by round")
	head, sep := "| Label |", "|---|"
	for _, r := range m.Rounds {
		head += fmt.Sprintf(" R%d |", r.N)
		sep += "---|"
	}
	w("%s\n%s", head, sep)
	for ai := range attendees {
		a := &attendees[ai]
		row := "| " + a.Label + " |"
		for i := range m.Rounds {
			r := &m.Rounds[i]
			ans := answerOf(r, a.Label)
			switch {
			case ans == nil && r.EndedAt == "":
				row += " … |"
			case ans == nil || ans.Absent:
				row += " — |"
			default:
				opt := ans.Option
				if opt == "" {
					opt = "·"
				}
				conf := "?"
				if ans.Confidence != nil {
					conf = fmt.Sprint(*ans.Confidence)
				}
				row += " " + opt + "/" + conf + " |"
			}
		}
		w("%s", row)
	}
	w("\n## Arguments and evidence")
	anyArg := false
	for ai := range attendees {
		a := &attendees[ai]
		ans, r := lastAnswer(m, a.Label, len(m.Rounds)+1)
		if ans == nil {
			continue
		}
		anyArg = true
		w("\n### %s (round %d)", a.Label, r)
		opt := ""
		if ans.Option != "" {
			opt = "**" + ans.Option + "** — "
		}
		w("Stance: %s%s", opt, orText(answer.Collapse(ans.Stance), "(empty)"))
		if ans.Confidence != nil {
			w("Confidence: %d", *ans.Confidence)
		}
		for _, x := range ans.Arguments {
			w("- %s", answer.Collapse(x))
		}
		if len(ans.Claims) > 0 {
			v, l, u := answer.Counts(ans.Claims)
			w("Evidence (%d verified, %d located, %d unverified):", v, l, u)
			for _, c := range ans.Claims {
				w("- %s%s", answer.Collapse(c.Text), citeMarks(c.Cites, " ✗ no citation"))
			}
		}
		if ans.Rebuttal != "" {
			moved := ""
			if ans.Moved != "" {
				moved = " (moved: " + ans.Moved + ")"
			}
			w("Response: %s%s", answer.Collapse(ans.Rebuttal), moved)
		}
	}
	if !anyArg {
		w("_None yet._")
	}
	w("\n## Questions to the lead")
	if len(m.LeadQA) == 0 && len(m.LeadNotes) == 0 {
		w("_None._")
	}
	for _, q := range m.LeadQA {
		w("- **%s** (%s, round %d): %s", q.ID, q.Label, q.Round, answer.Collapse(q.Question))
		switch {
		case q.AnsweredAt != "":
			w("  → %s", answer.Collapse(q.Answer))
		case q.Round < m.Round || m.Status != api.MeetingStatusWaitingLead:
			w("  → _(not answered)_") // the lead went on without answering
		default:
			w("  → _(not answered yet)_")
		}
	}
	for _, n := range m.LeadNotes {
		w("- Note for round %d: %s", n.Round, answer.Collapse(n.Text))
	}
	w("\n## Decision")
	if d := m.Decision; d != nil {
		w("%s", d.Text)
		if d.Option != "" {
			t := ""
			if ot := optionText(m, d.Option); ot != "" {
				t = " — " + ot
			}
			w("Option: **%s**%s", d.Option, t)
		}
		if d.Rationale != "" {
			w("Rationale: %s", d.Rationale)
		}
	} else {
		w("_None yet._")
	}
	w("\n## Formal dissent")
	if len(m.Dissents) == 0 {
		w("_None._")
	}
	for di := range m.Dissents {
		d := &m.Dissents[di]
		kind := "formal dissent"
		switch d.Kind {
		case api.DissentConcede:
			kind = "concedes"
		case api.DissentAbsent:
			kind = "absent (no answer to the decision)"
		}
		w("- **%s** · %s (%s) · %s: %s", d.ID, d.Label, d.Member, kind, orText(answer.Collapse(d.Text), "(empty)"))
		for _, c := range d.Claims {
			w("  - %s%s", answer.Collapse(c.Text), citeMarks(c.Cites, ""))
		}
		if len(d.WatchPaths) > 0 {
			w("  - Watching: `%s`", strings.Join(d.WatchPaths, "`, `"))
		}
		for _, s := range d.Signals {
			w("  - Signal: %s %s (%s)", s.TaskID, s.Reason, s.At)
		}
		if d.Outcome != "" {
			w("  - Outcome: %s (%s)", d.Outcome, d.OutcomeAt)
		}
	}
	w("\n## Advice not taken")
	if len(m.AdviceNotTaken) == 0 {
		w("_None._")
	}
	for _, x := range m.AdviceNotTaken {
		w("- %s (%s): %s", x.Label, x.Member, answer.Collapse(x.Stance))
	}
	var risks, actions []string
	for i := range m.Rounds {
		for j := range m.Rounds[i].Answers {
			if a := &m.Rounds[i].Answers[j]; !a.Absent {
				risks = append(risks, a.Risks...)
				actions = append(actions, a.Actions...)
			}
		}
	}
	w("\n## Risks")
	list(w, dedup(risks))
	w("\n## Proposed actions")
	list(w, dedup(actions))
	if len(m.Actions) > 0 {
		w("\nDelegated:")
		for _, x := range m.Actions {
			who := ""
			if x.Member != "" {
				who = " (" + x.Member + ")"
			}
			w("- %s%s: %s", x.TaskID, who, answer.Collapse(x.Text))
		}
	}
	w("\n## Cost")
	var per []string
	for _, k := range sortedKeys(m.Usage.ByBackend) {
		per = append(per, fmt.Sprintf("%s: %d", k, m.Usage.ByBackend[k]))
	}
	runs := fmt.Sprintf("- Runs: %d", m.Usage.Runs)
	if len(per) > 0 {
		runs += " (" + strings.Join(per, ", ") + ")"
	}
	w("%s", runs)
	if m.Estimate != nil {
		w("- Estimate at open: %d runs", m.Estimate.Runs)
	}
	w("- Tokens: %d", m.Usage.Tokens)
	return b.String()
}

func list(w func(string, ...any), items []string) {
	if len(items) == 0 {
		w("_None._")
	}
	for _, x := range items {
		w("- %s", answer.Collapse(x))
	}
}

func citeMarks(cites []api.Citation, none string) string {
	if len(cites) == 0 {
		return none
	}
	var out []string
	for _, c := range cites {
		mark := "✗ " + orText(c.State, "unchecked")
		switch c.State {
		case proof.CiteVerified:
			mark = "✓"
		case proof.CiteLocated:
			mark = "○"
		}
		out = append(out, answer.CiteText(c)+" "+mark)
	}
	return " " + strings.Join(out, " ")
}

func dedup(items []string) []string {
	var out, seen []string
	for _, x := range items {
		k := strings.ToLower(answer.Collapse(x))
		if k == "" || slices.Contains(seen, k) {
			continue
		}
		seen = append(seen, k)
		out = append(out, strings.TrimSpace(x))
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "\r\n", " ", "\n", " ").Replace(s)
}

func dash(s string) string { return orText(s, "—") }

func orText(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
