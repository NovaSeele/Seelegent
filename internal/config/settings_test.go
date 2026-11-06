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
