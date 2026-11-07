package httpapi_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestRequestChecks covers contract §1: the Host check, X-Seelegent and Origin, the body limit, unknown fields,
// enums, absolute paths and the error shape (old B9, B13, B15, B19, B21).
func TestRequestChecks(t *testing.T) {
	e := newEnv(t)
	port := strconv.Itoa(e.port)

	// Host check on API, SSE and static paths alike.
	for _, p := range []string{"/api/v1/health", "/api/v1/events", "/", "/assets/app-1a2b.js"} {
		r := e.req(http.MethodGet, p, nil, map[string]string{"Host": "evil.example:" + port})
		e.wantError(r, http.StatusMisdirectedRequest, api.CodeMisdirected, "")
	}
	e.wantError(e.req(http.MethodGet, "/api/v1/health", nil, map[string]string{"Host": "127.0.0.1:1"}),
		http.StatusMisdirectedRequest, api.CodeMisdirected, "")
	for _, h := range []string{"127.0.0.1:" + port, "localhost:" + port, "LOCALHOST:" + port} {
		e.must(e.req(http.MethodGet, "/api/v1/health", nil, map[string]string{"Host": h}), http.StatusOK)
	}
	r := e.req(http.MethodGet, "/api/v1/health", nil, map[string]string{"Host": "evil.example", "Accept-Language": "vi"})
	if got := r.err(t); !strings.Contains(got.Message, "daemon chỉ trả lời") {
		t.Errorf("Vietnamese message = %q", got.Message)
	}

	// X-Seelegent and Origin on mutating requests, checked before routing.
	body := map[string]any{"prompt": "x", "cwd": e.cwd}
	e.wantError(e.req(http.MethodPost, "/api/v1/tasks", body, map[string]string{"X-Seelegent": ""}),
		http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/nothing-here", body, map[string]string{"X-Seelegent": ""}),
		http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/tasks", body, map[string]string{"Origin": "http://evil.example"}),
		http.StatusForbidden, api.CodeForbidden, "")
	e.wantError(e.req(http.MethodPost, "/api/v1/tasks", body,
		map[string]string{"Origin": "http://127.0.0.1:" + strconv.Itoa(e.port+1)}),
		http.StatusForbidden, api.CodeForbidden, "")
	// The own origin passes.
	r = e.req(http.MethodPatch, "/api/v1/settings", map[string]any{}, map[string]string{"Origin": "http://localhost:" + port})
	e.must(r, http.StatusOK)
	if r.header.Get("Cache-Control") != "no-store" {
		t.Errorf("JSON without no-store: %v", r.header)
	}

	// Body limit, unknown fields, wrong types, trailing data.
	big := `{"prompt":"` + strings.Repeat("a", 1<<20) + `","cwd":"x"}`
	e.wantError(e.post("/api/v1/tasks", big), http.StatusRequestEntityTooLarge, api.CodeTooLarge, "")
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "promt": 1}),
		http.StatusBadRequest, api.CodeInvalidArgument, "promt")
	// Old B21: internal links are not part of the request.
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "meetingId": "m1"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "meetingId")
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": 5, "cwd": e.cwd}),
		http.StatusBadRequest, api.CodeInvalidArgument, "prompt")
	e.wantError(e.post("/api/v1/tasks", `{"prompt":"x"} {}`), http.StatusBadRequest, api.CodeInvalidArgument, "")
	e.wantError(e.post("/api/v1/tasks", `{"prompt":`), http.StatusBadRequest, api.CodeInvalidArgument, "")

	// Old B13: a relative or missing cwd is refused, never resolved by the daemon.
	for _, cwd := range []string{".", "project", e.cwd + "-missing"} {
		e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": cwd}),
			http.StatusBadRequest, api.CodeInvalidArgument, "cwd")
	}
	e.wantError(e.get("/api/v1/memory?cwd=."), http.StatusBadRequest, api.CodeInvalidArgument, "cwd")

	// Enums.
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "mode": "write"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "mode")
	e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "x", "cwd": e.cwd, "tier": "huge"}),
		http.StatusBadRequest, api.CodeInvalidArgument, "tier")
	e.wantError(e.get("/api/v1/tasks?status=sleeping"), http.StatusBadRequest, api.CodeInvalidArgument, "status")
	e.wantError(e.get("/api/v1/usage/history?bucket=week"), http.StatusBadRequest, api.CodeInvalidArgument, "bucket")
	e.wantError(e.get("/api/v1/logs?name=secrets"), http.StatusBadRequest, api.CodeInvalidArgument, "name")
	e.wantError(e.get("/api/v1/tasks?limit=500"), http.StatusBadRequest, api.CodeInvalidArgument, "limit")

	// Old B19: ids must be an array of strings, bounded.
	e.wantError(e.post("/api/v1/wait", map[string]any{"ids": "t1", "timeoutSeconds": 1}),
		http.StatusBadRequest, api.CodeInvalidArgument, "ids")
	many := make([]string, 201)
	for i := range many {
		many[i] = "t" + strconv.Itoa(i)
	}
	e.wantError(e.post("/api/v1/wait", map[string]any{"ids": many, "timeoutSeconds": 1}),
		http.StatusBadRequest, api.CodeInvalidArgument, "ids")
	// Unknown ids fail before waiting.
	e.wantError(e.post("/api/v1/wait", map[string]any{"ids": []string{"t-nope"}, "timeoutSeconds": 30}),
		http.StatusNotFound, api.CodeNotFound, "")

	// Old B9: unknown ids and routes are 404, never 400.
	e.wantError(e.get("/api/v1/tasks/t-nope"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/tasks/t-nope/events"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.post("/api/v1/tasks/t-nope/review", map[string]any{"verdict": "accepted"}),
		http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/nothing-here"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.req(http.MethodDelete, "/api/v1/accounts/a-nope", nil, nil), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.req(http.MethodDelete, "/api/v1/health", nil, nil), http.StatusNotFound, api.CodeNotFound, "")

	// Reserved paths of later milestones.
	for _, p := range []string{"/api/v1/members", "/api/v1/members/track", "/api/v1/hooks", "/api/v1/hooks/h1/test",
		"/api/v1/lanes", "/api/v1/proof/x", "/api/v1/meetings", "/api/v1/github/repos", "/api/v1/automations",
		"/api/v1/tasks/t1/notes", "/api/v1/tasks/t1/consult"} {
		e.wantError(e.get(p), http.StatusNotImplemented, api.CodeNotImplemented, "")
	}
	e.wantError(e.post("/api/v1/hooks/h1/test", map[string]any{}), http.StatusNotImplemented, api.CodeNotImplemented, "")
	e.wantError(e.req(http.MethodPut, "/api/v1/hooks", map[string]any{"hooks": []any{}}, nil),
		http.StatusNotImplemented, api.CodeNotImplemented, "")
}

// TestStatic covers contract §8 and old D-23/B17.
func TestStatic(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/", "/tasks/t-123", "/settings/accounts", "/assets"} {
		r := e.must(e.get(p), http.StatusOK)
		if !strings.Contains(string(r.body), "<title>Seelegent</title>") || r.header.Get("Cache-Control") != "no-cache" ||
			!strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %v %s", p, r.header, r.body)
		}
	}
	r := e.must(e.get("/assets/app-1a2b.js"), http.StatusOK)
	if r.header.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.Contains(r.header.Get("Content-Type"), "javascript") || string(r.body) != "console.log(1)" {
		t.Errorf("asset: %v %s", r.header, r.body)
	}
	r = e.must(e.get("/favicon.svg"), http.StatusOK)
	if r.header.Get("Content-Type") != "image/svg+xml" {
		t.Errorf("svg type %q", r.header.Get("Content-Type"))
	}
	for _, p := range []string{"/missing.js", "/assets/nope.css", "/tasks/x.json", "/..%2f..%2fgo.mod"} {
		if r := e.get(p); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %s", p, r.status, r.body)
		}
	}
	// A path that tries to climb out still serves only the bundle.
	r = e.get("/../../../../index.html")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), "Seelegent") {
		t.Errorf("climb: %d %s", r.status, r.body)
	}
	if r := e.post("/", map[string]any{}); r.status != http.StatusNotFound {
		t.Errorf("POST /: %d", r.status)
	}
	var h api.Health
	e.must(e.get("/api/v1/health"), http.StatusOK).json(t, &h)
	if !h.OK || h.APIVersion != 1 || h.Port != e.port || len(h.Build) != 12 || h.PID == 0 || h.StartedAt == "" {
		t.Errorf("health = %+v", h)
	}
}

