package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/backend"
)

func win(key string, remaining float64, reset time.Time) Window {
	return Window{Window: key, Remaining: remaining, ResetAt: reset}
}

func TestSnapshotRemainingAndExhausted(t *testing.T) {
	soon, later := t0.Add(time.Hour), t0.Add(48*time.Hour)
	claude := Snapshot{Backend: "claude", FetchedAt: t0, Groups: []Group{
		{Name: "Claude", Windows: []Window{win("weekly", 0.6, later), win("5h", 0.4, soon)}},
		{Name: "Claude Opus", Windows: []Window{win("weekly", 0.01, later)}},
		{Name: "Claude Sonnet", Windows: []Window{win("weekly", 0.9, later)}},
	}}
	agy := Snapshot{Backend: "agy", FetchedAt: t0, Groups: []Group{
		{Name: "Gemini Models", Windows: []Window{win("weekly", 0.37, later), win("5h", 0.02, soon)}},
		{Name: "Claude and GPT models", Windows: nil},
	}}
	tests := []struct {
		name       string
		snap       Snapshot
		now        time.Time
		group      string
		model      string
		remaining  float64
		known      bool
		exhausted  bool
		restUntil  time.Time
		windowsLen int
	}{
		{"never read", Snapshot{Backend: "agy"}, t0, "Gemini Models", "gemini-x", UnknownRemaining, false, false, time.Time{}, 0},
		{"agy lowest window", agy, t0, "Gemini Models", "gemini-3.8-flash", 0.02, true, true, soon, 2},
		{"agy window reset passed counts as full", agy, soon.Add(time.Minute), "Gemini Models", "gemini", 0.37, true, false, time.Time{}, 2},
		{"agy unknown group has no limit", agy, t0, "Gemini 4 Argon", "gemini-4-argon", 1, true, false, time.Time{}, 0},
		{"agy group without windows", agy, t0, "Claude and GPT models", "gpt", 1, true, false, time.Time{}, 0},
		{"claude sonnet sub-limit", claude, t0, "Claude", "claude-sonnet-5", 0.4, true, false, time.Time{}, 3},
		{"claude opus sub-limit exhausted", claude, t0, "Claude", "claude-opus-5", 0.01, true, true, later, 3},
		{"claude haiku main only", claude, t0, "Claude", "claude-haiku", 0.4, true, false, time.Time{}, 2},
		{"non-agy unknown group falls back to all windows", claude, t0, "claude", "x", 0.01, true, true, later, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws, _ := tt.snap.Windows(tt.group, tt.model)
			if len(ws) != tt.windowsLen {
				t.Errorf("windows = %d, want %d", len(ws), tt.windowsLen)
			}
			rem, known := tt.snap.Remaining(tt.now, tt.group, tt.model)
			if rem != tt.remaining || known != tt.known {
				t.Errorf("Remaining = %v, %v; want %v, %v", rem, known, tt.remaining, tt.known)
			}
			until, ex := tt.snap.Exhausted(tt.now, tt.group, tt.model, 0.03)
			if ex != tt.exhausted || !until.Equal(tt.restUntil) {
				t.Errorf("Exhausted = %v, %v; want %v, %v", until, ex, tt.restUntil, tt.exhausted)
			}
		})
	}
}

func TestExhaustedTakesLatestResetAndRateLimit(t *testing.T) {
	a, b := t0.Add(time.Hour), t0.Add(5*time.Hour)
	s := Snapshot{Backend: "codex", FetchedAt: t0, Groups: []Group{{Name: "Codex", Windows: []Window{
		win("5h", 0, a), win("weekly", 0.01, b), win("30d", 0.9, t0.Add(100*time.Hour)),
	}}}}
	if until, ex := s.Exhausted(t0, "Codex", "", 0.03); !ex || !until.Equal(b) {
		t.Fatalf("Exhausted = %v, %v; want %v (both windows must reset)", until, ex, b)
	}
	// An exhausted window without a reset time: exhausted, time unknown.
	s.Groups[0].Windows = []Window{win("5h", 0, time.Time{})}
	if until, ex := s.Exhausted(t0, "Codex", "", 0.03); !ex || !until.IsZero() {
		t.Fatalf("Exhausted = %v, %v", until, ex)
	}
	// A reached rate limit counts even with a never-read snapshot.
	r := Snapshot{ExhaustedUntil: b}
	if until, ex := r.Exhausted(t0, "Codex", "", 0.03); !ex || !until.Equal(b) {
		t.Fatalf("rate limit Exhausted = %v, %v", until, ex)
	}
	if _, ex := r.Exhausted(b, "Codex", "", 0.03); ex {
		t.Fatal("a passed rate limit still counts")
	}
}

