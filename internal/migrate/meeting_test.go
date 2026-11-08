package migrate

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// oldMeetingJSON is a closed meeting as the old version wrote it: answers, runs and dissent turns keyed by label.
// P3 is the attendee listed first; P9 answered without being an attendee (a damaged record) and goes last.
const oldMeetingJSON = `{"id": "m2611051848-aa11", "title": "Cache or not", "question": "Add a cache?",
  "options": [{"key": "A", "text": "Yes"}, {"key": "B", "text": "No"}], "context": ["cache.go"],
  "cwd": "/repo", "repoRoot": "/repo", "status": "closed", "maxRounds": 2, "round": 1,
  "sealed": {"label": "P4", "text": "No cache", "hash": "abc", "revealedRound": 2},
  "attendees": [
    {"member": "cleo", "name": "Cleo", "rank": "peer", "backend": "agy", "model": "gemini-x", "tier": null, "provider": null, "label": "P3", "taskId": "t30", "present": true},
    {"member": "bo", "name": "Bo", "rank": "peer", "backend": "agy", "model": null, "tier": "fast", "provider": null, "label": "P1", "taskId": "t10", "present": false},
    {"member": "al", "name": "Al", "rank": "senior", "backend": "codex", "model": null, "tier": null, "provider": null, "label": "P2", "taskId": "t20", "present": true}
  ],
  "rounds": [{"n": 1, "startedAt": "2026-11-05T18:48:39.000Z", "endedAt": "2026-11-05T18:55:00.000Z",
    "answers": {
      "P9": {"option": "A", "stance": "stray", "confidence": 10, "arguments": [], "claims": [], "risks": [], "actions": [], "questions": [], "rebuttal": "", "moved": null, "raw": ""},
      "P2": {"option": "B", "stance": "Not needed.", "confidence": 70, "arguments": ["fast enough"], "claims": [], "risks": [], "actions": ["measure"], "questions": [], "rebuttal": "", "moved": "no", "raw": "r"},
      "P1": {"absent": true, "error": "run failed: quota"},
      "P3": {"option": "A", "stance": "Cache it.", "confidence": 60, "arguments": [], "claims": [{"text": "slow", "cites": [{"path": "db.go", "line": 4, "endLine": 6, "quote": "query(", "state": "verified", "detail": "quote found in lines 1-9"}]}], "risks": [], "actions": [], "questions": [], "rebuttal": "", "moved": null, "raw": "r"}
    },
    "runs": {"P2": {"taskId": "t20", "turn": 1}, "P1": {"taskId": "t10", "turn": 1}, "P3": {"taskId": "t30", "turn": 1}, "P5": {"taskId": "t50", "turn": 1}}}],
  "leadQA": [{"id": "q1", "round": 1, "label": "P2", "question": "How big?", "answer": null, "answeredAt": null}],
  "leadNotes": [{"round": 2, "text": "Measure first", "at": "2026-11-05T18:56:00.000Z"}],
  "decision": {"text": "No cache for now", "rationale": "", "option": "B", "at": "2026-11-05T19:00:00.000Z"},
  "dissents": [{"id": "d1", "label": "P3", "member": "cleo", "kind": "dissent", "text": "It will be slow", "claims": [],
    "watchPaths": ["db.go"], "signals": [], "outcome": null, "outcomeAt": null}],
  "adviceNotTaken": [], "actions": [{"text": "Measure", "taskId": "t60", "member": null}],
  "minutes": "# Biên bản", "estimate": {"runs": 8, "byBackend": {"agy": 6, "codex": 2}},
  "usage": {"runs": 4, "byBackend": {"agy": 3, "codex": 1}, "tokens": 1234},
  "createdAt": "2026-11-05T18:48:39.000Z", "updatedAt": "2026-11-05T19:01:00.000Z",
  "dissentRuns": {"P3": {"taskId": "t30", "turn": 2, "done": true}, "P1": {"taskId": "t10", "turn": 2, "done": false}}}`

func labels[T any](items []T, label func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, x := range items {
		out = append(out, label(x))
	}
	return out
}

