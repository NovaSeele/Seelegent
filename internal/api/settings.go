package api

import "github.com/NovaSeele/Seelegent/internal/config"

// Settings is GET /settings: config.json as internal/config.Settings describes it (the source of truth).
type Settings = config.Settings

// TierModels maps each tier to a model id (internal/config.TierModels).
type TierModels = config.TierModels

// FieldError names one invalid field of a request (details.fields of a 400).
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ErrorCode is the code of an API error (contract §1.1).
type ErrorCode string

// Error codes and their HTTP status: invalid_argument 400, forbidden 403, not_found 404, conflict 409, bad_state
// 409, too_large 413, misdirected_request 421, upstream 502, unavailable 503, daemon_stopping 503, internal 500.
const (
	CodeInvalidArgument ErrorCode = "invalid_argument"
	CodeForbidden       ErrorCode = "forbidden"
	CodeNotFound        ErrorCode = "not_found"
	CodeConflict        ErrorCode = "conflict"
	CodeBadState        ErrorCode = "bad_state"
	CodeTooLarge        ErrorCode = "too_large"
	CodeMisdirected     ErrorCode = "misdirected_request"
	CodeUpstream        ErrorCode = "upstream"
	CodeUnavailable     ErrorCode = "unavailable"
	// CodeDaemonStopping (503) ends a request the daemon cut short because it is shutting down (POST /wait):
	// unlike a lost connection it tells the caller not to start the daemon again.
	CodeDaemonStopping ErrorCode = "daemon_stopping"
	CodeInternal       ErrorCode = "internal"
	// CodeNotImplemented (501) marks a path a later milestone fills (contract §6, members, hooks).
	CodeNotImplemented ErrorCode = "not_implemented"
)

// HTTPStatus returns the HTTP status of an error code (500 for an unknown one).
func (c ErrorCode) HTTPStatus() int {
	switch c {
	case CodeInvalidArgument:
		return 400
	case CodeForbidden:
		return 403
	case CodeNotFound:
		return 404
	case CodeConflict, CodeBadState:
		return 409
	case CodeTooLarge:
		return 413
	case CodeMisdirected:
		return 421
	case CodeUpstream:
		return 502
	case CodeUnavailable, CodeDaemonStopping:
		return 503
	case CodeNotImplemented:
		return 501
	}
	return 500
}

// ErrorBody is the body of every error response.
type ErrorBody struct {
	Error ErrorInfo `json:"error"`
}

// ErrorInfo describes an error. Details carry machine-readable extras (field, fields, status, caller, source, ...).
type ErrorInfo struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}
