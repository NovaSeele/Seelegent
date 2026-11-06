package account

import (
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// Wire APIs a codex provider can speak.
const (
	WireChat      = "chat"
	WireResponses = "responses"
)

// providerID matches provider ids.
var providerID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// Provider is an endpoint API-key accounts talk to: an Anthropic-compatible API for claude, an OpenAI-compatible
// one for codex.
type Provider struct {
	ID string `json:"id"`
	// Name is shown to the user.
	Name string `json:"name"`
	// Backend is the CLI that talks to the provider: claude or codex.
	Backend string `json:"backend"`
	// Preset is the preset the provider was made from (empty for none).
	Preset string `json:"preset,omitempty"`
	// BaseURL is the API endpoint; empty only for the official Anthropic API, which the CLI knows itself.
	BaseURL string `json:"baseUrl,omitempty"`
	// WireAPI is chat or responses (codex only).
	WireAPI string `json:"wireApi,omitempty"`
	// Official marks the vendor's own API (the key goes in ANTHROPIC_API_KEY rather than a bearer token).
	Official bool `json:"official"`
	// Enabled providers may be used.
	Enabled bool `json:"enabled"`
	// AllowedModels is a case-insensitive RE2 pattern every model must match; empty allows only the tier models.
	AllowedModels string `json:"allowedModels"`
	// Tiers maps the tiers to this provider's models.
	Tiers config.TierModels `json:"tiers"`
	// CreatedAt and UpdatedAt are UTC times.
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Preset is a provider template.
type Preset struct {
	Name     string
	Backend  string
	BaseURL  string
	WireAPI  string
	Official bool
}

// Presets returns the provider templates offered to the user (same set as the old version).
func Presets() map[string]Preset {
	return map[string]Preset{
		"anthropic":        {Name: "Anthropic", Backend: config.BackendClaude, Official: true},
		"deepseek":         {Name: "DeepSeek", Backend: config.BackendClaude, BaseURL: "https://api.deepseek.com/anthropic"},
		"moonshot":         {Name: "Moonshot Kimi", Backend: config.BackendClaude, BaseURL: "https://api.moonshot.ai/anthropic"},
		"zai":              {Name: "Z.ai GLM", Backend: config.BackendClaude, BaseURL: "https://api.z.ai/api/anthropic"},
		"openrouter":       {Name: "OpenRouter", Backend: config.BackendClaude, BaseURL: "https://openrouter.ai/api"},
		"custom-anthropic": {Name: "Custom (Anthropic API)", Backend: config.BackendClaude},
		"openai":           {Name: "OpenAI", Backend: config.BackendCodex, BaseURL: "https://api.openai.com/v1", WireAPI: WireResponses},
		"groq":             {Name: "Groq", Backend: config.BackendCodex, BaseURL: "https://api.groq.com/openai/v1", WireAPI: WireChat},
		"ollama":           {Name: "Ollama (local)", Backend: config.BackendCodex, BaseURL: "http://localhost:11434/v1", WireAPI: WireChat},
		"custom-openai":    {Name: "Custom (OpenAI API)", Backend: config.BackendCodex, WireAPI: WireChat},
	}
}

// FromPreset returns a provider filled from the named preset; the caller sets ID, the tiers and the allowed models
// (and the base URL for the custom presets).
func FromPreset(id, preset string) (Provider, error) {
	p, ok := Presets()[preset]
	if !ok {
		return Provider{}, invalidf("unknown provider preset %q", preset)
	}
	return Provider{
		ID: id, Name: p.Name, Backend: p.Backend, Preset: preset, BaseURL: p.BaseURL, WireAPI: p.WireAPI,
		Official: p.Official, Enabled: true,
	}, nil
}

// Validate checks the provider and normalises it in place (trimmed fields, default wire API).
func (p *Provider) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if !providerID.MatchString(p.ID) {
		return invalidf("provider id %q must match %s", p.ID, providerID)
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if err := validateLabel(p.Name); err != nil {
		return err
	}
	switch p.Backend {
	case config.BackendClaude:
		if p.WireAPI != "" {
			return invalidf("wireApi is only for codex providers")
		}
	case config.BackendCodex:
		if p.Official {
			return invalidf("only the Anthropic preset is official")
		}
		if p.WireAPI == "" {
			p.WireAPI = WireChat
		}
		if p.WireAPI != WireChat && p.WireAPI != WireResponses {
			return invalidf("wireApi %q (want chat or responses)", p.WireAPI)
		}
	default:
		return invalidf("provider backend %q (want claude or codex)", p.Backend)
	}
	if p.Preset != "" {
		if _, ok := Presets()[p.Preset]; !ok {
			return invalidf("unknown provider preset %q", p.Preset)
		}
	}
	if p.Official {
		p.BaseURL = ""
	} else if err := validateBaseURL(p.BaseURL); err != nil {
		return err
	}
	return validatePolicy(p.AllowedModels, p.Tiers)
}

// validateBaseURL accepts https URLs, and http only for the local machine.
func validateBaseURL(raw string) error {
	if raw == "" {
		return invalidf("baseUrl is required for a provider that is not official")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return invalidf("baseUrl %q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" && h != "::1" {
			return invalidf("http baseUrl is only allowed for localhost")
		}
	default:
		return invalidf("baseUrl must use https (or http for localhost)")
	}
	if u.User != nil {
		return invalidf("baseUrl must not contain credentials")
	}
	return nil
}

// validatePolicy checks the allowed-models pattern and that every tier model matches it.
func validatePolicy(allowed string, tiers config.TierModels) error {
	var re *regexp.Regexp
	if allowed != "" {
		var err error
		if re, err = regexp.Compile("(?i)" + allowed); err != nil {
			return invalidf("allowedModels is not a valid regular expression: %v", err)
		}
	}
	for _, tier := range config.Tiers() {
		m, ok := tiers.Model(tier)
		if !ok {
			continue
		}
		if strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' }) {
			return invalidf("tier %s: model id %q must not contain spaces", tier, m)
		}
		if re != nil && !re.MatchString(m) {
			return invalidf("tier %s: model %q does not match allowedModels %q", tier, m, allowed)
		}
	}
	return nil
}
