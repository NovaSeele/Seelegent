package meeting

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/api"
)

// The answer format of meeting runs and the briefs that ask for it (docs/team.md).

var (
	optionLineRe = regexp.MustCompile(`(?i)^\s*[*_]*\s*option\s*[*_]*\s*[:\-–]\s*[*_]*\s*\(?([A-Za-z])\b`)
	confNumRe    = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
	confInlineRe = regexp.MustCompile(`(?i)confidence\s*[:=]\s*(\d{1,3})`)
	movedRe      = regexp.MustCompile(`(?i)moved\s*[*_]*\s*[:\-–]\s*[*_]*\s*(yes|no)\b`)
	opposingRe   = regexp.MustCompile(`(?is)strongest opposing argument\s*[*_]*\s*:\s*(.*?)(?:\n\s*[*_]*\s*moved\s*[*_]*\s*[:\-–]|$)`)
	movedLineRe  = regexp.MustCompile(`(?im)^.*moved\s*[:\-–].*$`)
	dissentRe    = regexp.MustCompile(`(?i)\b(dissent|concede)`)

	stanceRe    = regexp.MustCompile(`^(stance|position)`)
	confRe      = regexp.MustCompile(`^confidence`)
	argsRe      = regexp.MustCompile(`^arguments?`)
	claimsRe    = regexp.MustCompile(`^(claims?|evidence)`)
	risksRe     = regexp.MustCompile(`^risks?`)
	actionsRe   = regexp.MustCompile(`^(proposed actions?|actions?|next steps?)`)
	questionsRe = regexp.MustCompile(`^questions?`)
	responseRe  = regexp.MustCompile(`^(response|rebuttal)`)
	positionRe  = regexp.MustCompile(`^position`)
	statementRe = regexp.MustCompile(`^(statement|dissent|reason)`)
)

// Text limits of parsed answers.
const (
	maxStance    = 4000
	maxStatement = 6000
	maxQuestion  = 2000
)

// rawAnswer is an answer before its citations are checked.
type rawAnswer struct {
	api.MeetingAnswer
	claims []answer.RawClaim
}

// parseAnswer reads a round answer. It never fails: a reply without the format becomes the stance.
func parseAnswer(text string, keys []string) rawAnswer {
	a := rawAnswer{MeetingAnswer: api.MeetingAnswer{Arguments: []string{}, Claims: []api.Claim{}, Risks: []string{},
		Actions: []string{}, Questions: []string{}}}
	secs := answer.Sections(text)
	stance, hasStance := answer.Pick(secs, stanceRe)
	findOption := func(body string) (letter, line string) {
		for _, l := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
			if m := optionLineRe.FindStringSubmatch(l); m != nil {
				return strings.ToUpper(m[1]), l
			}
		}
		return "", ""
	}
	letter, line := "", ""
	if hasStance {
		letter, line = findOption(stance)
	}
	if letter == "" {
		letter, line = findOption(text)
	}
	if slices.Contains(keys, letter) {
		a.Option = letter
	}
	switch {
	case hasStance:
		var keep []string
		for _, l := range strings.Split(stance, "\n") {
			if line == "" || l != line {
				keep = append(keep, l)
			}
		}
		a.Stance = answer.Clip(strings.TrimSpace(strings.Join(keep, "\n")), maxStance)
	case len(secs) == 0:
		a.Stance = answer.Clip(strings.TrimSpace(text), maxStance)
	}
	conf, ok := answer.Pick(secs, confRe)
	if !ok {
		if m := confInlineRe.FindStringSubmatch(text); m != nil {
			conf, ok = m[1], true
		}
	}
	if ok {
		if m := confNumRe.FindString(conf); m != "" {
			if f, err := strconv.ParseFloat(m, 64); err == nil {
				c := min(100, max(0, int(f+0.5)))
				a.Confidence = &c
			}
		}
	}
	if b, ok := answer.Pick(secs, argsRe); ok {
		a.Arguments = answer.Bullets(b)
	}
	if b, ok := answer.Pick(secs, claimsRe); ok {
		a.claims = answer.Claims(b)
	}
	if b, ok := answer.Pick(secs, risksRe); ok {
		a.Risks = answer.Bullets(b)
	}
	if b, ok := answer.Pick(secs, actionsRe); ok {
		a.Actions = answer.Bullets(b)
	}
	if b, ok := answer.Pick(secs, questionsRe); ok {
		for _, q := range answer.Questions(b) {
			a.Questions = append(a.Questions, answer.Clip(q, maxQuestion))
		}
	}
	resp, hasResp := answer.Pick(secs, responseRe)
	src := text
	if hasResp {
		src = resp
	}
	if m := movedRe.FindStringSubmatch(src); m != nil {
		a.Moved = strings.ToLower(m[1])
	}
	if hasResp {
		r := movedLineRe.ReplaceAllString(resp, "")
		if m := opposingRe.FindStringSubmatch(resp); m != nil {
			r = m[1]
		}
		// "Moved: no" written on the same line as the argument is not part of it (the minutes add it once).
		if loc := movedRe.FindStringIndex(r); loc != nil {
			before := strings.TrimRight(r[:loc[0]], " \t")
			after := strings.TrimLeft(r[loc[1]:], " \t-–:,.")
			r = strings.TrimSpace(before + " " + after)
		}
		a.Rebuttal = answer.Clip(strings.TrimSpace(r), maxStance)
	}
	return a
}

