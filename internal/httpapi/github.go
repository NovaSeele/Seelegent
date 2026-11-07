package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The GitHub and automation endpoints (milestone 15; described in docs/github.md and docs/automations.md). Each
// service is optional: without it its paths answer 501. Workers may not call any of them (the allow-list is
// unchanged). The token routes and the automation confirmation answer the dashboard only (dashboardOnly).

// Tickets is the GitHub issue to pull request flow (ticket.Service).
type Tickets interface {
	Access(ctx context.Context, force bool) api.GitHubAccess
	SetToken(token string) (api.GitHubTokenResult, error)
	RemoveToken() error
	Repos(ctx context.Context) ([]api.Repo, error)
	Repo(ctx context.Context, id string) (api.Repo, error)
	Connect(ctx context.Context, req api.RepoConnectRequest) (api.Repo, error)
	UpdateRepo(ctx context.Context, id string, patch api.RepoPatch) (api.Repo, error)
	Disconnect(ctx context.Context, id string) error
	CreateRemote(ctx context.Context, id string, req api.RemoteRequest) (api.Repo, error)
	Sync(ctx context.Context, id string) (api.SyncResult, error)
	Issues(ctx context.Context, id, state string) ([]api.Issue, error)
	Issue(ctx context.Context, id, key string) (api.Issue, error)
	AddIssue(ctx context.Context, id string, in api.IssueInput) (api.Issue, error)
	Suggest(ctx context.Context, id, key string, req api.SuggestRequest) (api.Issue, error)
	Triage(ctx context.Context, id, key string, req api.TriageRequest) (api.TriageResult, error)
	Tickets(ctx context.Context, repoID string, statuses []string) ([]api.TicketSummary, error)
	Ticket(ctx context.Context, id string) (api.Ticket, error)
	AddTask(ctx context.Context, id string, req api.TicketTaskRequest) (api.TicketTaskResult, error)
	PR(ctx context.Context, id string, req api.PRRequest) (api.Ticket, error)
	LinkPR(ctx context.Context, id string, req api.LinkPRRequest) (api.Ticket, error)
	Comment(ctx context.Context, id string, req api.CommentRequest) (api.Ticket, error)
	Drop(ctx context.Context, id string, req api.DropRequest) (api.Ticket, error)
	Cleanup(ctx context.Context, id string) (api.Ticket, error)
	FixChecks(ctx context.Context, id string, req api.FixChecksRequest) (api.TicketTaskResult, error)
	Poll(ctx context.Context) error
}

// Automations are the scheduled delegations (automation.Service).
type Automations interface {
	List(ctx context.Context) ([]api.Automation, error)
	Get(ctx context.Context, id string) (api.Automation, error)
	Create(ctx context.Context, in api.AutomationInput) (api.Automation, error)
	Update(ctx context.Context, id string, p api.AutomationPatch) (api.Automation, error)
	Delete(ctx context.Context, id string) error
	RequestEnable(ctx context.Context, id string) (api.AutomationEnableResult, error)
	EnableResult(a api.Automation) api.AutomationEnableResult
	ConfirmToken(ctx context.Context, id, revision string) (api.AutomationConfirmToken, error)
	Confirm(ctx context.Context, id, token string) (api.Automation, error)
	Runs(ctx context.Context, id string, limit int) ([]api.AutomationRun, error)
	RunNow(ctx context.Context, id string) (api.AutomationRun, error)
	Preview(req api.SchedulePreviewRequest) (api.SchedulePreview, error)
}

// dashboardOnly refuses a request that does not come from the dashboard page itself: a browser fetch from the
// daemon's own origin (Origin, Sec-Fetch-Site same-origin, Sec-Fetch-Mode cors or same-origin) carrying
// X-Seelegent-UI: 1. MCP, the CLI and other local programs send none of these; a same-user program that forges
// them is not stopped (docs/automations.md, trust model).
func (s *Server) dashboardOnly(r *http.Request) error {
	refuse := newError(api.CodeForbidden, "only the dashboard can do this; open it and use the page").
		with("reason", "dashboard_only")
	origin := r.Header.Get("Origin")
	if origin == "" || !s.ownOrigin(origin) || r.Header.Get("X-Seelegent-UI") != "1" {
		return refuse
	}
	if r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		return refuse
	}
	switch r.Header.Get("Sec-Fetch-Mode") {
	case "cors", "same-origin":
	default:
		return refuse
	}
	return nil
}

