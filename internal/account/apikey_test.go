package account

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// The base URL of a claude provider loses the API path Claude Code appends itself; codex keeps /v1.
func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct{ backend, in, want string }{
		{"claude", "https://api.example.test/v1/", "https://api.example.test"},
		{"claude", "https://api.example.test/v1", "https://api.example.test"},
		{"claude", "https://api.example.test/V1/Messages", "https://api.example.test"},
		{"claude", "https://api.example.test/v1/messages/", "https://api.example.test"},
		{"claude", "https://api.example.test/", "https://api.example.test"},
		{"claude", "https://x.example/anthropic/v1/", "https://x.example/anthropic"},
		{"claude", "https://x.example/anthropic", "https://x.example/anthropic"},
		{"claude", "https://x.example/v12", "https://x.example/v12"},
		{"claude", "https://x.example/v1?a=b", "https://x.example/v1?a=b"},
		{"claude", "https://v1", "https://v1"},
		{"codex", "https://api.example.test/v1", "https://api.example.test/v1"},
		{"codex", "https://api.example.test/v1/", "https://api.example.test/v1/"},
	}
	for _, c := range cases {
		if got := NormalizeBaseURL(c.backend, c.in); got != c.want {
			t.Errorf("NormalizeBaseURL(%s, %q) = %q, want %q", c.backend, c.in, got, c.want)
		}
	}
}

func TestCheckNotices(t *testing.T) {
	// A custom Anthropic-compatible provider with the URL from an OpenAI-SDK .env: trimmed, with a notice.
	p := Provider{ID: "x", Backend: "claude", Preset: "custom-anthropic", BaseURL: "https://x.example/v1/"}
	notices, err := p.Check()
	if err != nil || p.BaseURL != "https://x.example" || len(notices) != 1 || notices[0].Code != NoticeBaseURLTrimmed ||
		notices[0].Field != "baseUrl" {
		t.Fatalf("Check = %+v, %v; provider %+v", notices, err, p)
	}
	if again, err := p.Check(); err != nil || len(again) != 0 {
		t.Fatalf("second Check = %+v, %v", again, err)
	}
	// The same on api.anthropic.com becomes the official preset: the key must go as an API key.
	p = Provider{ID: "y", Name: "Custom (Anthropic API)", Backend: "claude", Preset: "custom-anthropic",
		BaseURL: "https://API.anthropic.com/v1/"}
	notices, err = p.Check()
	if err != nil || !p.Official || p.Preset != "anthropic" || p.BaseURL != "" || p.Name != "Anthropic" ||
		len(notices) != 1 || notices[0].Code != NoticeOfficialPreset {
		t.Fatalf("anthropic host: %+v, %v; provider %+v", notices, err, p)
	}
	// Another preset on that host, and codex, are left alone.
	p = Provider{ID: "z", Backend: "claude", Preset: "openrouter", BaseURL: "https://api.anthropic.com"}
	if notices, err = p.Check(); err != nil || p.Official || len(notices) != 0 {
		t.Fatalf("openrouter preset: %+v, %v; %+v", notices, err, p)
	}
	p = Provider{ID: "o", Backend: "codex", BaseURL: "https://api.openai.com/v1"}
	if notices, err = p.Check(); err != nil || p.BaseURL != "https://api.openai.com/v1" || len(notices) != 0 {
		t.Fatalf("codex: %+v, %v; %+v", notices, err, p)
	}
	// Errors name their field.
	p = Provider{ID: "b", Backend: "claude", BaseURL: "http://evil.test"}
	_, err = p.Check()
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Field != "baseUrl" || !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad base URL: %v", err)
	}
	if err := PrefixField("newProvider", err); !errors.As(err, &fe) || fe.Field != "newProvider.baseUrl" {
		t.Fatalf("prefixed: %v", err)
	}
}

// A provider without models or tiers is a valid "model per task" provider.
func TestModelPerTaskProvider(t *testing.T) {
	p, err := FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil || !p.ModelPerTask() || p.AllowedModels != "" {
		t.Fatalf("empty provider: %v, %+v", err, p)
	}
	p.Tiers = config.TierModels{Standard: "claude-x"}
	if p.ModelPerTask() {
		t.Error("a provider with a tier model is not model per task")
	}
}

