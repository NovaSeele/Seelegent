package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// Fake credentials, assembled from parts so that no complete token-shaped literal sits in the source.
var (
	fakeOpenAI    = "sk-" + "proj-FAKEfake0123456789FAKE"
	fakeAnthropic = "sk-" + "ant-api03-FAKE_fake-0123456789"
	fakeGHP       = "ghp_" + strings.Repeat("F4ke", 9)
	fakeGHO       = "gho_" + strings.Repeat("F4ke", 9)
	fakePAT       = "github_pat_" + strings.Repeat("F4ke", 6) + "_" + strings.Repeat("fAKE", 10)
	fakeSlack     = "xox" + "b-000000000000-FAKEFAKEFAKE"
	fakeGoogleKey = "AIza" + strings.Repeat("Fake", 9)
	fakeOAuth     = "ya29" + ".FAKE-fake_FAKE-fake"
	fakeAWS       = "AKIA" + "FAKEFAKEFAKEFAKE"
	fakeJWT       = "eyJ" + "hbGciOiJIUzI1NiJ9.eyJzdWIiOiJmYWtlIn0.ZmFrZS1zaWduYXR1cmU"
	fakeEmail     = "nova.seele+test@example.com"
)

func TestRedactorString(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name, in, want string
	}{
		{"openai key", "key " + fakeOpenAI + " used", "key [REDACTED] used"},
		{"anthropic key", "ANTHROPIC_API_KEY " + fakeAnthropic, "ANTHROPIC_API_KEY [REDACTED]"},
		{"github classic", "push with " + fakeGHP, "push with [REDACTED]"},
		{"github oauth", fakeGHO + ".", "[REDACTED]."},
		{"github fine-grained", "(" + fakePAT + ")", "([REDACTED])"},
		{"slack", "slack " + fakeSlack, "slack [REDACTED]"},
		{"google api key", "key=" + fakeGoogleKey, "key=[REDACTED]"},
		{"google oauth", "token " + fakeOAuth + " expired", "token [REDACTED] expired"},
		{"aws key id", "id " + fakeAWS, "id [REDACTED]"},
		{"bearer header", "Authorization: Bearer abc.DEF-ghi_123==", "Authorization: Bearer [REDACTED]"},
		{"bearer jwt", "authorization: bearer " + fakeJWT, "authorization: bearer [REDACTED]"},
		{"jwt", "cookie session=" + fakeJWT + ";", "cookie session=[REDACTED];"},
		{"email", "signed in as " + fakeEmail + " today", "signed in as [EMAIL] today"},
		{"api_key pair", "api_key=fakevalue123 next", "api_key=[REDACTED] next"},
		{"json password", `{"password": "hunter2hunter2"}`, `{"password": "[REDACTED]"}`},
		{"token pair", "TOKEN: abcdef123456", "TOKEN: [REDACTED]"},
		{"two secrets", fakeGHP + " and " + fakeOpenAI, "[REDACTED] and [REDACTED]"},
		// Things that look a little like secrets but are not.
		{"task id", "task-0123456789abcdefghij", "task-0123456789abcdefghij"},
		{"risk word", "risk-assessment-for-the-long-run", "risk-assessment-for-the-long-run"},
		{"short sk", "sk-short", "sk-short"},
		{"token count", "tokens: 123456789", "tokens: 123456789"},
		{"short token value", "token: none", "token: none"},
		{"npm package", "pnpm@11.20.0 and @types/node@22", "pnpm@11.20.0 and @types/node@22"},
		{"plain text", "gate passed in 3.2s", "gate passed in 3.2s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.String(tt.in); got != tt.want {
				t.Fatalf("String(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactorLiteralSecrets(t *testing.T) {
	r := NewRedactor("zz-provider-key-0001", "short")
	if got := r.String("using zz-provider-key-0001 and short"); got != "using [REDACTED] and short" {
		t.Fatalf("got %q", got)
	}
}

func TestSensitiveKey(t *testing.T) {
	tests := map[string]bool{
		"apiKey": true, "api_key": true, "GITHUB_TOKEN": true, "password": true, "client-secret": true,
		"Authorization": true, "set-cookie": true, "private_key": true, "credentials": true,
		"tokens": false, "totalTokens": false, "key": false, "account": false, "model": false,
	}
	for key, want := range tests {
		if got := sensitiveKey(key); got != want {
			t.Errorf("sensitiveKey(%q) = %v, want %v", key, got, want)
		}
	}
}

type secretValuer struct{}

func (secretValuer) LogValue() slog.Value { return slog.StringValue("valuer " + fakeGHP) }

type report struct {
	Account string `json:"account"`
	Note    string `json:"note"`
}

func TestRedactingHandler(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	log := slog.New(NewRedactingHandler(inner, NewRedactor()))
	log = log.With("owner", fakeEmail).WithGroup("task").With("brief", "uses "+fakeOpenAI)
	log.Info("worker said "+fakeGHO,
		"apiKey", "plain-looking-value",
		"empty_token", "",
		"tokens", 1234,
		"err", errors.New("auth failed for "+fakeEmail),
		"raw", []byte("Bearer abcdefgh"),
		"valuer", secretValuer{},
		"report", report{Account: "acc1", Note: "key " + fakeOpenAI},
		"clean", report{Account: "acc2", Note: "ok"},
		"clientSecret", report{Account: "acc3"},
		slog.Group("http", "authorization", "Basic Zm9vOmJhcg==", "status", 401),
	)

	out := buf.String()
	for _, secret := range []string{fakeOpenAI, fakeGHO, fakeGHP, fakeEmail, "plain-looking-value", "Zm9vOmJhcg=="} {
		if strings.Contains(out, secret) {
			t.Errorf("log line leaks %q:\n%s", secret, out)
		}
	}
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	task := line["task"].(map[string]any)
	checks := map[string]any{
		"msg":    "worker said [REDACTED]",
		"owner":  "[EMAIL]",
		"brief":  "uses [REDACTED]",
		"apiKey": "[REDACTED]",
		"err":    "auth failed for [EMAIL]",
		"raw":    "Bearer [REDACTED]",
		"valuer": "valuer [REDACTED]",
		"report": `{"account":"acc1","note":"key [REDACTED]"}`,
	}
	for key, want := range checks {
		got, ok := line[key]
		if !ok {
			got = task[key]
		}
		if got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	if task["tokens"] != float64(1234) || task["empty_token"] != "" {
		t.Errorf("non-secret values changed: tokens=%v empty_token=%v", task["tokens"], task["empty_token"])
	}
	if clean, ok := task["clean"].(map[string]any); !ok || clean["account"] != "acc2" {
		t.Errorf("a value without secrets must keep its structure: %#v", task["clean"])
	}
	if task["clientSecret"] != "[REDACTED]" {
		t.Errorf("a struct under a secret key must be masked: %#v", task["clientSecret"])
	}
	http := task["http"].(map[string]any)
	if http["authorization"] != "[REDACTED]" || http["status"] != float64(401) {
		t.Errorf("group = %#v", http)
	}
}

func TestRedactingHandlerEnabled(t *testing.T) {
	inner := slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := NewRedactingHandler(inner, NewRedactor())
	if h.Enabled(t.Context(), slog.LevelInfo) || !h.Enabled(t.Context(), slog.LevelError) {
		t.Fatal("Enabled must follow the inner handler")
	}
}

type unmarshalable struct{ C chan int }

func TestAnyAttrFallsBackToFormatting(t *testing.T) {
	h := NewRedactingHandler(slog.DiscardHandler, NewRedactor())
	a := h.anyAttr("x", unmarshalable{})
	if a.Value.Kind() != slog.KindAny {
		t.Fatalf("a value without secrets must stay as it is, got %v", a.Value.Kind())
	}
	a = h.anyAttr("x", struct {
		C chan int
		S string
	}{S: fakeGHP})
	if a.Value.Kind() != slog.KindString || strings.Contains(a.Value.String(), fakeGHP) {
		t.Fatalf("got %v", a.Value)
	}
}
