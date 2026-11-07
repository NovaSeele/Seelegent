package contest

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/answer"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// The judge compares the top three qualified variants, shown as anonymous letters A..C with their diffs, and
// answers with a winner, a ranking and reasons that cite lines of the variants' trees as [B path:12] "quote".
// Every citation is checked; the judge's winner is proposed only when it is qualified and at least one citation is
// verified.

const (
	letters      = "ABC"
	judgeDiffMax = 24000
	maxQuote     = 300
)

var (
	judgeCiteRe = regexp.MustCompile(`\[([A-E])\s+([^\]\s]+?):(\d+)(?:-(\d+))?\]`)
	joinerRe    = regexp.MustCompile(`(?i)^\s*(?:,|;|&|vs\.?|versus|and|or)?\s*$`)
	endRe       = regexp.MustCompile(`^\s*[.,;:)]?\s*$`)
	wordEndRe   = regexp.MustCompile(`^[.,;:)!?]?(?:\s|$)`)
	letterRe    = regexp.MustCompile(`(?:^|[^A-Za-z0-9])([A-E])(?:[^A-Za-z0-9]|$)`)
	winnerRe    = regexp.MustCompile(`(?i)^winner`)
	rankingRe   = regexp.MustCompile(`(?i)^ranking`)
	reasonsRe   = regexp.MustCompile(`(?i)^reasons?`)
	bulletRe    = regexp.MustCompile(`^\s*(?:[-*+•]|\d+[.)])\s+`)
)

// judgeCite is a citation of a judge's reason.
type judgeCite struct {
	Letter string
	Cite   proof.Cite
}

type judgeReason struct {
	Text  string
	Cites []judgeCite
}

type judgeAnswer struct {
	Winner  string
	Ranking []string
	Reasons []judgeReason
}

// splitCitations reads the citations of one reason: [A path:12] or [A path:12-14], each optionally followed by a
// "quote". A quote may itself contain double quotes; it ends at the last closing quote followed only by a joiner
// ("vs", "and", ",") before the next citation, or by the end; otherwise at the first closing quote that ends a word.
func splitCitations(src, allowed string) (cites []judgeCite, text string) {
	heads := judgeCiteRe.FindAllStringSubmatchIndex(src, -1)
	type span struct{ start, end int }
	spans := make([]span, 0, len(heads))
	for i, h := range heads {
		start, end := h[0], h[1]
		last := i+1 >= len(heads)
		limit := len(src)
		if !last {
			limit = heads[i+1][0]
		}
		quote := ""
		rest := src[end:limit]
		trimmed := strings.TrimLeft(rest, " \t")
		if strings.HasPrefix(trimmed, `"`) || strings.HasPrefix(trimmed, "“") {
			openLen := 1
			if strings.HasPrefix(trimmed, "“") {
				openLen = len("“")
			}
			qStart := end + (len(rest) - len(trimmed)) + openLen
			region := src[qStart:limit]
			var closers []int
			for j, r := range region {
				if (r == '"' || r == '”') && j > 0 {
					closers = append(closers, j)
				}
			}
			closeAt, closeLen := -1, 1
			for k := len(closers) - 1; k >= 0; k-- {
				c := closers[k]
				w := runeLen(region[c:])
				after := region[c+w:]
				if (last && endRe.MatchString(after)) || (!last && joinerRe.MatchString(after)) {
					closeAt, closeLen = c, w
					break
				}
			}
			if closeAt < 0 {
				for _, c := range closers {
					w := runeLen(region[c:])
					if wordEndRe.MatchString(region[c+w:]) {
						closeAt, closeLen = c, w
						break
					}
				}
			}
			if closeAt < 0 && len(closers) > 0 {
				closeAt, closeLen = closers[0], runeLen(region[closers[0]:])
			}
			if closeAt > 0 && closeAt <= maxQuote {
				quote = region[:closeAt]
				end = qStart + closeAt + closeLen
			}
		}
		letter := src[h[2]:h[3]]
		if strings.Contains(allowed, letter) {
			line, _ := strconv.Atoi(src[h[6]:h[7]])
			endLine := line
			if h[8] >= 0 {
				endLine, _ = strconv.Atoi(src[h[8]:h[9]])
			}
			cites = append(cites, judgeCite{Letter: letter, Cite: proof.Cite{Path: src[h[4]:h[5]], Line: line,
				EndLine: endLine, Quote: quote}})
		}
		spans = append(spans, span{start, end})
	}
	var b strings.Builder
	pos := 0
	for i, sp := range spans {
		gap := src[pos:sp.start]
		if i > 0 && joinerRe.MatchString(gap) {
			b.WriteString(" ")
		} else {
			b.WriteString(gap)
		}
		pos = sp.end
	}
	b.WriteString(src[pos:])
	text = answer.Collapse(b.String())
	text = strings.NewReplacer(" .", ".", " ,", ",", " ;", ";", " :", ":").Replace(text)
	return cites, strings.TrimSpace(text)
}

