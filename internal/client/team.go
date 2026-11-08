package client

import (
	"context"
	"net/http"
	"strconv"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Members is GET /members.
func (c *Client) Members(ctx context.Context) ([]api.Member, error) {
	var v []api.Member
	_, err := c.Do(ctx, http.MethodGet, "/members", nil, &v)
	return v, err
}

// CreateMember is POST /members.
func (c *Client) CreateMember(ctx context.Context, in api.MemberInput) (api.Member, error) {
	var v api.Member
	_, err := c.Do(ctx, http.MethodPost, "/members", in, &v)
	return v, err
}

// PatchMember is PATCH /members/{id}: the fields of patch merged onto the member.
func (c *Client) PatchMember(ctx context.Context, id string, patch map[string]any) (api.Member, error) {
	var v api.Member
	_, err := c.Do(ctx, http.MethodPatch, "/members/"+seg(id), patch, &v)
	return v, err
}

// DeleteMember is DELETE /members/{id}.
func (c *Client) DeleteMember(ctx context.Context, id string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/members/"+seg(id), nil, nil)
	return err
}

// MemberTrack is GET /members/track.
func (c *Client) MemberTrack(ctx context.Context) ([]api.MemberTrack, error) {
	var v []api.MemberTrack
	_, err := c.Do(ctx, http.MethodGet, "/members/track", nil, &v)
	return v, err
}

// Consult is POST /tasks/{id}/consult.
func (c *Client) Consult(ctx context.Context, id string, req api.ConsultRequest) (api.Consult, error) {
	var v api.Consult
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/consult", req, &v)
	return v, err
}

// Notes is GET /tasks/{id}/notes.
func (c *Client) Notes(ctx context.Context, id string) ([]api.Note, error) {
	var v []api.Note
	_, err := c.Do(ctx, http.MethodGet, "/tasks/"+seg(id)+"/notes", nil, &v)
	return v, err
}

// AddNote is POST /tasks/{id}/notes.
func (c *Client) AddNote(ctx context.Context, id string, in api.NoteInput) (api.Note, error) {
	var v api.Note
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/notes", in, &v)
	return v, err
}

// PatchNote is PATCH /tasks/{id}/notes/{noteId}.
func (c *Client) PatchNote(ctx context.Context, id, noteID string, p api.NotePatch) (api.Note, error) {
	var v api.Note
	_, err := c.Do(ctx, http.MethodPatch, "/tasks/"+seg(id)+"/notes/"+seg(noteID), p, &v)
	return v, err
}

// DeleteNote is DELETE /tasks/{id}/notes/{noteId}.
func (c *Client) DeleteNote(ctx context.Context, id, noteID string) error {
	_, err := c.Do(ctx, http.MethodDelete, "/tasks/"+seg(id)+"/notes/"+seg(noteID), nil, nil)
	return err
}

// SendNotes is POST /tasks/{id}/notes/send.
func (c *Client) SendNotes(ctx context.Context, id string, req api.NotesSendRequest) (api.NotesSendResult, error) {
	var v api.NotesSendResult
	_, err := c.Do(ctx, http.MethodPost, "/tasks/"+seg(id)+"/notes/send", req, &v)
	return v, err
}

// Meetings is GET /meetings.
func (c *Client) Meetings(ctx context.Context, limit int) ([]api.MeetingSummary, error) {
	var v []api.MeetingSummary
	_, err := c.Do(ctx, http.MethodGet, "/meetings"+query("limit", positive(limit)), nil, &v)
	return v, err
}

// OpenMeeting is POST /meetings.
func (c *Client) OpenMeeting(ctx context.Context, req api.MeetingRequest) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodPost, "/meetings", req, &v)
	return v, err
}

// EstimateMeeting is POST /meetings/estimate.
func (c *Client) EstimateMeeting(ctx context.Context, req api.MeetingRequest) (api.RunEstimate, error) {
	var v api.RunEstimate
	_, err := c.Do(ctx, http.MethodPost, "/meetings/estimate", req, &v)
	return v, err
}

// Meeting is GET /meetings/{id}.
func (c *Client) Meeting(ctx context.Context, id string) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodGet, "/meetings/"+seg(id), nil, &v)
	return v, err
}

// WaitMeeting is GET /meetings/{id}/wait (seconds 0..1500).
func (c *Client) WaitMeeting(ctx context.Context, id string, seconds int) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodGet, "/meetings/"+seg(id)+"/wait"+query("seconds", strconv.Itoa(seconds)), nil, &v)
	return v, err
}

// PostMeeting is POST /meetings/{id}/post.
func (c *Client) PostMeeting(ctx context.Context, id string, req api.MeetingPostRequest) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodPost, "/meetings/"+seg(id)+"/post", req, &v)
	return v, err
}

