package backend

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// ModelPolicy says which models a turn may use: the tier mapping and the allowed-models pattern of a backend (from
// config.json) or of an API-key provider.
type ModelPolicy struct {
	// Backend is the backend id.
	Backend string
	// Enabled is false when the backend (or provider) is switched off.
	Enabled bool
	// AllowedModels is a case-insensitive RE2 pattern; empty allows any model. The models in Tiers are always
	// allowed.
	AllowedModels string
	// Tiers maps tiers to models.
	Tiers config.TierModels
	// Source names where the policy comes from, for error messages ("backends.claude" or "provider zai").
	Source string
	// Provider is the provider id of an API-key account's policy ("" for a backend's), and Models its model
	// allow-list (examples for the error of a turn without a model).
	Provider string
	Models   []string
}

// NoDefaultModelError is a turn without a model (or with only a tier) on a provider that has no model for that
// tier: most often a "model per task" provider without any tier model (account.Provider.ModelPerTask), where the
// lead must name the model. It matches ErrModel.
type NoDefaultModelError struct {
	Provider string
	// Tier is the tier that has no model; Tiers are the tiers that have one (none for a model-per-task provider).
	Tier  string
	Tiers []string
	// Models are example model ids (the provider's allow-list), possibly none.
	Models []string
}

func (e *NoDefaultModelError) Error() string {
	example := ""
	if len(e.Models) > 0 {
		example = " (e.g. " + strings.Join(e.Models[:min(len(e.Models), 3)], ", ") + ")"
	}
	if len(e.Tiers) == 0 {
		return fmt.Sprintf("provider %s has no default model: pass model%s in delegate; this provider runs the model "+
			"each task names", e.Provider, example)
	}
	return fmt.Sprintf("provider %s has no model for tier %s: pass model%s in delegate, or a tier it has (%s)",
		e.Provider, e.Tier, example, strings.Join(e.Tiers, ", "))
}

// Is makes the error match ErrModel.
func (e *NoDefaultModelError) Is(target error) bool { return target == ErrModel }

// APIDetails gives the API error's details: reason no_default_model, the provider and the example models.
func (e *NoDefaultModelError) APIDetails() map[string]any {
	return map[string]any{"reason": "no_default_model", "provider": e.Provider, "models": slices.Clone(e.Models),
		"tier": e.Tier}
}

// PolicyFor returns the model policy of a turn on backend name: the provider's tiers and pattern for an API-key
// account (provider not nil), otherwise the backend's from the settings.
func PolicyFor(name string, s config.Settings, provider *account.Provider) (ModelPolicy, error) {
	b, ok := s.Backends.Backend(name)
	if !ok {
		return ModelPolicy{}, fmt.Errorf("%w %q", ErrUnknownBackend, name)
	}
	if provider != nil {
		return ModelPolicy{
			Backend: name, Enabled: b.Enabled && provider.Enabled, AllowedModels: provider.AllowedModels,
			Tiers: provider.Tiers, Source: "provider " + provider.ID, Provider: provider.ID,
			Models: slices.Clone(provider.Models),
		}, nil
	}
	return ModelPolicy{
		Backend: name, Enabled: b.Enabled, AllowedModels: b.AllowedModels, Tiers: b.Tiers,
		Source: "backends." + name,
	}, nil
}

// Resolve picks the model of a turn: model when set, otherwise the model of tier, otherwise the model of
// defaultTier. A value of model that is a tier name is treated as that tier. It fails (matching ErrModel) when the
// backend is disabled, the tier has no model, or the model is not allowed.
func (p ModelPolicy) Resolve(model, tier, defaultTier string) (string, error) {
	if !p.Enabled {
		return "", fmt.Errorf("%w: %s is disabled", ErrModel, p.Source)
	}
	model = strings.TrimSpace(model)
	if model != "" && slices.Contains(config.Tiers(), strings.ToLower(model)) {
		tier, model = strings.ToLower(model), ""
	}
	if model == "" {
		if tier == "" {
			tier = defaultTier
		}
		m, ok := p.Tiers.Model(tier)
		if !ok {
			if !slices.Contains(config.Tiers(), tier) {
				return "", fmt.Errorf("%w: unknown tier %q (want %s)", ErrModel, tier, strings.Join(config.Tiers(), ", "))
			}
			if p.Provider != "" {
				var have []string
				for _, t := range config.Tiers() {
					if _, ok := p.Tiers.Model(t); ok {
						have = append(have, t)
					}
				}
				return "", &NoDefaultModelError{Provider: p.Provider, Tier: tier, Tiers: have,
					Models: slices.Clone(p.Models)}
			}
			return "", fmt.Errorf("%w: tier %s has no model in %s", ErrModel, tier, p.Source)
		}
		model = m
	}
	if err := p.Allowed(model); err != nil {
		return "", err
	}
	return model, nil
}

