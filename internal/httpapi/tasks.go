package httpapi

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// maxWaitIDs caps the ids of one wait (old B19: no cap).
const maxWaitIDs = 200

func (s *Server) taskRoutes() {
	s.handle("GET /tasks", s.listTasks)
	s.handle("GET /tasks/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		d, err := s.o.Tasks.Get(r.Context(), r.PathValue("id"))
		return http.StatusOK, d, err
	})
	s.handle("GET /tasks/{id}/events", s.taskEvents)
	s.handle("GET /tasks/{id}/report", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		cwd, err := optionalCwd(r.URL.Query().Get("cwd"))
		if err != nil {
			return 0, nil, err
		}
		rep, err := s.o.Tasks.Report(r.Context(), r.PathValue("id"), cwd)
		return http.StatusOK, rep, err
	})
	s.handle("GET /tasks/{id}/prompt", s.taskPrompt)
	s.handle("POST /tasks", s.delegate)
	s.handle("POST /tasks/{id}/followup", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.FollowupRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if err := oneOf("by", req.By, true, "lead", "user"); err != nil {
			return 0, nil, err
		}
		if err := oneOf("tier", req.Tier, true, tiers()...); err != nil {
			return 0, nil, err
		}
		sum, err := s.o.Tasks.Followup(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, sum, err
	})
	s.handle("POST /tasks/{id}/review", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.ReviewRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		// Old B15: a mistyped verdict became "rejected"; here it is refused.
		if err := oneOf("verdict", req.Verdict, false, api.VerdictAccepted, api.VerdictRejected); err != nil {
			return 0, nil, err
		}
		sum, err := s.o.Tasks.Review(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, sum, err
	})
	s.handle("POST /tasks/{id}/cancel", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.CancelRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if req.WaitSeconds < 0 || req.WaitSeconds > 60 {
			return 0, nil, invalid("waitSeconds must be 0..60").withField("waitSeconds")
		}
		sum, stopped, err := s.o.Tasks.Cancel(r.Context(), r.PathValue("id"), req)
		if stopped {
			return http.StatusOK, sum, err
		}
		return http.StatusAccepted, sum, err
	})
	s.handle("POST /tasks/{id}/answer", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.AnswerRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		sum, err := s.o.Tasks.Answer(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, sum, err
	})
	s.handle("POST /tasks/{id}/attention/dismiss", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		sum, err := s.o.Tasks.DismissAttention(r.Context(), r.PathValue("id"))
		return http.StatusOK, sum, err
	})
	s.handle("POST /tasks/{id}/takeover", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		sum, err := s.o.Tasks.Takeover(r.Context(), r.PathValue("id"))
		return http.StatusOK, sum, err
	})
	s.handle("POST /wait", s.wait)
}

func tiers() []string {
	return []string{string(api.TierFast), string(api.TierStandard), string(api.TierStrong), string(api.TierMax)}
}

// optionalCwd checks an optional absolute cwd ("" stays "").
func optionalCwd(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return absDir(raw)
}

// listTasks is GET /tasks: newest first by (createdAt, id) with a keyset cursor.
func (s *Server) listTasks(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	q := r.URL.Query()
	limit, err := queryInt(r, "limit", 50, 200)
	if err != nil {
		return 0, nil, err
	}
	query := task.Query{ProjectKey: q.Get("project"), Text: q.Get("q"), Limit: limit, Cursor: q.Get("cursor")}
	if raw := q.Get("status"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			st := task.Status(strings.TrimSpace(part))
			if !slices.Contains(task.Statuses(), st) {
				return 0, nil, invalid("unknown status %q", part).withField("status")
			}
			query.Statuses = append(query.Statuses, st)
		}
	}
	page, err := s.o.Tasks.List(r.Context(), query)
	return http.StatusOK, page, err
}

