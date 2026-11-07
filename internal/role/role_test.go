package role

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/store"
)

type fakeClock struct{ at time.Time }

func (c *fakeClock) now() time.Time { return c.at }

func newStore(t *testing.T) (s *Store, st *store.Store, clk *fakeClock, home string) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk = &fakeClock{at: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)}
	home = t.TempDir()
	return New(Options{DB: st, Home: home, Now: clk.now}), st, clk, home
}

func TestValidate(t *testing.T) {
	ok := Role{ID: "be-1", Name: "Backend"}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	bad := []Role{
		{ID: "Bad", Name: "x"}, {ID: "-x", Name: "x"}, {ID: strings.Repeat("a", 41), Name: "x"},
		{ID: "x", Name: " "}, {ID: "x", Name: strings.Repeat("n", 61)},
		{ID: "x", Name: "x", Description: strings.Repeat("d", 301)},
		{ID: "x", Name: "x", Rules: strings.Repeat("r", 8001)},
		{ID: "x", Name: "x", OutputStyle: strings.Repeat("o", 2001)},
		{ID: "x", Name: "x", Skills: make([]string, 21)},
		{ID: "x", Name: "x", DefaultTier: "huge"},
		{ID: "x", Name: "x", DefaultEffort: "extreme"},
	}
	for _, r := range bad {
		if err := Validate(r); !errors.Is(err, ErrInvalid) {
			t.Errorf("Validate(%+v) = %v", r.ID, err)
		}
	}
	for _, r := range Templates() {
		if err := Validate(r); err != nil {
			t.Errorf("template %s: %v", r.ID, err)
		}
	}
}

func TestScopesAndOverride(t *testing.T) {
	ctx := context.Background()
	s, _, clk, _ := newStore(t)
	proj := t.TempDir()
	g, err := s.Upsert(ctx, Role{ID: "backend", Name: " Backend ", Rules: " be careful "}, ScopeGlobal, "")
	if err != nil || g.Name != "Backend" || g.Rules != "be careful" || g.Scope != ScopeGlobal || g.Skills == nil {
		t.Fatalf("Upsert global = %+v, %v", g, err)
	}
	if _, err := s.Upsert(ctx, Role{ID: "alpha", Name: "Alpha"}, ScopeGlobal, ""); err != nil {
		t.Fatal(err)
	}
	clk.at = clk.at.Add(time.Hour)
	p, err := s.Upsert(ctx, Role{ID: "backend", Name: "Project backend"}, ScopeProject, proj)
	if err != nil || !p.OverridesGlobal || p.Scope != ScopeProject || p.Cwd == "" {
		t.Fatalf("Upsert project = %+v, %v", p, err)
	}
	// Updating keeps createdAt.
	clk.at = clk.at.Add(time.Hour)
	p2, err := s.Upsert(ctx, Role{ID: "backend", Name: "Project backend 2"}, ScopeProject, proj)
	if err != nil || !p2.CreatedAt.Equal(p.CreatedAt) || !p2.UpdatedAt.After(p.UpdatedAt) {
		t.Fatalf("update = %+v, %v", p2, err)
	}

	inProj, err := s.List(ctx, proj, false)
	if err != nil || len(inProj) != 2 || inProj[0].ID != "alpha" || inProj[1].Name != "Project backend 2" ||
		!inProj[1].OverridesGlobal {
		t.Fatalf("List(proj) = %+v, %v", inProj, err)
	}
	globalOnly, err := s.List(ctx, "", false)
	if err != nil || len(globalOnly) != 2 || globalOnly[1].Name != "Backend" {
		t.Fatalf("List() = %+v, %v", globalOnly, err)
	}
	all, err := s.List(ctx, "", true)
	if err != nil || len(all) != 3 {
		t.Fatalf("List(all) = %+v, %v", all, err)
	}
	got, err := s.Get(ctx, "backend", proj)
	if err != nil || got.Scope != ScopeProject {
		t.Fatalf("Get(proj) = %+v, %v", got, err)
	}
	if got, err := s.Get(ctx, "backend", t.TempDir()); err != nil || got.Scope != ScopeGlobal {
		t.Fatalf("Get(other) = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, "nope", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get(nope) = %v", err)
	}
	projects, err := s.Projects(ctx)
	if err != nil || len(projects) != 1 || projects[0].Count != 1 || projects[0].Name != filepath.Base(p.Cwd) {
		t.Fatalf("Projects = %+v, %v", projects, err)
	}

	if ok, err := s.Delete(ctx, "backend", ScopeProject, proj); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if ok, err := s.Delete(ctx, "backend", ScopeProject, proj); err != nil || ok {
		t.Fatalf("second Delete = %v, %v", ok, err)
	}
	if got, _ := s.Get(ctx, "backend", proj); got.Scope != ScopeGlobal {
		t.Fatalf("after delete Get = %+v", got)
	}
	for _, c := range []struct{ scope, cwd string }{{"team", ""}, {ScopeProject, ""}, {ScopeProject, "rel"}} {
		if _, err := s.Upsert(ctx, Role{ID: "x", Name: "x"}, c.scope, c.cwd); !errors.Is(err, ErrInvalid) {
			t.Errorf("Upsert(%s, %q) = %v", c.scope, c.cwd, err)
		}
		if _, err := s.Delete(ctx, "x", c.scope, c.cwd); !errors.Is(err, ErrInvalid) {
			t.Errorf("Delete(%s, %q) = %v", c.scope, c.cwd, err)
		}
	}
	if _, err := s.Upsert(ctx, Role{ID: "BAD", Name: "x"}, ScopeGlobal, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id = %v", err)
	}
	if _, err := s.List(ctx, filepath.Join(proj, "missing"), false); !errors.Is(err, ErrInvalid) {
		t.Errorf("List(missing) = %v", err)
	}
}

