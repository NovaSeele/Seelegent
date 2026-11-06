// Package usage keeps the usage history: one snapshot of every account per local clock hour (status, plan, quota
// windows, tokens and cost of the day), kept for 30 days, and the token usage of every worker attempt, which the
// task engine records when an attempt ends. It answers the dashboard's history query (hourly rows or a daily
// rollup) and usage summary (tokens and cost per project, task, turn, account and model over 1 to 30 days, with the
// period before for comparison).
//
// Snapshots are built only from what the pool already knows; recording never reads quota or starts anything. No
// row holds tokens, keys or email addresses: labels that look like an email are replaced by the account id when
// they are written and again when they are read.
package usage

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Limits (old usage-history.js and usage-api.js).
const (
	// RetentionDays is how many local days of snapshots are kept (today counts as one).
	RetentionDays = 30
	// TurnRetentionDays is how many days of attempt usage are kept: two summary periods of MaxDays, so the
	// longest summary still has its previous period.
	TurnRetentionDays = 2 * MaxDays
	// MaxDays is the longest history or summary period.
	MaxDays = 30
	// HistoryDefaultDays is the history period when no start is given.
	HistoryDefaultDays = 7
	// SummaryDefaultDays is the summary period when none is given.
	SummaryDefaultDays = 7
)

// ErrInvalid marks a query with an invalid parameter (HTTP 400).
var ErrInvalid = errors.New("invalid usage query")

// timeFormat is how times are stored: UTC, fixed width, so text order is time order.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// emailPattern matches text that contains an email address.
var emailPattern = regexp.MustCompile(`(?i)[^\s@<>]+@[^\s@<>]+\.[a-z]{2,}`)

// SafeLabel returns label unless it is empty or looks like an email address; then it returns fallback.
func SafeLabel(label, fallback string) string {
	s := strings.TrimSpace(label)
	if s == "" || emailPattern.MatchString(s) {
		return fallback
	}
	return s
}

// startOfDay returns local midnight of t's day in loc.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// addDays returns local midnight k calendar days after the local midnight day (safe across DST changes).
func addDays(day time.Time, k int) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d+k, 0, 0, 0, 0, day.Location())
}

// dateOf returns the local date of t as YYYY-MM-DD.
func dateOf(t time.Time, loc *time.Location) string { return t.In(loc).Format(time.DateOnly) }

// hourKey returns the local clock hour of t as YYYY-MM-DDTHH (one snapshot per account and key).
func hourKey(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02T15") }

// roundCost rounds a cost to a millionth of a dollar.
func roundCost(c float64) float64 {
	const scale = 1e6
	if c < 0 {
		return 0
	}
	return float64(int64(c*scale+0.5)) / scale
}
