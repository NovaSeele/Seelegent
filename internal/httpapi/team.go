package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The team endpoints (milestone 14): department members, consults, review notes, meetings, contests and hooks.
// Each group is optional; without its service its paths answer 501 like the other reserved paths.

// Members is the department (member.Store).
type Members interface {
	Get(ctx context.Context, id string) (api.Member, error)
	List(ctx context.Context) ([]api.Member, error)
	Create(ctx context.Context, in api.MemberInput) (api.Member, error)
	Update(ctx context.Context, id string, in api.MemberInput) (api.Member, error)
	Delete(ctx context.Context, id string) error
}

// Consults asks a member for a second opinion on a task (task.Service).
type Consults interface {
	Consult(ctx context.Context, id string, req api.ConsultRequest) (api.Consult, error)
}

// Notes are the review notes of tasks (notes.Store).
type Notes interface {
	List(ctx context.Context, id string) ([]api.Note, error)
	Add(ctx context.Context, id string, in api.NoteInput) (api.Note, error)
	Update(ctx context.Context, id, noteID string, p api.NotePatch) (api.Note, error)
	Delete(ctx context.Context, id, noteID string) error
	Send(ctx context.Context, id string, req api.NotesSendRequest) (api.NotesSendResult, error)
}

// Meetings are the structured meetings (meeting.Service).
type Meetings interface {
	Estimate(ctx context.Context, req api.MeetingRequest) (api.RunEstimate, error)
	Open(ctx context.Context, req api.MeetingRequest) (api.Meeting, error)
	Post(ctx context.Context, id string, req api.MeetingPostRequest) (api.Meeting, error)
	Decide(ctx context.Context, id string, req api.MeetingDecideRequest) (api.Meeting, error)
	Act(ctx context.Context, id string, req api.MeetingActRequest) (api.MeetingActResult, error)
	Cancel(ctx context.Context, id string) (api.Meeting, error)
	DissentOutcome(ctx context.Context, id, dissentID, outcome string) (api.Meeting, error)
	Get(ctx context.Context, id string) (api.Meeting, error)
	List(ctx context.Context, limit int) ([]api.MeetingSummary, error)
	Wait(ctx context.Context, id string, d time.Duration) (api.Meeting, error)
	Events(ctx context.Context, id string) ([]api.TeamLogEntry, error)
	Track(ctx context.Context) ([]api.MemberTrack, error)
}

// Contests are the contests in worktrees (contest.Service).
type Contests interface {
	Estimate(ctx context.Context, req api.ContestRequest) (api.ContestEstimate, error)
	Create(ctx context.Context, req api.ContestRequest) (api.Contest, error)
	Get(ctx context.Context, id string) (api.Contest, error)
	List(ctx context.Context, limit int) ([]api.ContestSummary, error)
	Wait(ctx context.Context, id string, d time.Duration) (api.Contest, error)
	Events(ctx context.Context, id string) ([]api.TeamLogEntry, error)
	Diff(ctx context.Context, id, label string) (api.ContestDiff, error)
	Pick(ctx context.Context, id string, req api.ContestPickRequest) (api.Contest, error)
	Cancel(ctx context.Context, id string) (api.Contest, error)
}

// Hooks are the event hooks (hook.Service).
type Hooks interface {
	List(ctx context.Context) ([]api.Hook, error)
	Replace(ctx context.Context, hooks []api.Hook) ([]api.Hook, error)
	Test(ctx context.Context, id string) (api.HookRun, error)
}

// teamWaitDefault and teamWaitMax bound GET /meetings/{id}/wait and /contests/{id}/wait (seconds).
const (
	teamWaitDefault = 600
	teamWaitMax     = 1500
	teamListMax     = 200
)

func (s *Server) teamRoutes() {
	s.memberRoutes()
	s.noteRoutes()
	s.meetingRoutes()
	s.contestRoutes()
	s.hookRoutes()
}

