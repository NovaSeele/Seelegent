package proof

import (
	"regexp"
	"strings"
)

// TaskLine is one task of a Spec Kit tasks.md: "- [ ] T012 [P] [US1] text Covers: FR-001 Gate: `cmd`".
type TaskLine struct {
	ID       string `json:"id"`
	Done     bool   `json:"done"`
	Parallel bool   `json:"parallel,omitempty"`
	Story    string `json:"story,omitempty"`
	Text     string `json:"text"`
	// Covers are the requirement (FR-nnn) and success criterion (SC-nnn) ids after "Covers:".
	Covers []string `json:"covers"`
	// Gates are the back-quoted commands after the last "Gate:".
	Gates []string `json:"gates"`
	// PathTokens are the back-quoted file paths and globs before Covers/Gate: the task's lane.
	PathTokens []string `json:"pathTokens"`
	// Line is the raw line, for the frozen bar.
	Line string `json:"-"`
}

// Tasks is a parsed tasks.md.
type Tasks struct {
	// FeatureGate are the commands of the "Feature gate:" line, run for every task of the feature.
	FeatureGate []string   `json:"featureGate"`
	Tasks       []TaskLine `json:"tasks"`
}

// Find returns the task with id, if any.
func (ts Tasks) Find(id string) (TaskLine, bool) {
	for i := range ts.Tasks {
		t := &ts.Tasks[i]
		if t.ID == id {
			return *t, true
		}
	}
	return TaskLine{}, false
}

var (
	taskLineRe    = regexp.MustCompile(`^\s*-\s*\[( |x|X)\]\s*(T\d+)\s*(\[P\])?\s*(\[(US\d+)\])?\s*(.*)$`)
	coversRe      = regexp.MustCompile(`\bCovers:\s*((?:FR|SC)-\d+(?:\s*,\s*(?:FR|SC)-\d+)*)`)
	gateWordRe    = regexp.MustCompile(`\bGate:`)
	backtickRe    = regexp.MustCompile("`([^`]+)`")
	pathTokenRe   = regexp.MustCompile(`^[A-Za-z0-9_.\-/*?]+$`)
	pathExtRe     = regexp.MustCompile(`\.[A-Za-z0-9]+$`)
	featureGateRe = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+)?(?:\*{1,2})?\s*Feature gate:\s*(.*)$`)
	titleRe       = regexp.MustCompile(`^#\s+(.*)$`)
	specPrefixRe  = regexp.MustCompile(`(?i)^Feature Specification:\s*`)
	storyRe       = regexp.MustCompile(`###\s+User Story\s+(\d+)\s*[-–:]\s*(.+?)\s*\(Priority:\s*(P\d)\)`)
	requirementRe = regexp.MustCompile(`(?m)^\s*-\s*\*\*((?:FR|SC)-\d+)\*\*:?\s*(.*)$`)
)

// insideBackticks reports whether byte offset i of s lies inside a back-quoted span.
func insideBackticks(s string, i int) bool {
	return strings.Count(s[:i], "`")%2 == 1
}

