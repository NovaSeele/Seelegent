package quota

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// captureHandler keeps the level and message of every record.
type captureHandler struct {
	mu    sync.Mutex
	lines []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *captureHandler) WithGroup(string) slog.Handler            { return h }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lines = append(h.lines, r.Level.String()+" "+r.Message)
	return nil
}

// take returns the lines logged since the last call.
func (h *captureHandler) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.lines
	h.lines = nil
	return out
}

// A failure is logged at Warn when its kind changes, at Debug while the same kind repeats (also after a restart,
// since the last kind is the stored one), and a recovery once at Info.
func TestRefresherLogsKindChangesOnly(t *testing.T) {
	f := newFixture(t)
	store := NewStore(f.db)
	acc := f.add(t, account.New{Backend: "claude", Kind: account.KindIsolated, Enabled: true})
	var next error
	reader := &scriptReader{fn: func(context.Context, string) (backend.Quota, error) {
		if next != nil {
			return backend.Quota{}, next
		}
		return okQuota(0.5), nil
	}}
	logs := &captureHandler{}
	newRefresher := func() *Refresher {
		r, err := NewRefresher(RefresherOptions{
			Accounts: f.m, Store: store, Logger: slog.New(logs),
			Env: func(_ context.Context, a account.Account) (account.Profile, []string, error) {
				return account.Profile{AccountID: a.ID, Kind: a.Kind}, nil, nil
			},
			Readers: map[string]backend.QuotaReader{"claude": reader},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := newRefresher()
	expired := fmt.Errorf("claude quota: %w: renewed on the next run", ErrTokenExpired)
	steps := []struct {
		name    string
		err     error
		restart bool
		want    string // "" = no line
	}{
		{"first failure", expired, false, "WARN quota refresh failed"},
		{"same kind again", expired, false, "DEBUG quota refresh failed again"},
		{"same kind after a restart", expired, true, "DEBUG quota refresh failed again"},
		{"another kind", ErrUnavailable, false, "WARN quota refresh failed"},
		{"recovered", nil, false, "INFO quota refresh recovered"},
		{"still fine", nil, false, ""},
		{"nothing to read", ErrPassive, false, ""},
		{"fails again", expired, false, "WARN quota refresh failed"},
	}
	for _, s := range steps {
		if s.restart {
			r = newRefresher()
		}
		next = s.err
		_, _ = r.Refresh(t.Context(), acc.ID)
		got := logs.take()
		switch {
		case s.want == "" && len(got) != 0:
			t.Fatalf("%s: logged %q, want nothing", s.name, got)
		case s.want != "" && (len(got) != 1 || got[0] != s.want):
			t.Fatalf("%s: logged %q, want %q", s.name, got, s.want)
		}
	}
}
