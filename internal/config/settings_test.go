package config

import (
	"errors"
	"strings"
	"testing"
)

func TestDefaultsAreValid(t *testing.T) {
	s := Defaults()
	if err := s.Validate(); err != nil {
		t.Fatalf("defaults do not validate: %v", err)
	}
	if s.Port != DefaultPort || DefaultPort != 47821 {
		t.Fatalf("default port = %d, want 47821", s.Port)
	}
}

func TestDefaultsAreFresh(t *testing.T) {
	a := Defaults()
	a.LinkIntoProfiles[0] = "changed"
	a.Backends.Agy.Tiers.Fast = "changed"
	b := Defaults()
	if b.LinkIntoProfiles[0] == "changed" || b.Backends.Agy.Tiers.Fast == "changed" {
		t.Fatal("Defaults must return a value that does not share state with earlier calls")
	}
}

func TestTierModelsModel(t *testing.T) {
	tm := TierModels{Fast: "f", Standard: "s", Strong: "", Max: "m"}
	tests := []struct {
		tier   string
		want   string
		wantOK bool
	}{
		{TierFast, "f", true},
		{TierStandard, "s", true},
		{TierStrong, "", false},
		{TierMax, "m", true},
		{"turbo", "", false},
	}
	for _, tt := range tests {
		got, ok := tm.Model(tt.tier)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Model(%q) = %q, %v; want %q, %v", tt.tier, got, ok, tt.want, tt.wantOK)
		}
	}
	if len(Tiers()) != 4 {
		t.Fatalf("Tiers() = %v", Tiers())
	}
}

