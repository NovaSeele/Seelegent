package client

import (
	"context"
	"net/http"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The GitHub flow and the automations (milestone 15). The token routes and the automation confirmation are
// dashboard-only and have no client method.

// GitHubAccess is GET /github/access.
func (c *Client) GitHubAccess(ctx context.Context, refresh bool) (api.GitHubAccess, error) {
	var v api.GitHubAccess
	q := ""
	if refresh {
		q = query("refresh", "1")
	}
	_, err := c.Do(ctx, http.MethodGet, "/github/access"+q, nil, &v)
	return v, err
}

// Repos is GET /github/repos.
func (c *Client) Repos(ctx context.Context) ([]api.Repo, error) {
	var v []api.Repo
	_, err := c.Do(ctx, http.MethodGet, "/github/repos", nil, &v)
	return v, err
}

// ConnectRepo is POST /github/repos.
func (c *Client) ConnectRepo(ctx context.Context, req api.RepoConnectRequest) (api.Repo, error) {
	var v api.Repo
	_, err := c.Do(ctx, http.MethodPost, "/github/repos", req, &v)
	return v, err
}

// UpdateRepo is PATCH /github/repos/{id}.
func (c *Client) UpdateRepo(ctx context.Context, id string, p api.RepoPatch) (api.Repo, error) {
	var v api.Repo
	_, err := c.Do(ctx, http.MethodPatch, "/github/repos/"+seg(id), p, &v)
	return v, err
}

// DisconnectRepo is DELETE /github/repos/{id}.
func (c *Client) DisconnectRepo(ctx context.Context, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/github/repos/"+seg(id), nil, nil)
	return err
}

// CreateRemote is POST /github/repos/{id}/remote.
func (c *Client) CreateRemote(ctx context.Context, id string, req api.RemoteRequest) (api.Repo, error) {
	var v api.Repo
	_, err := c.Do(ctx, http.MethodPost, "/github/repos/"+seg(id)+"/remote", req, &v)
	return v, err
}

// SyncRepo is POST /github/repos/{id}/sync.
func (c *Client) SyncRepo(ctx context.Context, id string) (api.SyncResult, error) {
	var v api.SyncResult
	_, err := c.Do(ctx, http.MethodPost, "/github/repos/"+seg(id)+"/sync", nil, &v)
	return v, err
}

// Issues is GET /github/repos/{id}/issues (state open, closed or all; "" is open).
func (c *Client) Issues(ctx context.Context, id, state string) ([]api.Issue, error) {
	var v []api.Issue
	_, err := c.Do(ctx, http.MethodGet, "/github/repos/"+seg(id)+"/issues"+query("state", state), nil, &v)
	return v, err
}

// Issue is GET /github/repos/{id}/issues/{key}.
func (c *Client) Issue(ctx context.Context, id, key string) (api.Issue, error) {
	var v api.Issue
	_, err := c.Do(ctx, http.MethodGet, "/github/repos/"+seg(id)+"/issues/"+seg(key), nil, &v)
	return v, err
}

// AddIssue is POST /github/repos/{id}/issues (a local issue).
func (c *Client) AddIssue(ctx context.Context, id string, in api.IssueInput) (api.Issue, error) {
	var v api.Issue
	_, err := c.Do(ctx, http.MethodPost, "/github/repos/"+seg(id)+"/issues", in, &v)
	return v, err
}

// SuggestIssue is POST /github/repos/{id}/issues/{key}/suggest.
func (c *Client) SuggestIssue(ctx context.Context, id, key string, req api.SuggestRequest) (api.Issue, error) {
	var v api.Issue
	_, err := c.Do(ctx, http.MethodPost, "/github/repos/"+seg(id)+"/issues/"+seg(key)+"/suggest", req, &v)
	return v, err
}

// TriageIssue is POST /github/repos/{id}/issues/{key}/triage.
func (c *Client) TriageIssue(ctx context.Context, id, key string, req api.TriageRequest) (api.TriageResult, error) {
	var v api.TriageResult
	_, err := c.Do(ctx, http.MethodPost, "/github/repos/"+seg(id)+"/issues/"+seg(key)+"/triage", req, &v)
	return v, err
}

// Tickets is GET /github/tickets.
func (c *Client) Tickets(ctx context.Context, repoID string, statuses []string) ([]api.TicketSummary, error) {
	var v []api.TicketSummary
	_, err := c.Do(ctx, http.MethodGet, "/github/tickets"+query("repo", repoID, "status", strings.Join(statuses, ",")),
		nil, &v)
	return v, err
}

// Ticket is GET /github/tickets/{id}.
func (c *Client) Ticket(ctx context.Context, id string) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodGet, "/github/tickets/"+seg(id), nil, &v)
	return v, err
}

