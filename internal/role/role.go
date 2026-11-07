// Package role keeps worker roles (profiles): a name, a description, binding rules, an output style, skills and
// default tier and effort that a task can run with. A role is global, or belongs to one project (keyed by
// fsutil.ProjectKey of its folder) where it overrides a global role with the same id. Roles live in seelegent.db
// (table roles, migration 0005), one JSON document per role; a role that cannot be read is an error.
package role

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Scopes.
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// Field limits (the old ones).
const (
	maxName        = 60
	maxDescription = 300
	maxRules       = 8000
	maxOutputStyle = 2000
	maxSkills      = 20
)

// Errors.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
)

// idPattern is what a role id looks like.
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// tiers and efforts return what a role may default to.
func tiers() []string   { return []string{"fast", "standard", "strong", "max"} }
func efforts() []string { return []string{"low", "medium", "high", "xhigh", "max"} }

// Role is a worker role. The JSON tags are the stored document's (table roles); the API shape is api.Role (ToAPI).
type Role struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	Rules             string    `json:"rules"`
	OutputStyle       string    `json:"outputStyle"`
	Skills            []string  `json:"skills"`
	DefaultTier       string    `json:"defaultTier,omitempty"`
	DefaultEffort     string    `json:"defaultEffort,omitempty"`
	PreferredAccounts []string  `json:"preferredAccounts"`
	CreatedAt         time.Time `json:"createdAt,omitzero"`
	UpdatedAt         time.Time `json:"updatedAt,omitzero"`

	// Scope, OverridesGlobal and Cwd describe where the role was found; they are not stored.
	Scope           string `json:"-"`
	OverridesGlobal bool   `json:"-"`
	Cwd             string `json:"-"`
}

// ToAPI returns the role as the API shows it.
func ToAPI(r Role) api.Role {
	return api.Role{ID: r.ID, Name: r.Name, Description: r.Description, Rules: r.Rules, OutputStyle: r.OutputStyle,
		Skills: api.NonNil(r.Skills), DefaultTier: r.DefaultTier, DefaultEffort: r.DefaultEffort,
		PreferredAccounts: api.NonNil(r.PreferredAccounts), Scope: r.Scope, Cwd: r.Cwd,
		OverridesGlobal: r.OverridesGlobal, CreatedAt: api.FormatTime(r.CreatedAt), UpdatedAt: api.FormatTime(r.UpdatedAt)}
}

// ListToAPI returns roles and their projects as GET /roles shows them.
func ListToAPI(roles []Role, projects []Project) api.RoleList {
	out := api.RoleList{Items: make([]api.Role, 0, len(roles)), Projects: make([]api.RoleProject, 0, len(projects))}
	for i := range roles {
		out.Items = append(out.Items, ToAPI(roles[i]))
	}
	for _, p := range projects {
		out.Projects = append(out.Projects, api.RoleProject{Key: p.Key, Cwd: p.Cwd, Name: p.Name, Count: p.Count})
	}
	return out
}

