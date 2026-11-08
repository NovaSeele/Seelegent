package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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
	s.handle("GET /tasks/{id}/git/commits/{sha}", s.taskCommit)
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
		n, err := turnNumber(r)
		if err != nil {
			return 0, nil, err
		}
		d, err := s.o.Tasks.OutsideDiff(r.Context(), r.PathValue("id"), n)
		return http.StatusOK, d, err
	})
	s.handle("POST /tasks/{id}/turns/{n}/restore", func(w http.ResponseWriter, r *http.Request) (int, any,
		error) {
		var req api.RestoreRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		n, err := turnNumber(r)
		if err != nil {
			return 0, nil, err
		}
		id := r.PathValue("id")
		res, err := s.o.Tasks.Restore(r.Context(), id, n, req)
		return http.StatusOK, res, err
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

// turnNumber reads the {n} path value: a turn number from 1.
func turnNumber(r *http.Request) (int, error) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		return 0, invalid("the turn must be a positive integer (got %q)", r.PathValue("n")).withField("n")
	}
	return n, nil
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
	return gitx.TaskFiles{Cwd: g.Cwd, Baseline: g.Baseline, Files: g.Files, Start: g.Start, Turns: g.Turns}
}

// taskDiff is GET /tasks/{id}/git/diff?path=&base=: one file of the task in the working tree against the task's
// start (base=start, the default: committed and uncommitted work together) or against HEAD (base=head: what a
// commit would take). Without a usable start (TaskGit.startState not recorded) both compare against HEAD. turn is
// accepted; per-turn diffs need the snapshots and show the current change.
func (s *Server) taskDiff(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	if _, err := queryInt(r, "turn", 0, 1<<30); err != nil {
		return 0, nil, err
	}
	against := r.URL.Query().Get("base")
	if against != "" && against != "start" && against != "head" {
		return 0, nil, invalid("base must be start or head (got %q)", against).withField("base")
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
	base := ""
	if against != "head" && g.StartState == api.GitStartRecorded && g.Head != "" {
		base = g.StartHead
		if base == "" { // the repository had no commit when the task started
			base = gitx.EmptyTree(g.Head)
		}
	}
	d, err := s.o.Git.Diff(r.Context(), g.Root, rel, base, untracked)
	return http.StatusOK, d, err
}

// taskCommit is GET /tasks/{id}/git/commits/{sha}: one of the task's commits (start..HEAD) with its diff. A
// commit outside that range is 404 with details.reason "not_in_task"; a task whose start is unknown or unreachable
// is 409 bad_state with details.reason "start_unknown" or "start_unreachable".
func (s *Server) taskCommit(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	sha := r.PathValue("sha")
	if !gitx.ValidSHA(sha) {
		return 0, nil, invalid("the commit must be 4 to 64 hex digits (got %q)", sha).withField("sha")
	}
	scope, err := s.o.Tasks.GitScope(r.Context(), r.PathValue("id"))
	if err != nil {
		return 0, nil, err
	}
	d, err := s.o.Git.TaskCommit(r.Context(), taskFiles(scope), sha)
	var se *gitx.StartError
	switch {
	case err == nil:
		return http.StatusOK, d, nil
	case errors.As(err, &se):
		return 0, nil, newError(api.CodeBadState, "%s", se.Error()).with("reason", "start_"+se.State)
	case errors.Is(err, gitx.ErrNotInTask):
		return 0, nil, newError(api.CodeNotFound, "%s", err.Error()).with("reason", "not_in_task")
	}
	return 0, nil, err
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