func (s *Server) memberRoutes() {
	if s.o.Members != nil {
		s.handle("GET /members", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			list, err := s.o.Members.List(r.Context())
			return http.StatusOK, api.NonNil(list), err
		})
		s.handle("POST /members", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var in api.MemberInput
			if err := decode(w, r, &in); err != nil {
				return 0, nil, err
			}
			m, err := s.o.Members.Create(r.Context(), in)
			return http.StatusCreated, m, err
		})
		s.handle("PATCH /members/{id}", s.patchMember)
		s.handle("DELETE /members/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			err := s.o.Members.Delete(r.Context(), r.PathValue("id"))
			return http.StatusNoContent, nil, err
		})
	} else {
		s.reserve("/members", "/members/")
	}
	if s.o.Meetings != nil {
		s.handle("GET /members/track", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
			list, err := s.o.Meetings.Track(r.Context())
			return http.StatusOK, api.NonNil(list), err
		})
	}
	if s.o.Consults != nil {
		s.handle("POST /tasks/{id}/consult", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
			var req api.ConsultRequest
			if err := decode(w, r, &req); err != nil {
				return 0, nil, err
			}
			if req.Member == "" {
				return 0, nil, invalid("member is required").withField("member")
			}
			c, err := s.o.Consults.Consult(r.Context(), r.PathValue("id"), req)
			return http.StatusCreated, c, err
		})
	} else {
		s.reserve("/tasks/{id}/consult")
	}
}

// patchMember is PATCH /members/{id}: the body's fields merged onto the stored member.
func (s *Server) patchMember(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	data, err := readBody(w, r)
	if err != nil {
		return 0, nil, err
	}
	id := r.PathValue("id")
	old, err := s.o.Members.Get(r.Context(), id)
	if err != nil {
		return 0, nil, err
	}
	enabled := old.Enabled
	in := api.MemberInput{ID: old.ID, Name: old.Name, Backend: old.Backend, Provider: old.Provider, Rank: old.Rank,
		Tier: old.Tier, Model: old.Model, Role: old.Role, PreferredAccounts: old.PreferredAccounts,
		FallbackBackends: old.FallbackBackends, Enabled: &enabled}
	if err := decodeBytes(data, &in); err != nil {
		return 0, nil, err
	}
	if in.ID != id {
		return 0, nil, invalid("the id of a member cannot change").withField("id")
	}
	m, err := s.o.Members.Update(r.Context(), id, in)
	return http.StatusOK, m, err
}

func (s *Server) noteRoutes() {
	if s.o.Notes == nil {
		s.reserve("/tasks/{id}/notes", "/tasks/{id}/notes/")
		return
	}
	s.handle("GET /tasks/{id}/notes", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := s.o.Notes.List(r.Context(), r.PathValue("id"))
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /tasks/{id}/notes", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var in api.NoteInput
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		if err := oneOf("by", in.By, true, "user", "lead"); err != nil {
			return 0, nil, err
		}
		if err := oneOf("side", in.Side, true, "new", "old"); err != nil {
			return 0, nil, err
		}
		n, err := s.o.Notes.Add(r.Context(), r.PathValue("id"), in)
		return http.StatusCreated, n, err
	})
	s.handle("POST /tasks/{id}/notes/send", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.NotesSendRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if err := oneOf("by", req.By, true, "user", "lead"); err != nil {
			return 0, nil, err
		}
		res, err := s.o.Notes.Send(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, res, err
	})
	s.handle("PATCH /tasks/{id}/notes/{nid}", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var p api.NotePatch
		if err := decode(w, r, &p); err != nil {
			return 0, nil, err
		}
		if err := oneOf("state", p.State, true, api.NoteResolved, api.NoteOpen); err != nil {
			return 0, nil, err
		}
		n, err := s.o.Notes.Update(r.Context(), r.PathValue("id"), r.PathValue("nid"), p)
		return http.StatusOK, n, err
	})
	s.handle("DELETE /tasks/{id}/notes/{nid}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		err := s.o.Notes.Delete(r.Context(), r.PathValue("id"), r.PathValue("nid"))
		return http.StatusNoContent, nil, err
	})
}

