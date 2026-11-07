package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
)

// memoryArgs are the arguments of memory.
type memoryArgs struct {
	Cwd    string `json:"cwd"`
	Action string `json:"action"`
	Text   string `json:"text"`
}

func (s *Server) memory(ctx context.Context, k *call) (result, error) {
	var a memoryArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	// Old C-B18: an unknown action used to append.
	if a.Action != "read" && a.Action != "append" && a.Action != "replace" {
		return result{}, fmt.Errorf("action %q must be read, append or replace", a.Action)
	}
	if strings.TrimSpace(a.Cwd) == "" {
		return result{}, errors.New("cwd is required (the absolute path of the project folder)")
	}
	cwd, err := s.absPath(a.Cwd)
	if err != nil {
		return result{}, err
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	var m api.Memory
	switch a.Action {
	case "read":
		m, err = c.Memory(ctx, cwd)
	case "append":
		m, err = c.AppendMemory(ctx, cwd, a.Text)
	default:
		m, err = c.ReplaceMemory(ctx, cwd, a.Text)
	}
	if err != nil {
		return result{}, err
	}
	return result{value: m, summary: fmt.Sprintf("Project memory of %s: %d characters.", m.Cwd,
		utf8.RuneCountInString(m.Text))}, nil
}

// profilesArgs are the arguments of profiles.
type profilesArgs struct {
	Action string          `json:"action"`
	Cwd    string          `json:"cwd"`
	Scope  string          `json:"scope"`
	ID     string          `json:"id"`
	Role   json.RawMessage `json:"role"`
}

func (s *Server) profiles(ctx context.Context, k *call) (result, error) {
	var a profilesArgs
	if err := k.decode(&a); err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list", "get", "upsert", "delete", "skills":
	default:
		return result{}, fmt.Errorf("unknown profiles action: %s (list, get, upsert, delete or skills)", a.Action)
	}
	cwd, err := s.roleCwd(a.Cwd, a.Action == "delete")
	if err != nil {
		return result{}, err
	}
	scope := a.Scope
	if scope == "" {
		scope = "global"
	}
	switch a.Action {
	case "get", "delete":
		if a.ID == "" {
			return result{}, fmt.Errorf("id is required for %s action", a.Action)
		}
	case "upsert":
		if r := bytes.TrimSpace(a.Role); len(r) == 0 || r[0] != '{' {
			return result{}, errors.New("role object is required for upsert action")
		}
	}
	c, err := k.client(ctx)
	if err != nil {
		return result{}, err
	}
	switch a.Action {
	case "list", "get":
		return listRoles(ctx, c, cwd, a.ID)
	case "upsert":
		r, err := c.UpsertRole(ctx, client.RoleUpsert{Role: a.Role, Scope: scope, Cwd: cwd})
		if err != nil {
			return result{}, err
		}
		return result{value: r, summary: fmt.Sprintf("Role %s saved (%s).", r.ID, r.Scope)}, nil
	case "delete":
		if err := c.DeleteRole(ctx, a.ID, scope, cwd); err != nil {
			return result{}, err
		}
		return result{value: map[string]any{"id": a.ID, "deleted": true}, summary: "Role " + a.ID + " deleted."}, nil
	}
	skills, err := c.Skills(ctx, cwd)
	if err != nil {
		return result{}, err
	}
	return result{value: api.NonNil(skills), summary: fmt.Sprintf("%d skill%s.", len(skills), plural(len(skills)))},
		nil
}

// listRoles answers list (every role without its timestamps) and get (one role).
func listRoles(ctx context.Context, c *client.Client, cwd, id string) (result, error) {
	l, err := c.Roles(ctx, cwd)
	if err != nil {
		return result{}, err
	}
	if id != "" {
		for i := range l.Items {
			if r := &l.Items[i]; r.ID == id {
				return result{value: *r, summary: "Role " + r.ID + " (" + r.Scope + ")."}, nil
			}
		}
		return result{}, fmt.Errorf("role %q not found", id)
	}
	out := l.Items
	for i := range out {
		out[i].CreatedAt, out[i].UpdatedAt = "", ""
	}
	if out == nil {
		out = []api.Role{}
	}
	return result{value: out, summary: fmt.Sprintf("%d role%s.", len(out), plural(len(out)))}, nil
}

// roleCwd resolves the cwd of a profiles call. For delete it may also be a project key (contract §5), which is
// passed as given.
func (s *Server) roleCwd(cwd string, keyAllowed bool) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if keyAllowed && cwd != "" && !filepath.IsAbs(cwd) && !strings.ContainsAny(cwd, `/\`) && !strings.HasPrefix(cwd, ".") {
		return cwd, nil
	}
	return s.absPath(cwd)
}
