package migrate

import (
	"context"
	"crypto/sha1" //nolint:gosec // the old version's project key formula, not a security use
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/role"
)

// oldKeyJunk is what the old project key replaced in a folder name.
var oldKeyJunk = regexp.MustCompile(`[^a-z0-9._-]+`)

// OldProjectKey is the project key of the old version for cwd (bug F6): the lower-cased absolute path's base name
// with every run of other characters than a-z, 0-9, '.', '_' and '-' replaced by '-', then '-' and the first 8 hex
// digits of the SHA-1 of the whole lower-cased path. It keyed memory/<key>.md, <key>.history.md and
// <key>.roles.json.
func OldProjectKey(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = filepath.Clean(cwd)
	}
	norm := strings.ToLower(abs)
	base := filepath.Base(norm)
	if base == string(filepath.Separator) || base == "." || strings.HasSuffix(base, ":") ||
		strings.HasSuffix(norm, string(filepath.Separator)) {
		base = "" // a drive or file system root: Node's path.basename gives ""
	}
	sum := sha1.Sum([]byte(norm)) //nolint:gosec // see the import
	return oldKeyJunk.ReplaceAllString(base, "-") + "-" + hex.EncodeToString(sum[:])[:8]
}

// project is an old project key with the folder it belongs to.
type project struct {
	key, cwd string
}

var cwdHeader = regexp.MustCompile(`(?m)^<!-- cwd: (.+?) -->\r?$`)

// projects maps the old project keys to their folders. The key is a hash, so the folder comes from what the old
// files recorded (the memory header, the roles file, the tasks' folders), checked by recomputing the key.
func (m *migrator) projects() map[string]string {
	out := map[string]string{}
	try := func(cwd string) {
		if cwd = strings.TrimSpace(cwd); cwd == "" || !filepath.IsAbs(cwd) {
			return
		}
		if k := OldProjectKey(cwd); out[k] == "" {
			out[k] = cwd
		}
	}
	entries, _ := os.ReadDir(m.old("memory"))
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".roles.json"):
			var f struct {
				Cwd string `json:"cwd"`
			}
			if data, err := fsutil.ReadFile(m.old("memory", name)); err == nil && json.Unmarshal(data, &f) == nil {
				try(f.Cwd)
			}
		case strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".history.md"):
			if data, err := fsutil.ReadFile(m.old("memory", name)); err == nil {
				if mm := cwdHeader.FindSubmatch(data); mm != nil {
					try(string(mm[1]))
				}
			}
		}
	}
	tasks, _ := os.ReadDir(m.old("tasks"))
	for _, e := range tasks {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fsutil.ReadFile(m.old("tasks", e.Name()))
		if err != nil {
			continue
		}
		var t struct {
			Cwd        string `json:"cwd"`
			ProjectCwd string `json:"projectCwd"`
		}
		if json.Unmarshal(data, &t) == nil {
			try(t.ProjectCwd)
			try(t.Cwd)
		}
	}
	return out
}

