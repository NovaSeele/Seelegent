package backend

import (
	"fmt"
	"regexp"
	"slices"
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
	// AllowedModels is a case-insensitive RE2 pattern; empty allows only the models in Tiers.
	AllowedModels string
	// Tiers maps tiers to models.
	Tiers config.TierModels
	// Source names where the policy comes from, for error messages ("backends.claude" or "provider zai").
	Source string
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
			Tiers: provider.Tiers, Source: "provider " + provider.ID,
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
			return "", fmt.Errorf("%w: tier %s has no model in %s", ErrModel, tier, p.Source)
		}
		model = m
	}
	if err := p.Allowed(model); err != nil {
		return "", err
	}
	return model, nil
}

// Allowed checks a model id against the policy.
func (p ModelPolicy) Allowed(model string) error {
	if model == "" || strings.ContainsFunc(model, func(r rune) bool { return r <= ' ' }) {
		return fmt.Errorf("%w: invalid model id %q", ErrModel, model)
	}
	if p.AllowedModels == "" {
		for _, t := range config.Tiers() {
			if m, ok := p.Tiers.Model(t); ok && strings.EqualFold(m, model) {
				return nil
			}
		}
		return fmt.Errorf("%w: %s allows only its tier models (allowedModels is empty), not %q", ErrModel, p.Source,
			model)
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