func TestBackendsBackend(t *testing.T) {
	b := Defaults().Backends
	b.Codex.AllowedModels = "^gpt-"
	for _, id := range []string{BackendAgy, BackendCodex, BackendClaude} {
		if _, ok := b.Backend(id); !ok {
			t.Errorf("Backend(%q) not found", id)
		}
	}
	if got, _ := b.Backend(BackendCodex); got.AllowedModels != "^gpt-" {
		t.Errorf("Backend(codex).AllowedModels = %q", got.AllowedModels)
	}
	if got, _ := b.Backend(BackendAgy); !got.Enabled {
		t.Error("agy must be enabled by default")
	}
	if _, ok := b.Backend("opencode"); ok {
		t.Error("opencode is no longer a backend")
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Settings)
		fields []string
	}{
		{"port zero", func(s *Settings) { s.Port = 0 }, []string{"port"}},
		{"port too big", func(s *Settings) { s.Port = 70000 }, []string{"port"}},
		{"unknown tier", func(s *Settings) { s.DefaultTier = "turbo" }, []string{"defaultTier"}},
		{"bad regexp", func(s *Settings) { s.Backends.Claude.AllowedModels = "(" }, []string{"backends.claude.allowedModels"}},
		{"js-only regexp", func(s *Settings) { s.Backends.Agy.AllowedModels = "^(?!gemini-4)" }, []string{"backends.agy.allowedModels"}},
		{"tier outside allowed models", func(s *Settings) { s.Backends.Agy.Tiers.Max = "gemini-4-argon" }, []string{"backends.agy.tiers.max"}},
		{"tier with spaces", func(s *Settings) { s.Backends.Claude.Tiers.Fast = "claude haiku" }, []string{"backends.claude.tiers.fast"}},
		{"codex sandbox", func(s *Settings) { s.Backends.Codex.Sandbox = "none" }, []string{"backends.codex.sandbox"}},
		{"zero concurrency stalls every task", func(s *Settings) { s.MaxConcurrentPerAccount = 0 }, []string{"maxConcurrentPerAccount"}},
		{"negative minRemaining", func(s *Settings) { s.MinRemaining = -0.1 }, []string{"minRemaining"}},
		{"minRemaining one", func(s *Settings) { s.MinRemaining = 1 }, []string{"minRemaining"}},
		{"zero timeout", func(s *Settings) { s.TaskTimeoutMinutes = 0 }, []string{"taskTimeoutMinutes"}},
		{"zero quota refresh", func(s *Settings) { s.QuotaRefreshMinutes = 0 }, []string{"quotaRefreshMinutes"}},
		{"zero passive cooldown", func(s *Settings) { s.PassiveCooldownMinutes = 0 }, []string{"passiveCooldownMinutes"}},
		{"absolute link", func(s *Settings) { s.LinkIntoProfiles = []string{"/etc"} }, []string{"linkIntoProfiles[0]"}},
		{"escaping link", func(s *Settings) { s.LinkIntoProfiles = []string{".ssh", "../x"} }, []string{"linkIntoProfiles[1]"}},
		{"dot link", func(s *Settings) { s.LinkIntoProfiles = []string{"."} }, []string{"linkIntoProfiles[0]"}},
		{"empty link", func(s *Settings) { s.LinkIntoProfiles = []string{" "} }, []string{"linkIntoProfiles[0]"}},
		{"duplicate link", func(s *Settings) { s.LinkIntoProfiles = []string{".ssh", ".ssh/"} }, []string{"linkIntoProfiles[1]"}},
		{"attention", func(s *Settings) {
			s.StallMinutes, s.StallToolMinutes, s.LoopRepeat, s.AttentionDecayHours = 0, 0, 1, 0
		}, []string{"stallMinutes", "stallToolMinutes", "loopRepeat", "attentionDecayHours"}},
		{"gates", func(s *Settings) {
			s.GateTimeoutSec, s.GateMaxAutoFixes, s.GateSettleMinutes = 0, 11, -1
			s.GateSettlePollMs, s.GateOutputTailBytes = 10, 0
		}, []string{"gateTimeoutSec", "gateMaxAutoFixes", "gateSettleMinutes", "gateSettlePollMs", "gateOutputTailBytes"}},
		{"lanes", func(s *Settings) { s.Lanes, s.LaneHoldHours = "strict", -1 }, []string{"lanes", "laneHoldHours"}},
		{"verifier tier", func(s *Settings) { s.VerifierTier = "" }, []string{"verifierTier"}},
		{"retention and polling", func(s *Settings) {
			s.SnapshotRetentionDays, s.ContentionWindowHours, s.GithubPollMinutes = 0, 0, -1
		}, []string{"snapshotRetentionDays", "contentionWindowHours", "githubPollMinutes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Defaults()
			tt.mutate(&s)
			err := s.Validate()
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %T, want *ValidationError", err)
			}
			got := make([]string, len(verr.Fields))
			for i, f := range verr.Fields {
				got[i] = f.Field
				if f.Message == "" {
					t.Errorf("%s: empty message", f.Field)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.fields, ",") {
				t.Fatalf("fields = %v, want %v (%v)", got, tt.fields, err)
			}
		})
	}
}

func TestValidationValuesThatAreAllowed(t *testing.T) {
	s := Defaults()
	s.GithubPollMinutes = 0 // polling off
	s.GateMaxAutoFixes = 0  // no automatic fixes
	s.MinRemaining = 0
	s.LinkIntoProfiles = []string{".config/gh", "go"}
	s.Backends.Claude = Backend{Enabled: true, Tiers: TierModels{Standard: "claude-sonnet-5"}}
	s.Backends.Codex.AllowedModels = "^gpt-5"
	s.Backends.Codex.Tiers.Fast = "GPT-5-mini" // the pattern ignores case
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	s := Defaults()
	s.Port = 0
	s.Lanes = "x"
	err := s.Validate()
	want := `invalid settings: port: must be between 1 and 65535 (got 0); lanes: must be one of off, warn, enforce (got "x")`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant  %s", err, want)
	}
}