func TestDecodeOldMeeting(t *testing.T) {
	mt, err := decodeOldMeeting([]byte(oldMeetingJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(mt.Rounds) != 1 {
		t.Fatalf("rounds %+v", mt.Rounds)
	}
	r := mt.Rounds[0]
	ansLabel := func(a api.MeetingAnswer) string { return a.Label }
	runLabel := func(x api.MeetingRun) string { return x.Label }
	if got := labels(r.Answers, ansLabel); !slices.Equal(got, []string{"P3", "P1", "P2", "P9"}) {
		t.Errorf("answer order %v", got)
	}
	if got := labels(r.Runs, runLabel); !slices.Equal(got, []string{"P3", "P1", "P2", "P5"}) {
		t.Errorf("run order %v", got)
	}
	for _, x := range r.Runs {
		if x.Done != (x.Label != "P5") {
			t.Errorf("run %+v: done should say whether the round has its answer", x)
		}
	}
	if x := r.Runs[0]; x.TaskID != "t30" || x.Turn != 1 {
		t.Errorf("run %+v", x)
	}
	absent := r.Answers[1]
	if !absent.Absent || absent.Error != "run failed: quota" || absent.Confidence != nil || absent.Arguments == nil ||
		absent.Claims == nil || absent.Risks == nil || absent.Actions == nil || absent.Questions == nil {
		t.Errorf("absent answer %+v", absent)
	}
	p3 := r.Answers[0]
	if p3.Option != "A" || p3.Confidence == nil || *p3.Confidence != 60 || len(p3.Claims) != 1 ||
		p3.Claims[0].Cites[0].Quote != "query(" || p3.Claims[0].Cites[0].EndLine != 6 {
		t.Errorf("P3 answer %+v", p3)
	}
	if p2 := r.Answers[2]; p2.Moved != "no" || len(p2.Arguments) != 1 || p2.Actions[0] != "measure" {
		t.Errorf("P2 answer %+v", p2)
	}
	if got := labels(mt.DissentRuns, runLabel); !slices.Equal(got, []string{"P3", "P1"}) {
		t.Errorf("dissent run order %v", got)
	}
	if d := mt.DissentRuns; d[0] != (api.MeetingRun{Label: "P3", TaskID: "t30", Turn: 2, Done: true}) ||
		d[1] != (api.MeetingRun{Label: "P1", TaskID: "t10", Turn: 2}) {
		t.Errorf("dissent runs %+v", d)
	}
	if len(mt.LeadQA) != 1 || mt.LeadQA[0].Label != "P2" || mt.LeadQA[0].Answer != "" || len(mt.LeadNotes) != 1 ||
		mt.Sealed == nil || mt.Sealed.Label != "P4" || mt.Decision == nil || mt.Decision.Option != "B" ||
		len(mt.Dissents) != 1 || mt.Dissents[0].Claims == nil || len(mt.Actions) != 1 || mt.AdviceNotTaken == nil ||
		mt.Estimate == nil || mt.Estimate.ByBackend["codex"] != 2 || mt.Usage.Tokens != 1234 ||
		len(mt.Attendees) != 3 || mt.Attendees[1].Present || mt.Attendees[1].Tier != "fast" {
		t.Errorf("meeting %+v", mt)
	}

	// A dissent turn whose dissent is on record is done even when the old record did not say so.
	mt, err = decodeOldMeeting([]byte(strings.Replace(oldMeetingJSON, `"turn": 2, "done": true`, `"turn": 2, "done": false`, 1)))
	if err != nil || !mt.DissentRuns[0].Done {
		t.Errorf("dissent runs %+v, %v", mt.DissentRuns, err)
	}

	// An undecided meeting has no dissentRuns at all; its lists come out empty, not null.
	mt, err = decodeOldMeeting([]byte(`{"id": "m1", "status": "round", "rounds": [{"n": 1, "answers": {}, "runs": {}}]}`))
	if err != nil || mt.DissentRuns == nil || len(mt.DissentRuns) != 0 || mt.Rounds[0].Answers == nil ||
		mt.Rounds[0].Runs == nil || mt.Attendees == nil || mt.Dissents == nil || mt.Usage.ByBackend == nil {
		t.Errorf("undecided meeting %+v, %v", mt, err)
	}
}

func TestDecodeOldMeetingErrors(t *testing.T) {
	for name, rec := range map[string]string{
		"answers":     `{"id": "m1", "rounds": [{"n": 1, "answers": 5, "runs": {}}]}`,
		"runs":        `{"id": "m1", "rounds": [{"n": 1, "answers": {}, "runs": {"P1": "t1"}}]}`,
		"dissentRuns": `{"id": "m1", "dissentRuns": "x"}`,
		"answer":      `{"id": "m1", "rounds": [{"n": 1, "answers": {"P1": {"confidence": "high"}}}]}`,
		"syntax":      `{"id": "m1", "rounds": [`,
	} {
		if _, err := decodeOldMeeting([]byte(rec)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestMigrateMeetingShapes(t *testing.T) {
	f := newFixture(t)
	f.write("settings.json", `{}`)
	f.write("meetings/m2611051848-aa11.json", oldMeetingJSON)
	// A record already in the new shape (lists) is read as it is.
	f.write("meetings/m2611061000-bb22.json", `{"id": "m2611061000-bb22", "title": "Lists", "status": "cancelled",
  "attendees": [{"label": "P1", "member": "al", "rank": "peer", "backend": "agy", "present": true}],
  "rounds": [{"n": 1, "startedAt": "2026-11-06T10:00:00.000Z",
    "runs": [{"label": "P1", "taskId": "t1", "turn": 1, "done": true}],
    "answers": [{"label": "P1", "option": "", "stance": "Fine", "confidence": 50, "arguments": [], "claims": [], "risks": [], "actions": [], "questions": []}]}],
  "dissentRuns": [{"label": "P1", "taskId": "t1", "turn": 2}],
  "createdAt": "2026-11-06T10:00:00.000Z", "updatedAt": "2026-11-06T10:01:00.000Z"}`)
	f.write("meetings/m2611071000-cc33.json", `{"id": "m2611071000-cc33", "status": "closed",
  "rounds": [{"n": 1, "answers": {"P1": ["not", "an", "answer"]}, "runs": {}}]}`)
	rep := f.run(false)

	if it := want(t, rep, KindMeeting, "m2611051848-aa11", Imported); it.Detail != "Cache or not" {
		t.Errorf("detail %q", it.Detail)
	}
	want(t, rep, KindMeeting, "m2611061000-bb22", Imported)
	if it := want(t, rep, KindMeeting, "m2611071000-cc33", Error); !strings.Contains(it.Detail, "unreadable") ||
		!strings.Contains(it.Detail, "rounds[0].answers") {
		t.Errorf("malformed meeting detail %q", it.Detail)
	}

	db, err := store.Open(context.Background(), f.paths.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	meetings := meeting.New(meeting.Options{DB: db})
	defer meetings.Close()
	mt, err := meetings.Get(context.Background(), "m2611051848-aa11")
	if err != nil {
		t.Fatal(err)
	}
	if r := mt.Rounds; len(r) != 1 || len(r[0].Answers) != 4 || r[0].Answers[0].Label != "P3" ||
		!r[0].Answers[1].Absent || len(r[0].Runs) != 4 || len(mt.DissentRuns) != 2 ||
		mt.Status != api.MeetingStatusClosed || len(mt.LeadQA) != 1 || strings.Contains(mt.Minutes, "Biên") {
		t.Errorf("imported meeting %+v", mt)
	}
	mt, err = meetings.Get(context.Background(), "m2611061000-bb22")
	if err != nil || len(mt.Rounds) != 1 || mt.Rounds[0].Answers[0].Stance != "Fine" ||
		!mt.Rounds[0].Runs[0].Done || len(mt.DissentRuns) != 1 || mt.DissentRuns[0].Turn != 2 {
		t.Errorf("list-shaped meeting %+v, %v", mt, err)
	}
	if _, err := meetings.Get(context.Background(), "m2611071000-cc33"); err == nil {
		t.Error("the malformed meeting was imported")
	}
}
