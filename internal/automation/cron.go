package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA zones on machines without a zone database (Windows)

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Cron is a parsed 5-field cron expression: minute hour day-of-month month day-of-week, each a set of values
// with *, lists, ranges, steps and the names jan-dec and sun-sat (7 is Sunday too).
type Cron struct {
	minute uint64
	hour   uint32
	dom    uint32
	month  uint16
	dow    uint8
	// dayAnd: the day-of-month or the day-of-week field starts with "*" ("*", "*/2"), and a day must match both.
	// Otherwise a day matching either one runs. This is Vixie cron's rule (cronie and most crons follow it):
	// "0 9 */2 * 1" runs on Mondays that are odd days, "0 9 1,15 * 1" on the 1st, the 15th and every Monday.
	dayAnd bool
}

type cronField struct {
	name     string
	min, max int
	names    []string
	base     int
}

var (
	monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	dayNames   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
	cronFields = []cronField{
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day of month", min: 1, max: 31},
		{name: "month", min: 1, max: 12, names: monthNames, base: 1},
		{name: "day of week", min: 0, max: 7, names: dayNames, base: 0},
	}
)

func cronErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (f cronField) value(s string) (int, error) {
	if n, err := strconv.Atoi(s); err == nil && s != "" && s[0] != '+' && s[0] != '-' {
		return n, nil
	}
	for i, n := range f.names {
		if s == n {
			return i + f.base, nil
		}
	}
	return 0, cronErr("cron %s: bad value %q", f.name, s)
}

// parse returns the field's values as a bit set.
func (f cronField) parse(text string) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(text, ",") {
		rng, stepText, hasStep := strings.Cut(part, "/")
		if part == "" || strings.Contains(stepText, "/") {
			return 0, cronErr("cron %s: bad part %q", f.name, part)
		}
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 || stepText[0] == '+' {
				return 0, cronErr("cron %s: bad step %q", f.name, stepText)
			}
			step = n
		}
		var lo, hi int
		switch {
		case rng == "*":
			lo, hi = f.min, f.max
			if f.name == "day of week" {
				hi = 6
			}
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			if strings.Contains(b, "-") {
				return 0, cronErr("cron %s: bad range %q", f.name, rng)
			}
			var err error
			if lo, err = f.value(a); err != nil {
				return 0, err
			}
			if hi, err = f.value(b); err != nil {
				return 0, err
			}
		default:
			v, err := f.value(rng)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
			if hasStep {
				hi = f.max
			}
		}
		if lo < f.min || hi > f.max || lo > hi {
			return 0, cronErr("cron %s: %s is outside %d-%d", f.name, rng, f.min, f.max)
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	if f.name == "day of week" && set&(1<<7) != 0 {
		set = set&^(1<<7) | 1
	}
	return set, nil
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (Cron, error) {
	parts := strings.Fields(strings.ToLower(expr))
	if len(parts) != 5 {
		return Cron{}, cronErr("cron needs 5 fields (minute hour day-of-month month day-of-week), got %d", len(parts))
	}
	var sets [5]uint64
	for i, p := range parts {
		s, err := cronFields[i].parse(p)
		if err != nil {
			return Cron{}, err
		}
		sets[i] = s
	}
	// Each field's bits stay below its maximum (23, 31, 12 and 6), so the narrower types hold them.
	return Cron{minute: sets[0], hour: uint32(sets[1]), dom: uint32(sets[2]), //nolint:gosec // see above
		month: uint16(sets[3]), dow: uint8(sets[4]), //nolint:gosec // see above
		dayAnd: strings.HasPrefix(parts[2], "*") || strings.HasPrefix(parts[4], "*")}, nil
}

func (c Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<t.Day()) != 0
	dow := c.dow&(1<<int(t.Weekday())) != 0
	if c.dayAnd {
		return dom && dow
	}
	return dom || dow
}

// searchYears is how far Next looks ahead.
const searchYears = 5

