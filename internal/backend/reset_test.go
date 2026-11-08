package backend

import (
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

func TestResetHint(t *testing.T) {
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh") // UTC+7, no daylight saving
	if err != nil {
		t.Fatal(err)
	}
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	// Sunday 2026-11-08 01:00 in Ho Chi Minh City (Saturday 19:00 in Paris, 18:00 UTC).
	end := time.Date(2026, 11, 8, 1, 0, 0, 0, hcm)
	// Sunday 2026-11-08 15:00 in Ho Chi Minh City.
	afternoon := time.Date(2026, 11, 8, 15, 0, 0, 0, hcm)
	at := func(loc *time.Location, y int, mo time.Month, d, h, mi int) time.Time {
		return time.Date(y, mo, d, h, mi, 0, 0, loc).UTC()
	}
	cases := []struct {
		name string
		text string
		end  time.Time
		want time.Time
	}{
		{"field report", "You've hit your 5-hour limit · resets 3:10am (Asia/Ho_Chi_Minh)", end,
			at(hcm, 2026, 11, 8, 3, 10)},
		{"pm", "You've hit your 5-hour limit · resets 3:10pm (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 15, 10)},
		{"hour only", "5-hour limit reached · resets 3am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 3, 0)},
		{"noon", "resets 12pm (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 12, 0)},
		{"24-hour clock", "limit · resets 15:00 (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 15, 0)},
		{"other zone", "resets 9am (Europe/Paris)", end, at(paris, 2026, 11, 8, 9, 0)},

		// A time alone that has passed: by up to resetLate it is a late reset (passed), beyond that tomorrow's.
		{"time just passed", "resets 12:55am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 0, 55)},
		{"late reset seen again", "resets 3:10am (Asia/Ho_Chi_Minh)", time.Date(2026, 11, 8, 3, 12, 0, 0, hcm),
			at(hcm, 2026, 11, 8, 3, 10)},
		{"passed midnight", "resets 12am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 0, 0)},
		{"passed by 1.5h", "resets 1:30pm (Asia/Ho_Chi_Minh)", afternoon, at(hcm, 2026, 11, 8, 13, 30)},
		{"passed by 2.5h is tomorrow", "resets 12:30pm (Asia/Ho_Chi_Minh)", afternoon, at(hcm, 2026, 11, 9, 12, 30)},
		{"month end rolls over", "resets 1am (Asia/Ho_Chi_Minh)", time.Date(2026, 11, 30, 23, 0, 0, 0, hcm),
			at(hcm, 2026, 12, 1, 1, 0)},

		// Dates: this year's unless it passed more than resetWrap ago.
		{"date", "You've hit your weekly limit · resets Nov 10, 3pm (Europe/Paris)", end,
			at(paris, 2026, 11, 10, 15, 0)},
		{"full month name", "resets November 10 at 3:30pm (Europe/Paris)", end, at(paris, 2026, 11, 10, 15, 30)},
		{"date with weekday", "resets Tue, Nov 10, 3pm (Europe/Paris)", end, at(paris, 2026, 11, 10, 15, 0)},
		{"date just passed is not next year", "resets Nov 8, 3pm (Europe/Paris)",
			time.Date(2026, 11, 8, 15, 12, 0, 0, paris), at(paris, 2026, 11, 8, 15, 0)},
		{"date passed 3 weeks ago", "resets Oct 19, 3pm (Europe/Paris)", end, at(paris, 2026, 10, 19, 15, 0)},
		{"Dec to Jan wrap", "resets Jan 2, 9am (Europe/Paris)", time.Date(2026, 12, 30, 12, 0, 0, 0, paris),
			at(paris, 2027, 1, 2, 9, 0)},
		{"leap day", "resets Feb 29, 9am (Europe/Paris)", time.Date(2028, 2, 28, 12, 0, 0, 0, paris),
			at(paris, 2028, 2, 29, 9, 0)},
		{"within 8 days", "resets Nov 15, 3pm (Europe/Paris)", end, at(paris, 2026, 11, 15, 15, 0)},

		// Weekdays: the next occurrence, today's while its time is ahead or passed by up to resetLate.
		{"bare weekday", "You've hit your weekly limit · resets Monday", end, at(time.Local, 2026, 11, 9, 0, 0)},
		{"weekday and hour", "resets Mon 9am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 9, 9, 0)},
		{"weekday in its zone", "resets Monday 9:00 (Europe/Paris)", end, at(paris, 2026, 11, 9, 9, 0)},
		{"weekday at", "resets on Tues at 9am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 10, 9, 0)},
		{"weekday zone only", "resets Sat (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 14, 0, 0)},
		{"today ahead", "resets Sunday 3am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 3, 0)},
		{"today just passed", "resets Sun 12:30am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 0, 30)},
		{"today long passed is next week", "resets Sunday 9am (Asia/Ho_Chi_Minh)", afternoon,
			at(hcm, 2026, 11, 15, 9, 0)},

		// Other wordings.
		{"bare UTC", "You've hit your limit · resets at 00:00 UTC", end, at(time.UTC, 2026, 11, 8, 0, 0)},
		{"bare GMT", "resets 3pm GMT", end, at(time.UTC, 2026, 11, 8, 15, 0)},
		{"bare Z", "resets 21:00Z", end, at(time.UTC, 2026, 11, 7, 21, 0)},
		{"weekday UTC", "resets Mon 9am UTC", end, at(time.UTC, 2026, 11, 9, 9, 0)},
		{"will reset", "Claude usage limit reached. Your limit will reset at 3pm (Asia/Ho_Chi_Minh).", end,
			at(hcm, 2026, 11, 8, 15, 0)},
		{"will reset date", "Your limit will reset on Nov 10 at 9am (Europe/Paris)", end, at(paris, 2026, 11, 10, 9, 0)},

		{"unix time", "Claude AI usage limit reached|1762459200", end, time.Unix(1762459200, 0).UTC()},
		{"unix time without end", "Claude AI usage limit reached|1762459200", time.Time{},
			time.Unix(1762459200, 0).UTC()},
		{"unix time near", "Claude AI usage limit reached|1794168000", end, time.Unix(1794168000, 0).UTC()},

		// Unreadable or out of bounds: zero.
		{"11-digit unix", "Claude AI usage limit reached|17624592001", end, time.Time{}},
		{"unix beyond 8 days", "Claude AI usage limit reached|9999999999", end, time.Time{}},
		{"date beyond 8 days", "resets Nov 17, 3pm (Europe/Paris)", end, time.Time{}},
		{"next year beyond 8 days", "resets Jan 2, 9am (Europe/Paris)", end, time.Time{}},
		{"no reset", "You've hit your 5-hour limit", end, time.Time{}},
		{"garbage", "resets whenever it likes", end, time.Time{}},
		{"not a weekday", "resets Monkey 9am", end, time.Time{}},
		{"gemini wording", "Your quota will reset after 7h44m8s.", end, time.Time{}},
		{"bare number", "resets 3 (Asia/Ho_Chi_Minh)", end, time.Time{}},
		{"weekday bare number", "resets Monday 3", end, time.Time{}},
		{"bad hour", "resets 13pm", end, time.Time{}},
		{"bad 24-hour", "resets 25:00", end, time.Time{}},
		{"bad minute", "resets 3:75am", end, time.Time{}},
		{"bad month", "resets Smarch 10, 3pm", end, time.Time{}},
		{"no such day", "resets Nov 31, 3pm", end, time.Time{}},
		{"no leap day", "resets Feb 29, 9am (Europe/Paris)", time.Date(2027, 2, 27, 12, 0, 0, 0, paris), time.Time{}},
		{"no end time", "resets 3:10am (Asia/Ho_Chi_Minh)", time.Time{}, time.Time{}},
		{"empty", "", end, time.Time{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resetHint(c.text, c.end); !got.Equal(c.want) {
				t.Errorf("resetHint(%q) = %v, want %v", c.text, got, c.want)
			}
		})
	}

	// An unknown zone falls back to local time.
	got := resetHint("resets 3:10am (Mars/Olympus_Mons)", end)
	local := end.In(time.Local)
	want := time.Date(local.Year(), local.Month(), local.Day(), 3, 10, 0, 0, time.Local)
	if end.Sub(want) > resetLate {
		want = time.Date(local.Year(), local.Month(), local.Day()+1, 3, 10, 0, 0, time.Local)
	}
	if !got.Equal(want) {
		t.Errorf("unknown zone = %v, want %v (local)", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("reset time is not UTC: %v", got.Location())
	}
}

// TestResetHintDST: Paris turns its clocks back from 03:00 CEST to 02:00 CET on Sunday 2026-10-25, so 02:00-03:00
// happens twice. A reset inside the repeated hour is one of its two instants; one just after the change is CET.
func TestResetHintDST(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 10, 24, 23, 0, 0, 0, time.UTC) // 01:00 CEST
	got := resetHint("resets 2:30am (Europe/Paris)", end)
	first, second := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	if !got.Equal(first) && !got.Equal(second) {
		t.Errorf("repeated hour = %v, want %v or %v", got, first, second)
	}
	if got, want := resetHint("resets 3:10am (Europe/Paris)", end),
		time.Date(2026, 10, 25, 2, 10, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("after the change = %v, want %v", got, want)
	}
	// Seen at 02:40 CET (the second 02:xx), a 2:30 reset passed 10 or 70 minutes ago: a late reset, not tomorrow's.
	late := time.Date(2026, 10, 25, 1, 40, 0, 0, time.UTC)
	if got := resetHint("resets 2:30am (Europe/Paris)", late); got.After(late) || late.Sub(got) > resetLate {
		t.Errorf("late in the repeated hour = %v", got.In(paris))
	}
}

// TestOutcomeResetAt: a quota failure carries the reset time of its message, read relative to the exit time; other
// failures carry none, and neither do other backends' messages.
func TestOutcomeResetAt(t *testing.T) {
	end := time.Date(2026, 11, 7, 19, 0, 0, 0, time.UTC) // 02:00 on Nov 8 in Ho Chi Minh City
	outcome := func(backend string, class event.Class, msg string, exit launch.Exit) Outcome {
		c := Collector{Backend: backend}
		c.Add(event.Fail(class, msg))
		return c.Outcome(exit)
	}
	exit := launch.Exit{Reason: launch.ReasonExited, Code: 1, EndedAt: end}
	msg := "You've hit your 5-hour limit · resets 3:10am (Asia/Ho_Chi_Minh)"
	claude := config.BackendClaude
	if o := outcome(claude, event.ClassQuotaExhausted, msg, exit); !o.ResetAt.Equal(time.Date(2026, 11, 7, 20, 10, 0,
		0, time.UTC)) {
		t.Errorf("quota reset = %v", o.ResetAt)
	}
	if o := outcome(claude, event.ClassFatal, msg, exit); !o.ResetAt.IsZero() {
		t.Errorf("a fatal failure has a reset time: %v", o.ResetAt)
	}
	if o := outcome(claude, event.ClassQuotaExhausted, msg, launch.Exit{Reason: launch.ReasonExited}); !o.ResetAt.
		IsZero() {
		t.Errorf("a reset time without an exit time: %v", o.ResetAt)
	}
	for _, b := range []string{config.BackendAgy, config.BackendCodex, ""} {
		if o := outcome(b, event.ClassQuotaExhausted, msg, exit); !o.ResetAt.IsZero() {
			t.Errorf("backend %q: a reset time read from its message: %v", b, o.ResetAt)
		}
	}
	// The whole stream: the parser classifies the CLI's own text, not the tool output before it.
	_, o := replay(t, mustBackend(t, "claude"), "limit.jsonl")
	if o.Class != event.ClassQuotaExhausted || o.ConversationID != "7e5d1c3b-2a4f-4b6e-8c9d-0f1e2d3c4b5a" ||
		o.Usage.InputTokens != 54000 {
		t.Fatalf("limit stream = %+v", o)
	}
}