// Allowed checks a model id against the policy: a tier model is always allowed, any other model must match
// AllowedModels, and an empty AllowedModels allows any model (the old version refused every model there for codex,
// claude and providers; migrate writes a pattern of the tier models instead, ^$ without tiers).
func (p ModelPolicy) Allowed(model string) error {
	if model == "" || strings.ContainsFunc(model, func(r rune) bool { return r <= ' ' }) {
		return fmt.Errorf("%w: invalid model id %q", ErrModel, model)
	}
	for _, t := range config.Tiers() {
		if m, ok := p.Tiers.Model(t); ok && strings.EqualFold(m, model) {
			return nil
		}
	}
	if p.AllowedModels == "" {
		return nil
	}
	re, err := regexp.Compile("(?i)" + p.AllowedModels)
	if err != nil {
		return fmt.Errorf("%w: allowedModels of %s is not a valid pattern: %w", ErrModel, p.Source, err)
	}
	if !re.MatchString(model) {
		return fmt.Errorf("%w: model %q does not match allowedModels %q of %s", ErrModel, model, p.AllowedModels,
			p.Source)
	}
	return nil
}

// codexFamilies ranks the codex model families; a model of no known family ranks in the middle.
var codexFamilies = []struct {
	re   *regexp.Regexp
	rank float64
}{
	{regexp.MustCompile(`(^|[-_.])luna($|[-_.])`), 1},
	{regexp.MustCompile(`(^|[-_.])terra($|[-_.])`), 2},
	{regexp.MustCompile(`(^|[-_.])sol($|[-_.])`), 3},
	{regexp.MustCompile(`(^|[-_.])astra($|[-_.])`), 4},
}

// codexVersionRe finds the version number in a model id.
var codexVersionRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

// codexUnknownRank is the rank of a model of no known family.
const codexUnknownRank = 2.5

// codexRank returns the family rank of a codex model id.
func codexRank(slug string) float64 {
	s := strings.ToLower(slug)
	for _, f := range codexFamilies {
		if f.re.MatchString(s) {
			return f.rank
		}
	}
	return codexUnknownRank
}

// codexVersion returns the first number in a model id (0 when none).
func codexVersion(slug string) float64 {
	m := codexVersionRe.FindString(slug)
	if m == "" {
		return 0
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0
	}
	return v
}

// FitModel returns the model a turn that asks for requested runs on an account of backend name whose CLI reports
// the catalog available (nil when unknown). allowed filters the catalog (nil allows everything). ok is false when
// the account cannot run any allowed model.
//
// Only codex sign-ins have per-account catalogs: when the requested model is missing, FitModel picks the closest
// allowed one (old codex fitModel): families rank luna < terra < sol < astra; the best-ranked model not above the
// requested rank wins (newer version first), else the lowest-ranked one. Every other account runs requested
// unchanged.
func FitModel(name, requested string, available []string, allowed func(string) bool) (model string, ok bool) {
	if name != config.BackendCodex || available == nil || requested == "" {
		return requested, true
	}
	var list []string
	for _, m := range available {
		if m != "" && (allowed == nil || allowed(m)) && !slices.Contains(list, m) {
			list = append(list, m)
		}
	}
	if len(list) == 0 {
		return "", false
	}
	if slices.Contains(list, requested) {
		return requested, true
	}
	want := codexRank(requested)
	index := func(m string) int { return slices.Index(list, m) }
	better := func(a, b string) int { // best first: higher rank, then newer version, then catalog order
		if ra, rb := codexRank(a), codexRank(b); ra != rb {
			if ra > rb {
				return -1
			}
			return 1
		}
		if va, vb := codexVersion(a), codexVersion(b); va != vb {
			if va > vb {
				return -1
			}
			return 1
		}
		return index(a) - index(b)
	}
	var notAbove []string
	for _, m := range list {
		if codexRank(m) <= want {
			notAbove = append(notAbove, m)
		}
	}
	if len(notAbove) > 0 {
		slices.SortStableFunc(notAbove, better)
		return notAbove[0], true
	}
	lowest := slices.Clone(list)
	slices.SortStableFunc(lowest, func(a, b string) int {
		if ra, rb := codexRank(a), codexRank(b); ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		return better(a, b)
	})
	return lowest[0], true
}
