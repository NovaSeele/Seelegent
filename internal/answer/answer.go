// Package answer reads the structured markdown answers of read-only member runs (consults, meeting rounds,
// dissents, contest judges) and checks the citations in them. It also holds the rules every such brief shares and
// the redactor that hides who said what from the other attendees.
//
// An answer is markdown with headings ("## Verdict", "## Claims", ...); a section is the text under a heading.
// Claims are bullets with citations "[path:12]" or "[path:12-20]", each optionally followed by a short exact quote
// in double quotes; CheckClaims checks every citation against the repository (proof.CheckCite).
package answer

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// ReadOnlyRule is the rule of every read-only member run. How the run can read files depends on the backend it runs
// on, so the task engine puts that first in a fresh conversation (brief.ReadOnlyRun), not this rule.
const ReadOnlyRule = "Do not edit, create or delete files. Read the code to support your claims, and always end with " +
	"a text answer in the format below."

// CitationRule is how claims about the code must cite it.
const CitationRule = "Every claim about the code must cite [path:line] or [path:line-line], path relative to the " +
	"repo root, optionally followed by a short exact quote in double quotes. Claims without valid citations are " +
	"marked unverified for everyone."

// Patterns of the answer format.
var (
	headingRe = regexp.MustCompile(`^\s{0,3}#{1,4}\s+(.+?)\s*#*\s*$`)
	bulletRe  = regexp.MustCompile(`^\s*(?:[-*+•]|\d+[.)])\s+`)
	citeRe    = regexp.MustCompile(`\[([^\]\s]+?):(\d+)(?:-(\d+))?\](?:\s*["“]([^"”]{1,300})["”])?`)
	noneRe    = regexp.MustCompile(`(?i)^(none|n/a|no|nothing|no questions?|không|không có)\.?$`)
	spacesRe  = regexp.MustCompile(`\s+`)
	beforeRe  = regexp.MustCompile(`\s+([.,;:])`)
)

// Section is the text under one heading; Title is lower case without markup.
type Section struct {
	Title string
	Body  string
}

// Sections splits text at its headings. Text before the first heading is dropped.
func Sections(text string) []Section {
	var out []Section
	var cur *Section
	var lines []string
	flush := func() {
		if cur != nil {
			cur.Body = strings.TrimSpace(strings.Join(lines, "\n"))
			out = append(out, *cur)
		}
	}
	for _, line := range splitLines(text) {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush()
			cur, lines = &Section{Title: normTitle(m[1])}, nil
			continue
		}
		if cur != nil {
			lines = append(lines, line)
		}
	}
	flush()
	return out
}

func normTitle(t string) string {
	t = strings.ToLower(t)
	t = strings.Map(func(r rune) rune {
		if r == '*' || r == '_' || r == '`' {
			return -1
		}
		return r
	}, t)
	return strings.TrimSpace(strings.TrimRight(t, ":."))
}

// Pick joins the bodies of the sections whose title matches re; ok is false when none does.
func Pick(secs []Section, re *regexp.Regexp) (body string, ok bool) {
	var parts []string
	for _, s := range secs {
		if re.MatchString(s.Title) {
			parts = append(parts, s.Body)
			ok = true
		}
	}
	return strings.Join(parts, "\n"), ok
}

// Bullets returns the items of a list; a line that is not a bullet continues the item before it, and a blank line
// ends an item.
func Bullets(body string) []string {
	var items []string
	open := false
	for _, raw := range splitLines(body) {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			open = false
		case bulletRe.MatchString(raw):
			items = append(items, strings.TrimSpace(bulletRe.ReplaceAllString(raw, "")))
			open = true
		case open:
			items[len(items)-1] += " " + line
		default:
			items = append(items, line)
			open = true
		}
	}
	out := items[:0]
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

// Questions are the bullets of a questions section without markup and without "none".
func Questions(body string) []string {
	var out []string
	for _, q := range Bullets(body) {
		q = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '*' || r == '_' || r == '`' {
				return -1
			}
			return r
		}, q))
		if q != "" && !noneRe.MatchString(q) {
			out = append(out, q)
		}
	}
	return out
}

// ExtractCites returns the citations in text and the text without them.
func ExtractCites(text string) (cites []proof.Cite, rest string) {
	for _, m := range citeRe.FindAllStringSubmatch(text, -1) {
		line, _ := strconv.Atoi(m[2])
		end := line
		if m[3] != "" {
			end, _ = strconv.Atoi(m[3])
		}
		cites = append(cites, proof.Cite{Path: m[1], Line: line, EndLine: end, Quote: m[4]})
	}
	rest = citeRe.ReplaceAllString(text, "")
	rest = strings.TrimSpace(beforeRe.ReplaceAllString(spacesRe.ReplaceAllString(rest, " "), "$1"))
	if rest == "" {
		rest = strings.TrimSpace(text)
	}
	return cites, rest
}

// RawClaim is a claim before its citations are checked.
type RawClaim struct {
	Text  string
	Cites []proof.Cite
}

