package migrate

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// oldMeeting is a meeting record as the old version wrote it (meetings.js). Everything has the new shape except
// three lists the old version kept as objects keyed by attendee label: a round's answers and runs, and the dissent
// turns ({"P2": {...}}). Records that already hold lists are read as they are.
type oldMeeting struct {
	api.Meeting
	Rounds      []oldRound      `json:"rounds"`
	DissentRuns json.RawMessage `json:"dissentRuns"`
}

// oldRound is one round of an old meeting record.
type oldRound struct {
	N         int             `json:"n"`
	StartedAt string          `json:"startedAt"`
	EndedAt   string          `json:"endedAt"`
	Runs      json.RawMessage `json:"runs"`
	Answers   json.RawMessage `json:"answers"`
}

// decodeOldMeeting reads an old meeting record into the new shape. The label-keyed entries become list items in
// the attendees' order (keys that are not attendees after them, by label); a round's run is done once the round
// has the attendee's answer, a dissent turn once the old record said so or its dissent is on record.
func decodeOldMeeting(data []byte) (api.Meeting, error) {
	var o oldMeeting
	if err := json.Unmarshal(data, &o); err != nil {
		return api.Meeting{}, err
	}
	mt := o.Meeting
	order := make([]string, 0, len(mt.Attendees))
	for i := range mt.Attendees {
		order = append(order, mt.Attendees[i].Label)
	}
	mt.Rounds = make([]api.MeetingRound, 0, len(o.Rounds))
	for i, r := range o.Rounds {
		answers, err := byLabel(r.Answers, order, func(a *api.MeetingAnswer, l string) { a.Label = l })
		if err != nil {
			return api.Meeting{}, fmt.Errorf("rounds[%d].answers: %w", i, err)
		}
		runs, err := byLabel(r.Runs, order, func(x *api.MeetingRun, l string) { x.Label = l })
		if err != nil {
			return api.Meeting{}, fmt.Errorf("rounds[%d].runs: %w", i, err)
		}
		for j := range runs {
			runs[j].Done = runs[j].Done || slices.ContainsFunc(answers, func(a api.MeetingAnswer) bool {
				return a.Label == runs[j].Label
			})
		}
		mt.Rounds = append(mt.Rounds, api.MeetingRound{N: r.N, StartedAt: r.StartedAt, EndedAt: r.EndedAt, Runs: runs,
			Answers: answers})
	}
	dissentRuns, err := byLabel(o.DissentRuns, order, func(x *api.MeetingRun, l string) { x.Label = l })
	if err != nil {
		return api.Meeting{}, fmt.Errorf("dissentRuns: %w", err)
	}
	for i := range dissentRuns {
		dissentRuns[i].Done = dissentRuns[i].Done || slices.ContainsFunc(mt.Dissents, func(d api.Dissent) bool {
			return d.Label == dissentRuns[i].Label
		})
	}
	mt.DissentRuns = dissentRuns
	fillLists(&mt)
	return mt, nil
}

// byLabel reads a list kept either as a JSON array or as an object keyed by attendee label; set gives each item
// of the object its key as label. Items come in order's order, then the other keys by label (P2 before P10).
func byLabel[T any](raw json.RawMessage, order []string, set func(*T, string)) ([]T, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return []T{}, nil
	}
	if raw[0] == '[' {
		var list []T
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		return orEmpty(list), nil
	}
	var keyed map[string]*T // a null entry ("P1": null) is no answer or run at all: it is dropped
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil, err
	}
	rank := func(l string) int {
		if i := slices.Index(order, l); i >= 0 {
			return i
		}
		return len(order)
	}
	labels := slices.SortedFunc(maps.Keys(keyed), func(a, b string) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(labelNum(a), labelNum(b)), strings.Compare(a, b))
	})
	out := make([]T, 0, len(keyed))
	for _, l := range labels {
		v := keyed[l]
		if v == nil {
			continue
		}
		set(v, l)
		out = append(out, *v)
	}
	return out, nil
}

// labelNum is the number in a label (P12 → 12), 0 when it has none.
func labelNum(label string) int {
	n, _ := strconv.Atoi(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, label))
	return n
}

// fillLists gives the lists and maps an old record left out or set to null their empty value, as the meeting
// service writes them (an absent answer, for one, has no arguments or claims in the old record).
func fillLists(mt *api.Meeting) {
	mt.Options, mt.Context, mt.Attendees = orEmpty(mt.Options), orEmpty(mt.Context), orEmpty(mt.Attendees)
	mt.LeadQA, mt.LeadNotes, mt.Dissents = orEmpty(mt.LeadQA), orEmpty(mt.LeadNotes), orEmpty(mt.Dissents)
	mt.AdviceNotTaken, mt.Actions = orEmpty(mt.AdviceNotTaken), orEmpty(mt.Actions)
	if mt.Usage.ByBackend == nil {
		mt.Usage.ByBackend = map[string]int{}
	}
	if mt.Estimate != nil && mt.Estimate.ByBackend == nil {
		mt.Estimate.ByBackend = map[string]int{}
	}
	for i := range mt.Rounds {
		for j := range mt.Rounds[i].Answers {
			a := &mt.Rounds[i].Answers[j]
			a.Arguments, a.Claims, a.Risks = orEmpty(a.Arguments), fillClaims(a.Claims), orEmpty(a.Risks)
			a.Actions, a.Questions = orEmpty(a.Actions), orEmpty(a.Questions)
		}
	}
	for i := range mt.Dissents {
		d := &mt.Dissents[i]
		d.Claims, d.WatchPaths, d.Signals = fillClaims(d.Claims), orEmpty(d.WatchPaths), orEmpty(d.Signals)
	}
}

func fillClaims(claims []api.Claim) []api.Claim {
	claims = orEmpty(claims)
	for i := range claims {
		claims[i].Cites = orEmpty(claims[i].Cites)
	}
	return claims
}

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
