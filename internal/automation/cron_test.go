package automation

import (
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := Zone(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestCronNext(t *testing.T) {
	utc := time.UTC
	from := time.Date(2026, 11, 7, 10, 30, 0, 0, utc) // a Saturday
	cases := []struct {
		expr string
		want string
	}{
		{"*/15 * * * *", "2026-11-07T10:45:00Z"},
		{"0 9 * * *", "2026-11-08T09:00:00Z"},
		{"0 9 * * 1-5", "2026-11-09T09:00:00Z"},
		{"0 9 * * mon-fri", "2026-11-09T09:00:00Z"},
		{"0 9 * * 7", "2026-11-08T09:00:00Z"},
		{"30 10 * * *", "2026-11-08T10:30:00Z"},
		{"0 0 1 jan *", "2027-01-01T00:00:00Z"},
		{"0 12 15 * 1", "2026-11-09T12:00:00Z"}, // either day field
		{"5,10 */6 * * *", "2026-11-07T12:05:00Z"},
		{"0 0 29 2 *", "2028-02-29T00:00:00Z"},
		{"10/20 * * * *", "2026-11-07T10:50:00Z"},
	}
	for _, c := range cases {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.expr, err)
		}
		got, ok := cr.Next(from, utc)
		if !ok || got.Format(time.RFC3339) != c.want {
			t.Errorf("%s: %v %v, want %s", c.expr, got, ok, c.want)
		}
	}
	never, _ := ParseCron("0 0 30 2 *")
	if _, ok := never.Next(from, utc); ok {
		t.Error("February 30 ran")
	}
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8",
		"*/0 * * * *", "5-1 * * * *", "a * * * *", "1-2-3 * * * *", "1/2/3 * * * *", ",1 * * * *", "-1 * * * *"} {
		if _, err := ParseCron(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestCronZones(t *testing.T) {
	ny := mustZone(t, "America/New_York")
	c, _ := ParseCron("30 2 * * *")
	// 2026-03-08 02:30 does not exist in New York: the run happens an hour later, at 03:30.
	got, ok := c.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny), ny)
	if !ok || got.In(ny).Format("2006-01-02 15:04") != "2026-03-08 03:30" {
		t.Fatalf("spring forward: %v", got.In(ny))
	}
	// 2026-11-01 01:30 happens twice: the run happens once.
	c, _ = ParseCron("30 1 * * *")
	first, _ := c.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, ny), ny)
	second, _ := c.Next(first, ny)
	if second.Sub(first) < 20*time.Hour {
		t.Fatalf("fall back ran twice: %v then %v", first, second)
	}
	tokyo := mustZone(t, "Asia/Tokyo")
	c, _ = ParseCron("0 9 * * *")
	got, _ = c.Next(time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC), tokyo)
	if got.Format(time.RFC3339) != "2026-11-08T00:00:00Z" {
		t.Fatalf("tokyo 09:00 = %v", got)
	}
	for _, bad := range []string{"Mars/Base", "../etc/passwd", `C:\x`, "/usr/share/zoneinfo/UTC"} {
		if _, err := Zone(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("zone %q: %v", bad, err)
		}
	}
	if loc, err := Zone("local"); err != nil || loc != time.Local {
		t.Fatalf("local zone: %v %v", loc, err)
	}
}

func TestScheduleCron(t *testing.T) {
	wd := 3
	cases := []struct {
		s    api.Schedule
		want string
	}{
		{api.Schedule{Every: api.EveryHourly, At: "10:15"}, "15 * * * *"},
		{api.Schedule{Every: api.EveryHourly}, "0 * * * *"},
		{api.Schedule{Every: api.EveryDaily}, "0 9 * * *"},
		{api.Schedule{Every: api.EveryDaily, At: "7:05"}, "5 7 * * *"},
		{api.Schedule{Every: api.EveryWeekdays, At: "18:00"}, "0 18 * * 1-5"},
		{api.Schedule{Every: api.EveryWeekly}, "0 9 * * 1"},
		{api.Schedule{Every: api.EveryWeekly, Weekday: &wd, At: "08:30"}, "30 8 * * 3"},
		{api.Schedule{Cron: " 1 2 3 4 5 "}, "1 2 3 4 5"},
	}
	for _, c := range cases {
		got, err := ScheduleCron(c.s)
		if err != nil || got != c.want {
			t.Errorf("%+v: %q %v", c.s, got, err)
		}
	}
	bad := 7
	for _, s := range []api.Schedule{{}, {Every: "monthly"}, {Every: api.EveryDaily, At: "24:00"},
		{Every: api.EveryDaily, At: "9"}, {Every: api.EveryDaily, At: "09:5"}, {Every: api.EveryWeekly, Weekday: &bad}} {
		if _, err := ScheduleCron(s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted: %v", s, err)
		}
	}
}