// Claims reads the bullets of a claims (or findings) section.
func Claims(body string) []RawClaim {
	var out []RawClaim
	for _, b := range Bullets(body) {
		cites, text := ExtractCites(b)
		if text != "" || len(cites) > 0 {
			out = append(out, RawClaim{Text: text, Cites: cites})
		}
	}
	return out
}

// CheckClaims checks every citation of claims against the repository at root.
func CheckClaims(root string, claims []RawClaim) []api.Claim {
	out := make([]api.Claim, 0, len(claims))
	for _, c := range claims {
		ac := api.Claim{Text: c.Text, Cites: make([]api.Citation, 0, len(c.Cites))}
		for _, ci := range c.Cites {
			ac.Cites = append(ac.Cites, Checked(root, ci, ""))
		}
		out = append(out, ac)
	}
	return out
}

// Checked checks one citation against root; variant labels a judge's citation.
func Checked(root string, c proof.Cite, variant string) api.Citation {
	r := proof.CheckCite(root, c)
	return api.Citation{Path: r.Path, Line: r.Line, EndLine: r.EndLine, Quote: c.Quote, State: r.State,
		Detail: r.Detail, Variant: variant}
}

// Counts counts the citations of claims by outcome: verified, located (the lines exist) and the rest.
func Counts(claims []api.Claim) (verified, located, unverified int) {
	for _, c := range claims {
		for _, ci := range c.Cites {
			switch ci.State {
			case proof.CiteVerified:
				verified++
			case proof.CiteLocated:
				located++
			default:
				unverified++
			}
		}
	}
	return verified, located, unverified
}

// CiteText writes a citation the way members write them.
func CiteText(c api.Citation) string {
	r := strconv.Itoa(c.Line)
	if c.EndLine != 0 && c.EndLine != c.Line {
		r += "-" + strconv.Itoa(c.EndLine)
	}
	s := "[" + c.Path + ":" + r + "]"
	if c.Quote != "" {
		s += ` "` + c.Quote + `"`
	}
	return s
}

// CiteTag is the mark another attendee sees next to a citation.
func CiteTag(c api.Citation) string {
	switch c.State {
	case proof.CiteVerified:
		return "(verified)"
	case proof.CiteLocated:
		return "(located)"
	case "":
		return "(UNVERIFIED: unchecked)"
	}
	return "(UNVERIFIED: " + c.State + ")"
}

// Clip cuts s to n characters (runes) with an ellipsis.
func Clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// Collapse joins s into one line.
func Collapse(s string) string { return strings.TrimSpace(spacesRe.ReplaceAllString(s, " ")) }

func splitLines(s string) []string { return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") }

// BrandWords are vendor and model names that would tell attendees who wrote an answer.
func BrandWords() []string {
	return []string{"Antigravity", "Anthropic", "OpenAI", "Gemini", "Google", "Claude", "Codex", "GPT"}
}

// ordinary are words a member id, name, model or tier may be made of that also occur in normal text; they are
// never redacted on their own (old bug D20: a member called "api" or a tier "max" blanked ordinary words).
func ordinary() []string {
	return []string{"agy", "api", "fast", "standard", "strong", "max", "high", "low", "medium", "the", "and", "for",
		"dev", "test", "lead", "peer", "senior", "worker", "review", "reviewer", "default", "model", "member", "pro",
		"mini", "flash", "lite", "main", "code", "coder", "auto", "new", "old", "one", "two", "app", "web", "ops",
		"data", "core", "team", "user", "bot", "agent", "expert", "fix", "build", "plan", "edit", "chat"}
}

// Identity is what may identify an attendee.
type Identity struct {
	Member, Name, Backend, Model, Provider, Tier string
}

// Redactor returns a function that hides identity hints in text one attendee wrote before another sees it: brand
// words always, and the members' ids, names, models and providers as whole words when they are distinctive (at
// least 3 characters and not an ordinary word). Backends and tiers are not redacted: they are ordinary words.
func Redactor(ids []Identity) func(string) string {
	var words []string
	for _, a := range ids {
		for _, v := range []string{a.Member, a.Name, a.Model, a.Provider} {
			v = strings.TrimSpace(v)
			if utf8.RuneCountInString(v) < 3 || slices.Contains(ordinary(), strings.ToLower(v)) {
				continue
			}
			if !slices.Contains(words, v) {
				words = append(words, v)
			}
		}
	}
	slices.SortFunc(words, func(a, b string) int { return len(b) - len(a) })
	var bound *regexp.Regexp
	if len(words) > 0 {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = regexp.QuoteMeta(w)
		}
		bound = regexp.MustCompile(`(?i)(^|[^A-Za-z0-9])(` + strings.Join(quoted, "|") + `)([^A-Za-z0-9]|$)`)
	}
	brands := make([]string, 0, len(BrandWords()))
	for _, b := range BrandWords() {
		brands = append(brands, regexp.QuoteMeta(b))
	}
	brand := regexp.MustCompile(`(?i)` + strings.Join(brands, "|"))
	return func(s string) string {
		if bound != nil {
			// Twice: adjacent matches share the separator between them.
			for range 2 {
				s = bound.ReplaceAllString(s, "${1}[redacted]${3}")
			}
		}
		return brand.ReplaceAllString(s, "[redacted]")
	}
}
