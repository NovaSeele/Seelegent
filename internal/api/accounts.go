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
	// TestResult is the test state of an API-key account (absent for the others): not_tested until a key test or
	// a turn on the current key and provider endpoint succeeds, then ok, or the reason of the last failed test.
	// Storing a key or changing the provider's endpoint makes it not_tested again. It never changes Status or
	// selection: a not_tested account is picked like any other. TestedAt is when it was set (absent while
	// not_tested).
	TestResult string `json:"testResult,omitempty" ts:"'not_tested' | 'ok' | 'refused' | 'not_found' | 'no_models_endpoint' | 'unreachable' | 'redirected' | 'error'"`
	TestedAt   string `json:"testedAt,omitempty"`
	// ModelPerTask is true for an API-key account whose provider has no tier models: a delegate on it must name
	// its model (shown as "Model: chosen per task").
	ModelPerTask bool   `json:"modelPerTask,omitempty"`
	TwinOf       string `json:"twinOf,omitempty"`
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
	// Notices are set only in the answers to POST /accounts and PUT /accounts/{id}/key: what the call changed on
	// its own (key cleaned, base URL trimmed, preset switched, provider reused) and advice about the key.
	Notices []Notice `json:"notices,omitempty"`
}

// AccountCreated is the answer to POST /accounts: the account, and the provider when the body had newProvider.
type AccountCreated struct {
	Account
	// NewProvider is the provider the call created or reused for newProvider (absent otherwise);
	// ProviderCreated is true when it was created.
	NewProvider     *Provider `json:"newProvider,omitempty"`
	ProviderCreated bool      `json:"providerCreated,omitempty"`
}

// Notice codes (Notice.Code).
const (
	NoticeBaseURLTrimmed   = "base_url_trimmed"
	NoticeOfficialPreset   = "official_preset"
	NoticeKeyNameRemoved   = "key_name_removed"
	NoticeKeyQuotesRemoved = "key_quotes_removed"
	NoticeKeyPrefix        = "key_prefix"
	NoticeProviderReused   = "provider_reused"
)

// NoticeCodes lists every notice code.
func NoticeCodes() []string {
	return []string{NoticeBaseURLTrimmed, NoticeOfficialPreset, NoticeKeyNameRemoved, NoticeKeyQuotesRemoved,
		NoticeKeyPrefix, NoticeProviderReused}
}