func (s *Server) githubRoutes() {
	t := s.o.Tickets
	if t == nil {
		s.reserve("/github/")
		return
	}
	s.handle("GET /github/access", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		q := r.URL.Query().Get("refresh")
		return http.StatusOK, t.Access(r.Context(), q == "1" || q == "true"), nil
	})
	s.handle("PUT /github/token", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.dashboardOnly(r); err != nil {
			return 0, nil, err
		}
		var req api.GitHubTokenRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if strings.TrimSpace(req.Token) == "" {
			return 0, nil, invalid("token is required").withField("token")
		}
		res, err := t.SetToken(req.Token)
		if err != nil {
			return 0, nil, err
		}
		s.PublishIssuesChanged("")
		return http.StatusOK, res, nil
	})
	s.handle("DELETE /github/token", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.dashboardOnly(r); err != nil {
			return 0, nil, err
		}
		if err := t.RemoveToken(); err != nil {
			return 0, nil, err
		}
		s.PublishIssuesChanged("")
		return http.StatusNoContent, nil, nil
	})
	s.handle("POST /github/poll", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		return http.StatusNoContent, nil, t.Poll(r.Context())
	})
	s.repoRoutes(t)
	s.ticketRoutes(t)
}

func (s *Server) repoRoutes(t Tickets) {
	s.handle("GET /github/repos", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := t.Repos(r.Context())
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /github/repos", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.RepoConnectRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if strings.TrimSpace(req.Path) == "" {
			return 0, nil, invalid("path is required").withField("path")
		}
		repo, err := t.Connect(r.Context(), req)
		return http.StatusCreated, repo, err
	})
	s.handle("GET /github/repos/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		repo, err := t.Repo(r.Context(), r.PathValue("id"))
		return http.StatusOK, repo, err
	})
	s.handle("PATCH /github/repos/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var p api.RepoPatch
		if err := decode(w, r, &p); err != nil {
			return 0, nil, err
		}
		if p.SharedDirs == nil {
			return 0, nil, invalid("sharedDirs is required").withField("sharedDirs")
		}
		repo, err := t.UpdateRepo(r.Context(), r.PathValue("id"), p)
		return http.StatusOK, repo, err
	})
	s.handle("DELETE /github/repos/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		return http.StatusNoContent, nil, t.Disconnect(r.Context(), r.PathValue("id"))
	})
	s.handle("POST /github/repos/{id}/remote", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.RemoteRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		repo, err := t.CreateRemote(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, repo, err
	})
	s.handle("POST /github/repos/{id}/sync", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		res, err := t.Sync(r.Context(), r.PathValue("id"))
		return http.StatusOK, res, err
	})
	s.handle("GET /github/repos/{id}/issues", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		state := r.URL.Query().Get("state")
		switch state {
		case "", "open", "closed", "all":
		default:
			return 0, nil, invalid("state must be open, closed or all").withField("state")
		}
		list, err := t.Issues(r.Context(), r.PathValue("id"), state)
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /github/repos/{id}/issues", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var in api.IssueInput
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		it, err := t.AddIssue(r.Context(), r.PathValue("id"), in)
		return http.StatusCreated, it, err
	})
	s.handle("GET /github/repos/{id}/issues/{key}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		it, err := t.Issue(r.Context(), r.PathValue("id"), r.PathValue("key"))
		return http.StatusOK, it, err
	})
	s.handle("POST /github/repos/{id}/issues/{key}/suggest", func(w http.ResponseWriter, r *http.Request) (int, any,
		error) {
		var req api.SuggestRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		it, err := t.Suggest(r.Context(), r.PathValue("id"), r.PathValue("key"), req)
		return http.StatusOK, it, err
	})
	s.handle("POST /github/repos/{id}/issues/{key}/triage", func(w http.ResponseWriter, r *http.Request) (int, any,
		error) {
		var req api.TriageRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		res, err := t.Triage(r.Context(), r.PathValue("id"), r.PathValue("key"), req)
		return http.StatusOK, res, err
	})
}

