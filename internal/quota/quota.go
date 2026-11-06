// Package quota reads and keeps what each worker account may still spend: the limit windows of its plan (for
// example a 5-hour and a weekly window), how much of each is left and when it resets, the plan itself, and the
// last read error.
//
// Sign-in accounts have active quota: a Reader per backend asks the CLI or its service (agy /usage, the Claude
// OAuth usage endpoint, the codex app-server) and the Refresher does that for every account on a timer, in the
// background, one account at a time per goroutine and with a time limit, so a slow or broken reader never blocks
// the daemon or the other accounts. API-key accounts have passive quota: nothing can be read in advance, so they
// only rest after a limit error (PassiveRest).
//
// Readers are tolerant: output they do not recognize gives an error matching ErrFormat on that account, never a
// panic, and the account keeps its last known numbers (bug B2 of the old version).
package quota

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// Source says how an account's quota is known.
type Source string

// Sources.
const (
	// SourceActive: the quota is read from the CLI or its service before it runs out.
	SourceActive Source = "active"
	// SourcePassive: nothing can be read; the account rests only after a limit error (API-key accounts).
	SourcePassive Source = "passive"
)

// UnknownRemaining is the remaining fraction assumed for an account whose quota was never read: usable, but
// ranked below accounts known to have quota left.
const UnknownRemaining = 0.5

// Window is one limit window of a quota group.
type Window struct {
	// Window is the length key: "5h", "weekly", "30d", "<n>m", or "window" when unknown.
	Window string `json:"window"`
	// Label is the CLI's name for the window.
	Label string `json:"label,omitempty"`
	// Minutes is the window length (0 when unknown).
	Minutes int `json:"minutes,omitempty"`
	// Remaining is the fraction left, 0..1.
	Remaining float64 `json:"remaining"`
	// ResetAt is when the window starts over (zero when unknown).
	ResetAt time.Time `json:"resetAt,omitzero"`
}

// RemainingAt returns the fraction left at now: a window whose reset time has passed counts as full, because its
// numbers are stale (an expired Claude token is only renewed by the next run).
func (w Window) RemainingAt(now time.Time) float64 {
	if !w.ResetAt.IsZero() && !w.ResetAt.After(now) {
		return 1
	}
	return w.Remaining
}

// Group is the quota of a group of models that share limits. A group named "<group> <family>" (for example
// "Claude Opus") is a sub-limit of <group> for models whose id contains <family>.
type Group struct {
	Name    string   `json:"name"`
	Windows []Window `json:"windows"`
}