func TestNormalizeKey(t *testing.T) {
	const secret = "sk-ant-api03-SECRETSECRET"
	ok := []struct {
		in      string
		notices []string
	}{
		{secret, nil},
		{"  " + secret + "\n", nil},
		{"ANTHROPIC_API_KEY=" + secret, []string{NoticeKeyNameRemoved}},
		{"export LLM_API_KEY = \"" + secret + "\"", []string{NoticeKeyNameRemoved, NoticeKeyQuotesRemoved}},
		{"LLM_API_KEY=" + secret, []string{NoticeKeyNameRemoved}},
		{"'" + secret + "'", []string{NoticeKeyQuotesRemoved}},
		{"gsk_abcdEFGH1234", nil},
	}
	for _, c := range ok {
		key, notices, err := NormalizeKey(c.in)
		if err != nil || (key != secret && key != "gsk_abcdEFGH1234") {
			t.Errorf("NormalizeKey(%q) = %q, %v", c.in, key, err)
			continue
		}
		var codes []string
		for _, n := range notices {
			codes = append(codes, n.Code)
			if strings.Contains(n.Message, secret) {
				t.Errorf("notice quotes the key: %q", n.Message)
			}
		}
		if strings.Join(codes, ",") != strings.Join(c.notices, ",") {
			t.Errorf("NormalizeKey(%q) notices = %v, want %v", c.in, codes, c.notices)
		}
	}
	bad := []struct{ in, reason string }{
		{"https://api.anthropic.com/v1/", KeyReasonURL},
		{"sk-ant-" + "abc://def", KeyReasonURL},
		{"sk-ant-abc def", KeyReasonWhitespace},
		{"sk-ant-abc\ndef", KeyReasonWhitespace},
		{"sk-ant-abc\r\ndef", KeyReasonWhitespace},
		{"sk-ant-abc\tdef", KeyReasonWhitespace},
		{"sk-ant-abc\x00def", KeyReasonControl},
		{"", KeyReasonEmpty},
		{strings.Repeat("k", maxKeyLen+1), KeyReasonTooLong},
	}
	for _, c := range bad {
		_, _, err := NormalizeKey(c.in)
		var fe *FieldError
		if !errors.Is(err, ErrBadKey) || !errors.Is(err, ErrInvalid) || !errors.As(err, &fe) || fe.Reason != c.reason ||
			fe.Field != "apiKey" {
			t.Errorf("NormalizeKey(%q) = %v, want reason %s", c.in, err, c.reason)
			continue
		}
		if c.in != "" && strings.Contains(err.Error(), strings.TrimSpace(c.in)) {
			t.Errorf("the error quotes the key: %v", err)
		}
	}
	// Keys that hold "=" or look like name=value are kept whole: only an env-style NAME= followed by a whole key
	// is a pasted .env line. A notice never quotes key characters.
	for _, in := range []string{"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVpkZWZnaGlqa2xtbm9wZGVm==", "abc_DEF=xyz12345",
		"ABCDEFGH==ijklmnopqrst", "KEY=short"} {
		key, notices, err := NormalizeKey(in)
		if err != nil || key != in || len(notices) != 0 {
			t.Errorf("NormalizeKey(%q) = %q, %v, %v; want it unchanged", in, key, notices, err)
		}
	}
	long := strings.Repeat("A", 41)
	key, notices, err := NormalizeKey(long + "=" + secret)
	if err != nil || key != secret || len(notices) != 1 || strings.Contains(notices[0].Message, long) ||
		notices[0].Message != "removed a NAME= prefix from the start of the key" {
		t.Errorf("long name: %q, %+v, %v", key, notices, err)
	}
	if key, notices, err := NormalizeKey(`export OPENAI_API_KEY="sk-proj-0123456789"`); err != nil ||
		key != "sk-proj-0123456789" || len(notices) != 2 || notices[0].Message != "removed OPENAI_API_KEY= from the start of the key" {
		t.Errorf("export line: %q, %+v, %v", key, notices, err)
	}
	_, _, err = NormalizeKey("https://api.anthropic.com/v1/")
	if !strings.Contains(err.Error(), "this looks like a URL, not an API key") {
		t.Errorf("URL error = %v", err)
	}
	if err := KeyFieldAs("key", err); !strings.Contains(err.Error(), "URL") {
		t.Errorf("renamed: %v", err)
	}
}