func (s *Server) meetingRoutes() {
	m := s.o.Meetings
	if m == nil {
		s.reserve("/meetings", "/meetings/")
		return
	}
	s.handle("GET /meetings", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		limit, err := queryInt(r, "limit", 50, teamListMax)
		if err != nil {
			return 0, nil, err
		}
		list, err := m.List(r.Context(), limit)
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /meetings", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.MeetingRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		v, err := m.Open(r.Context(), req)
		return http.StatusCreated, v, err
	})
	s.handle("POST /meetings/estimate", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.MeetingRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		v, err := m.Estimate(r.Context(), req)
		return http.StatusOK, v, err
	})
	s.handle("GET /meetings/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		v, err := m.Get(r.Context(), r.PathValue("id"))
		return http.StatusOK, v, err
	})
	s.handle("GET /meetings/{id}/wait", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		d, err := waitSeconds(r)
		if err != nil {
			return 0, nil, err
		}
		v, err := m.Wait(r.Context(), r.PathValue("id"), d)
		return http.StatusOK, v, err
	})
	s.handle("GET /meetings/{id}/events", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		v, err := m.Events(r.Context(), r.PathValue("id"))
		return http.StatusOK, api.NonNil(v), err
	})
	s.handle("POST /meetings/{id}/post", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.MeetingPostRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		v, err := m.Post(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, v, err
	})
	s.handle("POST /meetings/{id}/decide", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.MeetingDecideRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		v, err := m.Decide(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, v, err
	})
	s.handle("POST /meetings/{id}/act", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.MeetingActRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		v, err := m.Act(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, v, err
	})
	s.handle("POST /meetings/{id}/cancel", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		v, err := m.Cancel(r.Context(), r.PathValue("id"))
		return http.StatusOK, v, err
	})
	s.handle("POST /meetings/{id}/dissents/{did}/outcome", func(w http.ResponseWriter, r *http.Request) (int, any,
		error) {
		var req api.DissentOutcomeRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if err := oneOf("outcome", req.Outcome, false, "vindicated", "dismissed", api.DissentOpen); err != nil {
			return 0, nil, err
		}
		v, err := m.DissentOutcome(r.Context(), r.PathValue("id"), r.PathValue("did"), req.Outcome)
		return http.StatusOK, v, err
	})
}

func (s *Server) contestRoutes() {
	c := s.o.Contests
	if c == nil {
		s.reserve("/contests", "/contests/")
		return
	}
	s.handle("GET /contests", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		limit, err := queryInt(r, "limit", 50, teamListMax)
		if err != nil {
			return 0, nil, err
		}
		list, err := c.List(r.Context(), limit)
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /contests", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		req, err := s.contestRequest(w, r)
		if err != nil {
			return 0, nil, err
		}
		v, err := c.Create(r.Context(), req)
		return http.StatusCreated, v, err
	})
	s.handle("POST /contests/estimate", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		req, err := s.contestRequest(w, r)
		if err != nil {
			return 0, nil, err
		}
		v, err := c.Estimate(r.Context(), req)
		return http.StatusOK, v, err
	})
	s.handle("GET /contests/{id}", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		v, err := c.Get(r.Context(), r.PathValue("id"))
		return http.StatusOK, v, err
	})
	s.handle("GET /contests/{id}/wait", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		d, err := waitSeconds(r)
		if err != nil {
			return 0, nil, err
		}
		v, err := c.Wait(r.Context(), r.PathValue("id"), d)
		return http.StatusOK, v, err
	})
	s.handle("GET /contests/{id}/events", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		v, err := c.Events(r.Context(), r.PathValue("id"))
		return http.StatusOK, api.NonNil(v), err
	})
	s.handle("GET /contests/{id}/variants/{label}/diff", func(_ http.ResponseWriter, r *http.Request) (int, any,
		error) {
		v, err := c.Diff(r.Context(), r.PathValue("id"), r.PathValue("label"))
		return http.StatusOK, v, err
	})
	s.handle("POST /contests/{id}/pick", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.ContestPickRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		if err := oneOf("by", req.By, true, "lead", "user"); err != nil {
			return 0, nil, err
		}
		v, err := c.Pick(r.Context(), r.PathValue("id"), req)
		return http.StatusOK, v, err
	})
	s.handle("POST /contests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		v, err := c.Cancel(r.Context(), r.PathValue("id"))
		return http.StatusOK, v, err
	})
}

