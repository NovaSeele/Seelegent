package httpapi

import (
	"context"
	"net/http"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/role"
)

func (s *Server) settingsRoutes() {
	s.handle("GET /settings", func(http.ResponseWriter, *http.Request) (int, any, error) {
		return http.StatusOK, s.o.Settings.Settings(), nil
	})
	s.handle("PATCH /settings", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		body, err := readBody(w, r)
		if err != nil {
			return 0, nil, err
		}
		set, err := s.o.Settings.PatchSettings(r.Context(), body)
		if err != nil {
			return 0, nil, err
		}
		s.publish(api.StreamSettingsUpdated, set)
		s.o.Pool.Notify()
		s.kickAccounts()
		return http.StatusOK, set, nil
	})
	s.handle("GET /memory", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := absDir(r.URL.Query().Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		n, err := s.o.Memory.Get(r.Context(), cwd)
		return http.StatusOK, n.API(), err
	})
	s.handle("PUT /memory", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return s.writeMemory(w, r, s.o.Memory.Replace)
	})
	s.handle("POST /memory", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		return s.writeMemory(w, r, s.o.Memory.Append)
	})
	s.roleRoutes()
}

// writeMemory replaces or appends to a project's notes.
func (s *Server) writeMemory(w http.ResponseWriter, r *http.Request,
	write func(ctx context.Context, cwd, text string) (memory.Note, error)) (code int, body any, err error) {
	var req api.MemoryRequest
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	cwd, err := absDir(req.Cwd)
	if err != nil {
		return 0, nil, err
	}
	n, err := write(r.Context(), cwd, req.Text)
	if err != nil {
		return 0, nil, err
	}
	s.publish(api.StreamMemoryChanged, api.MemoryChanged{Cwd: n.Cwd})
	return http.StatusOK, n.API(), nil
}

func (s *Server) roleRoutes() {
	s.handle("GET /roles", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := optionalCwd(r.URL.Query().Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		all, err := queryBool(r, "all")
		if err != nil {
			return 0, nil, err
		}
		roles, err := s.o.Roles.List(r.Context(), cwd, all)
		if err != nil {
			return 0, nil, err
		}
		projects, err := s.o.Roles.Projects(r.Context())
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, role.ListToAPI(roles, projects), nil
	})
	s.handle("GET /roles/templates", func(http.ResponseWriter, *http.Request) (int, any, error) {
		t := role.Templates()
		out := make([]api.Role, 0, len(t))
		for i := range t {
			out = append(out, role.ToAPI(t[i]))
		}
		return http.StatusOK, out, nil
	})
	s.handle("GET /roles/skills", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := optionalCwd(r.URL.Query().Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, role.SkillsToAPI(s.o.Roles.Skills(cwd)), nil
	})
	s.handle("POST /roles", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.RoleRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if err := oneOf("scope", req.Scope, false, role.ScopeGlobal, role.ScopeProject); err != nil {
			return 0, nil, err
		}
		cwd := ""
		if req.Scope == role.ScopeProject || req.Cwd != "" {
			var err error
			if cwd, err = absDir(req.Cwd); err != nil {
				return 0, nil, err
			}
		}
		in := req.Role
		saved, err := s.o.Roles.Upsert(r.Context(), role.Role{ID: in.ID, Name: in.Name, Description: in.Description,
			Rules: in.Rules, OutputStyle: in.OutputStyle, Skills: in.Skills, DefaultTier: in.DefaultTier,
			DefaultEffort: in.DefaultEffort, PreferredAccounts: in.PreferredAccounts}, req.Scope, cwd)
		if err != nil {
			return 0, nil, err
		}
		s.changed(api.StreamRolesChanged)
		return http.StatusCreated, role.ToAPI(saved), nil
	})
	s.handle("DELETE /roles/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		q := r.URL.Query()
		scope := q.Get("scope")
		if err := oneOf("scope", scope, false, role.ScopeGlobal, role.ScopeProject); err != nil {
			return 0, nil, err
		}
		found, err := s.o.Roles.Delete(r.Context(), r.PathValue("id"), scope, q.Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		if !found {
			return 0, nil, newError(api.CodeNotFound, "role %s not found", r.PathValue("id"))
		}
		s.changed(api.StreamRolesChanged)
		return http.StatusNoContent, nil, nil
	})
}
