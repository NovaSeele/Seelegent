package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/hook"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/member"
)

// team is what the team import writes through.
type team struct {
	members  *member.Store
	meetings *meeting.Service
	hooks    *hook.Service
}

// importMembers imports members.json ({"members": [...]}). A member whose id exists is kept as it is.
func (m *migrator) importMembers(ctx context.Context) {
	data, err := fsutil.ReadFile(m.old("members.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		m.add(KindMember, "members.json", Error, "%v", err)
		return
	}
	var f struct {
		Members []json.RawMessage `json:"members"`
	}
	if err := json.Unmarshal(data, &f); err != nil || f.Members == nil {
		m.add(KindMember, "members.json", Error, "not a members file (it needs a \"members\" list): %v", err)
		return
	}
	for i, raw := range f.Members {
		var in api.MemberInput
		if err := json.Unmarshal(raw, &in); err != nil {
			m.add(KindMember, fmt.Sprintf("#%d", i+1), Error, "unreadable: %v", err)
			continue
		}
		id := in.ID
		if id == "" {
			id = fmt.Sprintf("#%d", i+1)
		}
		if in.ID == "" {
			m.add(KindMember, id, Error, "the member has no id")
			continue
		}
		if in.Backend == "opencode" {
			m.add(KindMember, id, Skipped, "its backend (opencode) was removed")
			continue
		}
		in.FallbackBackends = slices.DeleteFunc(in.FallbackBackends, func(b string) bool { return b == "opencode" })
		v, err := m.team.members.Validate(ctx, in, "")
		if err != nil {
			m.add(KindMember, id, Error, "%v", err)
			continue
		}
		cur, err := m.team.members.Get(ctx, in.ID)
		switch {
		case err == nil && sameMember(cur, v):
			m.add(KindMember, id, Unchanged, "already imported")
			continue
		case err == nil:
			m.add(KindMember, id, Kept, "a member with this id already exists and differs; it was left as it is")
			continue
		case !errors.Is(err, member.ErrNotFound):
			m.add(KindMember, id, Error, "%v", err)
			continue
		}
		if !m.o.DryRun {
			if _, err := m.team.members.Import(ctx, in); err != nil {
				m.add(KindMember, id, Error, "%v", err)
				continue
			}
		}
		m.add(KindMember, id, Imported, "%s, %s on %s", v.Name, v.Rank, v.Backend)
	}
}

func sameMember(a, b api.Member) bool {
	return a.Name == b.Name && a.Backend == b.Backend && a.Provider == b.Provider && a.Rank == b.Rank &&
		a.Tier == b.Tier && a.Model == b.Model && a.Role == b.Role && a.Enabled == b.Enabled &&
		slices.Equal(nonNil(a.PreferredAccounts), nonNil(b.PreferredAccounts)) &&
		slices.Equal(nonNil(a.FallbackBackends), nonNil(b.FallbackBackends))
}

// importHooks imports hooks.json (a list, or {"hooks": [...]}) after the hooks this version has; a hook that is
// already there is not added again.
func (m *migrator) importHooks(ctx context.Context) {
	data, err := fsutil.ReadFile(m.old("hooks.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		m.add(KindHook, "hooks.json", Error, "%v", err)
		return
	}
	list, err := oldHooks(data)
	if err != nil {
		m.add(KindHook, "hooks.json", Error, "not a hooks file: %v", err)
		return
	}
	if len(list) == 0 {
		m.add(KindHook, "hooks.json", Skipped, "no hooks")
		return
	}
	if !m.o.DryRun {
		added, unchanged, skipped, err := m.team.hooks.Import(ctx, list)
		if err != nil {
			m.add(KindHook, "hooks.json", Error, "%v", err)
			return
		}
		m.hookOutcome(added, unchanged, skipped)
		return
	}
	have, err := m.team.hooks.List(ctx)
	if err != nil {
		m.add(KindHook, "hooks.json", Error, "%v", err)
		return
	}
	added, unchanged, skipped := 0, 0, []string{}
	for i, h := range list {
		v, err := hook.Validate(h)
		switch {
		case err != nil:
			skipped = append(skipped, fmt.Sprintf("hook %d: %v", i, err))
		case slices.ContainsFunc(have, func(c api.Hook) bool { return hook.Same(c, v) }):
			unchanged++
		default:
			have = append(have, v)
			added++
		}
	}
	m.hookOutcome(added, unchanged, skipped)
}

// oldHooks reads the old hooks file: "on" was one event or a list, and "enabled" defaulted to true.
func oldHooks(data []byte) ([]api.Hook, error) {
	type oldHook struct {
		Name           string          `json:"name"`
		On             json.RawMessage `json:"on"`
		Run            string          `json:"run"`
		Project        string          `json:"project"`
		TimeoutSeconds int             `json:"timeoutSeconds"`
		Enabled        *bool           `json:"enabled"`
	}
	var raw []oldHook
	if err := json.Unmarshal(data, &raw); err != nil {
		var f struct {
			Hooks []oldHook `json:"hooks"`
		}
		if err2 := json.Unmarshal(data, &f); err2 != nil {
			return nil, err
		}
		raw = f.Hooks
	}
	out := make([]api.Hook, 0, len(raw))
	for _, o := range raw {
		h := api.Hook{Name: o.Name, Run: o.Run, Project: o.Project, TimeoutSeconds: o.TimeoutSeconds,
			Enabled: o.Enabled == nil || *o.Enabled, On: []string{}}
		var one string
		if json.Unmarshal(o.On, &one) == nil {
			if one != "" {
				h.On = []string{one}
			}
		} else {
			_ = json.Unmarshal(o.On, &h.On)
		}
		out = append(out, h)
	}
	return out, nil
}

// hookOutcome reports the hooks file as one item, then one error per hook that could not be imported.
func (m *migrator) hookOutcome(added, unchanged int, skipped []string) {
	defer func() {
		for _, s := range skipped {
			m.add(KindHook, "hooks.json", Error, "%s", s)
		}
	}()
	switch {
	case added > 0:
		m.add(KindHook, "hooks.json", Imported, "%d hooks added after the existing ones (%d already there)", added,
			unchanged)
	case unchanged > 0:
		m.add(KindHook, "hooks.json", Unchanged, "already imported")
	}
}

// meetingFile is how the old version named a meeting record (meetings/<id>.json; <id>.jsonl is its event log).
var meetingFile = regexp.MustCompile(`^m[\w-]+\.json$`)

// importMeetings imports the old meeting records: the minutes, decisions and dissents (the dissent memory and the
// members' track records read them). A meeting that was still running is imported as cancelled.
func (m *migrator) importMeetings(ctx context.Context) {
	dir := m.old("meetings")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		m.add(KindMeeting, "meetings", Error, "%v", err)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !meetingFile.MatchString(e.Name()) {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		data, err := fsutil.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			m.add(KindMeeting, id, Error, "%v", err)
			continue
		}
		mt, err := decodeOldMeeting(data)
		if err != nil {
			m.add(KindMeeting, id, Error, "unreadable: %v", err)
			continue
		}
		if mt.ID != id {
			m.add(KindMeeting, id, Error, "the record's id (%q) does not match its file name", mt.ID)
			continue
		}
		_, err = m.team.meetings.Get(ctx, id)
		if err == nil {
			m.add(KindMeeting, id, Unchanged, "already imported")
			continue
		}
		if !errors.Is(err, meeting.ErrNotFound) {
			m.add(KindMeeting, id, Error, "%v", err)
			continue
		}
		detail := mt.Title
		switch mt.Status {
		case api.MeetingStatusClosed, api.MeetingStatusCancelled, api.MeetingStatusFailed:
		default:
			detail += " (it was still running: imported as cancelled)"
		}
		if !m.o.DryRun {
			if _, err := m.team.meetings.Import(ctx, mt); err != nil {
				m.add(KindMeeting, id, Error, "%v", err)
				continue
			}
		}
		m.add(KindMeeting, id, Imported, "%s", detail)
	}
}

// countNotes counts the old review notes (notes/<task id>.json, a list each).
func (m *migrator) countNotes() (notes, tasks int) {
	entries, err := os.ReadDir(m.old("notes"))
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fsutil.ReadFile(m.old("notes", e.Name()))
		if err != nil {
			continue
		}
		var list []json.RawMessage
		if json.Unmarshal(data, &list) == nil && len(list) > 0 {
			notes += len(list)
			tasks++
		}
	}
	return notes, tasks
}
