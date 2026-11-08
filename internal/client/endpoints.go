package client

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Health is GET /health.
func (c *Client) Health(ctx context.Context) (api.Health, error) {
	var h api.Health
	_, err := c.Do(ctx, http.MethodGet, "/health", nil, &h)
	return h, err
}

// Delegate is POST /tasks.
func (c *Client) Delegate(ctx context.Context, req api.DelegateRequest) (api.TaskSummary, error) {
	var s api.TaskSummary
	_, err := c.Do(ctx, http.MethodPost, "/tasks", req, &s)
	return s, err
}

// Followup is POST /tasks/{id}/followup.
func (c *Client) Followup(ctx context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error) {
	var s api.TaskSummary
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/followup", req, &s)
	return s, err
}

// Review is POST /tasks/{id}/review.
func (c *Client) Review(ctx context.Context, id string, req api.ReviewRequest) (api.TaskSummary, error) {
	var s api.TaskSummary
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/review", req, &s)
	return s, err
}

// Cancel is POST /tasks/{id}/cancel. stopped is false when the worker was still stopping when the call's wait
// ended (202).
func (c *Client) Cancel(ctx context.Context, id string, req api.CancelRequest) (sum api.TaskSummary, stopped bool,
	err error) {
	status, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/cancel", req, &sum)
	return sum, status == http.StatusOK, err
}

// Wait is POST /wait. cwd (optional, absolute) is the folder the reports show files relative to. since (zero: none)
// is a mark taken before the caller created or followed up the tasks: a flag raised at or after it wakes the wait
// although it was there when the wait began.
func (c *Client) Wait(ctx context.Context, req api.WaitRequest, cwd string, since time.Time) (api.WaitResult, error) {
	var res api.WaitResult
	_, err := c.Do(ctx, http.MethodPost, "/wait"+query("cwd", cwd, "since", api.FormatTime(since)), req, &res)
	return res, err
}

// Task is GET /tasks/{id}.
func (c *Client) Task(ctx context.Context, id string) (api.TaskDetail, error) {
	var d api.TaskDetail
	_, err := c.Do(ctx, http.MethodGet, "/tasks/"+seg(id), nil, &d)
	return d, err
}

// Restore is POST /tasks/{id}/turns/{n}/restore.
func (c *Client) Restore(ctx context.Context, id string, turn int, req api.RestoreRequest) (api.RestoreResult,
	error) {
	var res api.RestoreResult
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/turns/"+strconv.Itoa(turn)+"/restore", req, &res)
	return res, err
}

// Report is GET /tasks/{id}/report. cwd (optional, absolute) is the folder files are shown relative to.
func (c *Client) Report(ctx context.Context, id, cwd string) (api.TaskReport, error) {
	var r api.TaskReport
	_, err := c.Do(ctx, http.MethodGet, "/tasks/"+seg(id)+"/report"+query("cwd", cwd), nil, &r)
	return r, err
}

// TaskQuery filters GET /tasks.
type TaskQuery struct {
	Statuses []api.TaskStatus
	Project  string
	Text     string
	Limit    int
	Cursor   string
}

// Tasks is GET /tasks: one page, newest first.
func (c *Client) Tasks(ctx context.Context, q TaskQuery) (api.TaskPage, error) {
	statuses := make([]string, 0, len(q.Statuses))
	for _, s := range q.Statuses {
		statuses = append(statuses, string(s))
	}
	limit := ""
	if q.Limit > 0 {
		limit = strconv.Itoa(q.Limit)
	}
	var page api.TaskPage
	_, err := c.Do(ctx, http.MethodGet, "/tasks"+query("status", strings.Join(statuses, ","), "project", q.Project,
		"q", q.Text, "limit", limit, "cursor", q.Cursor), nil, &page)
	return page, err
}

// RawEvent is a task event whose data is kept as JSON.
type RawEvent struct {
	Seq     int64           `json:"seq"`
	TS      string          `json:"ts"`
	TaskID  string          `json:"taskId"`
	Turn    int             `json:"turn,omitempty"`
	Attempt int             `json:"attempt,omitempty"`
	Type    api.EventType   `json:"type"`
	Data    json.RawMessage `json:"data"`
}

// EventPage is a page of GET /tasks/{id}/events.
type EventPage struct {
	Items   []RawEvent `json:"items"`
	LastSeq int64      `json:"lastSeq"`
}

// TailEvents is GET /tasks/{id}/events?tail=n: the newest n events (1..2000), ascending.
func (c *Client) TailEvents(ctx context.Context, id string, n int) (EventPage, error) {
	var page EventPage
	_, err := c.Do(ctx, http.MethodGet, "/tasks/"+seg(id)+"/events"+query("tail", strconv.Itoa(n)), nil, &page)
	return page, err
}

// Accounts is GET /accounts.
func (c *Client) Accounts(ctx context.Context) ([]api.Account, error) {
	var list []api.Account
	_, err := c.Do(ctx, http.MethodGet, "/accounts", nil, &list)
	return list, err
}

// Backends is GET /backends.
func (c *Client) Backends(ctx context.Context) ([]api.Backend, error) {
	var list []api.Backend
	_, err := c.Do(ctx, http.MethodGet, "/backends", nil, &list)
	return list, err
}

// Memory is GET /memory.
func (c *Client) Memory(ctx context.Context, cwd string) (api.Memory, error) {
	var m api.Memory
	_, err := c.Do(ctx, http.MethodGet, "/memory"+query("cwd", cwd), nil, &m)
	return m, err
}

// AppendMemory is POST /memory.
func (c *Client) AppendMemory(ctx context.Context, cwd, text string) (api.Memory, error) {
	var m api.Memory
	_, err := c.Do(ctx, http.MethodPost, "/memory", api.Memory{Cwd: cwd, Text: text}, &m)
	return m, err
}

// ReplaceMemory is PUT /memory.
func (c *Client) ReplaceMemory(ctx context.Context, cwd, text string) (api.Memory, error) {
	var m api.Memory
	_, err := c.Do(ctx, http.MethodPut, "/memory", api.Memory{Cwd: cwd, Text: text}, &m)
	return m, err
}

// Roles is GET /roles. cwd (optional, absolute) adds that project's roles.
func (c *Client) Roles(ctx context.Context, cwd string) (api.RoleList, error) {
	var l api.RoleList
	_, err := c.Do(ctx, http.MethodGet, "/roles"+query("cwd", cwd), nil, &l)
	return l, err
}

// RoleUpsert is the body of POST /roles with the role as given by the caller.
type RoleUpsert struct {
	Role  json.RawMessage `json:"role"`
	Scope string          `json:"scope"`
	Cwd   string          `json:"cwd,omitempty"`
}

// UpsertRole is POST /roles.
func (c *Client) UpsertRole(ctx context.Context, req RoleUpsert) (api.Role, error) {
	var r api.Role
	_, err := c.Do(ctx, http.MethodPost, "/roles", req, &r)
	return r, err
}

// DeleteRole is DELETE /roles/{id}?scope=&cwd=.
func (c *Client) DeleteRole(ctx context.Context, id, scope, cwd string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/roles/"+seg(id)+query("scope", scope, "cwd", cwd), nil, nil)
	return err
}

// Skills is GET /roles/skills.
func (c *Client) Skills(ctx context.Context, cwd string) ([]api.Skill, error) {
	var list []api.Skill
	_, err := c.Do(ctx, http.MethodGet, "/roles/skills"+query("cwd", cwd), nil, &list)
	return list, err
}