// memoryFiles lists the old files of one kind: suffix ".md" (notes), ".history.md" or ".roles.json", as
// key -> file name, sorted by key.
func (m *migrator) memoryFiles(suffix string) []project {
	entries, err := os.ReadDir(m.old("memory"))
	if err != nil {
		return nil
	}
	var out []project
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, suffix) {
			continue
		}
		if suffix == ".md" && strings.HasSuffix(name, ".history.md") {
			continue
		}
		out = append(out, project{key: strings.TrimSuffix(name, suffix), cwd: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

// folder returns the project folder of an old key, or reports why there is none.
func (m *migrator) folder(kind, key string, projects map[string]string) (string, bool) {
	cwd := projects[key]
	if cwd == "" {
		m.add(kind, key, Skipped, "the project folder of this key is not recorded anywhere in the old data")
		return "", false
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		m.add(kind, key, Skipped, "the project folder %s no longer exists", cwd)
		return "", false
	}
	return cwd, true
}

// noteHeader is what the old version put at the top of a new notes file.
var noteHeader = regexp.MustCompile(`\A# Project notes for workers\r?\n<!-- cwd: .*? -->\r?\n(\r?\n)?`)

// importMemory imports memory/<key>.md as the project's notes.
func (m *migrator) importMemory(ctx context.Context, projects map[string]string) {
	for _, f := range m.memoryFiles(".md") {
		cwd, ok := m.folder(KindMemory, f.key, projects)
		if !ok {
			continue
		}
		data, err := fsutil.ReadFile(m.old("memory", f.cwd))
		if err != nil {
			m.add(KindMemory, f.key, Error, "%v", err)
			continue
		}
		text := strings.ReplaceAll(string(noteHeader.ReplaceAll(data, nil)), "\r\n", "\n")
		if strings.TrimSpace(text) == "" {
			m.add(KindMemory, f.key, Skipped, "no notes in %s", f.cwd)
			continue
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if len(text) > memory.MaxBytes {
			m.add(KindMemory, f.key, Error, "the notes are %d bytes, over this version's %d byte cap; shorten %s "+
				"and run migrate again", len(text), memory.MaxBytes, m.old("memory", f.cwd))
			continue
		}
		have, err := m.memory.Get(ctx, cwd)
		switch {
		case err != nil:
			m.add(KindMemory, f.key, Error, "%v", err)
			continue
		case have.Text == text:
			m.add(KindMemory, f.key, Unchanged, "notes of %s already imported", cwd)
			continue
		case strings.TrimSpace(have.Text) != "":
			m.add(KindMemory, f.key, Kept, "%s already has notes in this version; they were left as they are", cwd)
			continue
		}
		if !m.o.DryRun {
			if _, err := m.memory.Replace(ctx, cwd, text); err != nil {
				m.add(KindMemory, f.key, Error, "%v", err)
				continue
			}
		}
		m.add(KindMemory, f.key, Imported, "notes of %s (%d bytes)", cwd, len(text))
	}
}

// historyLine is one verdict line of the old history file.
var historyLine = regexp.MustCompile(`^- (\d{4}-\d\d-\d\dT\d\d:\d\d) (\S+) "(.*)": (accepted|rejected)(?: - (.*))?$`)

// importHistory imports memory/<key>.history.md into the project's review history.
func (m *migrator) importHistory(ctx context.Context, projects map[string]string) {
	for _, f := range m.memoryFiles(".history.md") {
		cwd, ok := m.folder(KindHistory, f.key, projects)
		if !ok {
			continue
		}
		data, err := fsutil.ReadFile(m.old("memory", f.cwd))
		if err != nil {
			m.add(KindHistory, f.key, Error, "%v", err)
			continue
		}
		added, known, bad := 0, 0, 0
		var failed error
		for line := range strings.SplitSeq(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			mm := historyLine.FindStringSubmatch(line)
			if mm == nil {
				bad++
				continue
			}
			at, err := time.Parse("2006-01-02T15:04", mm[1])
			if err != nil {
				bad++
				continue
			}
			v := memory.Verdict{Cwd: cwd, TaskID: mm[2], Title: mm[3], Verdict: mm[4], Notes: mm[5]}
			if m.o.DryRun {
				added++
				continue
			}
			ok, err := m.memory.ImportVerdict(ctx, v, at)
			switch {
			case err != nil:
				failed = err
			case ok:
				added++
			default:
				known++
			}
			if failed != nil {
				break
			}
		}
		detail := fmt.Sprintf("%s: %d verdicts imported, %d already there", cwd, added, known)
		if bad > 0 {
			detail += fmt.Sprintf(", %d unreadable lines skipped", bad)
		}
		switch {
		case failed != nil:
			m.add(KindHistory, f.key, Error, "%s; then: %v", detail, failed)
		case added == 0 && known > 0:
			m.add(KindHistory, f.key, Unchanged, "%s", detail)
		case added == 0:
			m.add(KindHistory, f.key, Skipped, "%s", detail)
		default:
			m.add(KindHistory, f.key, Imported, "%s", detail)
		}
	}
}

// importRoles imports roles.json (global roles) and memory/<key>.roles.json (project roles).
func (m *migrator) importRoles(ctx context.Context, projects map[string]string) {
	m.importRoleFile(ctx, m.old("roles.json"), role.ScopeGlobal, "", "global")
	for _, f := range m.memoryFiles(".roles.json") {
		cwd, ok := m.folder(KindRole, f.key, projects)
		if !ok {
			continue
		}
		m.importRoleFile(ctx, m.old("memory", f.cwd), role.ScopeProject, cwd, f.key)
	}
}

func (m *migrator) importRoleFile(ctx context.Context, path, scope, cwd, label string) {
	data, err := fsutil.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		m.add(KindRole, label, Error, "%v", err)
		return
	}
	var f struct {
		Roles []json.RawMessage `json:"roles"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		m.add(KindRole, label, Error, "%s is not a roles file: %v", filepath.Base(path), err)
		return
	}
	have, err := m.roles.List(ctx, cwd, false)
	if err != nil {
		m.add(KindRole, label, Error, "%v", err)
		return
	}
	for _, raw := range f.Roles {
		var r role.Role
		if err := json.Unmarshal(raw, &r); err != nil {
			m.add(KindRole, label, Error, "a role is unreadable: %v", err)
			continue
		}
		id := scope + ":" + r.ID
		if scope == role.ScopeProject {
			id = label + ":" + r.ID
		}
		r.Skills, r.PreferredAccounts = nonNil(r.Skills), nonNil(r.PreferredAccounts)
		if err := role.Validate(trimmed(r)); err != nil {
			m.add(KindRole, id, Error, "%v", err)
			continue
		}
		if cur, ok := findRole(have, r.ID, scope); ok {
			if sameRole(cur, trimmed(r)) {
				m.add(KindRole, id, Unchanged, "already imported")
			} else {
				m.add(KindRole, id, Kept, "a %s role with this id already exists and differs; it was left as it is",
					scope)
			}
			continue
		}
		if !m.o.DryRun {
			if _, err := m.roles.Upsert(ctx, r, scope, cwd); err != nil {
				m.add(KindRole, id, Error, "%v", err)
				continue
			}
		}
		where := "global role"
		if scope == role.ScopeProject {
			where = "role of " + cwd
		}
		m.add(KindRole, id, Imported, "%s %q", where, r.Name)
	}
}

func findRole(roles []role.Role, id, scope string) (role.Role, bool) {
	for i := range roles {
		if roles[i].ID == id && roles[i].Scope == scope {
			return roles[i], true
		}
	}
	return role.Role{}, false
}

// trimmed is the role as Upsert stores it.
func trimmed(r role.Role) role.Role {
	r.Name = strings.TrimSpace(r.Name)
	r.Description = strings.TrimSpace(r.Description)
	r.Rules = strings.TrimSpace(r.Rules)
	r.OutputStyle = strings.TrimSpace(r.OutputStyle)
	return r
}

func sameRole(a, b role.Role) bool {
	return a.Name == b.Name && a.Description == b.Description && a.Rules == b.Rules &&
		a.OutputStyle == b.OutputStyle && strings.Join(a.Skills, "\n") == strings.Join(b.Skills, "\n") &&
		a.DefaultTier == b.DefaultTier && a.DefaultEffort == b.DefaultEffort &&
		strings.Join(a.PreferredAccounts, "\n") == strings.Join(b.PreferredAccounts, "\n")
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// notImported lists the old data this version does not import.
func (m *migrator) notImported() {
	tasks := 0
	if entries, err := os.ReadDir(m.old("tasks")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				tasks++
			}
		}
	}
	if tasks > 0 {
		m.add(KindData, "tasks", Skipped, "%d tasks of the old version are not imported: their worker "+
			"conversations cannot continue in this version (the files stay in %s)", tasks, m.old("tasks"))
	}
	for _, d := range []struct{ name, what string }{
		{"members.json", "department members"},
		{"notes", "review notes"},
		{"meetings", "meetings"},
		{"contests", "contests"},
		{"automations.json", "automations"},
		{"hooks.json", "lead hooks"},
		{"proof", "proof records"},
		{"tickets", "tickets"},
		{"usage-history", "usage history"},
		{"quota", "quota caches (read again from the accounts)"},
	} {
		if exists(m.old(d.name)) {
			m.add(KindData, d.name, Skipped, "%s are not imported: this version does not have them yet", d.what)
		}
	}
}
