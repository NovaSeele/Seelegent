package backend

import (
	"testing"
	"time"

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
	// 2026-11-08 01:00 in Ho Chi Minh City.
	end := time.Date(2026, 11, 8, 1, 0, 0, 0, hcm)
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
		{"midnight rolls over", "resets 12am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 9, 0, 0)},
		{"next day", "You've hit your 5-hour limit · resets 12:30am (Asia/Ho_Chi_Minh)", end,
			at(hcm, 2026, 11, 9, 0, 30)},
		{"just passed stays today", "resets 12:55am (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 0, 55)},
		{"month end rolls over", "resets 1am (Asia/Ho_Chi_Minh)", time.Date(2026, 11, 30, 23, 0, 0, 0, hcm),
			at(hcm, 2026, 12, 1, 1, 0)},
		{"24-hour clock", "limit · resets 15:00 (Asia/Ho_Chi_Minh)", end, at(hcm, 2026, 11, 8, 15, 0)},
		{"other zone", "resets 9am (Europe/Paris)", end, at(paris, 2026, 11, 8, 9, 0)},
		{"date", "You've hit your weekly limit · resets Nov 10, 3pm (Europe/Paris)", end,
			at(paris, 2026, 11, 10, 15, 0)},
		{"full month name", "resets November 10 at 3:30pm (Europe/Paris)", end, at(paris, 2026, 11, 10, 15, 30)},
		{"date next year", "resets Jan 2, 9am (Europe/Paris)", end, at(paris, 2027, 1, 2, 9, 0)},
		{"leap day", "resets Feb 29, 9am (Europe/Paris)", time.Date(2027, 3, 1, 0, 0, 0, 0, paris),
			at(paris, 2028, 2, 29, 9, 0)},
		{"unix time", "Claude AI usage limit reached|1762459200", end, time.Unix(1762459200, 0).UTC()},
		{"unix time without end", "Claude AI usage limit reached|1762459200", time.Time{},
			time.Unix(1762459200, 0).UTC()},
		// Unreadable: zero.
		{"no reset", "You've hit your 5-hour limit", end, time.Time{}},
		{"garbage", "resets whenever it likes", end, time.Time{}},
		{"bare number", "resets 3 (Asia/Ho_Chi_Minh)", end, time.Time{}},
		{"bad hour", "resets 13pm", end, time.Time{}},
		{"bad 24-hour", "resets 25:00", end, time.Time{}},
		{"bad minute", "resets 3:75am", end, time.Time{}},
		{"bad month", "resets Smarch 10, 3pm", end, time.Time{}},
		{"no such day", "resets Nov 31, 3pm", end, time.Time{}},
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
	if want.Before(end.Add(-resetGrace)) {
		want = time.Date(local.Year(), local.Month(), local.Day()+1, 3, 10, 0, 0, time.Local)
	}
	if !got.Equal(want) {
		t.Errorf("unknown zone = %v, want %v (local)", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("reset time is not UTC: %v", got.Location())
	}
}

// TestOutcomeResetAt: a quota failure carries the reset time of its message, read relative to the exit time; other
// failures carry none.
func TestOutcomeResetAt(t *testing.T) {
	end := time.Date(2026, 11, 7, 19, 0, 0, 0, time.UTC) // 02:00 on Nov 8 in Ho Chi Minh City
	outcome := func(class event.Class, msg string, exit launch.Exit) Outcome {
		var c Collector
		c.Add(event.Fail(class, msg))
		return c.Outcome(exit)
	}
	exit := launch.Exit{Reason: launch.ReasonExited, Code: 1, EndedAt: end}
	msg := "You've hit your 5-hour limit · resets 3:10am (Asia/Ho_Chi_Minh)"
	if o := outcome(event.ClassQuotaExhausted, msg, exit); !o.ResetAt.Equal(time.Date(2026, 11, 7, 20, 10, 0, 0,
		time.UTC)) {
		t.Errorf("quota reset = %v", o.ResetAt)
	}
	if o := outcome(event.ClassFatal, msg, exit); !o.ResetAt.IsZero() {
		t.Errorf("a fatal failure has a reset time: %v", o.ResetAt)
	}
	if o := outcome(event.ClassQuotaExhausted, msg, launch.Exit{Reason: launch.ReasonExited}); !o.ResetAt.IsZero() {
		t.Errorf("a reset time without an exit time: %v", o.ResetAt)
	}
	// The whole stream: the parser classifies the CLI's own text, not the tool output before it.
	_, o := replay(t, mustBackend(t, "claude"), "limit.jsonl")
	if o.Class != event.ClassQuotaExhausted || o.ConversationID != "7e5d1c3b-2a4f-4b6e-8c9d-0f1e2d3c4b5a" ||
		o.Usage.InputTokens != 54000 {
		t.Fatalf("limit stream = %+v", o)
	}
}
