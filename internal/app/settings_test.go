package app

import (
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// countReschedule stands in for the quota refresher.
type countReschedule struct{ n atomic.Int32 }

func (c *countReschedule) Reschedule() { c.n.Add(1) }

// TestPatchSettingsReschedulesQuota: PATCH /settings re-arms the quota refresher when quotaRefreshMinutes changes,
// and only then (bugs.md F75).
func TestPatchSettingsReschedulesQuota(t *testing.T) {
	d := newDaemon(t)
	fake := &countReschedule{}
	d.a.rescheduler = fake
	addr, err := d.a.ListenHTTP(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	patch := func(body string) {
		t.Helper()
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, "http://"+addr+"/api/v1/settings",
			strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-Seelegent", "1")
		r.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("PATCH %s: %s", body, res.Status)
		}
	}
	cur := d.a.Settings().QuotaRefreshMinutes
	next := cur + 7
	for _, c := range []struct {
		body string
		want int32
	}{
		{`{"maxConcurrentPerAccount": 3}`, 0},
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(next) + `}`, 1},
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(next) + `}`, 1}, // unchanged
		{`{"quotaRefreshMinutes": ` + strconv.Itoa(cur) + `}`, 2},
	} {
		patch(c.body)
		if got := fake.n.Load(); got != c.want {
			t.Errorf("after %s: %d reschedules, want %d", c.body, got, c.want)
		}
	}
	if got := d.a.Settings().QuotaRefreshMinutes; got != cur {
		t.Errorf("quotaRefreshMinutes = %d, want %d", got, cur)
	}
}