func TestKeyNotices(t *testing.T) {
	anthropic, _ := FromPreset("anthropic", "anthropic")
	openai, _ := FromPreset("openai", "openai")
	custom, _ := FromPreset("proxy", "custom-anthropic")
	cases := []struct {
		p    Provider
		key  string
		want bool
	}{
		{anthropic, "sk-ant-api03-x", false},
		{anthropic, "sk-proxy-123", true},
		{openai, "sk-ant-api03-x", true},
		{openai, "sk-proj-123", false},
		{openai, "gsk_123", true},
		{custom, "sk-ant-api03-x", false},
		{custom, "anything", false},
	}
	for _, c := range cases {
		got := KeyNotices(c.p, c.key)
		if (len(got) > 0) != c.want {
			t.Errorf("KeyNotices(%s, %q) = %+v, want notice %v", c.p.ID, c.key, got, c.want)
		}
		for _, n := range got {
			if n.Code != NoticeKeyPrefix || strings.Contains(n.Message, c.key) {
				t.Errorf("notice %+v", n)
			}
		}
	}
}

// An API-key account is not tested until a test or a good turn; a new key or endpoint makes it not tested again;
// a test or turn that started before the change does not count.
func TestTestState(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	f.provider(t, "ds", "deepseek")
	a, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindAPIKey, Provider: "ds", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.TestState() != TestNotTested {
		t.Fatalf("new account: %q", a.TestState())
	}
	// Without a key nothing is recorded.
	if changed, err := f.m.MarkRunOK(ctx, a.ID, f.clock.now()); err != nil || changed {
		t.Fatalf("MarkRunOK without key = %v, %v", changed, err)
	}
	if a, err = f.m.SetKey(ctx, a.ID, "sk-abcdefgh1234"); err != nil {
		t.Fatal(err)
	}
	before := a.KeySetAt.Add(-time.Second)
	if changed, err := f.m.MarkRunOK(ctx, a.ID, before); err != nil || changed {
		t.Fatalf("a turn from before the key = %v, %v", changed, err)
	}
	if changed, err := f.m.RecordTest(ctx, a.ID, "refused", f.clock.now()); err != nil || !changed {
		t.Fatalf("RecordTest = %v, %v", changed, err)
	}
	if a, _ = f.m.Get(ctx, a.ID); a.TestState() != "refused" || a.TestedAt.IsZero() {
		t.Fatalf("after a refused test: %+v", a)
	}
	if changed, err := f.m.MarkRunOK(ctx, a.ID, f.clock.now()); err != nil || !changed {
		t.Fatalf("MarkRunOK = %v, %v", changed, err)
	}
	if changed, err := f.m.MarkRunOK(ctx, a.ID, f.clock.now()); err != nil || changed {
		t.Fatalf("second MarkRunOK wrote again: %v, %v", changed, err)
	}
	if a, _ = f.m.Get(ctx, a.ID); a.TestState() != TestOK {
		t.Fatalf("after a good turn: %+v", a)
	}
	if _, err := f.m.RecordTest(ctx, a.ID, "Bad Code", f.clock.now()); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad result code: %v", err)
	}
	// A new key: not tested again.
	if a, err = f.m.SetKey(ctx, a.ID, "sk-other-5678"); err != nil || a.TestState() != TestNotTested {
		t.Fatalf("after a new key: %+v, %v", a, err)
	}
	if _, err := f.m.MarkRunOK(ctx, a.ID, f.clock.now()); err != nil {
		t.Fatal(err)
	}
	// Renaming the provider keeps the state; a new base URL clears it.
	p, err := f.m.GetProvider(ctx, "ds")
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "Deep"
	if _, err := f.m.UpdateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if a, _ = f.m.Get(ctx, a.ID); a.TestState() != TestOK {
		t.Fatalf("after a rename: %q", a.TestState())
	}
	p.BaseURL = "https://other.example.test"
	if _, err := f.m.UpdateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if a, _ = f.m.Get(ctx, a.ID); a.TestState() != TestNotTested {
		t.Fatalf("after a new base URL: %q", a.TestState())
	}
	// Sign-in accounts have no test state.
	iso, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindIsolated}, nil)
	if err != nil || iso.TestState() != "" {
		t.Fatalf("isolated: %q, %v", iso.TestState(), err)
	}
}

