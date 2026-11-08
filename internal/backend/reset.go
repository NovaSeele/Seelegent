package backend

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones named in limit messages, on machines without a zone database (Windows)
)

// Bounds of a reset hint. A hint is only a hint: one that cannot be right is dropped, and one that has passed makes
// the pool fall back to its default rest (quota.ActiveRest, quota.PassiveRest), which is always safe.
const (
	// resetLate is how long ago a reset time without a date may lie and still be the reset that just passed rather
	// than tomorrow's (or next week's, for a weekday). Claude resets a few minutes late at times; a run resumed then
	// hits the limit again and the CLI prints the old time again. Reading that as tomorrow would rest the account for
	// about 24 hours instead of minutes, and no window that prints a bare time is that long: the 5-hour window resets
	// within 5 hours. So a time passed by up to resetLate is passed (default rest); only beyond it is it the next day.
	resetLate = 2 * time.Hour
	// resetWrap is how far in the past a date without a year must lie before it is read as next year's: "resets
	// Jan 2" seen on Dec 30. A date passed by less is a late reset, like a time.
	resetWrap = 30 * 24 * time.Hour
	// resetHorizon drops any hint further ahead than this: the longest window (weekly) is 7 days.
	resetHorizon = 8 * 24 * time.Hour
)

// Pattern pieces. The lead-in is Claude Code's "resets" or its older "will reset". A clock time is "3pm", "3:10am",
// "15:00" (groups: hour, minute, am/pm); a zone is an IANA name in parentheses or a bare UTC, GMT or Z (groups:
// name, bare).
const (
	reResetLead  = `(?i)\b(?:resets|will\s+reset)\s+`
	reResetClock = `(\d{1,2})(?::(\d{2}))?\s*([ap]\.?m\b\.?)?`
	reResetZone  = `(?:\s*\(\s*([A-Za-z][A-Za-z0-9_+\-]*(?:/[A-Za-z0-9_+\-]+)*)\s*\)|\s*(UTC|GMT|Z)\b)?`
	reWeekday    = `(mon|tue|wed|thu|fri|sat|sun)([a-z]*)`
)

// reResetAt reads a time with an optional date: "resets 3:10am (Asia/Ho_Chi_Minh)", "resets 3am", "resets at 00:00
// UTC", "will reset at 3pm", "resets Nov 10, 3pm (Europe/Paris)", "resets Mon, Nov 10 at 3pm". Groups: month, day,
// then the clock and the zone.
var reResetAt = regexp.MustCompile(reResetLead + `(?:at\s+|on\s+)?` +
	`(?:(?:(?:mon|tue|wed|thu|fri|sat|sun)[a-z]*\.?,?\s+)?([a-z]{3,9})\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(?:at\s+)?)?` +
	reResetClock + reResetZone)

// reResetWeekday reads a weekday with an optional time: "resets Monday", "resets Mon 9am", "resets on Monday at 9:00
// (Europe/Paris)". Groups: weekday (its first three letters, then the rest), then the clock and the zone.
var reResetWeekday = regexp.MustCompile(reResetLead + `(?:on\s+)?` + reWeekday + `\b\.?` +
	`(?:,?\s+(?:at\s+)?` + reResetClock + `)?` + reResetZone)

// reResetUnix reads the Unix time older Claude Code versions append to their limit message:
// "Claude AI usage limit reached|1762459200". Ten digits cover every year up to 2286; more is not a time.
var reResetUnix = regexp.MustCompile(`(?i)limit reached\|(\d{9,10})\b`)

// resetHint returns the reset time a Claude Code limit message gives, or zero when it gives none that can be read
// or none within resetHorizon of end (the attempt's end time). A returned time may lie before end: the reset has
// passed, and the caller rests the account for its default time.
//
// Relative times are read in the zone the message names, else in local time, relative to end:
//   - a time alone is today's when it is ahead or passed by up to resetLate, else tomorrow's;
//   - a weekday is its next occurrence, today's when its time (00:00 when none) is ahead or passed by up to
//     resetLate, else next week's;
//   - a date without a year is this year's, or next year's when this year's passed more than resetWrap ago.
//
// end zero leaves relative times unread and the Unix time unbounded.
func resetHint(text string, end time.Time) time.Time {
	at := readReset(text, end)
	if !at.IsZero() && !end.IsZero() && at.After(end.Add(resetHorizon)) {
		return time.Time{}
	}
	return at
}