// parsedDissent is a peer's answer to the decision.
type parsedDissent struct {
	kind   string
	text   string
	claims []answer.RawClaim
}

// parseDissent reads a dissent answer; a reply without a clear concession is a dissent.
func parseDissent(text string) parsedDissent {
	secs := answer.Sections(text)
	src := text
	if p, ok := answer.Pick(secs, positionRe); ok {
		src = p
	}
	d := parsedDissent{kind: "dissent"}
	if m := dissentRe.FindStringSubmatch(src); len(m) > 1 && strings.EqualFold(m[1], "concede") {
		d.kind = "concede"
	}
	if st, ok := answer.Pick(secs, statementRe); ok {
		d.text = st
	} else if len(secs) == 0 {
		d.text = text
	}
	d.text = answer.Clip(strings.TrimSpace(d.text), maxStatement)
	if b, ok := answer.Pick(secs, claimsRe); ok {
		d.claims = answer.Claims(b)
	}
	return d
}

func roleLine(rank string) string {
	switch rank {
	case api.RankPeer:
		return "You are a peer of the project lead in a structured design meeting. You stand level with the lead: " +
			"argue for what you believe is right, challenge weak arguments, and say so plainly when you disagree."
	case api.RankSenior:
		return "You are an expert adviser in a structured design meeting run by the project lead. Give your best " +
			"expert opinion, grounded in the code."
	}
	return "You are a witness in a structured design meeting run by the project lead: explain the facts of your own " +
		"area of the code, accurately and with citations."
}

func responseFormat(hasOptions bool) string {
	lines := []string{"## Stance"}
	if hasOptions {
		lines = append(lines, "Option: B")
	}
	lines = append(lines, "<one paragraph>", "## Confidence", "75", "## Arguments", "- ...", "## Claims",
		`- <claim> [src/tasks.go:120-128] "exact quote"`, "## Risks", "- ...", "## Proposed actions", "- ...",
		"## Questions for the lead", "- none", "## Response to others",
		"Strongest opposing argument: <label and argument>", "Moved: yes|no - <why>")
	return strings.Join(lines, "\n")
}

func optionList(m *api.Meeting) string {
	var b strings.Builder
	for i, o := range m.Options {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(o.Key + ". " + o.Text)
	}
	return b.String()
}

func optionText(m *api.Meeting, key string) string {
	for _, o := range m.Options {
		if o.Key == key {
			return o.Text
		}
	}
	return ""
}

// roundBrief is the first message of an attendee's run.
func roundBrief(m *api.Meeting, a *api.Attendee) string {
	has := len(m.Options) > 0
	parts := []string{roleLine(a.Rank),
		"You are " + a.Label + ". Every participant is identified only by a neutral label (P1, P2, …); never try " +
			"to identify the others.",
		answer.ReadOnlyRule, "## Question\n" + m.Question}
	if has {
		parts = append(parts, "## Options\n"+optionList(m)+
			"\nYour stance must name exactly one option on the \"Option:\" line.")
	}
	if len(m.Context) > 0 {
		parts = append(parts, "## Read these files first\n- "+strings.Join(m.Context, "\n- "))
	}
	wd := "Working directory: " + m.Cwd
	if m.RepoRoot != "" && m.RepoRoot != m.Cwd {
		wd += "\nRepo root (citation paths are relative to it): " + m.RepoRoot
	}
	parts = append(parts, wd, "## Citations\n"+answer.CitationRule,
		fmt.Sprintf("This is round 1 of up to %d. Answer alone: from round 2 you will see the other positions and "+
			"must answer the strongest of them. In round 1 write \"none\" under \"Response to others\". Ask the lead "+
			"only questions whose answer would change your stance; otherwise write \"none\".", m.MaxRounds))
	format := "## Response format\nReply in exactly this markdown format"
	if !has {
		format += " (there are no options, so leave out the Option line)"
	}
	parts = append(parts, format+":\n\n"+responseFormat(has))
	return strings.Join(parts, "\n\n")
}

