package account

import (
	"net/url"
	"regexp"
	"slices"
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
	// Models is the model allow-list as a list, exactly as last set (trimmed, without blanks or repeats). When it is
	// not empty, AllowedModels is the pattern that matches exactly these models (Validate sets it).
	Models []string `json:"models"`
	// AllowedModels is the case-insensitive RE2 pattern a model must match: built from Models, or a hand-written
	// pattern when Models is empty. Both empty allow any model; the tier models are always allowed.
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
	models, err := cleanModels(p.Models)
	if err != nil {
		return err
	}
	p.Models = models
	if len(models) > 0 {
		p.AllowedModels = ModelsPattern(models)
	}
	return validatePolicy(p.AllowedModels, p.Tiers)
}

// cleanModels trims the ids of an allow-list and drops blanks and repeats, keeping the order (never nil).
func cleanModels(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || slices.Contains(out, m) {
			continue
		}
		if strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' }) {
			return nil, invalidf("model id %q must not contain spaces", m)
		}
		out = append(out, m)
	}
	return out, nil
}

// ModelsPattern returns the anchored pattern that matches exactly the given models ("" for none: any model).
func ModelsPattern(models []string) string {
	quoted := make([]string, 0, len(models))
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" {
			quoted = append(quoted, regexp.QuoteMeta(m))
		}
	}
	if len(quoted) == 0 {
		return ""
	}
	return "^(?:" + strings.Join(quoted, "|") + ")$"
}

// ModelsOf turns a pattern made by ModelsPattern back into its list. It returns an empty list for any other
// pattern (a hand-written one cannot be expressed as a list) and for "".
func ModelsOf(pattern string) []string {
	inner, ok := strings.CutPrefix(pattern, "^(?:")
	if ok {
		inner, ok = strings.CutSuffix(inner, ")$")
	}
	if !ok || inner == "" {
		return []string{}
	}
	var (
		out []string
		cur strings.Builder
		esc bool
	)
	for _, r := range inner {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '|':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	// Only an exact round trip counts: "^(?:a.b)$" is a hand-written pattern, not the list [a.b].
	if esc || ModelsPattern(out) != pattern {
		return []string{}
	}
	return out
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

// validatePolicy checks the allowed-models pattern and that every tier model is a plain id. A tier model need not
// match the pattern: the tier models are always allowed on top of the allow-list.
func validatePolicy(allowed string, tiers config.TierModels) error {
	if allowed != "" {
		if _, err := regexp.Compile("(?i)" + allowed); err != nil {
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
	}
	return nil
}
