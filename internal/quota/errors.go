package quota

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/logx"
)

// Kind classifies a failed quota read.
type Kind string

// Kinds of read errors.
const (
	// KindNeedsLogin: the account is signed out (or its sign-in was refused). It is not read again until it
	// signs in (Refresher.Refresh).
	KindNeedsLogin Kind = "needs_login"
	// KindTokenExpired: the stored sign-in token expired; the CLI renews it on its next run. The account stays
	// usable with its last numbers.
	KindTokenExpired Kind = "token_expired"
	// KindFormat: the answer was not in a format this version understands (the CLI changed its output).
	KindFormat Kind = "format"
	// KindUnavailable: the CLI is missing, timed out, or its service could not be reached.
	KindUnavailable Kind = "unavailable"
	// KindFailed: any other failure.
	KindFailed Kind = "failed"
)

// Errors of the readers; match them with errors.Is.
var (
	// ErrNeedsLogin: the account must sign in again.
	ErrNeedsLogin = errors.New("sign-in required")
	// ErrTokenExpired: the stored token expired; the next run of the CLI renews it.
	ErrTokenExpired = errors.New("sign-in token expired")
	// ErrFormat: the answer had an unexpected shape.
	ErrFormat = errors.New("unrecognized quota answer")
	// ErrUnavailable: the quota source could not be reached in time.
	ErrUnavailable = errors.New("quota source unavailable")
	// ErrPassive: the account's quota cannot be read (API-key accounts).
	ErrPassive = errors.New("quota is passive for this account")
	// ErrNoReader: the backend has no quota reader.
	ErrNoReader = errors.New("no quota reader for this backend")
)

// maxErrorMessage caps the stored message of a read error.
const maxErrorMessage = 300

// KindOf returns the kind of a read error.
func KindOf(err error) Kind {
	switch {
	case errors.Is(err, ErrNeedsLogin):
		return KindNeedsLogin
	case errors.Is(err, ErrTokenExpired):
		return KindTokenExpired
	case errors.Is(err, ErrFormat):
		return KindFormat
	case errors.Is(err, ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return KindUnavailable
	}
	return KindFailed
}

// newError records err as the read error of a snapshot: classified, with secrets and email addresses masked and
// the message shortened.
func newError(err error, at time.Time) *Error {
	msg := logx.NewRedactor().String(err.Error())
	msg = strings.Join(strings.Fields(msg), " ")
	if utf8.RuneCountInString(msg) > maxErrorMessage {
		r := []rune(msg)
		msg = string(r[:maxErrorMessage-1]) + "…"
	}
	return &Error{Kind: KindOf(err), Message: msg, At: at}
}
