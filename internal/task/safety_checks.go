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

// hygiene checks the files the turn changed that were clean when it started: line endings and BOM against HEAD,
// junk files, a BOM in a new file of a repository that uses none, and new files the report never mentions
// (old hygiene.js; test_scaffold is left out).
func (s *Service) hygiene(ctx context.Context, root string, start, end snapshot.Manifest, changed []string,
	response string) []api.Finding {
	var out []api.Finding
	report := strings.ToLower(response)
	for _, rel := range changed {
		if b, ok := start.Find(rel); ok && b.Status != "" {
			continue // already changed before the turn: not the worker's doing
		}
		f, ok := end.Find(rel)
		if !ok || f.Missing {
			continue
		}
		isNew := f.Status == api.GitUntracked || f.Status == api.GitAdded
		if isNew && snapshot.JunkName(rel) {
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
		if isNew {
			if bytes.HasPrefix(data, utf8BOM) && s.repoWithoutBOM(ctx, root, start.Head, rel) {
				out = append(out, api.Finding{Kind: api.FindingBOMNewFile, Severity: api.SeverityRed, Path: rel})
				continue
			}
			if !strings.Contains(report, strings.ToLower(rel)) &&
				!strings.Contains(report, strings.ToLower(path.Base(rel))) {
				out = append(out, api.Finding{Kind: api.FindingUnreported, Severity: api.SeverityWarn, Path: rel})
			}
			continue
		}
		head, ok, err := s.o.Repos.Blob(ctx, root, start.Head, rel, hygieneMaxBytes+1, true)
		if err != nil || !ok || len(head) > hygieneMaxBytes {
			continue
		}
		if bytes.HasPrefix(head, utf8BOM) != bytes.HasPrefix(data, utf8BOM) {
			out = append(out, api.Finding{Kind: api.FindingBOMChanged, Severity: api.SeverityRed, Path: rel})
		}
		if eolChanged(head, data) {
			out = append(out, api.Finding{Kind: api.FindingEOLChanged, Severity: api.SeverityRed, Path: rel})
		}
	}
	return out
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

// eolChanged reports a file whose line endings drifted from the style it has at HEAD (at least two line breaks
// there): more bare LF in a CRLF file, or more CRLF in an LF file.
func eolChanged(head, now []byte) bool {
	hc, hl := lineBreaks(head)
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