// SkillsToAPI returns skills as GET /roles/skills shows them.
func SkillsToAPI(skills []Skill) []api.Skill {
	out := make([]api.Skill, 0, len(skills))
	for _, sk := range skills {
		out = append(out, api.Skill{Name: sk.Name, Description: sk.Description, Root: sk.Root})
	}
	return out
}

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// timeFormat is how times are stored.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Options configure a Store.
type Options struct {
	DB DB
	// Home is the user's home folder, where the global skill folders are (os.UserHomeDir when empty).
	Home string
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Store reads and writes roles and discovers skills.
type Store struct {
	db   DB
	home string
	now  func() time.Time

	mu     sync.Mutex
	skills map[string]skillCache
}

// New returns a store.
func New(o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	return &Store{db: o.DB, home: o.Home, now: o.Now, skills: map[string]skillCache{}}
}

// project returns the project key and canonical folder of cwd, which must be an absolute path to a folder.
func project(cwd string) (key, canonical string, err error) {
	canonical, key, _, err = fsutil.ProjectOf(cwd)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return key, canonical, nil
}

// Validate checks a role's fields.
func Validate(r Role) error {
	switch {
	case !idPattern.MatchString(r.ID):
		return fmt.Errorf("%w: role id %q must match %s", ErrInvalid, r.ID, idPattern)
	case strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > maxName:
		return fmt.Errorf("%w: role name must be 1..%d characters", ErrInvalid, maxName)
	case len([]rune(r.Description)) > maxDescription:
		return fmt.Errorf("%w: role description must be at most %d characters", ErrInvalid, maxDescription)
	case len([]rune(r.Rules)) > maxRules:
		return fmt.Errorf("%w: role rules must be at most %d characters", ErrInvalid, maxRules)
	case len([]rune(r.OutputStyle)) > maxOutputStyle:
		return fmt.Errorf("%w: role outputStyle must be at most %d characters", ErrInvalid, maxOutputStyle)
	case len(r.Skills) > maxSkills:
		return fmt.Errorf("%w: a role has at most %d skills", ErrInvalid, maxSkills)
	case r.DefaultTier != "" && !slices.Contains(tiers(), r.DefaultTier):
		return fmt.Errorf("%w: defaultTier %q must be one of %s", ErrInvalid, r.DefaultTier, strings.Join(tiers(), ", "))
	case r.DefaultEffort != "" && !slices.Contains(efforts(), r.DefaultEffort):
		return fmt.Errorf("%w: defaultEffort %q must be one of %s", ErrInvalid, r.DefaultEffort,
			strings.Join(efforts(), ", "))
	}
	return nil
}

// Upsert creates or replaces a role in scope (cwd names the project of a project role) and returns it.
func (s *Store) Upsert(ctx context.Context, r Role, scope, cwd string) (Role, error) {
	key, canonical, err := s.scopeKey(scope, cwd)
	if err != nil {
		return Role{}, err
	}
	r.Name = strings.TrimSpace(r.Name)
	r.Description = strings.TrimSpace(r.Description)
	r.Rules = strings.TrimSpace(r.Rules)
	r.OutputStyle = strings.TrimSpace(r.OutputStyle)
	r.Skills = nonNil(r.Skills)
	r.PreferredAccounts = nonNil(r.PreferredAccounts)
	if err := Validate(r); err != nil {
		return Role{}, err
	}
	now := s.now().UTC()
	r.Scope, r.Cwd, r.OverridesGlobal = scope, canonical, false
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var created string
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM roles WHERE scope = ? AND project_key = ? AND id = ?`,
			scope, key, r.ID).Scan(&created)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			r.CreatedAt = now
		case err != nil:
			return err
		default:
			if r.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
				return fmt.Errorf("role %s is unreadable: %w", r.ID, err)
			}
		}
		r.UpdatedAt = now
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO roles (scope, project_key, id, cwd, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (scope, project_key, id) DO UPDATE SET cwd = excluded.cwd,
			data = excluded.data, updated_at = excluded.updated_at`, scope, key, r.ID, canonical, string(data),
			r.CreatedAt.Format(timeFormat), now.Format(timeFormat)); err != nil {
			return err
		}
		if scope == ScopeProject {
			r.OverridesGlobal, err = exists(ctx, tx, ScopeGlobal, "", r.ID)
		}
		return err
	})
	if err != nil {
		return Role{}, err
	}
	return r, nil
}

// Delete removes a role and reports whether it existed. For a project role, project is the project's folder (an
// absolute path, which need not exist any more) or its key as Projects lists it.
func (s *Store) Delete(ctx context.Context, id, scope, project string) (bool, error) {
	key, err := deleteKey(scope, project)
	if err != nil {
		return false, err
	}
	deleted := false
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM roles WHERE scope = ? AND project_key = ? AND id = ?`, scope, key, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		deleted = n > 0
		return err
	})
	return deleted, err
}

// Get returns role id as a task in cwd sees it: the project's role when there is one, else the global role. cwd
// may be empty (global roles only).
func (s *Store) Get(ctx context.Context, id, cwd string) (Role, error) {
	roles, err := s.List(ctx, cwd, false)
	if err != nil {
		return Role{}, err
	}
	for i := range roles {
		if roles[i].ID == id {
			return roles[i], nil
		}
	}
	return Role{}, fmt.Errorf("%w: role %q", ErrNotFound, id)
}

// List returns the roles a task in cwd sees (global roles, overridden by the project's), or with all every global
// and project role. Roles are sorted by name, then id.
func (s *Store) List(ctx context.Context, cwd string, all bool) ([]Role, error) {
	key := ""
	if cwd != "" && !all {
		var err error
		if key, _, err = project(cwd); err != nil {
			return nil, err
		}
	}
	var out []Role
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		q := `SELECT scope, cwd, data FROM roles WHERE scope = 'global' OR project_key = ?`
		args := []any{key}
		if all {
			q, args = `SELECT scope, cwd, data FROM roles`, nil
		}
		rows, err := tx.QueryContext(ctx, q+` ORDER BY scope, project_key, id`, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var global, proj []Role
		for rows.Next() {
			var scope, dir, data string
			if err := rows.Scan(&scope, &dir, &data); err != nil {
				return err
			}
			var r Role
			if err := json.Unmarshal([]byte(data), &r); err != nil {
				return fmt.Errorf("a %s role is unreadable: %w", scope, err)
			}
			r.Scope = scope
			if scope == ScopeProject {
				r.Cwd = dir
				proj = append(proj, r)
			} else {
				global = append(global, r)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		out = merge(global, proj, all)
		return nil
	})
	return out, err
}

func merge(global, proj []Role, all bool) []Role {
	globalIDs := map[string]bool{}
	for i := range global {
		globalIDs[global[i].ID] = true
	}
	for i := range proj {
		proj[i].OverridesGlobal = globalIDs[proj[i].ID]
	}
	out := global
	if all {
		out = append(out, proj...)
	} else {
		for j := range proj {
			p := &proj[j]
			if i := slices.IndexFunc(out, func(r Role) bool { return r.ID == p.ID }); i >= 0 {
				out[i] = *p
			} else {
				out = append(out, *p)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Role) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return nonNil(out)
}

// Project is a project that has roles of its own.
type Project struct {
	Key   string
	Cwd   string
	Name  string
	Count int
}

// Projects lists the projects with roles of their own, by name.
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	var out []Project
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT project_key, MAX(cwd), COUNT(*) FROM roles WHERE scope = 'project'
			GROUP BY project_key ORDER BY project_key`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p Project
			if err := rows.Scan(&p.Key, &p.Cwd, &p.Count); err != nil {
				return err
			}
			p.Name = filepath.Base(p.Cwd)
			out = append(out, p)
		}
		return rows.Err()
	})
	slices.SortStableFunc(out, func(a, b Project) int { return strings.Compare(a.Name, b.Name) })
	return nonNil(out), err
}

