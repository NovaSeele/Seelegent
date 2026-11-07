package api

// AccountStatus is an account's status as the pool computes it on read (never stored). UI wording: ok → Ready,
// new → Checking quota, error → Ready (last read failed, old numbers), needs_login → Sign-in required, no_key → No
// API key, no_plan → Plan cannot use this backend, twin → Same sign-in as twinOf, disabled → Off.
type AccountStatus string

// Account statuses.
const (
	AccountOK         AccountStatus = "ok"
	AccountError      AccountStatus = "error"
	AccountNew        AccountStatus = "new"
	AccountNeedsLogin AccountStatus = "needs_login"
	AccountNoKey      AccountStatus = "no_key"
	AccountNoPlan     AccountStatus = "no_plan"
	AccountTwin       AccountStatus = "twin"
	AccountDisabled   AccountStatus = "disabled"
)

// Account is a worker account with what the pool knows about it right now (GET /accounts).
type Account struct {
	ID       string `json:"id"`
	Backend  string `json:"backend" ts:"'agy' | 'claude' | 'codex'"`
	Kind     string `json:"kind" ts:"'isolated' | 'system' | 'api-key'"`
	Label    string `json:"label"`
	Enabled  bool   `json:"enabled"`
	Priority int    `json:"priority"`
	Provider string `json:"provider,omitempty"`
	// KeyHint shows the last four characters of an API key ("…abcd"); the key itself is never sent.
	KeyHint string        `json:"keyHint,omitempty"`
	Status  AccountStatus `json:"status"`
	TwinOf  string        `json:"twinOf,omitempty"`
	// Busy is the number of running turns; MaxConcurrent the cap per account.
	Busy          int `json:"busy"`
	MaxConcurrent int `json:"maxConcurrent"`
	// Resting is true during a cooldown or while a quota window is at or below minRemaining.
	Resting      bool   `json:"resting"`
	RestingUntil string `json:"restingUntil,omitempty"`
	SigningIn    bool   `json:"signingIn"`
	// Email is shown in the local UI only and never logged.
	Email     string        `json:"email,omitempty"`
	Quota     QuotaSnapshot `json:"quota"`
	Models    []string      `json:"models"`
	CreatedAt string        `json:"createdAt"`
	UpdatedAt string        `json:"updatedAt"`
}

// QuotaSnapshot mirrors internal/quota.Snapshot (times as RFC 3339 strings, without the account id, backend,
// identity hash and model catalog, which Account carries or keeps private).
type QuotaSnapshot struct {
	// Source is active (read from the CLI or its service) or passive (only observed from runs: API-key accounts,
	// whose groups are empty).
	Source          string  `json:"source" ts:"'active' | 'passive'"`
	FetchedAt       string  `json:"fetchedAt,omitempty"`
	Plan            string  `json:"plan,omitempty"`
	NoPlan          bool    `json:"noPlan,omitempty"`
	NoPlanReason    string  `json:"noPlanReason,omitempty"`
	ExhaustedUntil  string  `json:"exhaustedUntil,omitempty"`
	ExhaustedReason string  `json:"exhaustedReason,omitempty"`
	Groups          []Group `json:"groups"`
	// Error is the last failed read; absent when the last read succeeded.
	Error *QuotaError `json:"error,omitempty"`
}

// Group mirrors internal/quota.Group. A group "<group> <family>" is a sub-limit of <group>.
type Group struct {
	Name    string        `json:"name"`
	Windows []QuotaWindow `json:"windows"`
}

// QuotaWindow mirrors internal/quota.Window.
type QuotaWindow struct {
	// Window is "5h", "weekly", "30d", "<n>m" or "window".
	Window  string `json:"window"`
	Label   string `json:"label,omitempty"`
	Minutes int    `json:"minutes,omitempty"`
	// Remaining is the fraction left, 0..1.
	Remaining float64 `json:"remaining"`
	ResetAt   string  `json:"resetAt,omitempty"`
}

