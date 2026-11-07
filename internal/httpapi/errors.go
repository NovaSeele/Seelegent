package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/automation"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/contest"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/hook"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/member"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/notes"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/role"
	"github.com/NovaSeele/Seelegent/internal/signin"
	"github.com/NovaSeele/Seelegent/internal/task"
	"github.com/NovaSeele/Seelegent/internal/ticket"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// apiError is an error with its API code. Message is shown to the caller; it never holds a secret.
type apiError struct {
	code    api.ErrorCode
	message string
	details map[string]any
	// msgID, when set, names a translated middleware message (see messages).
	msgID string
}

func (e *apiError) Error() string { return string(e.code) + ": " + e.message }

func newError(code api.ErrorCode, format string, args ...any) *apiError {
	return &apiError{code: code, message: fmt.Sprintf(format, args...)}
}

func invalid(format string, args ...any) *apiError {
	return newError(api.CodeInvalidArgument, format, args...)
}

// withField names the request field an invalid_argument is about.
func (e *apiError) withField(field string) *apiError {
	return e.with("field", field)
}

func (e *apiError) with(k string, v any) *apiError {
	if e.details == nil {
		e.details = map[string]any{}
	}
	e.details[k] = v
	return e
}

// translated is a middleware error whose message has translations (Accept-Language).
func translated(code api.ErrorCode, msgID string) *apiError {
	return &apiError{code: code, msgID: msgID, message: messages[msgID]["en"]}
}

// messages are the translated middleware messages. Domain errors stay in English (they come from the packages).
var messages = map[string]map[string]string{
	"misdirected": {
		"en": "this daemon answers only on 127.0.0.1, localhost or [::1] with its own port",
		"vi": "daemon chỉ trả lời trên 127.0.0.1, localhost hoặc [::1] với đúng cổng của nó",
	},
	"no_header": {
		"en": "a request that changes something needs the header X-Seelegent: 1",
		"vi": "yêu cầu thay đổi dữ liệu cần header X-Seelegent: 1",
	},
	"origin": {
		"en": "requests from another origin are refused",
		"vi": "yêu cầu từ nguồn (origin) khác bị từ chối",
	},
	"worker": {
		"en": "workers may not call this endpoint",
		"vi": "worker không được gọi endpoint này",
	},
	"too_large": {
		"en": "the request body is larger than 1 MiB",
		"vi": "nội dung yêu cầu lớn hơn 1 MiB",
	},
	"not_found": {
		"en": "no such endpoint",
		"vi": "không có endpoint này",
	},
	"internal": {
		"en": "internal error; the details are in the daemon log",
		"vi": "lỗi nội bộ; chi tiết nằm trong log của daemon",
	},
	"not_implemented": {
		"en": "this endpoint arrives with a later version",
		"vi": "endpoint này sẽ có ở phiên bản sau",
	},
	"stopping": {
		"en": "the daemon is stopping; the tasks keep running",
		"vi": "daemon đang dừng; các task vẫn tiếp tục chạy",
	},
	"shutting_down": {
		"en": "the daemon is shutting down",
		"vi": "daemon đang tắt",
	},
}

// language picks "vi" or "en" from Accept-Language.
func language(r *http.Request) string {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "vi"):
			return "vi"
		case strings.HasPrefix(tag, "en"):
			return "en"
		}
	}
	return "en"
}

