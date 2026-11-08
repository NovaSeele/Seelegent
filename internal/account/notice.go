package account

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Notice codes: what a save changed on its own, or advice that did not block it. The dashboard translates the
// code; Message is the English text for the CLI and the MCP tools.
const (
	// NoticeBaseURLTrimmed: the base URL of a claude provider lost the API path Claude Code appends itself.
	NoticeBaseURLTrimmed = "base_url_trimmed"
	// NoticeOfficialPreset: a custom claude provider pointed at api.anthropic.com and became the official
	// "Anthropic" preset (the key goes as an API key, not as a bearer token).
	NoticeOfficialPreset = "official_preset"
	// NoticeKeyNameRemoved: a leading "NAME=" (a line copied from a .env file) was removed from the key.
	NoticeKeyNameRemoved = "key_name_removed"
	// NoticeKeyQuotesRemoved: quotes around the key were removed.
	NoticeKeyQuotesRemoved = "key_quotes_removed"
	// NoticeKeyAssignment: the key was kept whole but looks like "name=value" (a lower-case variable name, which
	// is not removed on its own). Only advice: the service will likely refuse it.
	NoticeKeyAssignment = "key_assignment"
	// NoticeKeyPrefix: the key does not start the way the service's keys do. Only advice: proxies use their own
	// formats.
	NoticeKeyPrefix = "key_prefix"
	// NoticeProviderReused: the new account joined an existing provider with the same preset, backend, base URL
	// and models instead of creating a second one.
	NoticeProviderReused = "provider_reused"
)

// Notice is something a save changed on its own, or advice that did not block it.
type Notice struct {
	Code string
	// Field names the request field it is about ("apiKey", "baseUrl", ...), empty when none.
	Field   string
	Message string
}

// Reasons of a rejected API key (FieldError.Reason).
const (
	KeyReasonEmpty      = "empty"
	KeyReasonTooLong    = "too_long"
	KeyReasonURL        = "url"
	KeyReasonWhitespace = "whitespace"
	KeyReasonControl    = "control"
)

// FieldError is an invalid input that names its request field (and, for keys, a reason code). It matches what the
// wrapped error matches (ErrInvalid, and ErrBadKey for keys); its text is the wrapped error's.
type FieldError struct {
	// Field is the request field ("baseUrl", "apiKey", "newProvider.baseUrl", ...).
	Field string
	// Reason is a code for the dashboard (the key reasons above), empty when none.
	Reason string
	err    error
}

func (e *FieldError) Error() string { return e.err.Error() }
func (e *FieldError) Unwrap() error { return e.err }

// APIDetails returns the details of the API error: the field, and the reason when there is one.
func (e *FieldError) APIDetails() map[string]any {
	d := map[string]any{"field": e.Field}
	if e.Reason != "" {
		d["reason"] = e.Reason
	}
	return d
}

// withField names the field of an error that does not name one yet (nil stays nil).
func withField(field string, err error) error {
	if err == nil {
		return nil
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		return err
	}
	return &FieldError{Field: field, err: err}
}

// PrefixField puts prefix and a dot before the field an error names ("newProvider" + "baseUrl"), or names prefix
// itself when the error has no field. Errors that are not invalid inputs are returned unchanged.
func PrefixField(prefix string, err error) error {
	if err == nil || !errors.Is(err, ErrInvalid) {
		return err
	}
	var fe *FieldError
	if errors.As(err, &fe) {
		return &FieldError{Field: prefix + "." + fe.Field, Reason: fe.Reason, err: fe.err}
	}
	return &FieldError{Field: prefix, err: err}
}

// renameField returns err with its field set to field (used by PUT /accounts/{id}/key, whose field is "key").
func renameField(field string, err error) error {
	var fe *FieldError
	if errors.As(err, &fe) {
		return &FieldError{Field: field, Reason: fe.Reason, err: fe.err}
	}
	return withField(field, err)
}

// KeyFieldAs returns a key error of NormalizeKey or SetKey with its field renamed (nil stays nil).
func KeyFieldAs(field string, err error) error {
	if err == nil {
		return nil
	}
	return renameField(field, err)
}

// badKey returns an error about a malformed key (field apiKey). Its text never holds the key.
func badKey(reason, format string, args ...any) error {
	return &FieldError{Field: "apiKey", Reason: reason,
		err: fmt.Errorf("%w: %w: %s", ErrInvalid, ErrBadKey, fmt.Sprintf(format, args...))}
}

// envAssignment matches a leading "NAME=" or "export NAME=" (a line copied from a .env file or a shell). Only
// environment-style names count (upper case, digits, "_"): a key may itself hold "=" (base64 padding) or look like
// "abc_DEF=xyz".
var envAssignment = regexp.MustCompile(`^(?:export\s+)?([A-Z_][A-Z0-9_]*)\s*=\s*`)

