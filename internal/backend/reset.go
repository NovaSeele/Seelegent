package backend

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones named in limit messages, on machines without a zone database (Windows)
)

// resetGrace is how far before the attempt's end a reset time without a date may lie and still be today's: the
// message has minute precision and the attempt ends a little after the CLI printed it, so "resets 3:10am" seen at
// 3:10:20 means the reset that just passed, not the one 24 hours later.
const resetGrace = 10 * time.Minute

// reResetAt reads a reset time from a limit message: "resets 3:10am (Asia/Ho_Chi_Minh)", "resets 3am",
// "resets Nov 10, 3pm (Europe/Paris)", "resets 15:00". Groups: month, day, hour, minute, am/pm, zone.
var reResetAt = regexp.MustCompile(`(?i)\bresets\s+(?:at\s+|on\s+)?` +
	`(?:([a-z]{3,9})\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(?:at\s+)?)?` +
	`(\d{1,2})(?::(\d{2}))?\s*([ap]\.?m\b\.?)?` +
	`(?:\s*\(\s*([A-Za-z][A-Za-z0-9_+\-]*(?:/[A-Za-z0-9_+\-]+)*)\s*\))?`)

// reResetUnix reads the Unix time older Claude Code versions append to their limit message:
// "Claude AI usage limit reached|1762459200".
var reResetUnix = regexp.MustCompile(`(?i)limit reached\|(\d{9,11})\b`)

// resetHint returns the reset time a quota message gives, or zero when it gives none that can be read. A time
// without a date is its next occurrence after end (the attempt's end time), in the zone the message names or else
// in local time; a date without a year is its next occurrence too. end zero leaves such relative times unread.
func resetHint(text string, end time.Time) time.Time {
	if m := reResetUnix.FindStringSubmatch(text); m != nil {
		if sec, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.Unix(sec, 0).UTC()
		}
	}
	m := reResetAt.FindStringSubmatch(text)
	if m == nil || end.IsZero() {
		return time.Time{}
	}
	monthText, dayText, hourText, minText, ampm, zone := m[1], m[2], m[3], m[4], m[5], m[6]
	hour, err := strconv.Atoi(hourText)
	if err != nil {
		return time.Time{}
	}
	minute := 0
	if minText != "" {
		if minute, err = strconv.Atoi(minText); err != nil || minute > 59 {
			return time.Time{}
		}
	}
	switch ampm = strings.ToLower(strings.ReplaceAll(ampm, ".", "")); {
	case ampm != "":
		if hour < 1 || hour > 12 {
			return time.Time{}
		}
		hour %= 12
		if ampm == "pm" {
			hour += 12
		}
	case minText == "" || hour > 23:
		// A bare number ("resets 3") is too ambiguous to read.
		return time.Time{}
	}
	loc := time.Local
	if zone != "" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc = l
		}
	}
	now := end.In(loc)
	if monthText == "" {
		at := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if at.Before(end.Add(-resetGrace)) {
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
	for year := now.Year(); year <= now.Year()+1; year++ {
		at := time.Date(year, month, day, hour, minute, 0, 0, loc)
		if at.Month() != month || at.Day() != day {
			continue // no such day that year ("Feb 29", "Feb 30")
		}
		if !at.Before(end.Add(-resetGrace)) {
			return at.UTC()
		}
	}
	return time.Time{}
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
