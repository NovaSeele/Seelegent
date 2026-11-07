package github

import (
	"regexp"
	"strings"
)

// Redacted replaces a secret in text.
const Redacted = "[REDACTED]"

var (
	tokenRe    = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}|\bgh[pousr]_[A-Za-z0-9]{20,}`)
	userinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
	authRe     = regexp.MustCompile(`(?i)(authorization:\s*)(bearer|token|basic)\s+\S+`)
)

// minSecret is the shortest secret replaced literally (shorter strings would mangle ordinary text).
const minSecret = 8

// Redact removes the given secrets, anything that looks like a GitHub token, user:password@ in URLs and the value of
// an Authorization header from text.
func Redact(text string, secrets ...string) string {
	if text == "" {
		return text
	}
	for _, s := range secrets {
		if len(s) >= minSecret {
			text = strings.ReplaceAll(text, s, Redacted)
		}
	}
	text = tokenRe.ReplaceAllString(text, Redacted)
	text = userinfoRe.ReplaceAllString(text, "$1")
	return authRe.ReplaceAllString(text, "${1}${2} "+Redacted)
}

// oneLine flattens s to one line of at most limit runes.
func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > limit {
		return string(r[:limit])
	}
	return s
}
