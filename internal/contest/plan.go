package contest

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// Limits of a contest.
const (
	MinVariants     = 2
	MaxVariants     = 5
	DefaultDeadline = 60
	MaxDeadline     = 24 * 60
	// QuotaFloor is the remaining fraction under which a pool's best account does not run a variant.
	QuotaFloor = 0.2
)

// PoolAccount is what the quota plan needs to know about one account.
type PoolAccount struct {
	ID       string
	Backend  string
	Provider string
	// Usable is false for an account that is off, signed out, without a key or resting.
	Usable bool
	// Busy is the number of turns running on it.
	Busy int
	// Remaining is the account's remaining quota fraction for the backend's default group (nil when unknown).
	Remaining *float64
}

func label(i int) string { return "V" + strconv.Itoa(i+1) }

func labelIndex(l string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(l, "V"))
	if err != nil {
		return 1 << 20
	}
	return n
}

// poolKey is the account pool a spec draws from: provider:<id> for API-key providers, else the backend.
func poolKey(s api.VariantSpec, member func(string) (api.Member, bool)) string {
	provider, backend := s.Provider, s.Backend
	if m, ok := member(s.Member); ok && s.Member != "" {
		provider = cmpOr(provider, m.Provider)
		backend = cmpOr(backend, m.Backend)
	}
	if provider != "" {
		return "provider:" + provider
	}
	return cmpOr(backend, config.BackendAgy)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// capacity is the capacity of the pool key: accounts, usable ones (quota above minRemaining), free slots and the
// best remaining fraction.
func capacity(accounts []PoolAccount, key string, perAccount int, minRemaining float64) api.PoolCap {
	var c api.PoolCap
	provider, isProvider := strings.CutPrefix(key, "provider:")
	for _, a := range accounts {
		if isProvider {
			if a.Provider != provider {
				continue
			}
		} else if a.Backend != key || a.Provider != "" {
			continue
		}
		c.Accounts++
		if !a.Usable {
			continue
		}
		if a.Remaining != nil {
			r := *a.Remaining
			if c.Best == nil || r > *c.Best {
				c.Best = &r
			}
			if r <= minRemaining {
				continue
			}
		}
		c.Usable++
		c.Slots += max(0, perAccount-a.Busy)
	}
	return c
}

// isTier reports whether s names a tier rather than a model.
func isTier(s string) bool { return slices.Contains(config.Tiers(), s) }

// defaultSpecs makes count specs that differ: the enabled backends with free slots in turn (agy first), then their
// tiers. A base member or provider pins every spec to it.
func defaultSpecs(count int, base api.VariantSpec, st config.Settings, accounts []PoolAccount,
	member func(string) (api.Member, bool)) []api.VariantSpec {
	if count <= 0 {
		return nil
	}
	per, floor := st.MaxConcurrentPerAccount, st.MinRemaining
	var backends []string
	if base.Member != "" || base.Provider != "" || base.Backend != "" {
		b := base.Backend
		if m, ok := member(base.Member); ok && b == "" {
			b = m.Backend
		}
		backends = []string{cmpOr(b, config.BackendAgy)}
	} else {
		for _, id := range []string{config.BackendAgy, config.BackendClaude, config.BackendCodex} {
			cfg, ok := st.Backends.Backend(id)
			if !ok || !cfg.Enabled {
				continue
			}
			c := capacity(accounts, id, per, floor)
			if c.Slots > 0 && (c.Best == nil || *c.Best >= QuotaFloor) {
				backends = append(backends, id)
			}
		}
		if len(backends) == 0 {
			backends = []string{config.BackendAgy}
		}
	}
	models := map[string][]string{}
	for _, b := range backends {
		cfg, _ := st.Backends.Backend(b)
		first := base.Model
		if m, ok := member(base.Member); ok && first == "" {
			first = cmpOr(m.Model, m.Tier)
		}
		var list []string
		seen := map[string]bool{}
		for _, t := range []string{first, config.TierStrong, config.TierStandard, config.TierFast, config.TierMax} {
			id := t
			if isTier(t) {
				m, ok := cfg.Tiers.Model(t)
				if !ok && t != first {
					continue
				}
				id = cmpOr(m, "tier:"+t)
			}
			if t == "" || seen[id] {
				continue
			}
			seen[id] = true
			list = append(list, t)
		}
		if len(list) == 0 {
			list = []string{""}
		}
		models[b] = list
	}
	out := make([]api.VariantSpec, 0, count)
	for k := range count {
		b := backends[k%len(backends)]
		list := models[b]
		spec := api.VariantSpec{Member: base.Member, Provider: base.Provider, Model: list[(k/len(backends))%len(list)]}
		if base.Member == "" && base.Provider == "" {
			spec.Backend = b
		} else {
			spec.Backend = base.Backend
		}
		out = append(out, spec)
	}
	return out
}

// QuotaError refuses a contest the pool cannot run: fewer than two variants can start in parallel, or an explicit
// spec targets a pool whose best account is under QuotaFloor.
type QuotaError struct {
	Message  string
	Estimate api.ContestEstimate
}

func (e *QuotaError) Error() string { return "quota: " + e.Message }

// APIDetails are the details of the conflict error.
func (e *QuotaError) APIDetails() map[string]any {
	return map[string]any{"reason": "quota", "estimate": e.Estimate}
}

// quotaPlan keeps, in order, the specs the pool can run in parallel. explicit is the number of specs the lead gave;
// those are refused rather than dropped when their pool is under QuotaFloor.
func quotaPlan(specs []api.VariantSpec, explicit int, st config.Settings, accounts []PoolAccount,
	member func(string) (api.Member, bool)) (keep []int, est api.ContestEstimate, err error) {
	est = api.ContestEstimate{Requested: len(specs), Dropped: []api.DroppedVariant{}, Pools: map[string]api.PoolCap{}}
	left := map[string]int{}
	for i, s := range specs {
		key := poolKey(s, member)
		if _, ok := est.Pools[key]; !ok {
			est.Pools[key] = capacity(accounts, key, st.MaxConcurrentPerAccount, st.MinRemaining)
			left[key] = est.Pools[key].Slots
		}
		c := est.Pools[key]
		if c.Best != nil && *c.Best < QuotaFloor {
			why := fmt.Sprintf("best %s account has %d%% left (under %d%%)", key, int(*c.Best*100+0.5),
				int(QuotaFloor*100))
			if i < explicit {
				return nil, est, &QuotaError{Message: fmt.Sprintf("variant %s: %s", label(i), why), Estimate: est}
			}
			est.Dropped = append(est.Dropped, api.DroppedVariant{Label: label(i), Pool: key, Reason: why})
			continue
		}
		if left[key] > 0 {
			left[key]--
			keep = append(keep, i)
			continue
		}
		est.Dropped = append(est.Dropped, api.DroppedVariant{Label: label(i), Pool: key,
			Reason: "no free " + key + " account slot"})
	}
	est.Granted = len(keep)
	est.Reduced = len(keep) < len(specs)
	if len(keep) < MinVariants {
		var why []string
		for _, d := range est.Dropped {
			why = append(why, d.Label+" "+d.Reason)
		}
		msg := strings.Join(why, "; ")
		if msg == "" {
			msg = "no usable accounts"
		}
		return nil, est, &QuotaError{Message: fmt.Sprintf("only %d variant(s) can start in parallel (need %d): %s",
			len(keep), MinVariants, msg), Estimate: est}
	}
	return keep, est, nil
}

// ---- scoring ----------------------------------------------------------------------------------------------------

// compareRows ranks qualified rows: verifier accept, fewer automatic fix turns, fewer files outside the lane, a
// smaller diff, fewer tokens; the label breaks ties.
func compareRows(a, b *api.ScoreRow) int {
	va, vb := 1, 1
	if a.Verifier == "accept" {
		va = 0
	}
	if b.Verifier == "accept" {
		vb = 0
	}
	switch {
	case va != vb:
		return va - vb
	case a.AutoFixTurns != b.AutoFixTurns:
		return a.AutoFixTurns - b.AutoFixTurns
	case a.OutOfLane != b.OutOfLane:
		return a.OutOfLane - b.OutOfLane
	case a.Added+a.Removed != b.Added+b.Removed:
		return (a.Added + a.Removed) - (b.Added + b.Removed)
	case a.Tokens != b.Tokens:
		if a.Tokens < b.Tokens {
			return -1
		}
		return 1
	}
	return labelIndex(a.Label) - labelIndex(b.Label)
}

// rank returns the labels of the qualified rows, best first.
func rank(rows []api.ScoreRow) []string {
	var q []*api.ScoreRow
	for i := range rows {
		if rows[i].Disqualified == "" {
			q = append(q, &rows[i])
		}
	}
	slices.SortStableFunc(q, compareRows)
	out := make([]string, 0, len(q))
	for _, r := range q {
		out = append(out, r.Label)
	}
	return out
}

// disqualification says why a variant cannot win ("" when it can).
func disqualification(d *api.TaskDetail, row *api.ScoreRow, deadlineCancelled bool) string {
	switch {
	case d == nil:
		return "task_missing"
	case d.Status == api.StatusFailed:
		return "failed"
	case d.Status == api.StatusCancelled && deadlineCancelled:
		return "deadline"
	case d.Status == api.StatusCancelled:
		return "cancelled"
	case d.Review != nil && d.Review.Verdict == api.VerdictRejected:
		return "rejected"
	case row.LaneViolation:
		return "lane_violation"
	case row.Gate == string(api.GateRed) || row.Gate == "error":
		return "gate_red"
	case row.Files == 0:
		return "no_changes"
	}
	return ""
}

// parseNumstat reads `git diff --numstat` output.
func parseNumstat(text string) (files []string, added, removed int) {
	for _, line := range strings.Split(text, "\n") {
		parts := strings.SplitN(strings.TrimRight(line, "\r"), "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		files = append(files, parts[2])
		if n, err := strconv.Atoi(parts[0]); err == nil {
			added += n
		}
		if n, err := strconv.Atoi(parts[1]); err == nil {
			removed += n
		}
	}
	return files, added, removed
}