// taskEvents is GET /tasks/{id}/events: after+limit, or tail (the newest N, old B6).
func (s *Server) taskEvents(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	q := r.URL.Query()
	if q.Get("tail") != "" && (q.Get("after") != "" || q.Get("limit") != "") {
		return 0, nil, invalid("tail cannot be combined with after or limit").withField("tail")
	}
	limit, err := queryInt(r, "limit", 500, 2000)
	if err != nil {
		return 0, nil, err
	}
	tail, err := queryInt(r, "tail", 0, 2000)
	if err != nil {
		return 0, nil, err
	}
	var after int64
	if raw := q.Get("after"); raw != "" {
		if after, err = strconv.ParseInt(raw, 10, 64); err != nil || after < 0 {
			return 0, nil, invalid("after must be a sequence number (0 or more)").withField("after")
		}
	}
	page, err := s.o.Tasks.Events(r.Context(), r.PathValue("id"), after, limit, tail)
	return http.StatusOK, page, err
}

// taskPrompt is GET /tasks/{id}/prompt?turn=&attempt=.
func (s *Server) taskPrompt(_ http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	if r.URL.Query().Get("turn") == "" {
		return 0, nil, invalid("turn is required").withField("turn")
	}
	turn, err := queryInt(r, "turn", 0, 1<<30)
	if err != nil {
		return 0, nil, err
	}
	attempt, err := queryInt(r, "attempt", 0, 1<<30)
	if err != nil {
		return 0, nil, err
	}
	p, err := s.o.Tasks.Prompt(r.Context(), r.PathValue("id"), turn, attempt)
	return http.StatusOK, api.PromptText{Prompt: p}, err
}

// delegate is POST /tasks. Unknown fields (meetingId, ticketId, kind, ...) are refused by the decoder, so a caller
// cannot attach a task to internal links (old B21).
func (s *Server) delegate(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var req api.DelegateRequest
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	cwd, err := absDir(req.Cwd)
	if err != nil {
		return 0, nil, err
	}
	req.Cwd = cwd
	if err := oneOf("mode", req.Mode, true, "edit", "plan"); err != nil {
		return 0, nil, err
	}
	if err := oneOf("tier", req.Tier, true, tiers()...); err != nil {
		return 0, nil, err
	}
	if err := oneOf("afterMode", req.AfterMode, true, "accepted", "done"); err != nil {
		return 0, nil, err
	}
	sum, err := s.o.Tasks.Delegate(r.Context(), req)
	return http.StatusCreated, sum, err
}

// wait is POST /wait. It ends early when the client goes away; that never touches the tasks (old B4).
func (s *Server) wait(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	var req api.WaitRequest
	if err := decode(w, r, &req); err != nil {
		return 0, nil, err
	}
	if len(req.IDs) == 0 {
		return 0, nil, invalid("ids are required").withField("ids")
	}
	if len(req.IDs) > maxWaitIDs {
		return 0, nil, invalid("at most %d ids", maxWaitIDs).withField("ids")
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > 3000 {
		return 0, nil, invalid("timeoutSeconds must be 0..3000").withField("timeoutSeconds")
	}
	cwd, err := optionalCwd(r.URL.Query().Get("cwd"))
	if err != nil {
		return 0, nil, err
	}
	// The daemon shutting down (Close: POST /daemon/stop or Ctrl+C) ends the wait at once with daemon_stopping, so
	// the shutdown does not wait out open waits and the caller knows not to start the daemon again.
	wctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	defer context.AfterFunc(s.ctx, func() { cancel(errStopping) })()
	res, err := s.o.Tasks.Wait(wctx, req, cwd)
	switch {
	case r.Context().Err() != nil:
		return 0, nil, nil // the client left; nobody reads an answer
	case errors.Is(context.Cause(wctx), errStopping) && (err != nil || res.Woke == api.WokeCancelled):
		// (A wait that ended on its own just before is answered normally.)
		return 0, nil, translated(api.CodeDaemonStopping, "stopping")
	}
	return http.StatusOK, res, err
}

// errStopping is the cause that ends a wait when the server closes.
var errStopping = errors.New("the daemon is stopping")