func TestGitHubEnterpriseHosts(t *testing.T) {
	s := Defaults()
	s.GithubEnterpriseHosts = []string{"ghe.example.com", "https://ghe2.example.com:8443", "http://127.0.0.1:9000"}
	if err := s.Validate(); err != nil {
		t.Fatalf("valid hosts refused: %v", err)
	}
	for _, bad := range []string{"", "github.com", "http://ghe.example.com", "https://u:p@ghe.example.com",
		"https://ghe.example.com/api", "ftp://x", "a b", "ghe.example.com:99999"} {
		s.GithubEnterpriseHosts = []string{bad}
		if err := s.Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	s.GithubEnterpriseHosts = []string{"GHE.example.com", "ghe.example.com"}
	if err := s.Validate(); err == nil {
		t.Error("duplicate host accepted")
	}
	h, err := NormalizeGitHubHost("http://[::1]:8080")
	if err != nil || h.Host != "[::1]:8080" || h.Scheme != "http" {
		t.Errorf("loopback v6 = %+v, %v", h, err)
	}
}

func TestResourceDefaults(t *testing.T) {
	s := Defaults()
	got := []any{s.WorkerPriority, s.GatePriority, s.MaxConcurrentWorkers, s.WorkerMemoryLimitMB,
		s.WorkerCPURatePercent, s.DaemonMemoryLimitMB}
	want := []any{"low", "low", 0, 0, 0, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("resource default %d = %v, want %v", i, got[i], want[i])
		}
	}
	if s.GateConcurrency != nil || s.MinFreeMemoryMB != nil || s.MinFreeCommitMB != nil ||
		s.CriticalFreeMemoryMB != nil || s.TestParallelism != nil {
		t.Errorf("the machine-sized settings must default to auto (nil): %+v", s)
	}
}

func TestResourceValidation(t *testing.T) {
	bad := []struct {
		field  string
		mutate func(*Settings)
	}{
		{"workerPriority", func(s *Settings) { s.WorkerPriority = "idle" }},
		{"workerPriority", func(s *Settings) { s.WorkerPriority = "" }},
		{"gatePriority", func(s *Settings) { s.GatePriority = "high" }},
		{"maxConcurrentWorkers", func(s *Settings) { s.MaxConcurrentWorkers = -1 }},
		{"maxConcurrentWorkers", func(s *Settings) { s.MaxConcurrentWorkers = 257 }},
		{"gateConcurrency", func(s *Settings) { s.GateConcurrency = Int(-1) }},
		{"gateConcurrency", func(s *Settings) { s.GateConcurrency = Int(65) }},
		{"minFreeMemoryMB", func(s *Settings) { s.MinFreeMemoryMB = Int(-1) }},
		{"minFreeMemoryMB", func(s *Settings) { s.MinFreeMemoryMB = Int(1<<20 + 1) }},
		{"minFreeCommitMB", func(s *Settings) { s.MinFreeCommitMB = Int(1<<20 + 1) }},
		{"criticalFreeMemoryMB", func(s *Settings) { s.CriticalFreeMemoryMB = Int(-1) }},
		{"criticalFreeMemoryMB", func(s *Settings) { s.MinFreeMemoryMB, s.CriticalFreeMemoryMB = Int(2048), Int(2048) }},
		{"criticalFreeMemoryMB", func(s *Settings) { s.MinFreeMemoryMB, s.CriticalFreeMemoryMB = Int(512), Int(600) }},
		{"workerMemoryLimitMB", func(s *Settings) { s.WorkerMemoryLimitMB = -5 }},
		{"workerCPURatePercent", func(s *Settings) { s.WorkerCPURatePercent = 101 }},
		{"testParallelism", func(s *Settings) { s.TestParallelism = Int(65) }},
		{"daemonMemoryLimitMB", func(s *Settings) { s.DaemonMemoryLimitMB = 65537 }},
	}
	for _, tt := range bad {
		s := Defaults()
		tt.mutate(&s)
		var verr *ValidationError
		if err := s.Validate(); !errors.As(err, &verr) || len(verr.Fields) != 1 || verr.Fields[0].Field != tt.field {
			t.Errorf("%s: err = %v, want one error on %s", tt.field, err, tt.field)
		}
	}
	for _, edge := range []func(*Settings){
		func(s *Settings) {
			s.WorkerPriority, s.GatePriority = PriorityNormal, PriorityNormal
			s.MaxConcurrentWorkers, s.GateConcurrency, s.MinFreeMemoryMB, s.MinFreeCommitMB = 0, Int(0), Int(0), Int(0)
			s.CriticalFreeMemoryMB = Int(0)
			s.WorkerMemoryLimitMB, s.WorkerCPURatePercent, s.TestParallelism, s.DaemonMemoryLimitMB = 0, 0, Int(0), 0
		},
		func(s *Settings) {
			s.MaxConcurrentWorkers, s.GateConcurrency = 256, Int(64)
			s.MinFreeMemoryMB, s.MinFreeCommitMB, s.WorkerMemoryLimitMB = Int(1<<20), Int(1<<20), 1<<20
			s.WorkerCPURatePercent, s.TestParallelism, s.DaemonMemoryLimitMB = 100, Int(64), 65536
			s.CriticalFreeMemoryMB = Int(1<<20 - 1)
		},
		func(s *Settings) { s.MinFreeMemoryMB, s.CriticalFreeMemoryMB = Int(0), Int(1<<20) }, // the floor alone
		func(s *Settings) { s.MinFreeMemoryMB, s.CriticalFreeMemoryMB = Int(4096), Int(0) },  // no floor
		func(s *Settings) { s.CriticalFreeMemoryMB = Int(1 << 19) },                          // the minimum on auto
		func(s *Settings) { s.MinFreeMemoryMB = Int(256) },                                   // the floor on auto
	} {
		s := Defaults()
		edge(&s)
		if err := s.Validate(); err != nil {
			t.Errorf("edge values refused: %v", err)
		}
	}
}

