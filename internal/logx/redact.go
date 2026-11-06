package logx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

// Replacement texts.
const (
	// Masked replaces a secret.
	Masked = "[REDACTED]"
	// MaskedEmail replaces an email address.
	MaskedEmail = "[EMAIL]"
)

// minSecretLen is the shortest literal secret the redactor masks; shorter strings would mask ordinary words.
const minSecretLen = 8

// rule is one pattern and its replacement (which may refer to groups, as in regexp.ReplaceAllString).
type rule struct {
	re   *regexp.Regexp
	repl string
}

// Redactor masks secrets and email addresses in text. It is immutable and safe for concurrent use.
type Redactor struct {
	rules   []rule
	secrets []string
}

// NewRedactor returns a redactor for the built-in patterns plus the given literal secrets (for example API keys of
// configured accounts whose format no pattern knows). Secrets shorter than 8 characters are ignored.
//
// Built-in patterns: OpenAI/Anthropic-style keys (sk-...), GitHub tokens (ghp_, gho_, ghu_, ghs_, ghr_,
// github_pat_), Slack tokens (xox?-), Google API keys (AIza...) and OAuth tokens (ya29.), AWS access key ids
// (AKIA...), Bearer credentials, JWTs, key=value pairs whose key names a secret (api_key, token, password, ...),
// and email addresses.
func NewRedactor(secrets ...string) *Redactor {
	keep := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if len(s) >= minSecretLen {
			keep = append(keep, s)
		}
	}
	return &Redactor{rules: builtinRules(), secrets: keep}
}

// builtinRules compiles the patterns. Specific token formats come first so that, for example, a Bearer header
// carrying a JWT is masked as one credential.
func builtinRules() []rule {
	const tok = `[A-Za-z0-9_\-]`
	return []rule{
		{regexp.MustCompile(`\bsk-` + tok + `{16,}`), Masked},
		{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), Masked},
		{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), Masked},
		{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{8,}`), Masked},
		{regexp.MustCompile(`\bAIza` + tok + `{30,}`), Masked},
		{regexp.MustCompile(`\bya29\.` + tok + `{10,}`), Masked},
		{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), Masked},
		{regexp.MustCompile(`(?i)\b(bearer)\s+[A-Za-z0-9\-._~+/]+=*`), "${1} " + Masked},
		{regexp.MustCompile(`\beyJ` + tok + `{5,}\.` + tok + `{5,}\.` + tok + `*`), Masked},
		{
			regexp.MustCompile(`(?i)\b((?:api[_-]?key|api[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|secret|password|passwd|token)["']?\s*[:=]\s*["']?)[^\s"',;&]{6,}`),
			"${1}" + Masked,
		},
		{regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}\b`), MaskedEmail},
	}
}

// String returns s with every secret and email address masked.
func (r *Redactor) String(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, Masked)
	}
	for _, rl := range r.rules {
		s = rl.re.ReplaceAllString(s, rl.repl)
	}
	return s
}

// sensitiveKey reports whether an attribute key names a secret, such as "apiKey", "github_token" or "Password":
// its lower-cased form without _ - . ends in password, passwd, secret, token, apikey, authorization, cookie,
// privatekey or credential(s). String values of such keys are masked whatever they look like.
func sensitiveKey(key string) bool {
	k := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
	for _, s := range [...]string{
		"password", "passwd", "secret", "token", "apikey", "authorization", "cookie", "privatekey", "credential",
		"credentials",
	} {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// RedactingHandler is a slog.Handler that masks secrets and email addresses in the message and in every attribute
// (including groups, errors, values from LogValuer and other values) before passing the record on.
type RedactingHandler struct {
	inner slog.Handler
	r     *Redactor
}

// NewRedactingHandler wraps inner so that every record it receives is redacted by r.
func NewRedactingHandler(inner slog.Handler, r *Redactor) *RedactingHandler {
	return &RedactingHandler{inner: inner, r: r}
}

// Enabled implements slog.Handler.
func (h *RedactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle implements slog.Handler.
func (h *RedactingHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

// WithAttrs implements slog.Handler.
func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	red := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		red[i] = h.attr(a)
	}
	return &RedactingHandler{inner: h.inner.WithAttrs(red), r: h.r}
}

// WithGroup implements slog.Handler.
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
	return &RedactingHandler{inner: h.inner.WithGroup(name), r: h.r}
}

// attr returns a with its value redacted.
func (h *RedactingHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		group := v.Group()
		red := make([]slog.Attr, len(group))
		for i, g := range group {
			red[i] = h.attr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(red...)}
	case slog.KindString:
		if sensitiveKey(a.Key) && v.String() != "" {
			return slog.String(a.Key, Masked)
		}
		return slog.String(a.Key, h.r.String(v.String()))
	case slog.KindAny:
		if sensitiveKey(a.Key) {
			return slog.String(a.Key, Masked)
		}
		return h.anyAttr(a.Key, v.Any())
	default: // numbers, booleans, durations and times carry no secrets
		return slog.Attr{Key: a.Key, Value: v}
	}
}

// anyAttr redacts an arbitrary value. Errors and byte slices become redacted strings. Other values are rendered as
// the JSON handler would render them; if that text holds a secret, the value is replaced by the redacted text,
// otherwise it is kept as it was.
func (h *RedactingHandler) anyAttr(key string, x any) slog.Attr {
	switch x := x.(type) {
	case error:
		return slog.String(key, h.r.String(x.Error()))
	case []byte:
		return slog.String(key, h.r.String(string(x)))
	}
	var text string
	if b, err := json.Marshal(x); err == nil {
		text = string(b)
	} else {
		text = fmt.Sprintf("%+v", x)
	}
	if red := h.r.String(text); red != text {
		return slog.String(key, red)
	}
	return slog.Any(key, x)
}
