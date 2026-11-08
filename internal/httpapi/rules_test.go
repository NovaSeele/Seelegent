package httpapi_test

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
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
	// A route that exists with another method is 405 (N4, TestWrongMethod).
	e.wantError(e.req(http.MethodDelete, "/api/v1/health", nil, nil), http.StatusMethodNotAllowed,
		api.CodeInvalidArgument, "")

	// Milestone 15 built the last reserved paths: an unknown automation is a plain 404.
	e.wantError(e.get("/api/v1/automations/a1"), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/github/nothing-here"), http.StatusNotFound, api.CodeNotFound, "")
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
	if h.OS != runtime.GOOS {
		t.Errorf("health os = %q, want %q", h.OS, runtime.GOOS)
	}
}

// TestAntiFraming: every answer forbids framing and referrers, the dashboard, the API, the stream and refusals
// alike (clickjacking defense). Every API answer, refusals before the routes included (the Host check, the
// X-Seelegent check, the worker guard), also carries Cross-Origin-Resource-Policy: same-origin.
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
		"api wrong host": e.must(e.req(http.MethodGet, "/api/v1/health", nil, map[string]string{"Host": "evil.test"}),
			http.StatusMisdirectedRequest).header,
		"stream": e.stream("").header,
	}
	// The worker guard's refusal, on a new connection (the caller is cached per connection).
	e.client.Transport = &http.Transport{DisableKeepAlives: true}
	e.who.set(&httpapi.Caller{Worker: true})
	answers["worker guard"] = e.must(e.get("/api/v1/tasks"), http.StatusForbidden).header
	e.who.set(nil)
	for name, h := range answers {
		if h.Get("X-Frame-Options") != "DENY" || h.Get("Content-Security-Policy") != "frame-ancestors 'none'" ||
			h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers %v", name, h)
		}
		// Only API answers are kept from other origins' pages; the dashboard's page and assets are not.
		want := ""
		switch name {
		case "api", "api 404", "no header", "api wrong host", "worker guard", "stream":
			want = "same-origin"
		}
		if got := h.Get("Cross-Origin-Resource-Policy"); got != want {
			t.Errorf("%s: Cross-Origin-Resource-Policy %q, want %q", name, got, want)
		}
	}
}

// TestWrongMethod: a path that exists, asked with a method it does not take, is 405 with an Allow header that lists
// the ones it takes; an unknown path stays 404 and the dashboard routes still get the page (final verification N4).
func TestWrongMethod(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		method, path string
		allow        []string
	}{
		{http.MethodDelete, "/api/v1/health", []string{"GET", "HEAD"}},
		{http.MethodPost, "/api/v1/health", []string{"GET", "HEAD"}},
		{http.MethodGet, "/api/v1/wait", []string{"POST"}},
		{http.MethodPut, "/api/v1/tasks", []string{"GET", "HEAD", "POST"}},
		{http.MethodDelete, "/api/v1/settings", []string{"GET", "HEAD", "PATCH"}},
	} {
		r := e.req(tc.method, tc.path, nil, nil)
		got := e.wantError(r, http.StatusMethodNotAllowed, api.CodeInvalidArgument, "")
		if allow := r.header.Get("Allow"); allow != strings.Join(tc.allow, ", ") {
			t.Errorf("%s %s: Allow %q, want %v", tc.method, tc.path, allow, tc.allow)
		}
		if got.Details["reason"] != "method_not_allowed" || got.Message == "" {
			t.Errorf("%s %s: error %+v", tc.method, tc.path, got)
		}
	}
	e.wantError(e.req(http.MethodDelete, "/api/v1/nothing-here", nil, nil), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get("/api/v1/nope"), http.StatusNotFound, api.CodeNotFound, "")
	// Without X-Seelegent a mutating method is refused before routing, as before.
	e.wantError(e.req(http.MethodDelete, "/api/v1/health", nil, map[string]string{"X-Seelegent": ""}),
		http.StatusForbidden, api.CodeForbidden, "")
	for _, p := range []string{"/", "/tasks", "/tasks/t-1", "/settings"} {
		if r := e.must(e.get(p), http.StatusOK); !strings.Contains(r.header.Get("Content-Type"), "text/html") {
			t.Errorf("GET %s: %v", p, r.header)
		}
	}
}
