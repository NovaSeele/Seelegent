package quota

import "time"

// Rest defaults (old pool.js).
const (
	// DefaultRest is how long an account with active quota rests after a quota error when no exhausted window says
	// when it resets.
	DefaultRest = time.Hour
	// DefaultPassiveRest is the longest rest of a passive account when passiveCooldownMinutes is not set.
	DefaultPassiveRest = 60 * time.Minute
	// PassiveLadderGap is how long after a passive rest ended a new limit error still climbs the rest ladder
	// (NextPassiveRest); a later error starts again at the first step.
	PassiveLadderGap = 30 * time.Minute
)

// PrevRest is the previous passive rest of an account for a quota group: how long it was set for and when it
// ends or ended. The zero value means there was none (or it does not count, being an active account's rest).
type PrevRest struct {
	Duration time.Duration
	Until    time.Time
}

// NextPassiveRest returns how long a passive account (API key) rests after a limit error at now with no reset
// hint. A limit on an API key is mostly a per-minute rate limit, so rests climb a ladder: 1 minute, then 5, then
// 15, then longest, each step capped at longest. The step after prev is used while prev still runs or ended less
// than PassiveLadderGap before now; otherwise (no previous rest, or an old one) the ladder starts again at 1
// minute. A previous rest of a length between steps (a provider's hint, an older setting) goes on to the next
// longer step.
func NextPassiveRest(now time.Time, prev PrevRest, longest time.Duration) time.Duration {
	steps := [...]time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, longest}
	if prev.Duration <= 0 || now.Sub(prev.Until) >= PassiveLadderGap {
		return min(steps[0], longest)
	}
	for _, s := range steps {
		if s = min(s, longest); s > prev.Duration {
			return s
		}
	}
	return longest
}

// PassiveRest returns until when an account with passive quota (API key) rests after a limit error at now: the
// reset time the provider gave (hint) when it lies in the future, else now plus the next step of the rest ladder
// after prev (NextPassiveRest), whose longest step is passiveMinutes (passiveCooldownMinutes; 60 when not
// positive).
func PassiveRest(now, hint time.Time, prev PrevRest, passiveMinutes int) time.Time {
	if hint.After(now) {
		return hint
	}
	longest := DefaultPassiveRest
	if passiveMinutes > 0 {
		longest = time.Duration(passiveMinutes) * time.Minute
	}
	return now.Add(NextPassiveRest(now, prev, longest))
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
