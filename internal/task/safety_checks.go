package task

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// Limits of the hygiene check and the scope warnings (old hygiene.js and worker-scope.js).
const (
	hygieneMaxBytes = 2 << 20
	// bomSample is how many tracked files of the same extension show whether the repository uses a BOM.
	bomSample = 20
	maxScope  = 20
	maxURLs   = 10
	// scopeEvents is how many of the task's latest events the URL check reads for tool parameters and output.
	scopeEvents = 4000
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// hygiene checks the files the turn changed. For those that were clean when the turn started: junk files, a BOM in
// a new file of a repository that uses none, and new files the report never mentions (old hygiene.js;
// test_scaffold is left out). For every changed file whose start is known: line endings and BOM against the styles
// the file may keep (styleRefs), so only what the turn did is judged.
func (s *Service) hygiene(ctx context.Context, root string, start, end snapshot.Manifest, changed []string,
	response string) []api.Finding {
	var out []api.Finding
	report := newMentions(response)
	for _, rel := range changed {
		b, inStart := start.Find(rel)
		dirty := inStart && b.Status != "" // already changed before the turn: those changes are not the worker's
		f, ok := end.Find(rel)
		if !ok || f.Missing {
			continue
		}
		isNew := f.Status == api.GitUntracked || f.Status == api.GitAdded
		if !dirty && isNew && snapshot.JunkName(rel) {
			out = append(out, api.Finding{Kind: api.FindingJunkFile, Severity: api.SeverityRed, Path: rel})
			continue
		}
		if f.Binary || !f.Stored || f.Size > hygieneMaxBytes || snapshot.Scratch(rel) {
			continue
		}
		data, err := s.o.Snapshots.Content(f)
		if err != nil {
			continue
		}
		if !dirty && isNew {
			if bytes.HasPrefix(data, utf8BOM) && s.repoWithoutBOM(ctx, root, start.Head, rel) {
				out = append(out, api.Finding{Kind: api.FindingBOMNewFile, Severity: api.SeverityRed, Path: rel})
				continue
			}
			if !report.names(rel) {
				out = append(out, api.Finding{Kind: api.FindingUnreported, Severity: api.SeverityWarn, Path: rel})
			}
			continue
		}
		var atStart []byte
		if inStart && !b.Missing && b.Stored && !b.Binary && b.Size <= hygieneMaxBytes {
			atStart, _ = s.o.Snapshots.Content(b)
		}
		if dirty && atStart == nil {
			continue // dirty before the turn, and its content then is unknown
		}
		refs := s.styleRefs(ctx, root, start.Head, rel, atStart)
		bom := bytes.HasPrefix(data, utf8BOM)
		if known, kept := refs.match(func(ref []byte) bool { return bytes.HasPrefix(ref, utf8BOM) == bom }); known &&
			!kept {
			out = append(out, api.Finding{Kind: api.FindingBOMChanged, Severity: api.SeverityRed, Path: rel})
		}
		if known, kept := refs.match(func(ref []byte) bool { return !eolChanged(ref, data) }); known && !kept &&
			!s.committedKeeps(ctx, root, rel, data, refs) {
			out = append(out, api.Finding{Kind: api.FindingEOLChanged, Severity: api.SeverityRed, Path: rel})
		}
	}
	return out
}

// mentions answers whether a report names a new file, directly or in grouped form ("`src/pages/`: AboutPage, ...").
type mentions struct {
	text string
	// dirs caches the answer per ancestor directory: the files of one folder share them.
	dirs map[string]bool
}

// newMentions prepares a report for matching: lower case, '/' separators.
func newMentions(response string) *mentions {
	return &mentions{text: strings.ReplaceAll(strings.ToLower(response), `\`, "/"), dirs: map[string]bool{}}
}

// names reports whether the report mentions rel (relative, '/' separators): its path, its base name, its stem (the
// base name without its last extension, at least 3 characters, as a whole word) or one of its folders written as a
// path ("src/pages/", or "src/pages" with two or more segments; a bare "src" does not count).
func (m *mentions) names(rel string) bool {
	rel = strings.ToLower(rel)
	base := path.Base(rel)
	if strings.Contains(m.text, rel) || strings.Contains(m.text, base) {
		return true
	}
	if stem := strings.TrimSuffix(base, path.Ext(base)); len(stem) >= 3 && m.word(stem, false) {
		return true
	}
	for dir := path.Dir(rel); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		said, ok := m.dirs[dir]
		if !ok {
			said = m.word(dir+"/", true) || strings.Contains(dir, "/") && m.word(dir, true)
			m.dirs[dir] = said
		}
		if said {
			return true
		}
	}
	return false
}

// word reports whether s appears in the report as a whole name: not continued after it by a word character, a '/'
// or an extension ("keycap/" is a folder, "vite.config.js" another file), and for a path (asPath) not continued
// before it by a word character, '/' or '.' ("other/src/" does not name "src/").
func (m *mentions) word(s string, asPath bool) bool {
	t := m.text
	for from := 0; ; {
		i := strings.Index(t[from:], s)
		if i < 0 {
			return false
		}
		i += from
		before := i == 0 || !wordByte(t[i-1]) && !(asPath && (t[i-1] == '/' || t[i-1] == '.'))
		end := i + len(s)
		after := end == len(t) || !wordByte(t[end]) && t[end] != '/' &&
			!(t[end] == '.' && end+1 < len(t) && wordByte(t[end+1]))
		if before && after {
			return true
		}
		from = i + 1
	}
}

// wordByte reports a byte that continues a word or a file name: letters, digits, '_' and '-' (and any byte of a
// non-ASCII character).
func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c >= 0x80
}

// committedKeeps reports whether git normalizes data on commit (every CRLF to LF: core.autocrlf true or input, the
// text and eol attributes) and that LF form keeps the style of a reference version: the working copy's line
// endings then never reach a commit, whatever mix of them the worker left.
func (s *Service) committedKeeps(ctx context.Context, root, rel string, data []byte, refs *fileRefs) bool {
	if norm, err := s.o.Repos.Normalizes(ctx, root, rel, data); err != nil || !norm {
		return false
	}
	lf := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	_, kept := refs.match(func(ref []byte) bool { return !eolChanged(ref, lf) })
	return kept
}

// fileRefs are the versions of one file whose line endings and BOM a turn may keep, read in order and only as far
// as a comparison needs.
type fileRefs struct {
	load     []func() ([]byte, bool)
	next     int
	versions [][]byte
}

// styleRefs returns the reference versions of rel: its content at the start of the turn (atStart, nil when the
// start snapshot did not keep it), then its blob at head as committed (no filters) and as a checkout writes it
// (core.autocrlf, the eol attributes, smudge filters such as LFS). The checkout form alone does not say what the
// worker changed: under core.autocrlf=true a file clean at the start may be LF or CRLF in the working tree (M18: an
// LF working copy was flagged against the CRLF checkout form, and two fix turns could not clear it).
func (s *Service) styleRefs(ctx context.Context, root, head, rel string, atStart []byte) *fileRefs {
	r := &fileRefs{}
	if atStart != nil {
		r.load = append(r.load, func() ([]byte, bool) { return atStart, true })
	}
	for _, filters := range []bool{false, true} {
		r.load = append(r.load, func() ([]byte, bool) {
			data, ok, err := s.o.Repos.Blob(ctx, root, head, rel, hygieneMaxBytes+1, filters)
			return data, err == nil && ok && len(data) <= hygieneMaxBytes
		})
	}
	return r
}

// match reports whether any reference version could be read (known) and whether one of them satisfies keeps.
func (r *fileRefs) match(keeps func(ref []byte) bool) (known, kept bool) {
	for i := 0; i < len(r.versions) || r.next < len(r.load); {
		if i == len(r.versions) {
			data, ok := r.load[r.next]()
			r.next++
			if ok {
				r.versions = append(r.versions, data)
			}
			continue
		}
		if keeps(r.versions[i]) {
			return true, true
		}
		i++
	}
	return len(r.versions) > 0, false
}

// lineBreaks counts CRLF and bare LF line breaks.
func lineBreaks(data []byte) (crlf, lf int) {
	for i, c := range data {
		if c == '\n' {
			if i > 0 && data[i-1] == '\r' {
				crlf++
			} else {
				lf++
			}
		}
	}
	return crlf, lf
}

// eolChanged reports a file whose line endings drifted from the style of a reference version (at least two line
// breaks there): more bare LF in a CRLF file, or more CRLF in an LF file.
func eolChanged(ref, now []byte) bool {
	hc, hl := lineBreaks(ref)
	if hc+hl < 2 {
		return false
	}
	nc, nl := lineBreaks(now)
	if hc*2 > hc+hl {
		return nl > hl
	}
	return nc > hc
}

// repoWithoutBOM reports whether the repository tracks files with rel's extension and none of the first bomSample
// starts with a BOM. A repository with no such file says nothing.
func (s *Service) repoWithoutBOM(ctx context.Context, root, head, rel string) bool {
	ext := path.Ext(rel)
	if ext == "" || head == "" {
		return false
	}
	files, err := s.o.Repos.Tracked(ctx, root, ext, bomSample)
	if err != nil || len(files) == 0 {
		return false
	}
	for _, f := range files {
		data, ok, err := s.o.Repos.Blob(ctx, root, head, f, len(utf8BOM), false)
		if err != nil {
			return false
		}
		if ok && bytes.HasPrefix(data, utf8BOM) {
			return false
		}
	}
	return true
}

// scope returns the warn-only scope findings of a turn: writes outside the task's folder, repository and the
// allowed folders (from the edit tools and the shell commands), and links in the report that nothing in the task
// fetched or was given (old worker-scope.js).
func (s *Service) scope(ctx context.Context, t *Task, root string, edited, commands []string,
	response string) []api.Finding {
	allowed := s.allowedKeys(t, root)
	var out []api.Finding
	seen := map[string]bool{}
	add := func(p, detail string) {
		if len(out) >= maxScope {
			return
		}
		k, ok := s.outside(t.Cwd, p, allowed)
		if !ok || seen[k] {
			return
		}
		seen[k] = true
		if r := []rune(detail); len(r) > 200 {
			detail = string(r[:200]) + "…"
		}
		out = append(out, api.Finding{Kind: api.FindingOutsideWrite, Severity: api.SeverityWarn, Path: p,
			Detail: detail})
	}
	for _, p := range edited {
		add(p, "edit tool")
	}
	for _, c := range commands {
		for _, p := range guard.Writes(c) {
			add(p, c)
		}
	}
	if urls := s.unfetchedURLs(ctx, t, commands, response); len(urls) > 0 {
		out = append(out, api.Finding{Kind: api.FindingUnfetchedURL, Severity: api.SeverityWarn,
			Detail: strings.Join(urls, " ")})
	}
	return out
}

// allowedKeys returns the keys of the folders a worker may write to.
func (s *Service) allowedKeys(t *Task, root string) []string {
	dirs := append([]string{t.Cwd, root, os.TempDir()}, s.o.ScopeAllowed...)
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if k, err := fsutil.Key(d); err == nil {
			out = append(out, strings.TrimSuffix(k, "/"))
		}
	}
	return out
}

// gitBashDrive matches a Git Bash drive path (/c/Users/...).
var gitBashDrive = regexp.MustCompile(`^/([A-Za-z])(/.*)?$`)

// outside reports whether p (absolute, or relative to cwd) lies outside every allowed folder, with its key.
func (s *Service) outside(cwd, p string, allowed []string) (string, bool) {
	if p == "" || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$") || strings.HasPrefix(p, "%") {
		return "", false // the worker's own home, or a variable
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/dev/") || strings.HasPrefix(slash, "/proc/") || slash == "/dev/null" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		if m := gitBashDrive.FindStringSubmatch(slash); m != nil {
			p = strings.ToUpper(m[1]) + ":" + filepath.FromSlash(m[2]+"/")
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	k, err := fsutil.Key(p)
	if err != nil {
		return "", false
	}
	k = strings.TrimSuffix(k, "/")
	for _, a := range allowed {
		if k == a || strings.HasPrefix(k, a+"/") {
			return k, false
		}
	}
	return k, true
}

// urlRe finds links in a report.
var urlRe = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `)\]]+`)

// unfetchedURLs returns the links of the report (at most maxURLs) that appear nowhere in the lead's messages, the
// commands or the tool calls of the task: links the worker may have made up.
func (s *Service) unfetchedURLs(ctx context.Context, t *Task, commands []string, response string) []string {
	found := urlRe.FindAllString(response, -1)
	if len(found) == 0 {
		return nil
	}
	var hay strings.Builder
	hay.WriteString(t.Prompt)
	for _, tu := range t.Turns {
		hay.WriteString("\n" + tu.Message)
		for _, c := range tu.Commands {
			hay.WriteString("\n" + c)
		}
	}
	for _, c := range commands {
		hay.WriteString("\n" + c)
	}
	if evs, err := s.repo.Tail(ctx, t.ID, scopeEvents); err == nil {
		for _, e := range evs {
			raw, ok := e.Data.(json.RawMessage)
			if e.Type != api.EventWorker || !ok {
				continue
			}
			var ev event.Event
			if json.Unmarshal(raw, &ev) != nil || ev.Tool == nil {
				continue
			}
			hay.WriteString("\n" + string(ev.Tool.Input) + "\n" + ev.Tool.Command + "\n" + ev.Tool.Output)
		}
	}
	text := strings.ToLower(hay.String())
	var out []string
	seen := map[string]bool{}
	for _, u := range found {
		u = strings.TrimRight(u, ".,;:!?)]}>'\"")
		norm, ok := normURL(u)
		if !ok || seen[norm] {
			continue
		}
		seen[norm] = true
		if strings.Contains(text, norm) {
			continue
		}
		out = append(out, u)
		if len(out) >= maxURLs {
			break
		}
	}
	return out
}

// normURL lower-cases a link and drops its fragment and trailing slash; ok is false for a local address.
func normURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasPrefix(host, "127.") ||
		host == "::1" || host == "0.0.0.0" {
		return "", false
	}
	u.Fragment = ""
	return strings.TrimSuffix(strings.ToLower(u.String()), "/"), true
}
