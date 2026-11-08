package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"runtime"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Claude reader constants (old backends/claude.js).
const (
	// ClaudeUsageURL is the endpoint Claude Code itself reads for /usage; it answers OAuth (claude.ai) sign-ins.
	ClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"
	// ClaudeTimeout bounds one usage request.
	ClaudeTimeout = 10 * time.Second
	// claudeBeta is the beta header the endpoint requires.
	claudeBeta = "oauth-2025-04-20"
	// claudeMaxBody caps the answer that is read.
	claudeMaxBody = 1 << 20
)

// ClaudeReader reads the plan quota of a Claude Code sign-in account from the OAuth usage endpoint, with the
// access token in the profile's .credentials.json. The token is sent only to that endpoint and never stored or
// logged. API-key accounts are passive. It implements backend.QuotaReader.
//
// On macOS Claude Code keeps the sign-in in the login Keychain instead of that file, so there a profile without the
// file is not signed out: its quota is unknown (noSignIn).
type ClaudeReader struct {
	client *http.Client
	url    string
	now    func() time.Time
	goos   string // runtime.GOOS; tests set another system
}

// NewClaudeReader returns the reader. client nil uses a default client; url "" is ClaudeUsageURL; now nil is
// time.Now (used to see that a token expired).
func NewClaudeReader(client *http.Client, url string, now func() time.Time) *ClaudeReader {
	if client == nil {
		client = &http.Client{Timeout: ClaudeTimeout}
	}
	if url == "" {
		url = ClaudeUsageURL
	}
	if now == nil {
		now = time.Now
	}
	return &ClaudeReader{client: client, url: url, now: now, goos: runtime.GOOS}
}

// claudeCredentials is the part of .credentials.json the reader needs.
type claudeCredentials struct {
	ClaudeAiOauth *struct {
		AccessToken string  `json:"accessToken"`
		ExpiresAt   float64 `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

// claudeWindow is one window of the usage answer.
type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

// claudeWindows lists the windows of the usage answer: field, group, window key and minutes.
var claudeWindows = []struct {
	field, group, key, label string
	minutes                  int
}{
	{"seven_day", "Claude", "weekly", "Weekly", 10080},
	{"five_hour", "Claude", "5h", "Five hours", 300},
	{"seven_day_opus", "Claude Opus", "weekly", "Weekly (Opus)", 10080},
	{"seven_day_sonnet", "Claude Sonnet", "weekly", "Weekly (Sonnet)", 10080},
}

// ReadQuota implements backend.QuotaReader.
func (r *ClaudeReader) ReadQuota(ctx context.Context, prof account.Profile, _ []string) (backend.Quota, error) {
	if prof.Kind == account.KindAPIKey {
		return backend.Quota{}, ErrPassive
	}
	token, err := r.token(prof.Home)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("claude quota: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, ClaudeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, http.NoBody)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("claude quota: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", claudeBeta)
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		// The URL and token are not part of the message: only the cause.
		var uerr interface{ Unwrap() error }
		if errors.As(err, &uerr) && uerr.Unwrap() != nil {
			err = uerr.Unwrap()
		}
		return backend.Quota{}, fmt.Errorf("claude quota: %w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, claudeMaxBody))
	if err != nil {
		return backend.Quota{}, fmt.Errorf("claude quota: %w: %w", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return backend.Quota{}, fmt.Errorf("claude quota: %w: the server refused the sign-in (HTTP 401)", ErrNeedsLogin)
	case resp.StatusCode == http.StatusForbidden:
		// 403 is a missing scope, a block or a WAF answer, not a signed-out account: the account keeps its last
		// numbers and is read again next cycle.
		return backend.Quota{}, errors.New("claude quota: the server refused the request (HTTP 403)")
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return backend.Quota{}, fmt.Errorf("claude quota: %w: HTTP %d", ErrUnavailable, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return backend.Quota{}, fmt.Errorf("claude quota: HTTP %d", resp.StatusCode)
	}
	q, err := parseClaudeUsage(body)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("claude quota: %w", err)
	}
	q.Email = claudeReadEmail(prof.Home)
	return q, nil
}

// token returns the OAuth access token of the profile.
func (r *ClaudeReader) token(home string) (string, error) {
	data, err := fsutil.ReadFile(filepath.Join(home, ".credentials.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", r.noSignIn("no Claude Code sign-in in the profile")
	}
	if err != nil {
		return "", fmt.Errorf("read sign-in: %w", err)
	}
	var c claudeCredentials
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("%w: the sign-in file is not valid JSON", ErrFormat)
	}
	if c.ClaudeAiOauth == nil || c.ClaudeAiOauth.AccessToken == "" {
		return "", r.noSignIn("no OAuth sign-in in the profile")
	}
	if exp := c.ClaudeAiOauth.ExpiresAt; exp > 0 && time.UnixMilli(int64(exp)).Before(r.now()) {
		return "", fmt.Errorf("%w: renewed on the next run", ErrTokenExpired)
	}
	return c.ClaudeAiOauth.AccessToken, nil
}

// noSignIn is the error for a profile whose .credentials.json has no OAuth token (what says why). That means
// signed out on Linux and Windows. On macOS Claude Code stores the sign-in in the login Keychain (a generic password
// whose service name gets a suffix derived from CLAUDE_CONFIG_DIR) and may write no file at all, so there it says
// nothing: the quota is unavailable, the account stays usable, and a run refused for its sign-in still marks it
// signed out (pool). Reading the token from the Keychain (`security find-generic-password`) is left until it can
// be verified on a Mac (bugs.md, macOS Keychain).
func (r *ClaudeReader) noSignIn(what string) error {
	if r.goos == "darwin" {
		return fmt.Errorf("%w: the macOS Keychain holds the Claude Code sign-in; quota is not read", ErrUnavailable)
	}
	return fmt.Errorf("%w: %s", ErrNeedsLogin, what)
}

// parseClaudeUsage maps the usage answer to quota groups: Claude (weekly, 5h), Claude Opus and Claude Sonnet
// (weekly sub-limits of those model families).
func parseClaudeUsage(body []byte) (backend.Quota, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return backend.Quota{}, fmt.Errorf("%w: the answer is not a JSON object", ErrFormat)
	}
	q := backend.Quota{Entitled: true, Groups: []backend.QuotaGroup{}}
	index := map[string]int{}
	for _, w := range claudeWindows {
		raw, ok := fields[w.field]
		if !ok || string(raw) == "null" {
			continue
		}
		var cw claudeWindow
		if err := json.Unmarshal(raw, &cw); err != nil || cw.Utilization == nil {
			continue
		}
		i, ok := index[w.group]
		if !ok {
			i = len(q.Groups)
			index[w.group] = i
			q.Groups = append(q.Groups, backend.QuotaGroup{Name: w.group})
		}
		q.Groups[i].Buckets = append(q.Groups[i].Buckets, backend.QuotaBucket{
			Window: w.key, Label: w.label, WindowMinutes: w.minutes, UsedFraction: clamp01(*cw.Utilization / 100),
			ResetAt: parseTime(cw.ResetsAt),
		})
	}
	if len(q.Groups) == 0 {
		return backend.Quota{}, fmt.Errorf("%w: no quota window in the answer", ErrFormat)
	}
	return q, nil
}

// claudeReadEmail returns oauthAccount.emailAddress of the profile's .claude.json ("" when unknown).
func claudeReadEmail(home string) string {
	data, err := fsutil.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var c struct {
		OauthAccount *struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &c) != nil || c.OauthAccount == nil {
		return ""
	}
	return c.OauthAccount.EmailAddress
}
