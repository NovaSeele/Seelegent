package quota

import "time"

// Rest defaults (old pool.js).
const (
	// DefaultRest is how long an account with active quota rests after a quota error when no exhausted window says
	// when it resets.
	DefaultRest = time.Hour
	// DefaultPassiveRest is the rest of a passive account when passiveCooldownMinutes is not set.
	DefaultPassiveRest = 60 * time.Minute
)

// PassiveRest returns until when an account with passive quota (API key) rests after a limit error at now: the
// reset time the provider gave (hint) when it lies in the future, else now plus passiveMinutes
// (passiveCooldownMinutes; 60 when not positive).
func PassiveRest(now, hint time.Time, passiveMinutes int) time.Time {
	if hint.After(now) {
		return hint
	}
	d := DefaultPassiveRest
	if passiveMinutes > 0 {
		d = time.Duration(passiveMinutes) * time.Minute
	}
	return now.Add(d)
}

// ActiveRest returns until when an account with active quota rests after a quota error for model in group at
// now: the latest reset of the exhausted windows of its (freshly read) snapshot, or the provider's hint, whichever
// is later; DefaultRest from now when neither says.
func ActiveRest(s Snapshot, now time.Time, group, model string, minRemaining float64, hint time.Time) time.Time {
	until, _ := s.Exhausted(now, group, model, minRemaining)
	if hint.After(until) {
		until = hint
	}
	if !until.After(now) {
		until = now.Add(DefaultRest)
	}
	return until
}
