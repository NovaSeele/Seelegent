// Package proof holds the Spec Kit side of the proof gates (milestone 13, docs/gates.md): file lanes and their
// globs, the tasks.md and spec.md parsers, the doctrine file, the frozen bar, the evidence checker for file:line
// citations, the ledger of gate results per spec task and the requirement matrix built from it, and the
// regression runs that re-run every gate of a project.
package proof

import (
	"path"
	"regexp"
	"runtime"
	"strings"
)

// foldCase reports whether lane globs ignore letter case: on Windows and macOS, whose file systems do by default
// (old bug D13: the old version lower-cased on every OS).
func foldCase() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// Clean normalizes a repository-relative path or glob: forward slashes, no repeated slashes, no leading "./". A
// trailing slash (a directory lane) is kept.
func Clean(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// key is the comparison form of a cleaned path.
func key(p string) string {
	p = Clean(p)
	if foldCase() {
		p = strings.ToLower(p)
	}
	return p
}

// IsGlob reports whether a lane path is a glob: it holds * or ?, or ends with a slash (a folder). A plain path is not
// a glob, yet it still covers everything under it when it names a folder (Match).
func IsGlob(p string) bool {
	return strings.ContainsAny(p, "*?") || strings.HasSuffix(p, "/") || strings.HasSuffix(p, `\`)
}

// under reports whether the plain lane path entry covers file: the path itself or anything under it, so that the
// lane src/x covers src/x/a.vue (field report 4, F4: a folder named without its trailing slash matched one file, the
// gate called its seven components out of lane and the fix turn deleted them), but not src/xy.ts.
func under(entry, file string) bool {
	e, f := strings.TrimSuffix(key(entry), "/"), key(file)
	return e != "" && (f == e || strings.HasPrefix(f, e+"/"))
}

// globRegexp compiles a lane glob: ** crosses folders, * and ? stay inside one, a trailing slash covers the folder.
func globRegexp(pattern string) *regexp.Regexp {
	p := Clean(pattern)
	var b strings.Builder
	b.WriteString("^")
	if foldCase() {
		b.WriteString("(?i)")
	}
	for i := 0; i < len(p); {
		switch {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 3
		case p[i:] == "**":
			b.WriteString(".*")
			i += 2
		case i == len(p)-1 && p[i] == '/':
			b.WriteString("/.*")
			i++
		case p[i] == '*':
			b.WriteString("[^/]*")
			i++
		case p[i] == '?':
			b.WriteString("[^/]")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(p[i : i+1]))
			i++
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// Match reports whether the repository-relative file matches the lane path pattern: a glob, or else a plain path,
// which covers that path itself and everything under it (whether or not it names an existing folder).
func Match(pattern, file string) bool {
	if pattern == "" || file == "" {
		return false
	}
	if !IsGlob(pattern) {
		return under(pattern, file)
	}
	return globRegexp(pattern).MatchString(Clean(file))
}

// Contains reports whether one of the lane paths matches file.
func Contains(lane []string, file string) bool {
	for _, p := range lane {
		if Match(p, file) {
			return true
		}
	}
	return false
}

// Outside returns the files no lane path matches, in their order.
func Outside(lane, files []string) []string {
	var out []string
	for _, f := range files {
		if !Contains(lane, f) {
			out = append(out, f)
		}
	}
	return out
}

// literalPrefix is the part of a lane path before its first wildcard.
func literalPrefix(p string) string {
	p = key(p)
	if i := strings.IndexAny(p, "*?"); i >= 0 {
		return p[:i]
	}
	return p
}

// PathPair is one overlap between two lanes: a path of mine and one of theirs that can name the same file.
type PathPair struct {
	Mine   string `json:"mine"`
	Theirs string `json:"theirs"`
}

// globReaches reports whether the lane glob can match the plain lane path itself or a file under it, segment by
// segment: src/x/*.ts and src/*/a.ts reach src/x, src/*.ts does not reach src/x (its * stays in src/).
func globReaches(glob, plain string) bool {
	g := key(glob)
	if strings.HasSuffix(g, "/") {
		g += "**"
	}
	p := strings.TrimSuffix(key(plain), "/")
	if p == "" {
		return false
	}
	gs, ps := strings.Split(g, "/"), strings.Split(p, "/")
	for i, seg := range ps {
		if i >= len(gs) {
			return false // the glob ends above the plain path: it only names one of its folders
		}
		if gs[i] == "**" {
			return true
		}
		ok, err := path.Match(gs[i], seg)
		if err != nil {
			return true // a malformed segment (an unclosed [): cannot tell, so report the overlap
		}
		if !ok {
			return false
		}
	}
	return true // the glob names the plain path, or goes on below it
}

// Overlap returns every pair of lane paths, one from mine and one from theirs, that can name the same file. Two
// plain paths overlap when one is the other or lies under it (src/x and src/x/a.ts); a glob and a plain path when
// the glob can match the plain path or a file under it (globReaches). Two globs overlap when one's literal prefix
// starts with the other's (a cautious answer: src/*.go and src/*.ts are reported as overlapping).
func Overlap(mine, theirs []string) []PathPair {
	var out []PathPair
	for _, m := range mine {
		for _, t := range theirs {
			mg, tg := IsGlob(m), IsGlob(t)
			var hit bool
			switch {
			case !mg && !tg:
				hit = under(m, t) || under(t, m)
			case !mg:
				hit = globReaches(t, m)
			case !tg:
				hit = globReaches(m, t)
			default:
				pm, pt := literalPrefix(m), literalPrefix(t)
				hit = strings.HasPrefix(pm, pt) || strings.HasPrefix(pt, pm)
			}
			if hit {
				out = append(out, PathPair{Mine: m, Theirs: t})
			}
		}
	}
	return out
}

// NormalizeLane cleans the lane paths a lead passed: forward slashes, no duplicates, and a trailing slash on a plain
// path that names an existing folder, so lane ["src"] covers every file under src/ (old bug D13). isDir reports
// whether a repository-relative path is a folder. Absolute paths, paths that leave the repository and the root
// itself are refused with the offending path.
func NormalizeLane(paths []string, isDir func(rel string) bool) (out []string, bad string) {
	seen := map[string]bool{}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		c, ok := LanePath(p)
		if !ok {
			return nil, p
		}
		if !IsGlob(c) && isDir != nil && isDir(strings.TrimSuffix(c, "/")) && !strings.HasSuffix(c, "/") {
			c += "/"
		}
		if k := key(c); !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out, ""
}

// LanePath cleans one lane path or glob ("." and ".." segments resolved, so src/.. is the root and a/./b is a/b)
// and reports whether it may be a lane: relative, inside the repository and not the repository root itself. A
// trailing slash (a folder) is kept.
func LanePath(p string) (string, bool) {
	c := Clean(p)
	if c == "" || strings.HasPrefix(c, "/") || (len(c) > 1 && c[1] == ':') {
		return "", false
	}
	folder := strings.HasSuffix(c, "/")
	c = path.Clean(c)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return "", false
	}
	if folder {
		c += "/"
	}
	return c, true
}

// Ext returns the lower-case extension of a repository-relative path.
func Ext(p string) string { return strings.ToLower(path.Ext(Clean(p))) }
