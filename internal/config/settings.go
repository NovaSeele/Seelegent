package config

// DefaultPort is the port of the installed daemon. Development runs use 47831 through SEELEGENT_PORT.
const DefaultPort = 47821

// Range and default of the loopRepeat setting.
const (
	LoopRepeatMin     = 2
	LoopRepeatMax     = 100
	LoopRepeatDefault = 4
)

// Model tiers: aliases the lead passes instead of a model id. Each backend maps them to its own models.
const (
	TierFast     = "fast"
	TierStandard = "standard"
	TierStrong   = "strong"
	TierMax      = "max"
)

// Tiers returns the tier names from cheapest to strongest.
func Tiers() []string { return []string{TierFast, TierStandard, TierStrong, TierMax} }

// Worker backend ids.
const (
	BackendAgy    = "agy"
	BackendCodex  = "codex"
	BackendClaude = "claude"
)

// Codex sandbox modes (codex --sandbox).
const (
	SandboxReadOnly       = "read-only"
	SandboxWorkspaceWrite = "workspace-write"
	SandboxFullAccess     = "danger-full-access"
)

// Lane enforcement modes.
const (
	LanesOff     = "off"
	LanesWarn    = "warn"
	LanesEnforce = "enforce"
)

// Settings is the content of config.json: everything the user can configure in the daemon. Field names in JSON are
// the ones the old version used in settings.json, so existing knowledge and the import stay simple. Display
// preferences of the dashboard (theme, language, notifications) are not here: the browser keeps them.
//
// Change defaults only in Defaults and document every field in docs/configuration.md.
type Settings struct {
	// Port is the daemon's HTTP port on 127.0.0.1. SEELEGENT_PORT overrides it (see ResolvePort).
	Port int `json:"port"`

	// DefaultTier is the tier used when the lead names neither a model nor a tier.
	DefaultTier string `json:"defaultTier"`
	// Backends configures each worker CLI.
	Backends Backends `json:"backends"`

	// MaxConcurrentPerAccount caps the turns that run at the same time on one account.
	MaxConcurrentPerAccount int `json:"maxConcurrentPerAccount"`
	// QuotaRefreshMinutes is how often quota is read from every account.
	QuotaRefreshMinutes int `json:"quotaRefreshMinutes"`
	// MinRemaining skips an account for a quota group once its remaining fraction is at or below this value.
	MinRemaining float64 `json:"minRemaining"`
	// PassiveCooldownMinutes is how long an API-key account rests after a quota error when its provider has no
	// quota endpoint to tell the reset time.
	PassiveCooldownMinutes int `json:"passiveCooldownMinutes"`
	// UsageHistory turns the hourly usage snapshots on or off.
	UsageHistory bool `json:"usageHistory"`
	// TaskTimeoutMinutes is the hard limit of one worker turn.
	TaskTimeoutMinutes int `json:"taskTimeoutMinutes"`
	// LinkIntoProfiles lists entries of the real home directory linked into every isolated profile so worker
	// commands (git, ssh, npm, cargo, ...) keep their configuration.
	LinkIntoProfiles []string `json:"linkIntoProfiles"`

	// StallMinutes flags a running turn that produced no event for this long.
	StallMinutes int `json:"stallMinutes"`
	// StallToolMinutes flags a single tool call that has been running for this long.
	StallToolMinutes int `json:"stallToolMinutes"`
	// LoopRepeat flags a worker that repeats the same tool call this many times within its last 12 tool calls
	// without progress in between.
	LoopRepeat int `json:"loopRepeat"`
	// AttentionDecayHours drops an unanswered attention flag after this long.
	AttentionDecayHours int `json:"attentionDecayHours"`

	// Gates runs the lead's gate commands after each worker report; off leaves only the lane and hygiene steps.
	Gates bool `json:"gates"`
	// GateAutoChecks adds cheap syntax checks of changed files to the gate.
	GateAutoChecks bool `json:"gateAutoChecks"`
	// GateTimeoutSec is the time limit of one gate command.
	GateTimeoutSec int `json:"gateTimeoutSec"`
	// GateMaxAutoFixes is the number of automatic fix turns per lead instruction (shared by the gate, the verifier
	// and background-wait continuations).
	GateMaxAutoFixes int `json:"gateMaxAutoFixes"`
	// GateSettleMinutes is how long a red gate waits for other running tasks in the same repository before it is
	// run again.
	GateSettleMinutes int `json:"gateSettleMinutes"`
	// GateSettlePollMs is how often that wait checks the other tasks.
	GateSettlePollMs int `json:"gateSettlePollMs"`
	// GateOutputTailBytes is the size of the output tail kept for each gate step.
	GateOutputTailBytes int `json:"gateOutputTailBytes"`
	// Hygiene adds the hygiene step (line endings, BOM, junk files, unreported files) to the gate.
	Hygiene bool `json:"hygiene"`
	// Lanes sets how file lanes are enforced: off, warn or enforce.
	Lanes string `json:"lanes"`
	// LaneHoldHours is how long a finished but unreviewed task keeps its lane claim.
	LaneHoldHours int `json:"laneHoldHours"`
	// Verifier is the default of the per-task verify option (an LLM review of a green gate).
	Verifier bool `json:"verifier"`
	// VerifierTier is the model tier of the verifier.
	VerifierTier string `json:"verifierTier"`
	// GitGuard installs the git guard that stops workers from destroying uncommitted work.
	GitGuard bool `json:"gitGuard"`

	// CodeGraph builds a code graph of the repository and offers it to workers. Not implemented yet: the setting is
	// kept and validated, but nothing reads it.
	CodeGraph bool `json:"codeGraph"`
	// SnapshotRetentionDays is how long the turn snapshots and attempt folders of a finished task are kept after
	// its last change.
	SnapshotRetentionDays int `json:"snapshotRetentionDays"`
	// ContentionWindowHours is how far back tasks count when warning that several tasks touch the same files.
	ContentionWindowHours int `json:"contentionWindowHours"`
	// GithubPollMinutes is how often pull request states and GitHub inboxes are polled; 0 turns polling off.
	GithubPollMinutes int `json:"githubPollMinutes"`
	// GithubEnterpriseHosts are the GitHub Enterprise Server hosts (besides github.com) whose remotes count as
	// GitHub: a host name ("ghe.example.com", optionally with a port) or an https URL; a plain http URL only for a
	// loopback address (a local test server). The GitHub token is sent only to github.com and these hosts.
	GithubEnterpriseHosts []string `json:"githubEnterpriseHosts"`
	// AutomationsEnabled is the master switch of scheduled automations (each one is still created disabled).
	AutomationsEnabled bool `json:"automationsEnabled"`
}