func (s *Server) ticketRoutes(t Tickets) {
	s.handle("GET /github/tickets", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		var statuses []string
		if v := r.URL.Query().Get("status"); v != "" {
			statuses = strings.Split(v, ",")
		}
		list, err := t.Tickets(r.Context(), r.URL.Query().Get("repo"), statuses)
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("GET /github/tickets/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		tk, err := t.Ticket(r.Context(), r.PathValue("id"))
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/tasks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.TicketTaskRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		res, err := t.AddTask(r.Context(), r.PathValue("id"), req)
		return http.StatusCreated, res, err
	})
	s.handle("POST /github/tickets/{id}/pr", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.PRRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		tk, err := t.PR(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/link-pr", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.LinkPRRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if req.Number < 1 {
			return 0, nil, invalid("number must be a positive integer (the pull request number)").withField("number")
		}
		tk, err := t.LinkPR(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/comment", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.CommentRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if strings.TrimSpace(req.Body) == "" {
			return 0, nil, invalid("body is required").withField("body")
		}
		tk, err := t.Comment(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/drop", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.DropRequest
		if r.ContentLength != 0 {
			if err := decode(w, r, &req); err != nil {
				return 0, nil, err
			}
		}
		tk, err := t.Drop(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/cleanup", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		tk, err := t.Cleanup(r.Context(), r.PathValue("id"))
		return http.StatusOK, tk, err
	})
	s.handle("POST /github/tickets/{id}/fix-checks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.FixChecksRequest
		if r.ContentLength != 0 {
			if err := decode(w, r, &req); err != nil {
				return 0, nil, err
			}
		}
		res, err := t.FixChecks(r.Context(), r.PathValue("id"), req)
		return http.StatusCreated, res, err
	})
}

func (s *Server) automationRoutes() {
	a := s.o.Automations
	if a == nil {
		s.reserve("/automations", "/automations/")
		return
	}
	s.handle("GET /automations", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := a.List(r.Context())
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /automations", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var in api.AutomationInput
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		got, err := a.Create(r.Context(), in)
		if err != nil {
			return 0, nil, err
		}
		if in.Enabled {
			return http.StatusCreated, a.EnableResult(got), nil
		}
		return http.StatusCreated, got, nil
	})
	s.handle("POST /automations/preview", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.SchedulePreviewRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		p, err := a.Preview(req)
		return http.StatusOK, p, err
	})
	s.handle("GET /automations/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		got, err := a.Get(r.Context(), r.PathValue("id"))
		return http.StatusOK, got, err
	})
	s.handle("PATCH /automations/{id}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var p api.AutomationPatch
		if err := decode(w, r, &p); err != nil {
			return 0, nil, err
		}
		got, err := a.Update(r.Context(), r.PathValue("id"), p)
		if err != nil {
			return 0, nil, err
		}
		if p.Enabled != nil && *p.Enabled {
			return http.StatusOK, a.EnableResult(got), nil
		}
		return http.StatusOK, got, nil
	})
	s.handle("DELETE /automations/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		return http.StatusNoContent, nil, a.Delete(r.Context(), r.PathValue("id"))
	})
	s.handle("POST /automations/{id}/enable", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		res, err := a.RequestEnable(r.Context(), r.PathValue("id"))
		return http.StatusOK, res, err
	})
	s.handle("POST /automations/{id}/confirm-token", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.dashboardOnly(r); err != nil {
			return 0, nil, err
		}
		var req api.AutomationConfirmTokenRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		tok, err := a.ConfirmToken(r.Context(), r.PathValue("id"), req.Revision)
		return http.StatusOK, tok, err
	})
	s.handle("POST /automations/{id}/confirm", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := s.dashboardOnly(r); err != nil {
			return 0, nil, err
		}
		var req api.AutomationConfirmRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		got, err := a.Confirm(r.Context(), r.PathValue("id"), req.Token)
		return http.StatusOK, got, err
	})
	s.handle("POST /automations/{id}/run", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		run, err := a.RunNow(r.Context(), r.PathValue("id"))
		return http.StatusOK, run, err
	})
	s.handle("GET /automations/{id}/runs", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		limit, err := queryInt(r, "limit", 50, 500)
		if err != nil {
			return 0, nil, err
		}
		list, err := a.Runs(r.Context(), r.PathValue("id"), limit)
		return http.StatusOK, api.NonNil(list), err
	})
}

// PublishTicket sends ticket.updated (ticket.Options.TicketChanged).
func (s *Server) PublishTicket(t api.TicketSummary) { s.publish(api.StreamTicketUpdated, t) }

// PublishIssuesChanged sends issues.changed (ticket.Options.IssuesChanged; "" when the access changed).
func (s *Server) PublishIssuesChanged(repoID string) {
	s.publish(api.StreamIssuesChanged, api.IssuesChanged{RepoID: repoID, ChangedAt: api.FormatTime(s.clock())})
}

// PublishAutomation sends automation.updated (automation.Options.Changed).
func (s *Server) PublishAutomation(a api.Automation) { s.publish(api.StreamAutomationUpdated, a) }

// PublishAutomationDeleted sends automation.deleted (automation.Options.Deleted).
func (s *Server) PublishAutomationDeleted(id string) {
	s.publish(api.StreamAutomationDeleted, api.Deleted{ID: id})
}