// answerOf returns the answer of label in round r.
func answerOf(r *api.MeetingRound, label string) *api.MeetingAnswer {
	for i := range r.Answers {
		if r.Answers[i].Label == label {
			return &r.Answers[i]
		}
	}
	return nil
}

// lastAnswer is label's latest answer before round `before` (1-based), with its round.
func lastAnswer(m *api.Meeting, label string, before int) (a *api.MeetingAnswer, round int) {
	for i := min(before-1, len(m.Rounds)) - 1; i >= 0; i-- {
		if a := answerOf(&m.Rounds[i], label); a != nil && !a.Absent {
			return a, m.Rounds[i].N
		}
	}
	return nil, 0
}

func confText(c *int) string {
	if c == nil {
		return "(not given)"
	}
	return strconv.Itoa(*c)
}

func renderClaims(claims []api.Claim, redact func(string) string) []string {
	var out []string
	for i, c := range claims {
		if i == 15 {
			break
		}
		var cites []string
		for _, ci := range c.Cites {
			cites = append(cites, answer.CiteText(ci)+" "+answer.CiteTag(ci))
		}
		line := "- " + answer.Clip(redact(c.Text), 800)
		if len(cites) > 0 {
			line += " " + strings.Join(cites, " ")
		} else {
			line += " (UNVERIFIED: no citation)"
		}
		out = append(out, line)
	}
	return out
}

func renderPosition(m *api.Meeting, a *api.MeetingAnswer, redact func(string) string) string {
	var head []string
	if len(m.Options) > 0 {
		opt := a.Option
		if opt == "" {
			opt = "(none)"
		}
		head = append(head, "Option: "+opt)
	}
	head = append(head, "Confidence: "+confText(a.Confidence))
	lines := []string{strings.Join(head, " · ")}
	stance := answer.Clip(redact(a.Stance), 3000)
	if stance == "" {
		stance = "(empty)"
	}
	lines = append(lines, "Stance: "+stance)
	if len(a.Arguments) > 0 {
		lines = append(lines, "Arguments:")
		for i, x := range a.Arguments {
			if i == 12 {
				break
			}
			lines = append(lines, "- "+answer.Clip(redact(x), 1000))
		}
	}
	if len(a.Claims) > 0 {
		lines = append(lines, "Claims:")
		lines = append(lines, renderClaims(a.Claims, redact)...)
	}
	if a.Rebuttal != "" {
		r := "Response to others: " + answer.Clip(redact(a.Rebuttal), 1500)
		if a.Moved != "" {
			r += " (moved: " + a.Moved + ")"
		}
		lines = append(lines, r)
	}
	return strings.Join(lines, "\n")
}

// redactorOf hides who the attendees are from each other.
func redactorOf(m *api.Meeting) func(string) string {
	ids := make([]answer.Identity, 0, len(m.Attendees))
	for i := range m.Attendees {
		a := &m.Attendees[i]
		ids = append(ids, answer.Identity{Member: a.Member, Name: a.Name, Backend: a.Backend, Model: a.Model,
			Provider: a.Provider, Tier: a.Tier})
	}
	return answer.Redactor(ids)
}