// DecideMeeting is POST /meetings/{id}/decide.
func (c *Client) DecideMeeting(ctx context.Context, id string, req api.MeetingDecideRequest) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodPost, "/meetings/"+seg(id)+"/decide", req, &v)
	return v, err
}

// ActMeeting is POST /meetings/{id}/act.
func (c *Client) ActMeeting(ctx context.Context, id string, req api.MeetingActRequest) (api.MeetingActResult, error) {
	var v api.MeetingActResult
	_, err := c.Do(ctx, http.MethodPost, "/meetings/"+seg(id)+"/act", req, &v)
	return v, err
}

// CancelMeeting is POST /meetings/{id}/cancel.
func (c *Client) CancelMeeting(ctx context.Context, id string) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodPost, "/meetings/"+seg(id)+"/cancel", nil, &v)
	return v, err
}

// DissentOutcome is POST /meetings/{id}/dissents/{dissentId}/outcome.
func (c *Client) DissentOutcome(ctx context.Context, id, dissentID, outcome string) (api.Meeting, error) {
	var v api.Meeting
	_, err := c.Do(ctx, http.MethodPost, "/meetings/"+seg(id)+"/dissents/"+seg(dissentID)+"/outcome",
		api.DissentOutcomeRequest{Outcome: outcome}, &v)
	return v, err
}

// Contests is GET /contests.
func (c *Client) Contests(ctx context.Context, limit int) ([]api.ContestSummary, error) {
	var v []api.ContestSummary
	_, err := c.Do(ctx, http.MethodGet, "/contests"+query("limit", positive(limit)), nil, &v)
	return v, err
}

// CreateContest is POST /contests.
func (c *Client) CreateContest(ctx context.Context, req api.ContestRequest) (api.Contest, error) {
	var v api.Contest
	_, err := c.Do(ctx, http.MethodPost, "/contests", req, &v)
	return v, err
}

// EstimateContest is POST /contests/estimate.
func (c *Client) EstimateContest(ctx context.Context, req api.ContestRequest) (api.ContestEstimate, error) {
	var v api.ContestEstimate
	_, err := c.Do(ctx, http.MethodPost, "/contests/estimate", req, &v)
	return v, err
}

// Contest is GET /contests/{id}.
func (c *Client) Contest(ctx context.Context, id string) (api.Contest, error) {
	var v api.Contest
	_, err := c.Do(ctx, http.MethodGet, "/contests/"+seg(id), nil, &v)
	return v, err
}

// WaitContest is GET /contests/{id}/wait (seconds 0..1500).
func (c *Client) WaitContest(ctx context.Context, id string, seconds int) (api.Contest, error) {
	var v api.Contest
	_, err := c.Do(ctx, http.MethodGet, "/contests/"+seg(id)+"/wait"+query("seconds", strconv.Itoa(seconds)), nil, &v)
	return v, err
}

// ContestDiff is GET /contests/{id}/variants/{label}/diff.
func (c *Client) ContestDiff(ctx context.Context, id, label string) (api.ContestDiff, error) {
	var v api.ContestDiff
	_, err := c.Do(ctx, http.MethodGet, "/contests/"+seg(id)+"/variants/"+seg(label)+"/diff", nil, &v)
	return v, err
}

// PickContest is POST /contests/{id}/pick.
func (c *Client) PickContest(ctx context.Context, id string, req api.ContestPickRequest) (api.Contest, error) {
	var v api.Contest
	_, err := c.Do(ctx, http.MethodPost, "/contests/"+seg(id)+"/pick", req, &v)
	return v, err
}

// CancelContest is POST /contests/{id}/cancel.
func (c *Client) CancelContest(ctx context.Context, id string) (api.Contest, error) {
	var v api.Contest
	_, err := c.Do(ctx, http.MethodPost, "/contests/"+seg(id)+"/cancel", nil, &v)
	return v, err
}

// Hooks is GET /hooks.
func (c *Client) Hooks(ctx context.Context) ([]api.Hook, error) {
	var v []api.Hook
	_, err := c.Do(ctx, http.MethodGet, "/hooks", nil, &v)
	return v, err
}

// ReplaceHooks is PUT /hooks. A nil list is refused by the daemon; an empty one removes every hook.
func (c *Client) ReplaceHooks(ctx context.Context, hooks []api.Hook) ([]api.Hook, error) {
	var v []api.Hook
	_, err := c.Do(ctx, http.MethodPut, "/hooks", api.HooksRequest{Hooks: hooks}, &v)
	return v, err
}

// TestHook is POST /hooks/{id}/test.
func (c *Client) TestHook(ctx context.Context, id string) (api.HookRun, error) {
	var v api.HookRun
	_, err := c.Do(ctx, http.MethodPost, "/hooks/"+seg(id)+"/test", nil, &v)
	return v, err
}

// positive is n as text, or "" when it is not positive (the query then leaves it out).
func positive(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}