// Error is the last failed read of an account.
type Error struct {
	Kind    Kind      `json:"kind"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Snapshot is what is known about one account's quota. A failed read keeps the numbers of the last good one and
// records the error.
type Snapshot struct {
	AccountID string `json:"accountId"`
	Backend   string `json:"backend"`
	Source    Source `json:"source"`
	// FetchedAt is the time of the last successful read (zero when there was none).
	FetchedAt time.Time `json:"fetchedAt,omitzero"`
	// Plan is the subscription plan when known.
	Plan string `json:"plan,omitempty"`
	// NoPlan: the plan cannot use this backend at all. Only a later successful read lifts it.
	NoPlan       bool   `json:"noPlan,omitempty"`
	NoPlanReason string `json:"noPlanReason,omitempty"`
	// ExhaustedUntil: a rate or spend limit was reached; the account cannot run before then.
	ExhaustedUntil  time.Time `json:"exhaustedUntil,omitzero"`
	ExhaustedReason string    `json:"exhaustedReason,omitempty"`
	// Identity is a hash of the signed-in email (never the email): two accounts with the same identity share one
	// quota.
	Identity string `json:"identity,omitempty"`
	// Models is the account's model catalog when the CLI reports one (codex); nil means unknown.
	Models []string `json:"models,omitempty"`
	Groups []Group  `json:"groups"`
	// Error is the last failed read, when it came after the last good one.
	Error *Error `json:"error,omitempty"`
}

// Known reports whether the account's quota was ever read successfully.
func (s Snapshot) Known() bool { return !s.FetchedAt.IsZero() }

// ErrorKind returns the kind of the last read error ("" when the last read worked).
func (s Snapshot) ErrorKind() Kind {
	if s.Error == nil {
		return ""
	}
	return s.Error.Kind
}

// Windows returns the windows that govern a turn of model in group: the group's own windows and those of its
// sub-limits whose family the model id contains. found is false when the snapshot has no such group; agy then
// has no limit for the group, while other backends (whose groups are named after the backend) fall back to every
// window they report, as the old version did.
func (s Snapshot) Windows(group, model string) (ws []Window, found bool) {
	m := strings.ToLower(model)
	prefix := group + " "
	for _, g := range s.Groups {
		switch {
		case g.Name == group:
			found = true
			ws = append(ws, g.Windows...)
		case strings.HasPrefix(g.Name, prefix):
			family := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(g.Name, prefix)))
			if family != "" && strings.Contains(m, family) {
				found = true
				ws = append(ws, g.Windows...)
			}
		}
	}
	if !found && s.Backend != config.BackendAgy {
		for _, g := range s.Groups {
			ws = append(ws, g.Windows...)
		}
	}
	return ws, found
}

// Remaining returns the fraction left for a turn of model in group at now: the lowest of the governing windows.
// It is UnknownRemaining for an account never read and 1 when no window limits the group. known is false in the
// first case.
func (s Snapshot) Remaining(now time.Time, group, model string) (remaining float64, known bool) {
	if !s.Known() {
		return UnknownRemaining, false
	}
	ws, _ := s.Windows(group, model)
	remaining = 1
	for _, w := range ws {
		remaining = math.Min(remaining, w.RemainingAt(now))
	}
	return remaining, true
}

// Exhausted reports whether a turn of model in group cannot run at now because a governing window is at or below
// minRemaining or a rate limit was reached, and until when. until is the latest reset among the exhausted windows
// (the account is usable only when all of them have reset; the old code took the latest too, only its comment said
// "earliest") and is zero when no exhausted window says when it resets.
func (s Snapshot) Exhausted(now time.Time, group, model string, minRemaining float64) (until time.Time, exhausted bool) {
	if s.ExhaustedUntil.After(now) {
		until, exhausted = s.ExhaustedUntil, true
	}
	if !s.Known() {
		return until, exhausted
	}
	ws, _ := s.Windows(group, model)
	for _, w := range ws {
		if w.RemainingAt(now) > minRemaining {
			continue
		}
		exhausted = true
		if w.ResetAt.After(until) {
			until = w.ResetAt
		}
	}
	return until, exhausted
}

// FromBackend turns a reader's answer into the snapshot of account id on backend name, read at now.
func FromBackend(id, name string, q backend.Quota, now time.Time) Snapshot {
	s := Snapshot{
		AccountID: id, Backend: name, Source: SourceActive, FetchedAt: now, Plan: q.Plan,
		NoPlan: !q.Entitled, ExhaustedUntil: q.ExhaustedUntil, ExhaustedReason: q.ExhaustedReason,
		Identity: Identity(q.Email), Groups: []Group{},
	}
	if s.NoPlan {
		s.NoPlanReason = q.EntitlementError
	}
	if q.Models != nil {
		s.Models = append([]string{}, q.Models...)
	}
	for _, g := range q.Groups {
		out := Group{Name: g.Name, Windows: []Window{}}
		for _, b := range g.Buckets {
			out.Windows = append(out.Windows, Window{
				Window: b.Window, Label: b.Label, Minutes: b.WindowMinutes, Remaining: clamp01(1 - b.UsedFraction),
				ResetAt: b.ResetAt,
			})
		}
		s.Groups = append(s.Groups, out)
	}
	return s
}

// Identity returns the hash that stands for a signed-in email ("" for none). Case and surrounding spaces do not
// matter.
func Identity(email string) string {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("seelegent-identity\x00" + e))
	return hex.EncodeToString(sum[:16])
}

// clamp01 limits f to 0..1 (NaN becomes 0).
func clamp01(f float64) float64 {
	switch {
	case math.IsNaN(f) || f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

// windowKey returns the length key of a window of mins minutes (old codex windowKey).
func windowKey(mins int) string {
	switch {
	case mins == 300:
		return "5h"
	case mins == 10080:
		return "weekly"
	case mins > 0 && mins%1440 == 0:
		return strconv.Itoa(mins/1440) + "d"
	case mins > 0:
		return strconv.Itoa(mins) + "m"
	}
	return "window"
}

// windowMinutes parses a length key ("5h", "weekly", "30d", "90m") into minutes (0 when unknown).
func windowMinutes(key string) int {
	k := strings.ToLower(strings.TrimSpace(key))
	switch k {
	case "weekly", "week", "7d":
		return 10080
	case "daily", "day":
		return 1440
	case "monthly", "month":
		return 43200
	}
	if len(k) < 2 {
		return 0
	}
	n, unit := k[:len(k)-1], k[len(k)-1]
	v, err := strconv.Atoi(n)
	if err != nil || v <= 0 {
		return 0
	}
	switch unit {
	case 'm':
		return v
	case 'h':
		return v * 60
	case 'd':
		return v * 1440
	case 'w':
		return v * 10080
	}
	return 0
}
