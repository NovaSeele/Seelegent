package api

// Role is a worker role (profile) as GET /roles and POST /roles return it (contract §5). Scope ("global" or
// "project") and Cwd (the project folder of a project role) say where the role is stored: the dashboard needs them
// to edit or delete it (DELETE /roles/{id}?scope&cwd). OverridesGlobal marks a project role that hides a global role
// with the same id, which the roles page shows as a badge. In POST /roles bodies the three are ignored (scope and
// cwd travel next to the role).
type Role struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Rules             string   `json:"rules"`
	OutputStyle       string   `json:"outputStyle"`
	Skills            []string `json:"skills"`
	DefaultTier       string   `json:"defaultTier,omitempty"`
	DefaultEffort     string   `json:"defaultEffort,omitempty"`
	PreferredAccounts []string `json:"preferredAccounts"`
	Scope             string   `json:"scope"`
	Cwd               string   `json:"cwd,omitempty"`
	OverridesGlobal   bool     `json:"overridesGlobal"`
	CreatedAt         string   `json:"createdAt,omitempty"`
	UpdatedAt         string   `json:"updatedAt,omitempty"`
}

// RoleProject is a project that has roles of its own.
type RoleProject struct {
	Key   string `json:"key"`
	Cwd   string `json:"cwd"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// RoleList is GET /roles.
type RoleList struct {
	Items    []Role        `json:"items"`
	Projects []RoleProject `json:"projects"`
}

// Skill is a skill a role can name (GET /roles/skills). Root says where it was found: project (the task folder's
// .agents/skills or one above it), user (~/.agents/skills), gemini or claude.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Root        string `json:"root"`
}

// Memory is the notes of one project (GET, PUT and POST /memory).
type Memory struct {
	Cwd       string `json:"cwd"`
	Text      string `json:"text"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}