func TestAddDirect(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	np := func() *Provider {
		p, err := FromPreset("", "custom-anthropic")
		if err != nil {
			t.Fatal(err)
		}
		p.BaseURL = "https://x.example/v1/"
		return &p
	}
	res, err := f.m.AddDirect(ctx, DirectNew{NewProvider: np(), Key: "LLM_API_KEY=sk-test-12345678"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := res.Account
	if !res.ProviderCreated || res.Provider.ID != "custom-anthropic" || res.Provider.BaseURL != "https://x.example" ||
		a.Provider != res.Provider.ID || a.Backend != "claude" || a.Kind != KindAPIKey || a.KeyHint != "…5678" ||
		a.Label != "Custom (Anthropic API) 1" || a.TestState() != TestNotTested {
		t.Fatalf("AddDirect = %+v", res)
	}
	codes := map[string]bool{}
	for _, n := range res.Notices {
		codes[n.Code] = true
	}
	if !codes[NoticeKeyNameRemoved] || !codes[NoticeBaseURLTrimmed] {
		t.Errorf("notices = %+v", res.Notices)
	}
	if _, key, err := f.m.Credentials(ctx, a); err != nil || key != "sk-test-12345678" {
		t.Fatalf("stored key = %q, %v", key, err)
	}
	// The same service again: the provider is reused, the label counts on.
	res2, err := f.m.AddDirect(ctx, DirectNew{NewProvider: np(), Key: "sk-test-87654321"}, nil)
	if err != nil || res2.ProviderCreated || res2.Provider.ID != res.Provider.ID || res2.Account.Label != "Custom (Anthropic API) 2" {
		t.Fatalf("second AddDirect = %+v, %v", res2, err)
	}
	if res2.Notices[len(res2.Notices)-1].Code != NoticeProviderReused {
		t.Errorf("notices = %+v", res2.Notices)
	}
	// Another base URL makes a second provider with the next free id.
	other := np()
	other.BaseURL = "https://y.example"
	res3, err := f.m.AddDirect(ctx, DirectNew{NewProvider: other}, nil)
	if err != nil || !res3.ProviderCreated || res3.Provider.ID != "custom-anthropic-2" || res3.Account.KeyHint != "" {
		t.Fatalf("third AddDirect = %+v, %v", res3, err)
	}
	// A reused provider is never changed: tiers asked for on a model-per-task provider make a new provider, and the
	// first one stays model-per-task.
	tiered := np()
	tiered.Tiers.Standard = "glm-5"
	res5, err := f.m.AddDirect(ctx, DirectNew{NewProvider: tiered}, nil)
	if err != nil || !res5.ProviderCreated || res5.Provider.ID != "custom-anthropic-3" || res5.Provider.ModelPerTask() {
		t.Fatalf("tiers on a model-per-task provider = %+v, %v", res5, err)
	}
	if first, err := f.m.GetProvider(ctx, res.Provider.ID); err != nil || !first.ModelPerTask() {
		t.Fatalf("the reused provider changed: %+v, %v", first, err)
	}
	// The wire and the allow-list are part of the match: custom OpenAI providers on one base URL with another wire
	// are not merged.
	openai := func(wire string) *Provider {
		p, err := FromPreset("", "custom-openai")
		if err != nil {
			t.Fatal(err)
		}
		p.BaseURL, p.WireAPI = "https://llm.example/v1", wire
		return &p
	}
	chat, err := f.m.AddDirect(ctx, DirectNew{NewProvider: openai(WireChat)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	responses, err := f.m.AddDirect(ctx, DirectNew{NewProvider: openai(WireResponses)}, nil)
	if err != nil || !responses.ProviderCreated || responses.Provider.ID == chat.Provider.ID {
		t.Fatalf("another wire = %+v, %v (first %s)", responses.Provider, err, chat.Provider.ID)
	}
	again, err := f.m.AddDirect(ctx, DirectNew{NewProvider: openai(WireChat)}, nil)
	if err != nil || again.ProviderCreated || again.Provider.ID != chat.Provider.ID {
		t.Fatalf("same wire = %+v, %v", again.Provider, err)
	}
	// An existing provider by id, with a key; backend and kind come from it.
	res4, err := f.m.AddDirect(ctx, DirectNew{New: New{Provider: "custom-anthropic-2", Label: "Mine"}, Key: "sk-x-12345678"}, nil)
	if err != nil || res4.Account.Backend != "claude" || res4.Account.Label != "Mine" || res4.Account.KeyHint == "" {
		t.Fatalf("existing provider = %+v, %v", res4, err)
	}

	// Errors name their field and leave nothing behind.
	fieldOf := func(err error) string {
		var fe *FieldError
		if !errors.As(err, &fe) {
			return ""
		}
		return fe.Field
	}
	bad := np()
	bad.BaseURL = "http://evil.test"
	cases := []struct {
		in    DirectNew
		field string
	}{
		{DirectNew{NewProvider: np(), Key: "https://api.anthropic.com/v1/"}, "apiKey"},
		{DirectNew{NewProvider: bad, Key: "sk-test-12345678"}, "newProvider.baseUrl"},
		{DirectNew{New: New{Provider: "nope"}, Key: "sk-test-12345678"}, "provider"},
		{DirectNew{New: New{Provider: "custom-anthropic"}, NewProvider: np()}, "provider"},
		{DirectNew{New: New{Backend: "codex"}, NewProvider: np()}, "backend"},
		{DirectNew{New: New{Label: "a\x00"}, NewProvider: np()}, "label"},
		{DirectNew{}, "provider"},
	}
	for i, c := range cases {
		_, err := f.m.AddDirect(ctx, c.in, nil)
		if !errors.Is(err, ErrInvalid) || fieldOf(err) != c.field {
			t.Errorf("case %d: err = %v (field %q), want field %s", i, err, fieldOf(err), c.field)
		}
	}
}

// Atomicity: a failure at any step (or at the commit) leaves no provider, account, profile or key behind.
func TestAddDirectFaults(t *testing.T) {
	for _, step := range []string{stepProvider, stepAccount, stepProfile, stepKey, stepCommit} {
		t.Run(step, func(t *testing.T) {
			f := newFixture(t)
			ctx := ctxT(t)
			boom := errors.New("injected " + step)
			f.m.faults = func(s string) error {
				if s == step {
					return boom
				}
				return nil
			}
			p, err := FromPreset("", "anthropic")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.m.AddDirect(ctx, DirectNew{NewProvider: &p, Key: "sk-ant-api03-12345678"}, nil); !errors.Is(err, boom) {
				t.Fatalf("AddDirect = %v, want the injected error", err)
			}
			if list, err := f.m.ListProviders(ctx); err != nil || len(list) != 0 {
				t.Errorf("providers left: %+v, %v", list, err)
			}
			if list, err := f.m.List(ctx); err != nil || len(list) != 0 {
				t.Errorf("accounts left: %+v, %v", list, err)
			}
			entries, err := os.ReadDir(f.profiles)
			if err != nil || len(entries) != 0 {
				t.Errorf("profile files left: %v, %v", entries, err)
			}
			// The counter did not move either: the next account is acc1 again.
			f.m.faults = nil
			res, err := f.m.AddDirect(ctx, DirectNew{NewProvider: &p, Key: "sk-ant-api03-12345678"}, nil)
			if err != nil || res.Account.ID != "acc1" {
				t.Fatalf("after the fault: %+v, %v", res.Account, err)
			}
			if _, err := os.Stat(filepath.Join(f.profiles, "acc1", keyFileName)); err != nil {
				t.Errorf("key file: %v", err)
			}
		})
	}
}

// A provider saved before the base URL rules runs with the clean URL (and as the official preset on
// api.anthropic.com).
func TestCredentialsCleanLegacyProvider(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	for id, raw := range map[string]string{"old": "https://x.example/v1/", "anth": "https://api.anthropic.com/v1/"} {
		if err := f.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO providers (id, name, backend, preset, base_url, wire_api, official,
				enabled, allowed_models, tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
				VALUES (?, ?, 'claude', 'custom-anthropic', ?, '', 0, 1, '', '', '', '', '', ?, ?)`, id, id, raw,
				"2026-11-01T00:00:00.000000000Z", "2026-11-01T00:00:00.000000000Z")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]Provider{"old": {BaseURL: "https://x.example"}, "anth": {Official: true}} {
		a, _, err := f.m.Add(ctx, New{Backend: "claude", Kind: KindAPIKey, Provider: id, Enabled: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.m.SetKey(ctx, a.ID, "sk-ant-api03-12345678"); err != nil {
			t.Fatal(err)
		}
		p, _, err := f.m.Credentials(ctx, a)
		if err != nil || p.BaseURL != want.BaseURL || p.Official != want.Official {
			t.Errorf("%s: Credentials = %+v, %v", id, p, err)
		}
	}
}