func TestResourceSettingsPatch(t *testing.T) {
	s := Defaults()
	s, err := MergePatch(s, []byte(`{"workerPriority": "normal", "gateConcurrency": 0, "minFreeMemoryMB": 1536,
		"testParallelism": 8, "daemonMemoryLimitMB": 300}`))
	if err != nil {
		t.Fatal(err)
	}
	if s.WorkerPriority != PriorityNormal || !isInt(s.GateConcurrency, 0) || !isInt(s.MinFreeMemoryMB, 1536) ||
		!isInt(s.TestParallelism, 8) || s.DaemonMemoryLimitMB != 300 || s.CriticalFreeMemoryMB != nil {
		t.Fatalf("patch not applied: %+v", s)
	}
	// An explicit floor at or above an explicit minimum is refused, naming the floor.
	if _, err := MergePatch(s, []byte(`{"criticalFreeMemoryMB": 1536}`)); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "criticalFreeMemoryMB") {
		t.Fatalf("floor above the minimum: %v", err)
	}
	// A lone floor whose double (the derived minimum) would be out of range is refused, naming the floor.
	if _, err := MergePatch(Defaults(), []byte(`{"criticalFreeMemoryMB": 524289}`)); !errors.Is(err, ErrInvalid) ||
		!strings.Contains(err.Error(), "criticalFreeMemoryMB") || strings.Contains(err.Error(), "minFreeMemoryMB:") {
		t.Fatalf("lone floor above half the range: %v", err)
	}
	s, err = MergePatch(s, []byte(`{"workerPriority": null, "gateConcurrency": null, "minFreeMemoryMB": null,
		"testParallelism": null, "daemonMemoryLimitMB": null}`))
	if err != nil {
		t.Fatal(err)
	}
	d := Defaults()
	if s.WorkerPriority != d.WorkerPriority || s.GateConcurrency != nil || s.MinFreeMemoryMB != nil ||
		s.TestParallelism != nil || s.DaemonMemoryLimitMB != d.DaemonMemoryLimitMB {
		t.Fatalf("null did not reset to the defaults (auto): %+v", s)
	}
	if _, err := MergePatch(s, []byte(`{"gatePriority": "realtime", "workerCPURatePercent": 150}`)); !errors.Is(err,
		ErrInvalid) {
		t.Fatalf("out-of-range patch: err = %v", err)
	}
	c, err := Parse([]byte(`{"gatePriority": null, "maxConcurrentWorkers": 3, "testParallelism": null,
		"gateConcurrency": 3}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.GatePriority != PriorityLow || c.Settings.MaxConcurrentWorkers != 3 ||
		c.Settings.MinFreeCommitMB != nil || c.Settings.TestParallelism != nil || !isInt(c.Settings.GateConcurrency, 3) {
		t.Fatalf("parse: missing keys and null must take the defaults: %+v", c.Settings)
	}
}

func isInt(p *int, n int) bool { return p != nil && *p == n }