// Notice is something a save changed on its own, or advice that did not block it. Code names it for translation,
// Field the request field it is about (when any), Message is the English text.
type Notice struct {
	Code    string `json:"code" ts:"'base_url_trimmed' | 'official_preset' | 'key_name_removed' | 'key_quotes_removed' | 'key_prefix' | 'provider_reused'"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Key test reasons (KeyTestResult.Reason, Account.TestResult besides not_tested).
const (
	TestOK               = "ok"
	TestRefused          = "refused"
	TestNotFound         = "not_found"
	TestNoModelsEndpoint = "no_models_endpoint"
	TestUnreachable      = "unreachable"
	TestRedirected       = "redirected"
	TestError            = "error"
	// TestNotTested is only an Account.TestResult.
	TestNotTested = "not_tested"
)

// TestReasons lists every KeyTestResult reason.
func TestReasons() []string {
	return []string{TestOK, TestRefused, TestNotFound, TestNoModelsEndpoint, TestUnreachable, TestRedirected, TestError}
}

// KeyTestRequest is POST /providers/test: the provider (an existing id, or newProvider as in POST /providers,
// id optional) and the key (apiKey, or accountId to use that account's stored key; then provider may be left out
// and the result is saved on the account).
type KeyTestRequest struct {
	Provider    string         `json:"provider,omitempty"`
	NewProvider *ProviderInput `json:"newProvider,omitempty"`
	APIKey      string         `json:"apiKey,omitempty"`
	AccountID   string         `json:"accountId,omitempty"`
}

// KeyTestResult is the answer to POST /providers/test (status 200 also when the key is refused: the call worked).
type KeyTestResult struct {
	OK bool `json:"ok"`
	// Status is the HTTP status the service answered (0 when it could not be reached).
	Status    int      `json:"status"`
	Reason    string   `json:"reason" ts:"'ok' | 'refused' | 'not_found' | 'no_models_endpoint' | 'unreachable' | 'redirected' | 'error'"`
	Message   string   `json:"message"`
	Models    []string `json:"models"`
	LatencyMs int64    `json:"latencyMs"`
	// Notices are what the request's key or provider needed cleaning, and advice about the key.
	Notices []Notice `json:"notices,omitempty"`
	// Saved is true when the result was stored on the account (accountId).
	Saved bool `json:"saved,omitempty"`
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

// Sign-in states of a LoginSession. in_terminal: the sign-in goes on in the terminal window that opened (there is
// no code to post here); waiting_code only when POST /accounts/{id}/login/code can take a code; open_url when a
// URL was captured.
const (
	LoginStarting    = "starting"
	LoginOpenURL     = "open_url"
	LoginInTerminal  = "in_terminal"
	LoginWaitingCode = "waiting_code"
	LoginDone        = "done"
	LoginFailed      = "failed"
)

// LoginStates lists every LoginSession state, in the order a sign-in goes through them.
func LoginStates() []string {
	return []string{LoginStarting, LoginOpenURL, LoginInTerminal, LoginWaitingCode, LoginDone, LoginFailed}
}

// LoginSession is the progress of an account's sign-in (POST/GET /accounts/{id}/login, SSE account.login).
type LoginSession struct {
	AccountID string `json:"accountId"`
	State     string `json:"state" ts:"'starting' | 'open_url' | 'in_terminal' | 'waiting_code' | 'done' | 'failed'"`
	URL       string `json:"url,omitempty"`
	UserCode  string `json:"userCode,omitempty"`
	Message   string `json:"message,omitempty"`
}

// NewAccount is POST /accounts. A sign-in account needs backend and kind. An API-key account names an existing
// provider, or gives newProvider (a provider to create, or to reuse when an enabled one has the same preset,
// backend, base URL and models; its id is optional), and may give apiKey; it is all done in one transaction, so a
// failure leaves nothing behind. backend may then be left out (the provider's), kind too (api-key), and label
// ("<provider name> <n>").
type NewAccount struct {
	Backend     string         `json:"backend,omitempty" ts:"'agy' | 'claude' | 'codex'"`
	Kind        string         `json:"kind,omitempty" ts:"'isolated' | 'system' | 'api-key'"`
	Label       string         `json:"label,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	NewProvider *ProviderInput `json:"newProvider,omitempty"`
	// APIKey is cleaned and checked like PUT /accounts/{id}/key.
	APIKey string `json:"apiKey,omitempty"`
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
	// Models is the model allow-list as a list, exactly as last set through POST/PATCH models; never the tier
	// models. It is [] when the provider allows any model and when a hand-written allowedModels pattern (old data,
	// or set as a raw pattern) restricts it, since such a pattern cannot be expressed as a list.
	Models []string `json:"models"`
	// Accounts counts the accounts that use the provider.
	Accounts int    `json:"accounts"`
	Preset   string `json:"preset,omitempty"`
	WireAPI  string `json:"wireApi,omitempty"`
	Official bool   `json:"official"`
	// AllowedModels is the case-insensitive RE2 pattern actually enforced. Built from a list it matches exactly
	// that list (^(?:a|b)$); otherwise it is a hand-written pattern and Models is []. Both empty allow any model.
	// The tier models are always allowed on top of the allow-list.
	AllowedModels string     `json:"allowedModels"`
	Tiers         TierModels `json:"tiers"`
	// ModelPerTask is true when no tier has a model: every delegate on the provider must name its model ("Model:
	// chosen per task"). A valid set-up, not an error. Absent when false.
	ModelPerTask bool   `json:"modelPerTask,omitempty"`
	CreatedAt    string `json:"createdAt,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	// Notices are set only in the answers to POST and PATCH /providers: what the save changed on its own
	// (base_url_trimmed, official_preset).
	Notices []Notice `json:"notices,omitempty"`
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
	// Tiers are suggested tier models: for "anthropic" the claude backend's tiers from the settings, when set;
	// absent for the others (their models come from a key test).
	Tiers TierModels `json:"tiers,omitzero"`
}

// ProviderList is GET /providers.
type ProviderList struct {
	Items   []Provider       `json:"items"`
	Presets []ProviderPreset `json:"presets"`
}

// ProviderInput is POST /providers (every field but id and backend is optional) and PATCH /providers/{id} (every
// field is optional; id and backend cannot change). The allow-list, in order of precedence:
//   - models with at least one id replaces the list (trimmed, blanks and repeats dropped) and allowedModels becomes
//     its pattern; a given allowedModels is then ignored;
//   - otherwise allowedModels sets the pattern; models becomes the list it was built from, or [] for a
//     hand-written pattern. Sent with models [] and equal to the pattern of the stored list, it is that list's echo
//     and the list is cleared instead;
//   - otherwise models [] clears the list and the pattern: any model.
//
// Neither ever changes the tiers.
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