// toAPIError maps a domain error to its API error. Anything unknown is 500 internal (old B9: never 400).
func toAPIError(err error) *apiError {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae
	}
	var ve *config.ValidationError
	if errors.As(err, &ve) {
		fields := make([]api.FieldError, 0, len(ve.Fields))
		for _, f := range ve.Fields {
			fields = append(fields, api.FieldError{Field: f.Field, Message: f.Message})
		}
		return &apiError{code: api.CodeInvalidArgument, message: "some settings are not valid",
			details: map[string]any{"fields": fields}}
	}
	var ge *gitx.Error
	if errors.As(err, &ge) {
		e := newError(api.CodeUpstream, "%s", ge.Error()).with("source", "git")
		if ge.IndexLock {
			e = e.with("indexLock", true)
		}
		return e
	}
	var tge *ticket.GitError
	if errors.As(err, &tge) {
		return newError(api.CodeUpstream, "%s", tge.Error()).with("source", "git")
	}
	var ghe *github.Error
	if errors.As(err, &ghe) {
		return &apiError{code: api.CodeUpstream, message: ghe.Error(), details: ghe.APIDetails()}
	}
	msg := err.Error()
	// A removed contest variant whose changes cannot be shown: not_found when nothing was stored, bad_state when
	// the stored copy is damaged; details.reason tells the dashboard which.
	var de *contest.DataError
	if errors.As(err, &de) {
		code := api.CodeBadState
		if errors.Is(err, contest.ErrNotFound) {
			code = api.CodeNotFound
		}
		return &apiError{code: code, message: msg, details: de.APIDetails()}
	}
	// A domain error may carry details for the caller (a lane conflict's tasks, the run already in progress).
	var withDetails interface{ APIDetails() map[string]any }
	if errors.As(err, &withDetails) && (errors.Is(err, task.ErrConflict) || errors.Is(err, proof.ErrConflict) ||
		errors.Is(err, member.ErrDisabled) || errors.Is(err, contest.ErrConflict) ||
		errors.Is(err, ticket.ErrConflict)) {
		return &apiError{code: api.CodeConflict, message: msg, details: withDetails.APIDetails()}
	}
	// An automation run now refused because the user has not confirmed it as it is (details.reason not_confirmed).
	if errors.As(err, &withDetails) && errors.Is(err, automation.ErrBadState) {
		return &apiError{code: api.CodeBadState, message: msg, details: withDetails.APIDetails()}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return newError(api.CodeUnavailable, "the request timed out")
	case errors.Is(err, context.Canceled):
		// The client went away (writeError writes nothing then) or the daemon is stopping: never an internal error.
		return newError(api.CodeUnavailable, "the request was canceled")
	case errors.Is(err, task.ErrNotFound), errors.Is(err, account.ErrNotFound), errors.Is(err, role.ErrNotFound),
		errors.Is(err, signin.ErrNotFound), errors.Is(err, proof.ErrNotFound), errors.Is(err, member.ErrNotFound),
		errors.Is(err, notes.ErrNotFound), errors.Is(err, meeting.ErrNotFound), errors.Is(err, contest.ErrNotFound),
		errors.Is(err, hook.ErrNotFound), errors.Is(err, ticket.ErrNotFound), errors.Is(err, automation.ErrNotFound):
		return newError(api.CodeNotFound, "%s", msg)
	case errors.Is(err, automation.ErrForbidden):
		return newError(api.CodeForbidden, "%s", msg)
	case errors.Is(err, task.ErrBadState), errors.Is(err, signin.ErrBadState), errors.Is(err, notes.ErrBadState),
		errors.Is(err, meeting.ErrBadState), errors.Is(err, contest.ErrBadState), errors.Is(err, ticket.ErrBadState),
		errors.Is(err, automation.ErrBadState), errors.Is(err, github.ErrManual):
		return newError(api.CodeBadState, "%s", msg)
	case errors.Is(err, account.ErrConflict), errors.Is(err, task.ErrConflict), errors.Is(err, proof.ErrConflict),
		errors.Is(err, member.ErrConflict), errors.Is(err, member.ErrDisabled), errors.Is(err, contest.ErrConflict),
		errors.Is(err, ticket.ErrConflict):
		return newError(api.CodeConflict, "%s", msg)
	case errors.Is(err, task.ErrUnavailable), errors.Is(err, gitx.ErrNoGit):
		return newError(api.CodeUnavailable, "%s", msg)
	case errors.Is(err, memory.ErrTooLarge):
		return newError(api.CodeTooLarge, "%s", msg)
	case errors.Is(err, task.ErrInvalid), errors.Is(err, account.ErrInvalid), errors.Is(err, account.ErrNoKey),
		errors.Is(err, account.ErrProviderUnavailable), errors.Is(err, role.ErrInvalid),
		errors.Is(err, memory.ErrInvalid), errors.Is(err, usage.ErrInvalid), errors.Is(err, gitx.ErrInvalid),
		errors.Is(err, signin.ErrInvalid), errors.Is(err, config.ErrParse), errors.Is(err, gitx.ErrNotRepo),
		errors.Is(err, proof.ErrInvalid), errors.Is(err, member.ErrInvalid), errors.Is(err, notes.ErrInvalid),
		errors.Is(err, meeting.ErrInvalid), errors.Is(err, contest.ErrInvalid), errors.Is(err, hook.ErrInvalid),
		errors.Is(err, ticket.ErrInvalid), errors.Is(err, automation.ErrInvalid), errors.Is(err, github.ErrInvalid):
		return newError(api.CodeInvalidArgument, "%s", msg)
	}
	return &apiError{code: api.CodeInternal, msgID: "internal", message: messages["internal"]["en"]}
}

// writeError sends err as the API's error body. Internal errors are logged with the request; their text never
// reaches the client.
//
// When the client has gone away (its request context is done) nothing is written: there is no one to read it, and
// the cause is logged at Debug only.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		s.log.Debug("http: client went away", "method", r.Method, "path", r.URL.Path, "err", err)
		return
	}
	ae := toAPIError(err)
	if ae.code == api.CodeInternal {
		s.log.Error("http: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	} else {
		s.log.Debug("http: request failed", "method", r.Method, "path", r.URL.Path, "code", ae.code, "err", err)
	}
	msg := ae.message
	if ae.msgID != "" {
		if m, ok := messages[ae.msgID][language(r)]; ok {
			msg = m
		}
	}
	body := api.ErrorBody{Error: api.ErrorInfo{Code: ae.code, Message: msg, Details: ae.details}}
	writeJSON(w, ae.code.HTTPStatus(), body)
}

// writeJSON sends v as JSON with status. Every JSON answer has Cache-Control: no-store.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil { // a bug in a DTO; nothing to log it to here, the status says enough
		status = http.StatusInternalServerError
		data = []byte(`{"error":{"code":"internal","message":"internal error"}}`)
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
