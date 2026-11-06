package quota

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// claudeUsageOK is a recorded /api/oauth/usage answer (values changed, shape kept).
const claudeUsageOK = `{"five_hour":{"utilization":40,"resets_at":"2026-11-06T15:00:00+00:00"},
"seven_day":{"utilization":12.5,"resets_at":"2026-11-10T00:00:00Z"},
"seven_day_opus":{"utilization":100,"resets_at":"2026-11-10T00:00:00Z"},
"seven_day_sonnet":null,"extra_usage":{"is_enabled":false}}`

func claudeProfile(t *testing.T, expiresAt time.Time) account.Profile {
	t.Helper()
	prof := testProfile(t, account.KindIsolated)
	writeFile(t, filepath.Join(prof.Home, ".credentials.json"),
		`{"claudeAiOauth":{"accessToken":"tok-123","refreshToken":"r","expiresAt":`+
			strconv.FormatInt(expiresAt.UnixMilli(), 10)+`}}`)
	writeFile(t, filepath.Join(prof.Home, ".claude.json"), `{"oauthAccount":{"emailAddress":"c@example.com"}}`)
	return prof
}

func TestClaudeReader(t *testing.T) {
	var status int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" || r.Header.Get("anthropic-beta") != claudeBeta {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	now := func() time.Time { return t0 }
	r := NewClaudeReader(srv.Client(), srv.URL, now)
	prof := claudeProfile(t, t0.Add(time.Hour))

	status, body = http.StatusOK, claudeUsageOK
	q, err := r.ReadQuota(t.Context(), prof, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Email != "c@example.com" || !q.Entitled || len(q.Groups) != 2 || q.Groups[0].Name != "Claude" ||
		len(q.Groups[0].Buckets) != 2 || q.Groups[1].Name != "Claude Opus" {
		t.Fatalf("quota = %+v", q)
	}
	if b := q.Groups[0].Buckets[1]; b.Window != "5h" || b.UsedFraction != 0.4 || b.WindowMinutes != 300 ||
		!b.ResetAt.Equal(time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)) {
		t.Fatalf("5h bucket = %+v", b)
	}
	if q.Groups[1].Buckets[0].UsedFraction != 1 {
		t.Fatalf("opus = %+v", q.Groups[1])
	}

	tests := []struct {
		name   string
		status int
		body   string
		want   Kind
	}{
		{"refused", http.StatusUnauthorized, `{}`, KindNeedsLogin},
		{"forbidden is not signed out", http.StatusForbidden, `{}`, KindFailed},
		{"server error", http.StatusBadGateway, `oops`, KindUnavailable},
		{"rate limited", http.StatusTooManyRequests, `{}`, KindUnavailable},
		{"other status", http.StatusNotFound, `{}`, KindFailed},
		{"not JSON", http.StatusOK, `<html>`, KindFormat},
		{"no windows", http.StatusOK, `{"five_hour":null}`, KindFormat},
		{"changed field types", http.StatusOK, `{"five_hour":{"utilization":"40%"},"seven_day":[1]}`, KindFormat},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body = tt.status, tt.body
			_, err := r.ReadQuota(t.Context(), prof, nil)
			if KindOf(err) != tt.want {
				t.Fatalf("err = %v (kind %s), want %s", err, KindOf(err), tt.want)
			}
			if err != nil && strings.Contains(err.Error(), "tok-123") {
				t.Fatalf("the token leaked into the error: %v", err)
			}
		})
	}
}

func TestClaudeReaderCredentials(t *testing.T) {
	r := NewClaudeReader(nil, "http://127.0.0.1:1/never", func() time.Time { return t0 })
	if _, err := r.ReadQuota(t.Context(), testProfile(t, account.KindIsolated), nil); !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("no credentials = %v", err)
	}
	if _, err := r.ReadQuota(t.Context(), claudeProfile(t, t0.Add(-time.Minute)), nil); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired = %v", err)
	}
	if _, err := r.ReadQuota(t.Context(), testProfile(t, account.KindAPIKey), nil); !errors.Is(err, ErrPassive) {
		t.Fatalf("api key = %v", err)
	}
	bad := testProfile(t, account.KindIsolated)
	writeFile(t, filepath.Join(bad.Home, ".credentials.json"), `{not json`)
	if _, err := r.ReadQuota(t.Context(), bad, nil); !errors.Is(err, ErrFormat) {
		t.Fatalf("bad credentials = %v", err)
	}
	empty := testProfile(t, account.KindIsolated)
	writeFile(t, filepath.Join(empty.Home, ".credentials.json"), `{"claudeAiOauth":{}}`)
	if _, err := r.ReadQuota(t.Context(), empty, nil); !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("empty credentials = %v", err)
	}
	// A server that cannot be reached is "unavailable", without the token in the message.
	_, err := r.ReadQuota(t.Context(), claudeProfile(t, t0.Add(time.Hour)), nil)
	if KindOf(err) != KindUnavailable || strings.Contains(err.Error(), "tok-123") {
		t.Fatalf("unreachable = %v", err)
	}
	if NewClaudeReader(nil, "", nil).url != ClaudeUsageURL {
		t.Fatal("default url")
	}
	if got := claudeReadEmail(t.TempDir()); got != "" {
		t.Fatalf("email = %q", got)
	}
}
