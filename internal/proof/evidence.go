package proof

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Limits of the evidence checker (old evidence.js).
const (
	evidenceMaxBytes = 2 << 20
	maxSpan          = 400
	quoteSlack       = 3
)

// Cite states.
const (
	CiteVerified      = "verified"
	CiteLocated       = "located"
	CiteQuoteMismatch = "quote_mismatch"
	CiteBadRange      = "bad_range"
	CiteMissingFile   = "missing_file"
	CiteOutsideRepo   = "outside_repo"
)

// Cite is a file:line citation, optionally with the quoted text expected there.
type Cite struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	EndLine int    `json:"endLine,omitempty"`
	Quote   string `json:"quote,omitempty"`
}

// CiteResult is a checked citation.
type CiteResult struct {
	Cite
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// CheckCite checks that a citation points at existing lines of a regular file inside the repository at root and,
// when it carries a quote, that the quote appears there (whitespace collapsed, a few lines of slack).
func CheckCite(root string, c Cite) CiteResult {
	r := CiteResult{Cite: c}
	if r.EndLine == 0 {
		r.EndLine = r.Line
	}
	p := strings.TrimSpace(strings.ReplaceAll(c.Path, `\`, "/"))
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	r.Path = p
	if p == "" {
		return r.with(CiteMissingFile, "no path")
	}
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	in, err := fsutil.Within(root, abs)
	if err != nil || !in {
		return r.with(CiteOutsideRepo, "the path is outside the repository")
	}
	if rel, err := filepath.Rel(root, abs); err == nil {
		r.Path = filepath.ToSlash(rel)
	}
	if c, err := fsutil.Canonical(abs); err == nil {
		if in, err := fsutil.Within(root, c); err != nil || !in {
			return r.with(CiteOutsideRepo, "the path leads outside the repository")
		}
	}
	st, err := os.Stat(abs)
	if err != nil {
		return r.with(CiteMissingFile, "file not found")
	}
	if !st.Mode().IsRegular() {
		return r.with(CiteMissingFile, "not a regular file")
	}
	if st.Size() > evidenceMaxBytes {
		return r.with(CiteBadRange, "file too large")
	}
	data, _, err := fsutil.ReadRegular(abs, evidenceMaxBytes)
	if err != nil {
		return r.with(CiteMissingFile, "cannot read the file")
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	switch n := len(lines); {
	case r.Line < 1:
		return r.with(CiteBadRange, "line < 1")
	case r.EndLine < r.Line:
		return r.with(CiteBadRange, "end line before start line")
	case r.EndLine > n:
		return r.with(CiteBadRange, fmt.Sprintf("file has %d lines", n))
	case r.EndLine-r.Line+1 > maxSpan:
		return r.with(CiteBadRange, fmt.Sprintf("span over %d lines", maxSpan))
	}
	q := collapse(c.Quote)
	if q == "" {
		return r.with(CiteLocated, fmt.Sprintf("lines %d-%d exist", r.Line, r.EndLine))
	}
	from, to := max(1, r.Line-quoteSlack), min(len(lines), r.EndLine+quoteSlack)
	if strings.Contains(collapse(strings.Join(lines[from-1:to], "\n")), q) {
		return r.with(CiteVerified, fmt.Sprintf("quote found in lines %d-%d", from, to))
	}
	return r.with(CiteQuoteMismatch, fmt.Sprintf("quote not found in lines %d-%d", from, to))
}

func (r CiteResult) with(state, detail string) CiteResult {
	r.State, r.Detail = state, detail
	return r
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// citeRe finds "path/to/file.ext:12" or ":12-20" citations in free text (verifier reasons).
var citeRe = regexp.MustCompile("(?:^|[\\s(`'\"])((?:[A-Za-z]:)?[A-Za-z0-9_.\\-/\\\\]*[A-Za-z0-9_\\-]\\.[A-Za-z0-9]+):(\\d+)(?:-(\\d+))?")

// FindCites returns the file:line citations in text, in order, without duplicates.
func FindCites(text string) []Cite {
	var out []Cite
	seen := map[string]bool{}
	for _, m := range citeRe.FindAllStringSubmatch(text, -1) {
		line, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		c := Cite{Path: m[1], Line: line}
		if m[3] != "" {
			if end, err := strconv.Atoi(m[3]); err == nil {
				c.EndLine = end
			}
		}
		k := fmt.Sprintf("%s:%d-%d", c.Path, c.Line, c.EndLine)
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

// Summary counts checked citations.
type Summary struct {
	Total      int `json:"total"`
	Verified   int `json:"verified"`
	Located    int `json:"located"`
	Unverified int `json:"unverified"`
}

// Summarize counts the citations by outcome.
func Summarize(rs []CiteResult) Summary {
	var s Summary
	for _, r := range rs {
		s.Total++
		switch r.State {
		case CiteVerified:
			s.Verified++
		case CiteLocated:
			s.Located++
		default:
			s.Unverified++
		}
	}
	return s
}