// Limits of the NAME= prefix removal: what follows must look like a whole key, and a notice names the variable only
// when it is short (a long "name" may be part of the key, which a notice never quotes).
// looksAssigned matches a key kept whole that still reads as "name=value": identifier characters, "=", then at
// least minKeyAfterName characters that do not start with "=".
var looksAssigned = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[^=]`)

const (
	minKeyAfterName = 8
	maxNoticeName   = 40
)

// NormalizeKey cleans a pasted API key and checks it: surrounding whitespace, a leading "NAME=" and surrounding
// quotes are removed (each removal is a notice), then a key that holds "://", whitespace or control characters is
// refused (an error matching ErrBadKey and ErrInvalid, field apiKey). A key kept whole that still looks like
// "name=value" gets a key_assignment notice. Neither the notices nor the errors quote the key.
func NormalizeKey(raw string) (string, []Notice, error) {
	key := strings.TrimSpace(raw)
	var notices []Notice
	if m := envAssignment.FindStringSubmatch(key); m != nil {
		if rest := strings.TrimSpace(key[len(m[0]):]); len(rest) >= minKeyAfterName && !strings.HasPrefix(rest, "=") {
			key = rest
			msg := "removed a NAME= prefix from the start of the key"
			if len(m[1]) <= maxNoticeName {
				msg = fmt.Sprintf("removed %s= from the start of the key", m[1])
			}
			notices = append(notices, Notice{Code: NoticeKeyNameRemoved, Field: "apiKey", Message: msg})
		}
	}
	if len(key) >= 2 && (key[0] == '"' || key[0] == '\'' || key[0] == '`') && key[len(key)-1] == key[0] {
		key = strings.TrimSpace(key[1 : len(key)-1])
		notices = append(notices, Notice{Code: NoticeKeyQuotesRemoved, Field: "apiKey",
			Message: "removed the quotes around the key"})
	}
	if err := validateKey(key); err != nil {
		return "", nil, err
	}
	if i := strings.IndexByte(key, '='); looksAssigned.MatchString(key) && len(key)-i-1 >= minKeyAfterName {
		notices = append(notices, Notice{Code: NoticeKeyAssignment, Field: "apiKey",
			Message: "this looks like NAME=value; paste only the part after ="})
	}
	return key, notices, nil
}

// validateKey checks an API key (already cleaned). The errors match ErrBadKey (and ErrInvalid) and never quote the
// key: a pasted URL or two lines of text may well hold a secret.
func validateKey(key string) error {
	switch {
	case key == "":
		return badKey(KeyReasonEmpty, "API key is empty")
	case len(key) > maxKeyLen:
		return badKey(KeyReasonTooLong, "API key is longer than %d characters", maxKeyLen)
	case strings.Contains(key, "://"):
		return badKey(KeyReasonURL, "this looks like a URL, not an API key; the service's address goes in its base URL")
	case strings.IndexFunc(key, unicode.IsSpace) >= 0:
		return badKey(KeyReasonWhitespace, "API key must not contain spaces or line breaks; paste only the key")
	case strings.IndexFunc(key, unicode.IsControl) >= 0:
		return badKey(KeyReasonControl, "API key must not contain control characters")
	}
	return nil
}

// keyPrefixes are key prefixes that name one service for sure. OpenAI's "sk-" is left out: many services use it.
var keyPrefixes = []struct{ prefix, preset, name string }{
	{"sk-ant-", "anthropic", "Anthropic"},
	{"sk-or-", "openrouter", "OpenRouter"},
	{"gsk_", "groq", "Groq"},
}

// KeyNotices returns advice about a (cleaned) key for the provider: a key whose prefix names another service than
// the provider's preset, or a key of the official Anthropic API that does not start with "sk-ant-". Custom
// presets get no advice (proxies use their own formats). It never quotes the key.
func KeyNotices(p Provider, key string) []Notice {
	if p.Official && p.Backend == "claude" && !strings.HasPrefix(key, "sk-ant-") {
		return []Notice{{Code: NoticeKeyPrefix, Field: "apiKey",
			Message: `Anthropic API keys start with "sk-ant-"; this one does not, so check that the whole key was pasted`}}
	}
	if p.Preset == "" || strings.HasPrefix(p.Preset, "custom-") {
		return nil
	}
	for _, k := range keyPrefixes {
		if strings.HasPrefix(key, k.prefix) && p.Preset != k.preset {
			return []Notice{{Code: NoticeKeyPrefix, Field: "apiKey",
				Message: fmt.Sprintf("this looks like a %s key, but the service is %s", k.name, p.Name)}}
		}
	}
	return nil
}

// RenameNotices returns the notices with the field from renamed to to (PUT /accounts/{id}/key calls the key "key").
func RenameNotices(in []Notice, from, to string) []Notice {
	out := make([]Notice, 0, len(in))
	for _, n := range in {
		if n.Field == from {
			n.Field = to
		}
		out = append(out, n)
	}
	return out
}