// QuotaError mirrors internal/quota.Error.
type QuotaError struct {
	Kind    string `json:"kind" ts:"'needs_login' | 'token_expired' | 'format' | 'unavailable' | 'failed'"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// Sign-in states of a LoginSession.
const (
	LoginStarting    = "starting"
	LoginOpenURL     = "open_url"
	LoginWaitingCode = "waiting_code"
	LoginDone        = "done"
	LoginFailed      = "failed"
)

// LoginSession is the progress of an account's sign-in (POST/GET /accounts/{id}/login, SSE account.login).
type LoginSession struct {
	AccountID string `json:"accountId"`
	State     string `json:"state" ts:"'starting' | 'open_url' | 'waiting_code' | 'done' | 'failed'"`
	URL       string `json:"url,omitempty"`
	UserCode  string `json:"userCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

// NewAccount is POST /accounts.
type NewAccount struct {
	Backend  string `json:"backend" ts:"'agy' | 'claude' | 'codex'"`
	Kind     string `json:"kind" ts:"'isolated' | 'system' | 'api-key'"`
	Label    string `json:"label"`
	Provider string `json:"provider,omitempty"`
	// Enabled defaults to true.
	Enabled  *bool `json:"enabled,omitempty"`
	Priority *int  `json:"priority,omitempty"`
}

// AccountPatch is PATCH /accounts/{id}; absent fields stay as they are.
type AccountPatch struct {
	Label    *string `json:"label,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	Priority *int    `json:"priority,omitempty"`
}

// KeyRequest is PUT /accounts/{id}/key. The key never appears in a response, an error or a log.
type KeyRequest struct {
	Key string `json:"key"`
}

// LoginCodeRequest is POST /accounts/{id}/login/code.
type LoginCodeRequest struct {
	Code string `json:"code"`
}

// Provider is an endpoint API-key accounts talk to (GET /providers). The contract's fields come first; the rest
// is what the account package keeps about a provider, which the dashboard needs to edit one.
type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Backend string `json:"backend" ts:"'claude' | 'codex'"`
	// BaseURL is empty only for the official Anthropic API.
	BaseURL string `json:"baseUrl"`
	Enabled bool   `json:"enabled"`
	// Models lists the models the provider offers: its tier models, or the list given when it was saved.
	Models []string `json:"models"`
	// Accounts counts the accounts that use the provider.
	Accounts      int        `json:"accounts"`
	Preset        string     `json:"preset,omitempty"`
	WireAPI       string     `json:"wireApi,omitempty"`
	Official      bool       `json:"official"`
	AllowedModels string     `json:"allowedModels"`
	Tiers         TierModels `json:"tiers"`
	CreatedAt     string     `json:"createdAt,omitempty"`
	UpdatedAt     string     `json:"updatedAt,omitempty"`
}

// ProviderPreset is a provider template (Provider without accounts and times).
type ProviderPreset struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Backend  string   `json:"backend" ts:"'claude' | 'codex'"`
	BaseURL  string   `json:"baseUrl"`
	Enabled  bool     `json:"enabled"`
	Models   []string `json:"models"`
	WireAPI  string   `json:"wireApi,omitempty"`
	Official bool     `json:"official"`
}

// ProviderList is GET /providers.
type ProviderList struct {
	Items   []Provider       `json:"items"`
	Presets []ProviderPreset `json:"presets"`
}

// ProviderInput is POST /providers (every field but id and backend is optional) and PATCH /providers/{id} (every
// field is optional; id and backend cannot change). Models, when allowedModels is not given, becomes the pattern
// that allows exactly those models.
type ProviderInput struct {
	ID            *string     `json:"id,omitempty"`
	Name          *string     `json:"name,omitempty"`
	Backend       *string     `json:"backend,omitempty" ts:"'claude' | 'codex'"`
	BaseURL       *string     `json:"baseUrl,omitempty"`
	Enabled       *bool       `json:"enabled,omitempty"`
	Models        *[]string   `json:"models,omitempty"`
	Preset        *string     `json:"preset,omitempty"`
	WireAPI       *string     `json:"wireApi,omitempty"`
	AllowedModels *string     `json:"allowedModels,omitempty"`
	Tiers         *TierModels `json:"tiers,omitempty"`
	// Accounts is accepted (the contract body is a Provider) and ignored.
	Accounts *int `json:"accounts,omitempty"`
}