// Backends holds the configuration of each worker backend.
type Backends struct {
	// Agy is the Antigravity CLI (Gemini models).
	Agy Backend `json:"agy"`
	// Codex is the OpenAI Codex CLI.
	Codex CodexBackend `json:"codex"`
	// Claude is the Claude Code CLI.
	Claude Backend `json:"claude"`
}

// Backend is the configuration shared by every worker backend.
type Backend struct {
	// Enabled allows tasks on this backend.
	Enabled bool `json:"enabled"`
	// AllowedModels is a case-insensitive regular expression (Go RE2 syntax, unanchored) that every model id must
	// match. Empty allows any model. The models mapped in Tiers are always allowed.
	AllowedModels string `json:"allowedModels"`
	// Tiers maps each tier to a model id of this backend; an empty value leaves the tier unavailable.
	Tiers TierModels `json:"tiers"`
}

// CodexBackend is the Codex configuration: the shared fields plus the sandbox mode.
type CodexBackend struct {
	Backend
	// Sandbox is the codex --sandbox mode: read-only, workspace-write or danger-full-access.
	Sandbox string `json:"sandbox"`
}

// TierModels maps each tier to a model id.
type TierModels struct {
	// Fast is the model of the fast tier.
	Fast string `json:"fast"`
	// Standard is the model of the standard tier.
	Standard string `json:"standard"`
	// Strong is the model of the strong tier.
	Strong string `json:"strong"`
	// Max is the model of the max tier.
	Max string `json:"max"`
}

// Model returns the model mapped to tier and whether tier is a known tier with a model.
func (t TierModels) Model(tier string) (string, bool) {
	var m string
	switch tier {
	case TierFast:
		m = t.Fast
	case TierStandard:
		m = t.Standard
	case TierStrong:
		m = t.Strong
	case TierMax:
		m = t.Max
	}
	return m, m != ""
}

// Backend returns the shared configuration of the backend with the given id.
func (b Backends) Backend(id string) (Backend, bool) {
	switch id {
	case BackendAgy:
		return b.Agy, true
	case BackendCodex:
		return b.Codex.Backend, true
	case BackendClaude:
		return b.Claude, true
	}
	return Backend{}, false
}

// Defaults returns the default settings. This is the only place defaults are defined; every call returns a fresh
// value that the caller may change.
func Defaults() Settings {
	return Settings{
		Port:        DefaultPort,
		DefaultTier: TierStandard,
		Backends: Backends{
			Agy: Backend{
				Enabled:       true,
				AllowedModels: `^gemini-3\.8-`,
				Tiers: TierModels{
					Fast:     "gemini-3.8-flash-low",
					Standard: "gemini-3.8-flash-medium",
					Strong:   "gemini-3.8-flash-high",
					Max:      "gemini-3.8-flash-high",
				},
			},
			Codex:  CodexBackend{Sandbox: SandboxWorkspaceWrite},
			Claude: Backend{},
		},
		MaxConcurrentPerAccount: 2,
		QuotaRefreshMinutes:     10,
		MinRemaining:            0.03,
		PassiveCooldownMinutes:  60,
		UsageHistory:            true,
		TaskTimeoutMinutes:      45,
		LinkIntoProfiles: []string{
			".gitconfig", ".git-credentials", ".npmrc", ".ssh", ".config", ".cargo", ".rustup", ".m2", ".gradle",
			".nuget", "go",
		},
		StallMinutes:          10,
		StallToolMinutes:      25,
		LoopRepeat:            LoopRepeatDefault,
		AttentionDecayHours:   24,
		Gates:                 true,
		GateAutoChecks:        true,
		GateTimeoutSec:        300,
		GateMaxAutoFixes:      2,
		GateSettleMinutes:     10,
		GateSettlePollMs:      15000,
		GateOutputTailBytes:   2000,
		Hygiene:               true,
		Lanes:                 LanesEnforce,
		LaneHoldHours:         12,
		Verifier:              false,
		VerifierTier:          TierFast,
		GitGuard:              true,
		CodeGraph:             true,
		SnapshotRetentionDays: 14,
		ContentionWindowHours: 24,
		GithubPollMinutes:     10,
		GithubEnterpriseHosts: []string{},
		AutomationsEnabled:    true,
	}
}