func (s *Store) scopeKey(scope, cwd string) (key, canonical string, err error) {
	switch scope {
	case ScopeGlobal:
		return "", "", nil
	case ScopeProject:
		return project(cwd)
	}
	return "", "", fmt.Errorf("%w: scope %q must be global or project", ErrInvalid, scope)
}

// projectKeyPattern is what fsutil.ProjectKey returns.
var projectKeyPattern = regexp.MustCompile(`^[a-z0-9._-]*-[0-9a-f]{8}$`)

// deleteKey is the project key a Delete names: a folder that may be gone, or a key.
func deleteKey(scope, project string) (string, error) {
	switch scope {
	case ScopeGlobal:
		return "", nil
	case ScopeProject:
	default:
		return "", fmt.Errorf("%w: scope %q must be global or project", ErrInvalid, scope)
	}
	if filepath.IsAbs(project) {
		key, _, err := fsutil.ProjectKey(project)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		return key, nil
	}
	if !projectKeyPattern.MatchString(project) {
		return "", fmt.Errorf("%w: project %q is neither an absolute path nor a project key", ErrInvalid, project)
	}
	return project, nil
}

func exists(ctx context.Context, tx *sql.Tx, scope, key, id string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM roles WHERE scope = ? AND project_key = ? AND id = ?`, scope, key,
		id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// Templates returns the starter roles the dashboard offers when a role is created.
func Templates() []Role {
	return []Role{
		{ID: "frontend", Name: "Frontend",
			Description: "User interfaces: markup, styles, components, responsive layout and accessibility.",
			Rules: "- Use the existing design tokens; never hard-code colors.\n- Layouts work from 375 px phones to " +
				"desktop.\n- Respect prefers-reduced-motion.\n- Keep everything keyboard accessible.",
			OutputStyle:   "Describe the visible changes and the states you checked.",
			DefaultTier:   "standard",
			DefaultEffort: "medium"},
		{ID: "backend", Name: "Backend & API",
			Description: "Server architecture, HTTP and streaming APIs, data handling and business logic.",
			Rules: "- Validate every input and return clear errors.\n- Add no new dependencies without the lead's " +
				"OK.\n- Keep handlers thin; put logic where it can be tested.",
			OutputStyle:   "List the endpoints you changed, their request and response shapes, and the test results.",
			DefaultTier:   "strong",
			DefaultEffort: "medium"},
		{ID: "qa", Name: "Testing & QA",
			Description: "Writes tests, hunts for hidden bugs, checks compatibility and verifies quality.",
			Rules: "- Tests never touch real user data; use temporary folders.\n- Cover edge cases, failed " +
				"connections and malformed data.\n- Run the full test suite before reporting.",
			OutputStyle:   "Report the cases you ran, what they cover and every problem found.",
			DefaultTier:   "standard",
			DefaultEffort: "high"},
		{ID: "docs", Name: "Documentation",
			Description: "Writes and updates technical documentation, API contracts, specs and guides.",
			Rules: "- Keep the existing Markdown structure.\n- Never change a binding contract without the lead's " +
				"approval.\n- Be precise and concise.",
			OutputStyle:   "Summarize the sections you added or changed.",
			DefaultTier:   "fast",
			DefaultEffort: "low"},
	}
}