// roundMessage is the followup that opens round n for attendee a: the other positions, the lead's answers and
// notes.
func roundMessage(m *api.Meeting, a *api.Attendee, n int) string {
	redact := redactorOf(m)
	has := len(m.Options) > 0
	parts := []string{fmt.Sprintf("Round %d of %d. You are %s.", n, m.MaxRounds, a.Label)}
	if mine, r := lastAnswer(m, a.Label, n); mine != nil {
		opt := ""
		if has {
			o := mine.Option
			if o == "" {
				o = "(none)"
			}
			opt = "Option " + o + ", "
		}
		parts = append(parts, fmt.Sprintf("Your position in round %d: %sconfidence %s. %s", r, opt,
			confText(mine.Confidence), answer.Clip(redact(mine.Stance), 1500)))
	}
	q := "## Question\n" + m.Question
	if has {
		q += "\n" + optionList(m)
	}
	parts = append(parts, q)
	var labels []string
	for i := range m.Attendees {
		if l := m.Attendees[i].Label; l != a.Label {
			labels = append(labels, l)
		}
	}
	if m.Sealed != nil {
		labels = append(labels, m.Sealed.Label)
	}
	slices.SortFunc(labels, func(x, y string) int { return labelNum(x) - labelNum(y) })
	var others []string
	for _, l := range labels {
		if m.Sealed != nil && l == m.Sealed.Label {
			if n >= max(m.Sealed.RevealedRound, 2) {
				others = append(others, "### "+l+"\nStance: "+answer.Clip(redact(m.Sealed.Text), 4000))
			}
			continue
		}
		prev, r := lastAnswer(m, l, n)
		if prev == nil {
			others = append(others, "### "+l+"\n(no answer yet)")
			continue
		}
		note := ""
		if r != n-1 {
			note = fmt.Sprintf(" (last answered in round %d)", r)
		}
		others = append(others, "### "+l+note+"\n"+renderPosition(m, prev, redact))
	}
	parts = append(parts, "## The other positions\nCitation marks: (verified) the quote was found at that place; "+
		"(located) the lines exist; (UNVERIFIED: …) the citation failed the check.\n\n"+strings.Join(others, "\n\n"))
	var qa []string
	for _, x := range m.LeadQA {
		if x.Round == n-1 && x.AnsweredAt != "" {
			qa = append(qa, fmt.Sprintf("Q (%s): %s\nA: %s", x.Label, answer.Clip(redact(x.Question), 1500), x.Answer))
		}
	}
	if len(qa) > 0 {
		parts = append(parts, "## Answers from the lead\n"+strings.Join(qa, "\n\n"))
	}
	var notes []string
	for _, x := range m.LeadNotes {
		if x.Round == n {
			notes = append(notes, x.Text)
		}
	}
	if len(notes) > 0 {
		parts = append(parts, "## Note from the lead\n"+strings.Join(notes, "\n\n"))
	}
	parts = append(parts, "## Your task this round\nRe-examine your position against the others. "+answer.ReadOnlyRule+
		" Under \"Response to others\" name the strongest opposing argument (by label) and say whether it moved you. "+
		"Change your stance or confidence only when the arguments justify it.\n"+answer.CitationRule,
		"## Response format\n"+responseFormat(has))
	return strings.Join(parts, "\n\n")
}

// dissentMessage asks a peer whose final position differs from the decision to dissent or concede.
func dissentMessage(m *api.Meeting, final *api.MeetingAnswer) string {
	d := m.Decision
	lines := []string{"The lead has decided.", "Decision: " + d.Text}
	if d.Option != "" {
		o := "Chosen option: " + d.Option
		if t := optionText(m, d.Option); t != "" {
			o += " - " + t
		}
		lines = append(lines, o)
	}
	if d.Rationale != "" {
		lines = append(lines, "Rationale: "+d.Rationale)
	}
	if final != nil {
		f := "Your final position: "
		if final.Option != "" {
			f += "Option " + final.Option + ". "
		}
		lines = append(lines, f+answer.Clip(final.Stance, 1500))
	}
	lines = append(lines, "", "As a peer you may record a formal dissent or concede. A dissent stays on the record, "+
		"is quoted in the brief of every task created from this decision, and watches the files it cites: later "+
		"failures there are flagged as possibly vindicating it. Concede if the decision is acceptable to you.",
		answer.ReadOnlyRule, answer.CitationRule, "",
		"Reply with:\n## Position\nDissent or Concede\n## Statement\n<one paragraph>\n## Claims\n"+
			`- <claim> [path:line] "exact quote"`)
	return strings.Join(lines, "\n")
}

// actionPrompt is the brief of a task created from a decision: the item, the decision and the dissent on record.
func actionPrompt(m *api.Meeting, text string) string {
	d := m.Decision
	parts := []string{strings.TrimSpace(text)}
	dec := []string{"## Decision from meeting " + m.ID + ": " + m.Title, d.Text}
	if d.Option != "" {
		o := "Chosen option: " + d.Option
		if t := optionText(m, d.Option); t != "" {
			o += " - " + t
		}
		dec = append(dec, o)
	}
	if d.Rationale != "" {
		dec = append(dec, "Rationale: "+d.Rationale)
	}
	parts = append(parts, strings.Join(dec, "\n"))
	var ds []string
	for i := range m.Dissents {
		if x := &m.Dissents[i]; x.Kind == "dissent" {
			ds = append(ds, "- "+x.Label+": "+answer.Clip(x.Text, 3000))
		}
	}
	if len(ds) > 0 {
		parts = append(parts, "## Dissent on record\nFormal dissent was recorded against this decision. Keep it in "+
			"mind; if your work shows it was right, say so in your report.\n"+strings.Join(ds, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func labelNum(label string) int {
	n, _ := strconv.Atoi(strings.TrimLeft(label, "PV"))
	return n
}
