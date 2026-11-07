package migrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/store"
)

func TestOldProjectKey(t *testing.T) {
	// Expected values computed with the old version's formula in Node (roles.js projectKey).
	vectors := map[string]string{
		"/home/Me/My Project": "my-project-23430a30",
		"/":                   "-42099b4a",
	}
	if runtime.GOOS == "windows" {
		vectors = map[string]string{
			`D:\Work\Seelegent`:       "seelegent-cc6c4f8a",
			`C:\Users\Me\Dự Án (mới)`: "d-n-m-i--3bec85b6",
			`C:\`:                     "-59d75dd6",
			`D:\a.b_c-d\`:             "a.b_c-d-3e21729e",
		}
	}
	for in, want := range vectors {
		if got := OldProjectKey(in); got != want {
			t.Errorf("OldProjectKey(%q) = %s, want %s", in, got, want)
		}
	}
}

// fixture is an old data folder and a new one, both temporary.
type fixture struct {
	t       *testing.T
	old     string
	paths   config.Paths
	home    string
	project string // an existing project folder
	gone    string // a project folder that no longer exists
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, old: filepath.Join(root, "old"), home: filepath.Join(root, "home"),
		project: filepath.Join(root, "proj"), gone: filepath.Join(root, "gone")}
	var err error
	if f.paths, err = config.NewPaths(filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{f.old, f.home, f.project} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.old, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func js(s string) string { return strings.ReplaceAll(s, `\`, `\\`) }

// realistic builds an old data folder as the Node version left it.
func (f *fixture) realistic() {
	pk, gk := OldProjectKey(f.project), OldProjectKey(f.gone)
	f.write("settings.json", `{
  "port": 47821,
  "tiers": {"fast": "gemini-3.8-flash-low", "standard": "gemini-3.8-flash-medium", "strong": "gemini-3.8-flash-high", "max": "gemini-3.8-flash-high"},
  "defaultTier": "standard",
  "allowedModels": "^gemini-3\\.8-",
  "backends": {
    "agy": {"enabled": true, "allowedModels": "^gemini-", "tiers": {"fast": "x"}},
    "codex": {"enabled": true, "allowedModels": "", "tiers": {"fast": "gpt-5.5-mini", "standard": "gpt-5.5", "strong": "", "max": ""}, "sandbox": "workspace-write"},
    "claude": {"enabled": false, "allowedModels": "^claude-", "tiers": {"fast": "claude-haiku-5"}},
    "opencode": {"enabled": false}
  },
  "providers": {
    "deepseek": {"label": "DeepSeek", "harness": "claude", "baseUrl": "https://api.deepseek.com/anthropic", "enabled": true, "allowedModels": "^(?:deepseek-chat|deepseek-reasoner)$", "tiers": {"fast": "deepseek-chat", "standard": "deepseek-chat", "strong": "deepseek-reasoner", "max": "deepseek-reasoner"}, "preset": "deepseek"},
    "local": {"label": "Local", "harness": "codex", "baseUrl": "http://localhost:8080/v1", "wireApi": "responses", "allowedModels": "", "tiers": {"fast": "qwen", "standard": "", "strong": "", "max": ""}},
    "broken": {"label": "Broken", "harness": "codex", "baseUrl": "ftp://nope"}
  },
  "stallMinutes": 7,
  "updates": "notify",
  "linkIntoProfiles": [".gitconfig"]
}`)
	f.write("accounts.json", `[
  {"id": "system", "backend": "agy", "label": "Mine", "kind": "system", "auth": "login", "enabled": true, "priority": 5, "status": "ok", "createdAt": "2026-10-01T10:00:00.000Z"},
  {"id": "acc2", "backend": "agy", "label": "Second\u0007 account", "kind": "isolated", "auth": "login", "enabled": false, "priority": 99999, "status": "ok", "createdAt": "2026-10-02T10:00:00.000Z"},
  {"id": "acc5", "backend": "claude", "label": "DeepSeek key", "kind": "isolated", "auth": "apikey", "provider": "deepseek", "enabled": true, "priority": 0, "status": "ok", "createdAt": "2026-10-03T10:00:00.000Z"},
  {"id": "acc6", "backend": "opencode", "label": "Old", "kind": "isolated", "status": "removed"},
  {"id": "acc7", "backend": "codex", "label": "Broken provider", "kind": "isolated", "auth": "apikey", "provider": "broken"}
]`)
	f.write("profiles/acc2/home/.gemini/antigravity-cli/state.json", `{"signedIn": true}`)
	f.write("profiles/acc5/home/.claude/settings.json", `{}`)
	f.write("profiles/acc5/home/"+oldKeyFile, `{"key": "sk-test-1234567890abcd"}`)
	f.write("roles.json", `{"version": 1, "roles": [
  {"id": "reviewer", "name": "Reviewer", "description": "Reviews", "rules": "Be strict.", "outputStyle": "", "skills": [], "defaultTier": null, "defaultEffort": null, "preferredAccounts": ["acc2"], "createdAt": "2026-10-01T10:00:00.000Z", "updatedAt": "2026-10-01T10:00:00.000Z"},
  {"id": "Bad ID", "name": "Bad"}
]}`)
	f.write("memory/"+pk+".md", "# Project notes for workers\n<!-- cwd: "+f.project+" -->\n\nUse pnpm.\nRun go test.\n")
	f.write("memory/"+pk+".history.md", "- 2026-10-05T09:30 t1 \"Add \"quoted\" thing\": accepted\n"+
		"- 2026-10-05T10:00 t2 \"Fix\": rejected - missing tests, try again\ngarbage line\n")
	f.write("memory/"+pk+".roles.json", `{"version": 1, "cwd": "`+js(f.project)+`", "roles": [{"id": "tester", "name": "Tester", "rules": "Test it."}]}`)
	f.write("memory/"+gk+".md", "# Project notes for workers\n<!-- cwd: "+f.gone+" -->\n\nGone.\n")
	f.write("memory/deadbeef-12345678.md", "Replaced notes without a header.\n")
	f.write("tasks/t1.json", `{"id": "t1", "cwd": "`+js(f.project)+`", "status": "accepted"}`)
	f.write("tasks/t2.json", `{"id": "t2", "cwd": "`+js(f.project)+`"}`)
	f.write("members.json", `[]`)
	f.write("quota/acc2.json", `{}`)
}

func (f *fixture) run(dry bool) Report {
	f.t.Helper()
	rep, err := Run(context.Background(), Options{From: f.old, Paths: f.paths, RealHome: f.home, DryRun: dry,
		Now: func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) }})
	if err != nil {
		f.t.Fatalf("Run: %v", err)
	}
	return rep
}

func find(t *testing.T, rep Report, kind, id string) Item {
	t.Helper()
	for _, it := range rep.Items {
		if it.Kind == kind && it.ID == id {
			return it
		}
	}
	t.Fatalf("no %s %q in %+v", kind, id, rep.Items)
	return Item{}
}

func want(t *testing.T, rep Report, kind, id string, a Action) Item {
	t.Helper()
	it := find(t, rep, kind, id)
	if it.Action != a {
		t.Errorf("%s %s: %s (%s), want %s", kind, id, it.Action, it.Detail, a)
	}
	return it
}

// tree hashes every file under dir.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[p] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

func TestMigrate(t *testing.T) {
	f := newFixture(t)
	f.realistic()
	oldBefore := tree(t, f.old)
	pk := OldProjectKey(f.project)

	// A dry run writes nothing at all.
	dry := f.run(true)
	if _, err := os.Stat(f.paths.Home); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dry run created %s", f.paths.Home)
	}
	want(t, dry, KindSettings, "", Imported)
	if it := want(t, dry, KindHistory, pk, Imported); !strings.Contains(it.Detail, "2 verdicts imported") {
		t.Errorf("dry history detail %q", it.Detail)
	}
	want(t, dry, KindAccount, "acc5", Imported)
	if it := want(t, dry, KindKey, "acc5", Imported); !strings.Contains(it.Detail, "would store") {
		t.Errorf("dry key detail %q", it.Detail)
	}

	rep := f.run(false)
	var text bytes.Buffer
	if err := rep.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	t.Log(text.String())
	if strings.Contains(text.String(), "sk-test-1234567890abcd") {
		t.Fatal("the report shows the API key")
	}

	set := want(t, rep, KindSettings, "", Imported)
	if !strings.Contains(set.Detail, "codex allowed no model") || strings.Contains(set.Detail, "claude allowed") {
		t.Errorf("settings detail: %s", set.Detail)
	}
	c, err := config.Load(f.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	s := c.Settings
	if s.StallMinutes != 7 || s.Backends.Agy.AllowedModels != `^gemini-3\.8-` || s.Backends.Agy.Tiers.Fast != "gemini-3.8-flash-low" ||
		s.Backends.Codex.AllowedModels != `^(?:gpt-5\.5-mini|gpt-5\.5)$` || s.Backends.Claude.AllowedModels != "^claude-" ||
		len(s.LinkIntoProfiles) != 1 {
		t.Errorf("settings %+v", s)
	}
	if len(c.Unknown()) != 0 {
		t.Errorf("config.json got old keys: %v", c.Unknown())
	}

	want(t, rep, KindProvider, "deepseek", Imported)
	want(t, rep, KindProvider, "local", Imported)
	want(t, rep, KindProvider, "broken", Error)
	db, err := store.Open(context.Background(), f.paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := account.NewRepo(db, nil)
	local, err := repo.GetProvider(context.Background(), "local")
	if err != nil || local.AllowedModels != "^(?:qwen)$" || local.WireAPI != "responses" || !local.Enabled {
		t.Errorf("local provider %+v, %v", local, err)
	}

	want(t, rep, KindAccount, "system", Imported)
	want(t, rep, KindAccount, "acc2", Imported)
	want(t, rep, KindAccount, "acc5", Imported)
	want(t, rep, KindAccount, "acc6", Skipped)
	want(t, rep, KindAccount, "acc7", Error)
	acc2, err := repo.Get(context.Background(), "acc2")
	if err != nil || acc2.Label != "Second  account" || acc2.Enabled || acc2.Priority != 1000 ||
		!acc2.CreatedAt.Equal(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("acc2 %+v, %v", acc2, err)
	}
	// A new account gets an id after the imported ones.
	next, err := repo.Create(context.Background(), account.New{Backend: "agy", Kind: account.KindIsolated}, nil)
	if err != nil || next.ID != "acc6" {
		t.Errorf("next account %q, %v", next.ID, err)
	}

	want(t, rep, KindProfile, "acc2", Imported)
	if _, err := os.Stat(filepath.Join(f.paths.Profiles, "acc2", "home", ".gemini", "antigravity-cli", "state.json")); err != nil {
		t.Errorf("profile not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.paths.Profiles, "acc5", "home", oldKeyFile)); err == nil {
		t.Error("the plain-text key file was copied")
	}
	want(t, rep, KindKey, "acc5", Imported)
	keys := account.NewKeys(account.NewProfiles(f.paths.Profiles, f.home))
	if key, err := keys.Load("acc5"); err != nil || key != "sk-test-1234567890abcd" {
		t.Errorf("stored key: %v", err)
	}
	if a, _ := repo.Get(context.Background(), "acc5"); a.KeyHint != "…abcd" {
		t.Errorf("key hint %q", a.KeyHint)
	}

	want(t, rep, KindMemory, pk, Imported)
	want(t, rep, KindMemory, OldProjectKey(f.gone), Skipped)
	want(t, rep, KindMemory, "deadbeef-12345678", Skipped)
	mem := memory.New(db, nil)
	note, err := mem.Get(context.Background(), f.project)
	if err != nil || note.Text != "Use pnpm.\nRun go test.\n" {
		t.Errorf("notes %q, %v", note.Text, err)
	}
	hist := want(t, rep, KindHistory, pk, Imported)
	if !strings.Contains(hist.Detail, "2 verdicts imported") || !strings.Contains(hist.Detail, "1 unreadable") {
		t.Errorf("history detail %s", hist.Detail)
	}
	lines, err := mem.History(context.Background(), f.project, 8)
	if err != nil || len(lines) != 2 || lines[0] != `- 2026-10-05T09:30 t1 "Add \"quoted\" thing": accepted` ||
		lines[1] != `- 2026-10-05T10:00 t2 "Fix": rejected - missing tests, try again` {
		t.Errorf("history %q, %v", lines, err)
	}

	want(t, rep, KindRole, "global:reviewer", Imported)
	want(t, rep, KindRole, "global:Bad ID", Error)
	want(t, rep, KindRole, pk+":tester", Imported)
	roles := role.New(role.Options{DB: db, Home: f.home})
	got, err := roles.Get(context.Background(), "tester", f.project)
	if err != nil || got.Rules != "Test it." || got.Scope != role.ScopeProject {
		t.Errorf("tester %+v, %v", got, err)
	}

	want(t, rep, KindData, "tasks", Skipped)
	want(t, rep, KindData, "members.json", Skipped)
	want(t, rep, KindData, "quota", Skipped)
	if !rep.Failed() {
		t.Error("errors not reported as failed")
	}

	// The old folder is untouched.
	if after := tree(t, f.old); len(after) != len(oldBefore) {
		t.Errorf("old folder changed: %d files, was %d", len(after), len(oldBefore))
	} else {
		for p, h := range oldBefore {
			if after[p] != h {
				t.Errorf("old file changed: %s", p)
			}
		}
	}

	// A dry run after the real one finds nothing new, verdicts included.
	dryAgain := f.run(true)
	for _, it := range dryAgain.Items {
		if it.Action == Imported {
			t.Errorf("dry run after the real one would import %s %s: %s", it.Kind, it.ID, it.Detail)
		}
	}
	if it := want(t, dryAgain, KindHistory, pk, Unchanged); !strings.Contains(it.Detail, "0 verdicts imported, 2 already") {
		t.Errorf("dry history detail after the real run %q", it.Detail)
	}

	// A second run changes nothing.
	again := f.run(false)
	var changed []string
	for _, it := range again.Items {
		if it.Action == Imported {
			changed = append(changed, it.Kind+" "+it.ID)
		}
	}
	sort.Strings(changed)
	if len(changed) > 0 {
		t.Errorf("second run imported again: %v", changed)
	}
	want(t, again, KindSettings, "", Unchanged)
	want(t, again, KindAccount, "acc5", Unchanged)
	want(t, again, KindProfile, "acc2", Unchanged)
	want(t, again, KindKey, "acc5", Unchanged)
	want(t, again, KindMemory, pk, Unchanged)
	want(t, again, KindHistory, pk, Unchanged)
	want(t, again, KindRole, "global:reviewer", Unchanged)
	if lines, _ := mem.History(context.Background(), f.project, 8); len(lines) != 2 {
		t.Errorf("history doubled: %q", lines)
	}
}

func TestMigrateKeepsExistingData(t *testing.T) {
	f := newFixture(t)
	f.write("settings.json", `{"stallMinutes": 9}`)
	f.write("memory/"+OldProjectKey(f.project)+".md", "# Project notes for workers\n<!-- cwd: "+f.project+" -->\n\nold\n")
	if err := f.paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Settings: config.Defaults()}
	c.Settings.StallMinutes = 30
	if err := c.Save(f.paths.Config); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), f.paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := memory.New(db, nil).Replace(context.Background(), f.project, "new notes"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	rep := f.run(false)
	want(t, rep, KindSettings, "", Kept)
	want(t, rep, KindMemory, OldProjectKey(f.project), Kept)
	if c, _ := config.Load(f.paths.Config); c.Settings.StallMinutes != 30 {
		t.Error("existing settings overwritten")
	}
}

func TestMigrateInvalidSettingsKeepDefaults(t *testing.T) {
	f := newFixture(t)
	f.write("settings.json", `{"port": "eighty", "stallMinutes": -5, "defaultTier": "huge", "backends": {"agy": {"allowedModels": "^gemini-", "tiers": {"fast": "gpt-4"}}}, "maxConcurrentPerAccount": 3}`)
	rep := f.run(false)
	it := want(t, rep, KindSettings, "", Imported)
	for _, w := range []string{"port", "stallMinutes", "defaultTier", "backends.agy.tiers"} {
		if !strings.Contains(it.Detail, "default of "+w) {
			t.Errorf("detail lacks %s: %s", w, it.Detail)
		}
	}
	c, err := config.Load(f.paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	d := config.Defaults()
	if c.Settings.Port != d.Port || c.Settings.StallMinutes != d.StallMinutes || c.Settings.MaxConcurrentPerAccount != 3 {
		t.Errorf("settings %+v", c.Settings)
	}
}

func TestMigrateSameFolderAndErrors(t *testing.T) {
	f := newFixture(t)
	// Old and new data in one folder, as on a real machine.
	f.paths, _ = config.NewPaths(f.old)
	f.write("accounts.json", `{"accounts": [{"id": "acc1", "backend": "agy", "label": "A"}]}`)
	f.write("profiles/acc1/home/x.txt", "x")
	f.write("roles.json", `not json`)
	rep := f.run(false)
	want(t, rep, KindAccount, "acc1", Imported)
	if it := want(t, rep, KindProfile, "acc1", Unchanged); !strings.Contains(it.Detail, "in place") {
		t.Errorf("profile detail %s", it.Detail)
	}
	want(t, rep, KindRole, "global", Error)
	want(t, rep, KindSettings, "", Skipped)

	if _, err := Run(context.Background(), Options{From: t.TempDir(), Paths: f.paths}); !errors.Is(err, ErrNotOldData) {
		t.Errorf("empty folder: %v", err)
	}
}