// contestRequest reads the body of POST /contests and /contests/estimate with the checks of POST /tasks.
func (s *Server) contestRequest(w http.ResponseWriter, r *http.Request) (api.ContestRequest, error) {
	var req api.ContestRequest
	if err := decode(w, r, &req); err != nil {
		return req, err
	}
	if err := oneOf("tier", req.Tier, true, tiers()...); err != nil {
		return req, err
	}
	if err := oneOf("mode", req.Mode, true, "edit", "plan"); err != nil {
		return req, err
	}
	cwd, err := absDir(req.Cwd)
	if err != nil {
		return req, err
	}
	req.Cwd = cwd
	return req, nil
}

func (s *Server) hookRoutes() {
	h := s.o.Hooks
	if h == nil {
		s.reserve("/hooks", "/hooks/")
		return
	}
	s.handle("GET /hooks", func(_ http.ResponseWriter, r *http.Request) (int, any, error) {
		list, err := h.List(r.Context())
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("PUT /hooks", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		var req api.HooksRequest
		if err := decode(w, r, &req); err != nil {
			return 0, nil, err
		}
		list, err := h.Replace(r.Context(), req.Hooks)
		return http.StatusOK, api.NonNil(list), err
	})
	s.handle("POST /hooks/{id}/test", func(w http.ResponseWriter, r *http.Request) (int, any, error) {
		if err := decode(w, r, &struct{}{}); err != nil {
			return 0, nil, err
		}
		run, err := h.Test(r.Context(), r.PathValue("id"))
		return http.StatusOK, run, err
	})
}

// waitSeconds reads ?seconds= of a team wait: 0..1500, 600 when absent.
func waitSeconds(r *http.Request) (time.Duration, error) {
	raw := r.URL.Query().Get("seconds")
	if raw == "" {
		return teamWaitDefault * time.Second, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > teamWaitMax {
		return 0, invalid("seconds must be a whole number from 0 to %d", teamWaitMax).withField("seconds")
	}
	return time.Duration(n) * time.Second, nil
}

// reserve answers 501 on paths whose service is not wired.
func (s *Server) reserve(paths ...string) {
	for _, p := range paths {
		s.mux.HandleFunc(apiPrefix+p, func(w http.ResponseWriter, r *http.Request) {
			s.writeError(w, r, translated(api.CodeNotImplemented, "not_implemented"))
		})
	}
}

// PublishMeeting sends meeting.updated (meeting.Options.Changed).
func (s *Server) PublishMeeting(m api.MeetingSummary) { s.publish(api.StreamMeetingUpdated, m) }

// PublishContest sends contest.updated (contest.Options.Changed).
func (s *Server) PublishContest(c api.ContestSummary) { s.publish(api.StreamContestUpdated, c) }

// PublishNotes sends notes.changed (notes.Options.Changed).
func (s *Server) PublishNotes(taskID string) {
	s.publish(api.StreamNotesChanged, api.NotesChanged{TaskID: taskID})
}

// PublishMembers sends members.changed (member.Options.Changed: every change, through the API or not).
func (s *Server) PublishMembers() { s.changed(api.StreamMembersChanged) }

// PublishHookRun sends hook.ran (hook.Options.Ran).
func (s *Server) PublishHookRun(run api.HookRun) { s.publish(api.StreamHookRan, run) }