func readReset(text string, end time.Time) time.Time {
	if m := reResetUnix.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(sec, 0).UTC()
		}
	}
	if end.IsZero() {
		return time.Time{}
	}
	if m := reResetAt.FindStringSubmatch(text); m != nil {
		return readDateTime(m, end)
	}
	if m := reResetWeekday.FindStringSubmatch(text); m != nil {
		return readWeekday(m, end)
	}
	return time.Time{}
}

// readDateTime reads a reResetAt match.
func readDateTime(m []string, end time.Time) time.Time {
	monthText, dayText := m[1], m[2]
	hour, minute, ok := readClock(m[3], m[4], m[5])
	if !ok {
		return time.Time{}
	}
	loc := readZone(m[6], m[7])
	now := end.In(loc)
	if monthText == "" {
		at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if end.Sub(at) > resetLate {
			at = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, loc)
		}
		return at.UTC()
	}
	month, ok := parseMonth(monthText)
	if !ok {
		return time.Time{}
	}
	day, err := strconv.Atoi(dayText)
	if err != nil {
		return time.Time{}
	}
	date := func(year int) (time.Time, bool) {
		at := time.Date(year, month, day, hour, minute, 0, 0, loc)
		return at, at.Month() == month && at.Day() == day // false: no such day that year ("Feb 29", "Nov 31")
	}
	at, ok := date(now.Year())
	if ok && end.Sub(at) > resetWrap {
		at, ok = date(now.Year() + 1)
	}
	if !ok {
		return time.Time{}
	}
	return at.UTC()
}

// readWeekday reads a reResetWeekday match.
func readWeekday(m []string, end time.Time) time.Time {
	wd, ok := parseWeekday(m[1] + m[2])
	if !ok {
		return time.Time{}
	}
	hour, minute := 0, 0
	if m[3] != "" {
		if hour, minute, ok = readClock(m[3], m[4], m[5]); !ok {
			return time.Time{}
		}
	}
	loc := readZone(m[6], m[7])
	now := end.In(loc)
	days := (int(wd) - int(now.Weekday()) + 7) % 7
	at := time.Date(now.Year(), now.Month(), now.Day()+days, hour, minute, 0, 0, loc)
	if days == 0 && end.Sub(at) > resetLate {
		at = time.Date(now.Year(), now.Month(), now.Day()+7, hour, minute, 0, 0, loc)
	}
	return at.UTC()
}

// readClock reads a clock time: hour, minute and am/pm as the pattern captured them. A bare number ("resets 3") is
// too ambiguous to read.
func readClock(hourText, minText, ampm string) (hour, minute int, ok bool) {
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return 0, 0, false
	}
	if minText != "" {
		if minute, err = strconv.Atoi(minText); err != nil || minute > 59 {
			return 0, 0, false
		}
	}
	switch ampm = strings.ToLower(strings.ReplaceAll(ampm, ".", "")); {
	case ampm != "":
		if hour < 1 || hour > 12 {
			return 0, 0, false
		}
		hour %= 12
		if ampm == "pm" {
			hour += 12
		}
	case minText == "" || hour > 23:
		return 0, 0, false
	}
	return hour, minute, true
}

// readZone returns the zone a message names: an IANA name, or a bare UTC, GMT or Z; local time when it names none
// or one that is unknown.
func readZone(name, bare string) *time.Location {
	if bare != "" {
		return time.UTC
	}
	if name != "" {
		if l, err := time.LoadLocation(name); err == nil {
			return l
		}
	}
	return time.Local
}

// parseMonth reads an English month name or its three-letter abbreviation.
func parseMonth(s string) (time.Month, bool) {
	s = strings.ToLower(s)
	for m := time.January; m <= time.December; m++ {
		name := strings.ToLower(m.String())
		if len(s) >= 3 && strings.HasPrefix(name, s) {
			return m, true
		}
	}
	return 0, false
}

// parseWeekday reads an English weekday name or an abbreviation of at least three letters ("Mon", "Tues").
func parseWeekday(s string) (time.Weekday, bool) {
	s = strings.ToLower(s)
	for d := time.Sunday; d <= time.Saturday; d++ {
		name := strings.ToLower(d.String())
		if len(s) >= 3 && strings.HasPrefix(name, s) {
			return d, true
		}
	}
	return 0, false
}