// TicketTask is POST /github/tickets/{id}/tasks.
func (c *Client) TicketTask(ctx context.Context, id string, req api.TicketTaskRequest) (api.TicketTaskResult, error) {
	var v api.TicketTaskResult
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/tasks", req, &v)
	return v, err
}

// TicketPR is POST /github/tickets/{id}/pr.
func (c *Client) TicketPR(ctx context.Context, id string, req api.PRRequest) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/pr", req, &v)
	return v, err
}

// LinkPR is POST /github/tickets/{id}/link-pr.
func (c *Client) LinkPR(ctx context.Context, id string, req api.LinkPRRequest) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/link-pr", req, &v)
	return v, err
}

// TicketComment is POST /github/tickets/{id}/comment.
func (c *Client) TicketComment(ctx context.Context, id string, req api.CommentRequest) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/comment", req, &v)
	return v, err
}

// DropTicket is POST /github/tickets/{id}/drop.
func (c *Client) DropTicket(ctx context.Context, id string, req api.DropRequest) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/drop", req, &v)
	return v, err
}

// CleanupTicket is POST /github/tickets/{id}/cleanup.
func (c *Client) CleanupTicket(ctx context.Context, id string) (api.Ticket, error) {
	var v api.Ticket
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/cleanup", nil, &v)
	return v, err
}

// FixChecks is POST /github/tickets/{id}/fix-checks.
func (c *Client) FixChecks(ctx context.Context, id string, req api.FixChecksRequest) (api.TicketTaskResult, error) {
	var v api.TicketTaskResult
	_, err := c.Do(ctx, http.MethodPost, "/github/tickets/"+seg(id)+"/fix-checks", req, &v)
	return v, err
}

// Automations is GET /automations.
func (c *Client) Automations(ctx context.Context) ([]api.Automation, error) {
	var v []api.Automation
	_, err := c.Do(ctx, http.MethodGet, "/automations", nil, &v)
	return v, err
}

// Automation is GET /automations/{id}.
func (c *Client) Automation(ctx context.Context, id string) (api.Automation, error) {
	var v api.Automation
	_, err := c.Do(ctx, http.MethodGet, "/automations/"+seg(id), nil, &v)
	return v, err
}

// CreateAutomation is POST /automations. The automation is created disabled: in.Enabled must be false here (use
// EnableAutomation to ask for it).
func (c *Client) CreateAutomation(ctx context.Context, in api.AutomationInput) (api.Automation, error) {
	var v api.Automation
	_, err := c.Do(ctx, http.MethodPost, "/automations", in, &v)
	return v, err
}

// UpdateAutomation is PATCH /automations/{id}. p.Enabled must not be true here (use EnableAutomation).
func (c *Client) UpdateAutomation(ctx context.Context, id string, p api.AutomationPatch) (api.Automation, error) {
	var v api.Automation
	_, err := c.Do(ctx, http.MethodPatch, "/automations/"+seg(id), p, &v)
	return v, err
}

// DeleteAutomation is DELETE /automations/{id}.
func (c *Client) DeleteAutomation(ctx context.Context, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/automations/"+seg(id), nil, nil)
	return err
}

// EnableAutomation is POST /automations/{id}/enable: it only asks; the user confirms in the dashboard.
func (c *Client) EnableAutomation(ctx context.Context, id string) (api.AutomationEnableResult, error) {
	var v api.AutomationEnableResult
	_, err := c.Do(ctx, http.MethodPost, "/automations/"+seg(id)+"/enable", nil, &v)
	return v, err
}

// RunAutomation is POST /automations/{id}/run.
func (c *Client) RunAutomation(ctx context.Context, id string) (api.AutomationRun, error) {
	var v api.AutomationRun
	_, err := c.Do(ctx, http.MethodPost, "/automations/"+seg(id)+"/run", nil, &v)
	return v, err
}

// AutomationRuns is GET /automations/{id}/runs.
func (c *Client) AutomationRuns(ctx context.Context, id string, limit int) ([]api.AutomationRun, error) {
	var v []api.AutomationRun
	_, err := c.Do(ctx, http.MethodGet, "/automations/"+seg(id)+"/runs"+query("limit", positive(limit)), nil, &v)
	return v, err
}

// PreviewSchedule is POST /automations/preview.
func (c *Client) PreviewSchedule(ctx context.Context, req api.SchedulePreviewRequest) (api.SchedulePreview, error) {
	var v api.SchedulePreview
	_, err := c.Do(ctx, http.MethodPost, "/automations/preview", req, &v)
	return v, err
}