func TestFromBackendAndIdentity(t *testing.T) {
	reset := t0.Add(time.Hour)
	q := backend.Quota{Plan: "Plus", Email: " W@Example.com ", Entitled: false, EntitlementError: "no codex",
		Models: []string{"m"}, ExhaustedUntil: reset, ExhaustedReason: "rate",
		Groups: []backend.QuotaGroup{{Name: "Codex", Buckets: []backend.QuotaBucket{
			{Window: "5h", Label: "5 hours", WindowMinutes: 300, UsedFraction: 0.25, ResetAt: reset},
			{Window: "weekly", UsedFraction: 1.7},
		}}}}
	s := FromBackend("acc3", "codex", q, t0)
	if s.AccountID != "acc3" || s.Backend != "codex" || s.Source != SourceActive || !s.FetchedAt.Equal(t0) ||
		s.Plan != "Plus" || !s.NoPlan || s.NoPlanReason != "no codex" || !s.ExhaustedUntil.Equal(reset) ||
		len(s.Models) != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	if got := s.Groups[0].Windows; got[0].Remaining != 0.75 || got[0].Minutes != 300 || got[1].Remaining != 0 {
		t.Fatalf("windows = %+v", got)
	}
	if s.Identity == "" || s.Identity != Identity("w@example.com") || strings.Contains(s.Identity, "example") {
		t.Fatalf("identity = %q", s.Identity)
	}
	if Identity("  ") != "" || Identity("a@x.io") == Identity("b@x.io") {
		t.Fatal("Identity")
	}
	ok := FromBackend("acc1", "agy", backend.Quota{Entitled: true}, t0)
	if ok.NoPlan || ok.NoPlanReason != "" || ok.Groups == nil {
		t.Fatalf("entitled snapshot = %+v", ok)
	}
}

func TestWindowKeys(t *testing.T) {
	for mins, want := range map[int]string{300: "5h", 10080: "weekly", 43200: "30d", 90: "90m", 0: "window", -5: "window"} {
		if got := windowKey(mins); got != want {
			t.Errorf("windowKey(%d) = %q, want %q", mins, got, want)
		}
	}
	for key, want := range map[string]int{"5h": 300, "weekly": 10080, "Daily": 1440, "monthly": 43200, "30d": 43200,
		"90m": 90, "2w": 20160, "x": 0, "": 0, "zzh": 0, "5y": 0, "-3h": 0} {
		if got := windowMinutes(key); got != want {
			t.Errorf("windowMinutes(%q) = %d, want %d", key, got, want)
		}
	}
	for mins, want := range map[int]string{0: "Quota", 300: "5 hours", 10080: "Weekly", 43200: "30 days", 90: "1.5 hours"} {
		if got := windowLabel(mins); got != want {
			t.Errorf("windowLabel(%d) = %q, want %q", mins, got, want)
		}
	}
}

func TestRest(t *testing.T) {
	hint := t0.Add(20 * time.Minute)
	// A hint in the future wins over the ladder, even at its top.
	top := PrevRest{Duration: time.Hour, Until: t0}
	if got := PassiveRest(t0, hint, top, 60); !got.Equal(hint) {
		t.Errorf("PassiveRest with hint = %v", got)
	}
	if got := PassiveRest(t0, t0.Add(-time.Minute), PrevRest{}, 15); !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("PassiveRest past hint = %v", got)
	}
	if got := PassiveRest(t0, time.Time{}, PrevRest{Duration: 15 * time.Minute, Until: t0}, 15); !got.Equal(t0.Add(15 * time.Minute)) {
		t.Errorf("PassiveRest capped = %v", got)
	}
	if got := PassiveRest(t0, time.Time{}, top, 0); !got.Equal(t0.Add(time.Hour)) {
		t.Errorf("PassiveRest default longest = %v", got)
	}
	reset := t0.Add(3 * time.Hour)
	s := Snapshot{Backend: "agy", FetchedAt: t0, Groups: []Group{{Name: "G", Windows: []Window{win("5h", 0, reset)}}}}
	if got := ActiveRest(s, t0, "G", "", 0.03, hint); !got.Equal(reset) {
		t.Errorf("ActiveRest = %v, want the window reset", got)
	}
	if got := ActiveRest(s, t0, "G", "", 0.03, reset.Add(time.Hour)); !got.Equal(reset.Add(time.Hour)) {
		t.Errorf("ActiveRest with a later hint = %v", got)
	}
	fine := Snapshot{Backend: "agy", FetchedAt: t0, Groups: []Group{{Name: "G", Windows: []Window{win("5h", 0.9, reset)}}}}
	if got := ActiveRest(fine, t0, "G", "", 0.03, time.Time{}); !got.Equal(t0.Add(DefaultRest)) {
		t.Errorf("ActiveRest without data = %v", got)
	}
}

