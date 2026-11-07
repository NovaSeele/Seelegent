package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// maxBranchCwds caps GET /git/branches.
const maxBranchCwds = 50

func (s *Server) gitRoutes() {
	s.handle("GET /tasks/{id}/git", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		g, _, err := s.taskGit(r.Context(), r.PathValue("id"))
		return http.StatusOK, g, err
	})
	s.handle("GET /tasks/{id}/git/diff", s.taskDiff)
	s.handle("POST /tasks/{id}/git/suggest-message", s.suggestMessage)
	s.handle("POST /tasks/{id}/git/suggest-message/dismiss", func(w http.ResponseWriter, r *http.Request) (int, any,
		error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		if _, err := s.o.Tasks.GitScope(r.Context(), id); err != nil {
			return 0, nil, err
		}
		s.mu.Lock()
		delete(s.suggestions, id)
		s.mu.Unlock()
		g, _, err := s.taskGit(r.Context(), id)
		return http.StatusOK, g, err
	})
	s.handle("POST /tasks/{id}/git/commit", s.commit)
	s.handle("GET /tasks/{id}/turns/{n}/outside-diff", func(_ http.ResponseWriter, r *http.Request) (int, any,
		error) {
		if _, err := s.o.Tasks.GitScope(r.Context(), r.PathValue("id")); err != nil {
			return 0, nil, err
		}
		// Needs the per-turn snapshots of milestone 12.
		return 0, nil, newError(api.CodeUnavailable, "changes outside the task's files are not tracked yet")
	})
	s.handle("GET /git/status", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := absDir(r.URL.Query().Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		st, err := s.o.Git.Status(r.Context(), cwd)
		return http.StatusOK, st, err
	})
	s.handle("GET /git/diff", s.repoDiff)
	s.handle("GET /git/branches", s.branches)
}

// taskGit returns the git view of a task with its commit message suggestion.
func (s *Server) taskGit(ctx context.Context, id string) (api.TaskGit, task.GitScope, error) {
	scope, err := s.o.Tasks.GitScope(ctx, id)
	if err != nil {
		return api.TaskGit{}, scope, err
	}
	g, err := s.o.Git.TaskGit(ctx, taskFiles(scope))
	if err != nil {
		return api.TaskGit{}, scope, err
	}
	s.mu.Lock()
	if sg, ok := s.suggestions[id]; ok {
		g.Suggestion = &sg
	}
	s.mu.Unlock()
	return g, scope, nil
}

func taskFiles(g task.GitScope) gitx.TaskFiles {
	return gitx.TaskFiles{Cwd: g.Cwd, Baseline: g.Baseline, Files: g.Files}
}

// taskDiff is GET /tasks/{id}/git/diff?path=: one file of the task against HEAD (turn is accepted; per-turn diffs
// need the snapshots of milestone 12 and show the current change).
func (s *Server) taskDiff(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	if _, err := queryInt(r, "turn", 0, 1<<30); err != nil {
		return 0, nil, err
	}
	g, _, err := s.taskGit(r.Context(), r.PathValue("id"))
	if err != nil {
		return 0, nil, err
	}
	if !g.Repo {
		return 0, nil, invalid("the task's folder is not in a git repository")
	}
	rel, err := gitx.CleanPath(g.Root, r.URL.Query().Get("path"))
	if err != nil {
		return 0, nil, withPathField(err)
	}
	untracked := false
	for _, f := range g.Files {
		if f.Path == rel {
			untracked = f.Status == api.GitUntracked
		}
	}
	d, err := s.o.Git.Diff(r.Context(), g.Root, rel, "", untracked)
	return http.StatusOK, d, err
}

// withPathField names the path query in an invalid path error.
func withPathField(err error) error {
	e := toAPIError(err)
	if e.code == api.CodeInvalidArgument {
		e = e.withField("path")
	}
	return e
}

// suggestMessage is POST /tasks/{id}/git/suggest-message. The suggestion is a local summary of the task's title,
// last response and files, so it is ready at once; the answer is still 202 as the contract says, and task.updated
// tells other dashboards.
func (s *Server) suggestMessage(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	if err := decode(w, r, &struct{}{}); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("id")
	g, scope, err := s.taskGit(r.Context(), id)
	if err != nil {
		return 0, nil, err
	}
	sg := api.Suggestion{State: api.SuggestionReady, Message: gitx.SuggestMessage(scope.Title, scope.LastResponse,
		g.Files)}
	if len(g.Files) == 0 {
		sg = api.Suggestion{State: api.SuggestionFailed, Message: ""}
	}
	s.mu.Lock()
	s.suggestions[id] = sg
	s.mu.Unlock()
	g.Suggestion = &sg
	s.publishTask(r.Context(), id)
	return http.StatusAccepted, g, nil
}

// publishTask sends task.updated for a change the engine does not know about (git suggestion, commit).
func (s *Server) publishTask(ctx context.Context, id string) {
	if sum, err := s.o.Tasks.Summary(ctx, id); err == nil {
		s.publish(api.StreamTaskUpdated, sum)
	}
}

// commit is POST /tasks/{id}/git/commit: exactly the given files of the task (contract §3.3).
func (s *Server) commit(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var req api.CommitRequest
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	id := r.PathValue("id")
	scope, err := s.o.Tasks.GitScope(r.Context(), id)
	if err != nil {
		return 0, nil, err
	}
	if task.Active(scope.Status) || scope.Manual {
		return 0, nil, newError(api.CodeBadState, "task %s is %s; commit after its turn ended", id, scope.Status).
			with("status", string(scope.Status))
	}
	res, err := s.o.Git.Commit(r.Context(), taskFiles(scope), req.Message, req.Files)
	if err != nil {
		return 0, nil, err
	}
	s.mu.Lock()
	delete(s.suggestions, id)
	s.mu.Unlock()
	s.publishTask(r.Context(), id)
	return http.StatusOK, res, nil
}

// repoDiff is GET /git/diff?cwd=&path=&base=&untracked=.
func (s *Server) repoDiff(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	q := r.URL.Query()
	cwd, err := absDir(q.Get("cwd"))
	if err != nil {
		return 0, nil, err
	}
	untracked, err := queryBool(r, "untracked")
	if err != nil {
		return 0, nil, err
	}
	root, err := s.o.Git.Root(r.Context(), cwd)
	if err != nil {
		return 0, nil, err
	}
	rel, err := gitx.CleanPath(root, q.Get("path"))
	if err != nil {
		return 0, nil, withPathField(err)
	}
	d, err := s.o.Git.Diff(r.Context(), root, rel, q.Get("base"), untracked)
	return http.StatusOK, d, err
}

// branches is GET /git/branches?cwd=...: the branch of each folder (null outside git, or when the folder is
// gone), keyed by the cwd as given.
func (s *Server) branches(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	cwds := r.URL.Query()["cwd"]
	if len(cwds) > maxBranchCwds {
		return 0, nil, invalid("at most %d cwd values", maxBranchCwds).withField("cwd")
	}
	out := make(map[string]*api.BranchInfo, len(cwds))
	for _, raw := range cwds {
		if !filepath.IsAbs(raw) {
			return 0, nil, invalid("cwd must be an absolute path (got %q)", raw).withField("cwd")
		}
		if st, err := os.Stat(raw); err != nil || !st.IsDir() { //nolint:gosec // only whether the folder exists
			out[raw] = nil
			continue
		}
		info, err := s.o.Git.Branch(r.Context(), raw)
		if err != nil {
			s.log.Debug("git branches", "err", err)
			info = nil
		}
		out[raw] = info
	}
	return http.StatusOK, out, nil
}