// TestDeleteGoneProject deletes the roles of a project whose folder no longer exists, by its path or its key.
func TestDeleteGoneProject(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newStore(t)
	proj := filepath.Join(t.TempDir(), "Gone App")
	if err := os.Mkdir(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, err := s.Upsert(ctx, Role{ID: id, Name: id}, ScopeProject, proj); err != nil {
			t.Fatal(err)
		}
	}
	projects, err := s.Projects(ctx)
	if err != nil || len(projects) != 1 {
		t.Fatalf("Projects = %+v, %v", projects, err)
	}
	if err := os.Remove(proj); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Delete(ctx, "a", ScopeProject, proj); err != nil || !ok {
		t.Fatalf("Delete by path = %v, %v", ok, err)
	}
	if ok, err := s.Delete(ctx, "b", ScopeProject, projects[0].Key); err != nil || !ok {
		t.Fatalf("Delete by key = %v, %v", ok, err)
	}
	if left, err := s.Projects(ctx); err != nil || len(left) != 0 {
		t.Fatalf("Projects after = %+v, %v", left, err)
	}
	if _, err := s.Delete(ctx, "b", ScopeProject, "Not A Key"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Delete(bad key) = %v", err)
	}
}

func TestUnreadableRoleIsError(t *testing.T) {
	ctx := context.Background()
	s, st, _, _ := newStore(t)
	if _, err := s.Upsert(ctx, Role{ID: "a", Name: "A"}, ScopeGlobal, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE roles SET data = 'nope', created_at = 'bad'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx, "", false); err == nil {
		t.Error("unreadable role accepted")
	}
	if _, err := s.Upsert(ctx, Role{ID: "a", Name: "A"}, ScopeGlobal, ""); err == nil {
		t.Error("upsert over unreadable role accepted")
	}
}

