package pool

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// TestMaxConcurrentWorkers: maxConcurrentWorkers caps the turns running over all accounts (0 = no cap); lowering
// it stops nothing, it only holds new starts, and a release wakes a waiting request.
func TestMaxConcurrentWorkers(t *testing.T) {
	f := newFixture(t)
	a := f.agy(0)
	b := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	f.read(b.ID, "agy", 1, time.Time{})
	f.setting(func(s *config.Settings) { s.MaxConcurrentPerAccount, s.MaxConcurrentWorkers = 2, 2 })
	l1, _ := f.acquire(gemini("t1"))
	l2, _ := f.acquire(gemini("t2"))
	refused := func(want int) {
		t.Helper()
		_, _, err := f.p.Acquire(t.Context(), gemini("t3"))
		var u *Unavailable
		if !errors.As(err, &u) || u.Reason != ReasonBusy || u.Capped != want || !errors.Is(err, ErrAllBusy) {
			t.Fatalf("Acquire under the cap: %v (%+v)", err, u)
		}
		if !strings.Contains(u.Error(), "maxConcurrentWorkers") {
			t.Errorf("message %q", u.Error())
		}
	}
	refused(2)
	if c, err := f.p.Select(t.Context(), gemini("t3")); err == nil {
		t.Fatalf("Select under the cap chose %s", c.Account.ID)
	}

	// Lowering the cap below the running turns stops nothing.
	f.setting(func(s *config.Settings) { s.MaxConcurrentWorkers = 1 })
	f.p.Notify()
	if n := f.p.Busy(a.ID) + f.p.Busy(b.ID); n != 2 {
		t.Fatalf("running turns after lowering the cap = %d, want 2", n)
	}
	l1.Release()
	refused(1) // one still runs: at the cap

	// A waiting request starts when the last running turn ends.
	done, seen := f.startWait(t.Context(), gemini("t3"))
	f.waitBlocked()
	if s := seen(); len(s) == 0 || s[0].Capped != 1 {
		t.Fatalf("wait reasons %+v", s)
	}
	l2.Release()
	r := receive(t, done)
	if r.err != nil || r.lease.TaskID() != "t3" {
		t.Fatalf("wait result %+v", r)
	}

	// 0 is no cap.
	f.setting(func(s *config.Settings) { s.MaxConcurrentWorkers = 0 })
	f.acquire(gemini("t4"))
	f.acquire(gemini("t5"))
	f.acquire(gemini("t6"))
}
