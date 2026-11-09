package account

import (
	"fmt"
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
	// Prices is the user's price table, by model id (Repo.SetPrices; kept apart from the other fields, which
	// CreateProvider and UpdateProvider write).
	Prices []Price `json:"prices,omitempty"`
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

// Validate checks the provider and normalises it in place; see Check.
func (p *Provider) Validate() error {
	_, err := p.Check()
	return err
}

// Check checks the provider and normalises it in place: trimmed fields, the default wire API, and the base URL
// rules (normalizeEndpoint). It returns what the normalising changed as notices. Its errors match ErrInvalid and
// name the field (FieldError: "id", "name", "backend", "preset", "wireApi", "baseUrl", "models", "allowedModels",
// "tiers").
func (p *Provider) Check() ([]Notice, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.BaseURL = strings.TrimSpace(p.BaseURL)
	if !providerID.MatchString(p.ID) {
		return nil, withField("id", invalidf("provider id %q must match %s", p.ID, providerID))
	}
	if p.Name == "" {
		p.Name = p.ID
	}
	if err := validateLabel(p.Name); err != nil {
		return nil, withField("name", err)
	}
	switch p.Backend {
	case config.BackendClaude:
		if p.WireAPI != "" {
			return nil, withField("wireApi", invalidf("wireApi is only for codex providers"))
		}
	case config.BackendCodex:
		if p.Official {
			return nil, withField("preset", invalidf("only the Anthropic preset is official"))
		}
		if p.WireAPI == "" {
			p.WireAPI = WireChat
		}
		if p.WireAPI != WireChat && p.WireAPI != WireResponses {
			return nil, withField("wireApi", invalidf("wireApi %q (want chat or responses)", p.WireAPI))
		}
	default:
		return nil, withField("backend", invalidf("provider backend %q (want claude or codex)", p.Backend))
	}
	if p.Preset != "" {
		if _, ok := Presets()[p.Preset]; !ok {
			return nil, withField("preset", invalidf("unknown provider preset %q", p.Preset))
		}
	}
	if !p.Official {
		if err := validateBaseURL(p.BaseURL); err != nil {
			return nil, withField("baseUrl", err)
		}
	}
	notices := p.normalizeEndpoint()
	models, err := cleanModels(p.Models)
	if err != nil {
		return nil, withField("models", err)
	}
	p.Models = models
	if len(models) > 0 {
		p.AllowedModels = ModelsPattern(models)
	}
	if err := validatePolicy(p.AllowedModels, p.Tiers); err != nil {
		if strings.Contains(err.Error(), "allowedModels") {
			return nil, withField("allowedModels", err)
		}
		return nil, withField("tiers", err)
	}
	return notices, nil
}

// customAnthropicPreset is the preset of a hand-made Anthropic-compatible provider.
const customAnthropicPreset = "custom-anthropic"

// normalizeEndpoint applies the base URL rules to a provider whose base URL passed validateBaseURL and returns
// what it changed:
//   - a claude provider that is not official and points at api.anthropic.com with no preset or the custom one
//     becomes the official "Anthropic" preset: Claude Code must send such a key as ANTHROPIC_API_KEY (x-api-key),
//     never as the bearer token custom providers get;
//   - the base URL of any other claude provider loses a trailing "/", "/v1", "/v1/" or "/v1/messages"
//     (NormalizeBaseURL);
//   - a codex provider keeps its URL ("/v1" belongs to OpenAI-compatible base URLs);
//   - an official provider has no base URL.
func (p *Provider) normalizeEndpoint() []Notice {
	var notices []Notice
	if !p.Official && p.Backend == config.BackendClaude && (p.Preset == "" || p.Preset == customAnthropicPreset) &&
		isAnthropicHost(p.BaseURL) {
		official := Presets()["anthropic"]
		if p.Name == "" || p.Name == p.ID || p.Name == Presets()[customAnthropicPreset].Name {
			p.Name = official.Name
		}
		p.Official, p.Preset = true, "anthropic"
		notices = append(notices, Notice{Code: NoticeOfficialPreset, Field: "preset", Message: "api.anthropic.com " +
			"is the official Anthropic API: switched to the \"Anthropic\" preset, which sends the key the way it expects"})
	}
	if p.Official {
		p.BaseURL = ""
		return notices
	}
	if clean := NormalizeBaseURL(p.Backend, p.BaseURL); clean != p.BaseURL {
		notices = append(notices, Notice{Code: NoticeBaseURLTrimmed, Field: "baseUrl", Message: fmt.Sprintf(
			"saved the base URL as %s: Claude Code adds /v1/messages itself", clean)})
		p.BaseURL = clean
	}
	return notices
}

// isAnthropicHost reports whether raw is a URL on api.anthropic.com.
func isAnthropicHost(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Hostname(), anthropicHost)
}

// anthropicHost is the host of the official Anthropic API.
const anthropicHost = "api.anthropic.com"

// claudeURLSuffixes are the API paths Claude Code appends to ANTHROPIC_BASE_URL itself, longest first. A base URL
// that already ends in one makes the CLI call ".../v1/v1/messages" (404 on the first turn).
var claudeURLSuffixes = []string{"/v1/messages", "/v1"}

// NormalizeBaseURL returns the base URL a provider of the backend is stored and run with. For claude it drops a
// trailing "/", "/v1", "/v1/" or "/v1/messages" (any case), because Claude Code appends "/v1/messages" itself. A
// codex provider keeps its URL as it is: OpenAI-compatible base URLs end in "/v1". A URL with a query or a
// fragment is left alone.
func NormalizeBaseURL(backend, raw string) string {
	if backend != config.BackendClaude || strings.ContainsAny(raw, "?#") {
		return raw
	}
	out := strings.TrimRight(raw, "/")
	for _, suffix := range claudeURLSuffixes {
		if n := len(out) - len(suffix); n > 0 && strings.EqualFold(out[n:], suffix) {
			out = strings.TrimRight(out[:n], "/")
			break
		}
	}
	// Never trim a URL down to its scheme.
	if i := strings.Index(out, "://"); i < 0 || i+3 >= len(out) {
		return raw
	}
	return out
}

// endpoint is what decides where the provider's accounts send their key and how: when it changes, their test
// state is cleared.
func (p *Provider) endpoint() string {
	return fmt.Sprintf("%s|%s|%s|%t", p.Backend, p.BaseURL, p.WireAPI, p.Official)
}

// ModelPerTask reports a provider with no tier models: any model it allows must be named per task (delegate
// model), because a turn without a model has no tier model to fall back to. It is a valid set-up ("let the lead
// choose"), not an error.
func (p *Provider) ModelPerTask() bool {
	for _, tier := range config.Tiers() {
		if _, ok := p.Tiers.Model(tier); ok {
			return false
		}
	}
	return true
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
	// Only an exact round trip counts: "^(?:a.b)$" is a hand-written pattern, not the list [a.b]. A part with a space
	// or control character is no valid model id, so such a pattern stays hand-written too (else every later update
	// of the provider would fail validation of a list its user never sent).
	if esc || ModelsPattern(out) != pattern {
		return []string{}
	}
	for _, m := range out {
		if strings.ContainsFunc(m, func(r rune) bool { return r <= ' ' }) {
			return []string{}
		}
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