func writeSkill(t *testing.T, dir, name, content string) string {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSkills(t *testing.T) {
	s, _, clk, home := newStore(t)
	repo := t.TempDir()
	sub := filepath.Join(repo, "pkg", "x")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	projSkills := filepath.Join(repo, ".agents", "skills")
	writeSkill(t, projSkills, "speckit-implement", "---\r\nname: speckit-implement\r\ndescription: \"Implement tasks\"\r\n---\r\nbody")
	writeSkill(t, projSkills, "plain", "no front matter")
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "dup", "---\nname: Speckit-Implement\n---\n")
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "go", "---\nname: 'go-style'\ndescription: "+
		strings.Repeat("d", 250)+"\n---\n")
	if err := os.WriteFile(filepath.Join(projSkills, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	skills := s.Skills(sub)
	names := []string{}
	for _, sk := range skills {
		names = append(names, sk.Name+"@"+sk.Root)
	}
	if strings.Join(names, ",") != "plain@project,speckit-implement@project,go-style@user" {
		t.Fatalf("skills = %v", names)
	}
	if skills[1].Description != "Implement tasks" || len([]rune(skills[2].Description)) != 200 {
		t.Fatalf("descriptions %q / %d", skills[1].Description, len(skills[2].Description))
	}

	// Cached for 30 s: a new skill shows up only after that.
	writeSkill(t, projSkills, "late", "---\nname: late\n---\n")
	if len(s.Skills(sub)) != 3 {
		t.Fatal("cache not used")
	}
	clk.at = clk.at.Add(31 * time.Second)
	if len(s.Skills(sub)) != 4 {
		t.Fatal("cache not refreshed")
	}

	abs := writeSkill(t, t.TempDir(), "external", "---\nname: ext\ndescription: outside\n---\n")
	b := s.Brief(Role{ID: "r", Name: "R", Rules: "x", Skills: []string{"GO-STYLE", abs, " ", "missing",
		filepath.Join(abs, "nope")}}, sub)
	if len(b.Skills) != 2 || b.Skills[0].Name != "go-style" || b.Skills[1].Name != "ext" ||
		strings.Join(b.Missing, ",") != "missing,"+filepath.Join(abs, "nope") || b.Rules != "x" {
		t.Fatalf("brief role = %+v", b)
	}
	if b := s.Brief(Role{ID: "r", Name: "R"}, ""); len(b.Skills) != 0 || len(b.Missing) != 0 {
		t.Fatalf("brief without skills = %+v", b)
	}
}

func TestFrontMatter(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, content, wantName, wantDesc string }{
		{"folded", "---\nname: folded\ndescription: >\n  Builds the UI\n  from a spec.\nlicense: MIT\n---\n", "folded",
			"Builds the UI from a spec."},
		{"literal", "---\ndescription: |-\n  Line one.\n\n  Line two.\nname: lit\n---\n", "lit", "Line one. Line two."},
		{"bom", "\ufeff---\r\nname: with-bom\r\ndescription: 'Has a BOM'\r\n---\r\n", "with-bom", "Has a BOM"},
		{"plain-multi", "---\nname: multi\ndescription: starts here\n  and goes on\nmetadata:\n  name: nested\n---\n",
			"multi", "starts here and goes on"},
		{"nested-only", "---\nmetadata:\n  description: nested\n---\n", "nested-only", ""},
		{"no-header", "# Title\nname: x\n", "no-header", ""},
	}
	for _, c := range cases {
		d := writeSkill(t, dir, c.name, c.content)
		name, desc := frontMatter(filepath.Join(d, "SKILL.md"), c.name)
		if name != c.wantName || desc != c.wantDesc {
			t.Errorf("%s: name %q, description %q", c.name, name, desc)
		}
	}
}

// TestHomeSkillsAreUser labels ~/.agents/skills as the user's for a task folder under home, not as the project's.
func TestHomeSkillsAreUser(t *testing.T) {
	s, _, _, home := newStore(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"), "mine", "---\nname: mine\n---\n")
	proj := filepath.Join(home, "code", "app")
	if err := os.MkdirAll(proj, 0o750); err != nil {
		t.Fatal(err)
	}
	skills := s.Skills(proj)
	if len(skills) != 1 || skills[0].Root != RootUser {
		t.Fatalf("skills = %+v", skills)
	}
}

func TestAPIShapes(t *testing.T) {
	ctx := context.Background()
	s, _, _, _ := newStore(t)
	g, err := s.Upsert(ctx, Role{ID: "be", Name: "Backend"}, ScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{
		"template": ToAPI(Templates()[0]),
		"stored":   ToAPI(g),
		"list":     ListToAPI(nil, projects),
		"skills":   SkillsToAPI(nil),
	} {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(text, "null") {
			t.Errorf("%s has a null: %s", name, text)
		}
		if name == "stored" && !strings.Contains(text, `"createdAt":"2026-11-07T09:00:00.000Z"`) {
			t.Errorf("stored role times: %s", text)
		}
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "scope") || strings.Contains(string(data), "overridesGlobal") {
		t.Errorf("the stored document carries where the role was found: %s", data)
	}
}