// TestAntiFraming: every answer forbids framing and referrers, the dashboard, the API, the stream and refusals
// alike (clickjacking defense).
func TestAntiFraming(t *testing.T) {
	e := newEnv(t)
	answers := map[string]http.Header{
		"dashboard":     e.must(e.get("/"), http.StatusOK).header,
		"history route": e.must(e.get("/tasks/t-1"), http.StatusOK).header,
		"asset":         e.must(e.get("/assets/app-1a2b.js"), http.StatusOK).header,
		"missing asset": e.must(e.get("/assets/nope.js"), http.StatusNotFound).header,
		"api":           e.must(e.get("/api/v1/health"), http.StatusOK).header,
		"api 404":       e.must(e.get("/api/v1/nope"), http.StatusNotFound).header,
		"no header":     e.must(e.req(http.MethodPost, "/api/v1/quota/refresh", nil, map[string]string{"X-Seelegent": ""}), http.StatusForbidden).header,
		"wrong host":    e.must(e.req(http.MethodGet, "/", nil, map[string]string{"Host": "evil.test"}), http.StatusMisdirectedRequest).header,
		"stream":        e.stream("").header,
	}
	for name, h := range answers {
		if h.Get("X-Frame-Options") != "DENY" || h.Get("Content-Security-Policy") != "frame-ancestors 'none'" ||
			h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers %v", name, h)
		}
	}
}