func runeLen(s string) int {
	for i := range s {
		if i > 0 {
			return i
		}
	}
	return len(s)
}

// parseJudge reads the judge's answer; allowed are the letters in play.
func parseJudge(text, allowed string) judgeAnswer {
	secs := answer.Sections(text)
	found := func(s string) []string {
		var out []string
		for _, m := range letterRe.FindAllStringSubmatch(s, -1) {
			if strings.Contains(allowed, m[1]) {
				out = append(out, m[1])
			}
		}
		return out
	}
	var a judgeAnswer
	if w, ok := answer.Pick(secs, winnerRe); ok {
		if l := found(w); len(l) > 0 {
			a.Winner = l[0]
		}
	}
	if r, ok := answer.Pick(secs, rankingRe); ok {
		// Overlapping letters ("B > A") need a second pass: the separators are shared by neighbors.
		for _, l := range found(strings.NewReplacer(">", " > ", ",", " , ").Replace(r)) {
			if !slices.Contains(a.Ranking, l) {
				a.Ranking = append(a.Ranking, l)
			}
		}
	}
	body, _ := answer.Pick(secs, reasonsRe)
	var raws []string
	cur := -1
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			cur = -1
			continue
		}
		if bulletRe.MatchString(line) || cur < 0 {
			raws = append(raws, strings.TrimSpace(bulletRe.ReplaceAllString(line, "")))
			cur = len(raws) - 1
			continue
		}
		raws[cur] += " " + t
	}
	for _, raw := range raws {
		cites, txt := splitCitations(raw, allowed)
		if txt == "" {
			txt = raw
		}
		if txt != "" || len(cites) > 0 {
			a.Reasons = append(a.Reasons, judgeReason{Text: txt, Cites: cites})
		}
	}
	return a
}

// judgeEntry is one variant shown to the judge.
type judgeEntry struct {
	Letter         string
	Tree           string
	Files          int
	Added, Removed int
	Gate           string
	Diff           string
}

func judgeBrief(prompt string, entries []judgeEntry) string {
	parts := []string{
		"You are the judge of a contest: several independent engineers implemented the same brief in separate " +
			"copies of the repository. Compare their changes and pick the best one: correct, complete, minimal, " +
			"consistent with the existing code, tested where the project has tests. You only see anonymous letters; " +
			"judge the code, not the style of the report.",
		answer.ReadOnlyRule + " Read files with the file tools to support your claims; always end with a text " +
			"answer in the format below.",
		"## The brief\n" + prompt,
	}
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("## Variant %s\nFull tree of this variant (read-only): %s\nChanged: %d "+
			"file(s), +%d -%d, gate: %s\n\n```diff\n%s\n```", e.Letter, e.Tree, e.Files, e.Added, e.Removed, e.Gate,
			e.Diff))
	}
	parts = append(parts, strings.Join([]string{
		"## Answer format (exactly these headings)", "## Winner", "B", "## Ranking", "B > A > C", "## Reasons",
		`- <reason> [B src/file.go:12-14] "exact quote from that line range in variant B"`, "",
		"Every reason must cite at least one line as [<letter> <repo-relative path>:<line>] or " +
			"[<letter> <path>:<line>-<line>], followed by a short exact quote in double quotes, taken from that " +
			"variant's tree. Citations are checked mechanically; reasons without a verified citation do not count.",
	}, "\n"))
	return strings.Join(parts, "\n\n")
}