// ParseTaskLine parses one tasks.md line; ok is false for a line that is not a task.
func ParseTaskLine(line string) (TaskLine, bool) {
	m := taskLineRe.FindStringSubmatch(line)
	if m == nil {
		return TaskLine{}, false
	}
	t := TaskLine{ID: m[2], Done: m[1] != " ", Parallel: m[3] != "", Story: m[5], Text: strings.TrimSpace(m[6]),
		Covers: []string{}, Gates: []string{}, PathTokens: []string{}, Line: strings.TrimRight(line, "\r")}
	text := m[6]
	cut := len(text)
	for _, loc := range coversRe.FindAllStringSubmatchIndex(text, -1) {
		if insideBackticks(text, loc[0]) {
			continue
		}
		cut = loc[0]
		for _, id := range strings.Split(text[loc[2]:loc[3]], ",") {
			if id = strings.TrimSpace(id); id != "" {
				t.Covers = append(t.Covers, id)
			}
		}
		break
	}
	lastGate := -1
	for _, loc := range gateWordRe.FindAllStringIndex(text, -1) {
		if insideBackticks(text, loc[0]) {
			continue
		}
		cut = min(cut, loc[0])
		lastGate = loc[1]
	}
	if lastGate >= 0 {
		for _, g := range backtickRe.FindAllStringSubmatch(text[lastGate:], -1) {
			if g := strings.TrimSpace(g[1]); g != "" {
				t.Gates = append(t.Gates, g)
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range backtickRe.FindAllStringSubmatch(text[:cut], -1) {
		tok := p[1]
		if !pathTokenRe.MatchString(tok) || strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "/") {
			continue
		}
		if !strings.ContainsAny(tok, "/*") && !pathExtRe.MatchString(tok) {
			continue
		}
		if !seen[tok] {
			seen[tok] = true
			t.PathTokens = append(t.PathTokens, tok)
		}
	}
	return t, true
}

// ParseTasks parses a tasks.md: its tasks and the first "Feature gate:" line.
func ParseTasks(text string) Tasks {
	out := Tasks{FeatureGate: []string{}, Tasks: []TaskLine{}}
	found := false
	for _, line := range strings.Split(text, "\n") {
		if !found {
			if m := featureGateRe.FindStringSubmatch(strings.TrimRight(line, "\r")); m != nil {
				found = true
				for _, g := range backtickRe.FindAllStringSubmatch(m[1], -1) {
					if g := strings.TrimSpace(g[1]); g != "" {
						out.FeatureGate = append(out.FeatureGate, g)
					}
				}
			}
		}
		if t, ok := ParseTaskLine(line); ok {
			out.Tasks = append(out.Tasks, t)
		}
	}
	return out
}

// Story is a user story of a spec.md.
type Story struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
}

// Requirement is a functional requirement (FR) or success criterion (SC) of a spec.md.
type Requirement struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Spec is a parsed spec.md.
type Spec struct {
	Title        string        `json:"title"`
	Stories      []Story       `json:"stories"`
	Requirements []Requirement `json:"requirements"`
}

// ParseSpec parses a spec.md: the title, the user stories and the **FR-nnn** / **SC-nnn** items (first one wins).
func ParseSpec(text string) Spec {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	out := Spec{Stories: []Story{}, Requirements: []Requirement{}}
	for _, line := range strings.Split(text, "\n") {
		if m := titleRe.FindStringSubmatch(line); m != nil {
			out.Title = strings.TrimSpace(specPrefixRe.ReplaceAllString(m[1], ""))
			break
		}
	}
	for _, m := range storyRe.FindAllStringSubmatch(text, -1) {
		out.Stories = append(out.Stories, Story{ID: "US" + m[1], Title: strings.TrimSpace(m[2]), Priority: m[3]})
	}
	seen := map[string]bool{}
	var frs, scs []Requirement
	for _, m := range requirementRe.FindAllStringSubmatch(text, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		r := Requirement{ID: m[1], Kind: m[1][:2], Text: strings.TrimSpace(m[2])}
		if r.Kind == "FR" {
			frs = append(frs, r)
		} else {
			scs = append(scs, r)
		}
	}
	out.Requirements = append(append(out.Requirements, frs...), scs...)
	return out
}

// ResolveLane turns the path tokens of the tasks a worker implements into lane paths: tokens inside the feature
// folder (spec documents) are dropped, a bare name without a slash or wildcard counts only when that file exists,
// and a token naming a folder gets a trailing slash. stat reports "file", "dir" or "" for a repository-relative
// path; featureRel is the feature folder relative to the repository root ("" when it lies outside).
func ResolveLane(tokens []string, featureRel string, stat func(rel string) string) []string {
	feature := key(strings.TrimSuffix(featureRel, "/"))
	var out []string
	seen := map[string]bool{}
	for _, tok := range tokens {
		k := key(tok)
		if feature != "" && (k == feature || strings.HasPrefix(k, feature+"/")) {
			continue
		}
		if featureRel != "" && stat(strings.TrimSuffix(Clean(featureRel), "/")+"/"+Clean(tok)) != "" {
			continue // a spec document named relative to the feature folder
		}
		kind := stat(Clean(tok))
		if !strings.Contains(tok, "/") && !IsGlob(tok) && kind == "" {
			continue
		}
		p := Clean(tok)
		if kind == "dir" && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		if !seen[key(p)] {
			seen[key(p)] = true
			out = append(out, p)
		}
	}
	return out
}