// Next returns the first minute strictly after from that matches c in loc (false when none within 5 years, as
// for "0 0 30 2 *"). The search runs on wall-clock time: a time a daylight-saving change skips runs as late as the
// gap is long (02:30 becomes 03:30), and a time that happens twice runs once.
func (c Cron) Next(from time.Time, loc *time.Location) (time.Time, bool) {
	l := from.In(loc)
	wall := time.Date(l.Year(), l.Month(), l.Day(), l.Hour(), l.Minute(), 0, 0, time.UTC).Add(time.Minute)
	limit := wall.AddDate(searchYears, 0, 0)
	for wall.Before(limit) {
		y, mo, d := wall.Date()
		switch {
		case c.month&(1<<int(mo)) == 0:
			wall = time.Date(y, mo+1, 1, 0, 0, 0, 0, time.UTC)
		case !c.dayMatches(wall):
			wall = time.Date(y, mo, d+1, 0, 0, 0, 0, time.UTC)
		case c.hour&(1<<wall.Hour()) == 0:
			wall = time.Date(y, mo, d, wall.Hour()+1, 0, 0, 0, time.UTC)
		case c.minute&(1<<wall.Minute()) == 0:
			wall = wall.Add(time.Minute)
		default:
			at := time.Date(y, mo, d, wall.Hour(), wall.Minute(), 0, 0, loc)
			if l := at.In(loc); l.Hour() != wall.Hour() || l.Minute() != wall.Minute() {
				// In a gap time.Date may go back; use the offset from before the gap, which lands after it.
				_, off := at.Zone()
				at = wall.Add(-time.Duration(off) * time.Second)
			}
			if at.After(from) {
				return at.UTC(), true
			}
			wall = wall.Add(time.Minute)
		}
	}
	return time.Time{}, false
}

// ScheduleCron turns a schedule into its cron expression: a preset's At is HH:MM (hourly uses only the minutes;
// default 09:00, hourly :00) and weekly runs on Weekday (default Monday).
func ScheduleCron(s api.Schedule) (string, error) {
	if c := strings.TrimSpace(s.Cron); c != "" {
		return c, nil
	}
	at := s.At
	if at == "" {
		at = "09:00"
		if s.Every == api.EveryHourly {
			at = "00:00"
		}
	}
	hh, mm, ok := strings.Cut(at, ":")
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if !ok || err1 != nil || err2 != nil || len(hh) < 1 || len(hh) > 2 || len(mm) != 2 || h < 0 || h > 23 || m < 0 ||
		m > 59 {
		return "", cronErr("schedule.at must be HH:MM")
	}
	switch s.Every {
	case api.EveryHourly:
		return fmt.Sprintf("%d * * * *", m), nil
	case api.EveryDaily:
		return fmt.Sprintf("%d %d * * *", m, h), nil
	case api.EveryWeekdays:
		return fmt.Sprintf("%d %d * * 1-5", m, h), nil
	case api.EveryWeekly:
		wd := 1
		if s.Weekday != nil {
			wd = *s.Weekday
		}
		if wd < 0 || wd > 6 {
			return "", cronErr("schedule.weekday must be 0-6 (0 = Sunday)")
		}
		return fmt.Sprintf("%d %d * * %d", m, h, wd), nil
	}
	return "", cronErr("schedule needs every (hourly, daily, weekdays or weekly) or cron")
}

// Zone loads a time zone: "local" (or "") is the machine's zone, anything else an IANA name.
func Zone(tz string) (*time.Location, error) {
	if tz == "" || tz == "local" {
		return time.Local, nil
	}
	if strings.ContainsAny(tz, `\`) || strings.HasPrefix(tz, "/") || strings.Contains(tz, "..") || len(tz) > 64 {
		return nil, cronErr("unknown timezone: %s", tz)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, cronErr("unknown timezone: %s", tz)
	}
	return loc, nil
}

// NextRun is the first run of schedule s in zone tz after from.
func NextRun(s api.Schedule, tz string, from time.Time) (time.Time, bool, error) {
	expr, err := ScheduleCron(s)
	if err != nil {
		return time.Time{}, false, err
	}
	c, err := ParseCron(expr)
	if err != nil {
		return time.Time{}, false, err
	}
	loc, err := Zone(tz)
	if err != nil {
		return time.Time{}, false, err
	}
	t, ok := c.Next(from, loc)
	return t, ok, nil
}