func TestNextPassiveRest(t *testing.T) {
	ended := func(d time.Duration) PrevRest { return PrevRest{Duration: d, Until: t0} }
	cases := []struct {
		name    string
		prev    PrevRest
		longest time.Duration
		want    time.Duration
	}{
		{"none", PrevRest{}, time.Hour, time.Minute},
		{"1 to 5", ended(time.Minute), time.Hour, 5 * time.Minute},
		{"5 to 15", ended(5 * time.Minute), time.Hour, 15 * time.Minute},
		{"15 to 60", ended(15 * time.Minute), time.Hour, time.Hour},
		{"60 stays", ended(time.Hour), time.Hour, time.Hour},
		{"cap 10: none", PrevRest{}, 10 * time.Minute, time.Minute},
		{"cap 10: 1 to 5", ended(time.Minute), 10 * time.Minute, 5 * time.Minute},
		{"cap 10: 5 to 10", ended(5 * time.Minute), 10 * time.Minute, 10 * time.Minute},
		{"cap 10: 10 stays", ended(10 * time.Minute), 10 * time.Minute, 10 * time.Minute},
		{"cap below the first step", PrevRest{}, 30 * time.Second, 30 * time.Second},
		{"between steps goes up", ended(3 * time.Minute), time.Hour, 5 * time.Minute},
		{"longer than the cap", ended(2 * time.Hour), time.Hour, time.Hour},
		{"ended 29 min ago", PrevRest{Duration: 5 * time.Minute, Until: t0.Add(-29 * time.Minute)}, time.Hour, 15 * time.Minute},
		{"ended 30 min ago", PrevRest{Duration: 5 * time.Minute, Until: t0.Add(-30 * time.Minute)}, time.Hour, time.Minute},
		{"ended 31 min ago", PrevRest{Duration: 15 * time.Minute, Until: t0.Add(-31 * time.Minute)}, time.Hour, time.Minute},
		{"still running keeps the step", PrevRest{Duration: 5 * time.Minute, Until: t0.Add(2 * time.Minute)}, time.Hour, 5 * time.Minute},
		{"ends at now steps up", PrevRest{Duration: 5 * time.Minute, Until: t0}, time.Hour, 15 * time.Minute},
		{"running, cap lowered", PrevRest{Duration: time.Hour, Until: t0.Add(time.Minute)}, 10 * time.Minute, 10 * time.Minute},
		{"no end", PrevRest{Duration: 5 * time.Minute}, time.Hour, time.Minute},
	}
	for _, c := range cases {
		if got := NextPassiveRest(t0, c.prev, c.longest); got != c.want {
			t.Errorf("%s: NextPassiveRest = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestKindOfAndErrorMessage(t *testing.T) {
	tests := map[error]Kind{
		fmt.Errorf("x: %w", ErrNeedsLogin):            KindNeedsLogin,
		fmt.Errorf("x: %w", ErrTokenExpired):          KindTokenExpired,
		fmt.Errorf("x: %w", ErrFormat):                KindFormat,
		fmt.Errorf("x: %w", ErrUnavailable):           KindUnavailable,
		fmt.Errorf("x: %w", context.DeadlineExceeded): KindUnavailable,
		errors.New("boom"):                            KindFailed,
	}
	for err, want := range tests {
		if got := KindOf(err); got != want {
			t.Errorf("KindOf(%v) = %s, want %s", err, got, want)
		}
	}
	e := newError(fmt.Errorf("failed for someone@example.com with sk-ant-abcdefghijklmnopqrstuvwxyz\n%s",
		strings.Repeat("x", 500)), t0)
	if strings.Contains(e.Message, "someone@example.com") || strings.Contains(e.Message, "sk-ant-abcdefghij") ||
		strings.Contains(e.Message, "\n") || len([]rune(e.Message)) > maxErrorMessage || e.Kind != KindFailed ||
		!e.At.Equal(t0) {
		t.Fatalf("error = %+v", e)
	}
}
