package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]Remote{
		"https://github.com/o/r.git":              {Host: "github.com", Owner: "o", Repo: "r"},
		"https://user:pw@github.com/o/r":          {Host: "github.com", Owner: "o", Repo: "r"},
		"git@github.com:o/r.git":                  {Host: "github.com", SSH: true, Owner: "o", Repo: "r"},
		"ssh://git@ssh.github.com:443/o/r.git":    {Host: "ssh.github.com", SSH: true, Owner: "o", Repo: "r"},
		"http://127.0.0.1:9000/owner/repo.git":    {Host: "127.0.0.1", Port: "9000", Owner: "owner", Repo: "repo"},
		"https://GHE.Example.com:443/a.b/c-d.git": {Host: "ghe.example.com", Owner: "a.b", Repo: "c-d"},
	}
	for in, want := range cases {
		got, ok := ParseRemote(in)
		if !ok || got != want {
			t.Errorf("ParseRemote(%q) = %+v %v, want %+v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "C:/x/y", "/srv/git/r.git", "file:///srv/o/r.git", "https://github.com/o",
		"https://github.com/o/r/extra", "ext::sh -c x", "https://github.com/o/../r"} {
		if r, ok := ParseRemote(bad); ok {
			t.Errorf("ParseRemote(%q) = %+v, want false", bad, r)
		}
	}
}

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:ghp_abc@github.com/o/r.git": "https://github.com/o/r.git",
		"ssh://git:pw@host/o/r.git":               "ssh://git@host/o/r.git",
		"git@github.com:o/r.git":                  "git@github.com:o/r.git",
		"https://github.com/o/r":                  "https://github.com/o/r",
	}
	for in, want := range cases {
		if got := StripCredentials(in); got != want {
			t.Errorf("StripCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

// D4: only github.com and the listed enterprise hosts count, and the API address comes from the list.
func TestHostsLookup(t *testing.T) {
	h := NewHosts([]string{"ghe.corp.example", "http://127.0.0.1:9000", "bad host"})
	remote := func(s string) Remote {
		r, ok := ParseRemote(s)
		if !ok {
			t.Fatalf("parse %q", s)
		}
		return r
	}
	for _, s := range []string{"https://github.com/o/r", "git@github.com:o/r.git", "https://ghe.corp.example/o/r",
		"git@ghe.corp.example:o/r.git", "http://127.0.0.1:9000/o/r.git"} {
		if _, ok := h.Lookup(remote(s)); !ok {
			t.Errorf("%s is not GitHub", s)
		}
	}
	for _, s := range []string{"https://github.evil.com/o/r", "https://mygithub.example/o/r", "https://gitlab.com/o/r",
		"http://127.0.0.1:9001/o/r.git", "https://github.com:8443/o/r", "git@notgithub.com:o/r.git"} {
		if host, ok := h.Lookup(remote(s)); ok {
			t.Errorf("%s counted as GitHub (%+v)", s, host)
		}
	}
	gh, _ := h.Lookup(remote("http://127.0.0.1:9000/o/r.git"))
	if gh.APIBase() != "http://127.0.0.1:9000/api/v3" {
		t.Errorf("api base = %s", gh.APIBase())
	}
	if DotCom.APIBase() != "https://api.github.com" {
		t.Error(DotCom.APIBase())
	}
	if got := strings.Join(h.Names(), ","); got != "github.com,ghe.corp.example,127.0.0.1:9000" {
		t.Errorf("names = %s", got)
	}
}

func TestRedact(t *testing.T) {
	secret := "s3cr3t-token-value"
	in := "x " + secret + " ghp_" + strings.Repeat("a", 30) + " github_pat_" + strings.Repeat("B", 30) +
		" https://u:p@h/x Authorization: Bearer abc.def"
	out := Redact(in, secret)
	for _, leak := range []string{secret, "ghp_a", "github_pat_B", "u:p@", "abc.def"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q left in %q", leak, out)
		}
	}
}

func TestTokenStore(t *testing.T) {
	s := NewTokenStore(t.TempDir())
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("empty store: %v", err)
	}
	for _, bad := range []string{"", "a b", strings.Repeat("x", 600)} {
		if _, err := s.Save(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Save(%q) = %v", bad, err)
		}
	}
	hint, err := s.Save("  ghp_abcdefghijklmnop1234  ")
	if err != nil || hint != "…1234" {
		t.Fatalf("save = %q %v", hint, err)
	}
	tok, err := s.Load()
	if err != nil || tok != "ghp_abcdefghijklmnop1234" {
		t.Fatalf("load = %q %v", tok, err)
	}
	if err := s.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, ErrNoToken) {
		t.Fatalf("after remove: %v", err)
	}
}

// fakeGh answers gh commands from a function and records them.
type fakeGh struct {
	mu    sync.Mutex
	calls [][]string
	envs  [][]string
	fn    func(args []string, input []byte) ([]byte, []byte, error)
}

func (f *fakeGh) Run(_ context.Context, args []string, input []byte, env []string) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, args)
	f.envs = append(f.envs, env)
	f.mu.Unlock()
	if f.fn == nil {
		return nil, nil, ErrNoGh
	}
	return f.fn(args, input)
}

func TestDetector(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	tokens := NewTokenStore(t.TempDir())
	none := &fakeGh{}
	d := NewDetector(none, tokens, clock)
	if a := d.Access(context.Background(), false); a.Tier != api.TierManual {
		t.Fatalf("tier = %s", a.Tier)
	}
	if _, err := tokens.Save("ghp_abcdefghijklmnop9876"); err != nil {
		t.Fatal(err)
	}
	if a := d.Access(context.Background(), false); a.Tier != api.TierManual {
		t.Fatalf("cached access changed: %s", a.Tier)
	}
	if a := d.Access(context.Background(), true); a.Tier != api.TierToken || a.TokenHint != "…9876" {
		t.Fatalf("token access = %+v", a)
	}
	signed := &fakeGh{fn: func(args []string, _ []byte) ([]byte, []byte, error) {
		if args[0] == "--version" {
			return []byte("gh version 2.70.0 (2026-01-01)\n"), nil, nil
		}
		return []byte("octo\n"), nil, nil
	}}
	d = NewDetector(signed, tokens, clock)
	a := d.Access(context.Background(), false)
	if a.Tier != api.TierGh || a.GhUser != "octo" || a.GhVersion != "2.70.0" || a.TokenHint != "…9876" {
		t.Fatalf("gh access = %+v", a)
	}
	for _, env := range signed.envs {
		if !strings.Contains(strings.Join(env, " "), "GH_PROMPT_DISABLED=1") {
			t.Errorf("gh env %v", env)
		}
	}
}

// fakeAPI is a GitHub REST server for the tests.
type fakeAPI struct {
	srv   *httptest.Server
	mu    sync.Mutex
	auths []string
	paths []string
}

func newFakeAPI(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *fakeAPI {
	f := &fakeAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auths = append(f.auths, r.Header.Get("Authorization"))
		f.paths = append(f.paths, r.URL.RequestURI())
		f.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) host() Host {
	return Host{Name: strings.TrimPrefix(f.srv.URL, "http://"), Scheme: "http"}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestRESTClient(t *testing.T) {
	const token = "ghp_testtokentesttoken1234"
	logSrv := newFakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "2026-11-07T10:00:00.0000000Z \x1b[31merror\x1b[0m: boom "+token+"\n")
	})
	var api2 *fakeAPI
	api2 = newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/repos/o/r/issues":
			writeJSON(w, 200, []map[string]any{
				{"number": 1, "title": "Bug", "body": "text", "state": "open", "labels": []map[string]string{{"name": "bug"}},
					"user": map[string]string{"login": "u"}, "html_url": "https://h/o/r/issues/1", "updated_at": "2026-11-01T00:00:00Z"},
				{"number": 2, "title": "PR", "pull_request": map[string]string{"url": "x"}},
			})
		case r.URL.Path == "/api/v3/repos/o/r/pulls" && r.Method == http.MethodPost:
			var in PRInput
			_ = json.NewDecoder(r.Body).Decode(&in)
			if !in.Draft || in.Head != "seelegent/1-bug" {
				writeJSON(w, 422, map[string]string{"message": "bad"})
				return
			}
			writeJSON(w, 201, map[string]any{"number": 7, "html_url": "https://h/o/r/pull/7", "state": "open", "draft": true,
				"head": map[string]string{"ref": "seelegent/1-bug", "sha": "abc1234"}})
		case r.URL.Path == "/api/v3/repos/o/r/pulls/7":
			writeJSON(w, 200, map[string]any{"number": 7, "state": "closed", "merged_at": "2026-11-02T00:00:00Z"})
		case r.URL.Path == "/api/v3/repos/o/r/actions/jobs/55/logs":
			http.Redirect(w, r, logSrv.srv.URL+"/log", http.StatusFound)
		case r.URL.Path == "/api/v3/repos/o/r/issues/1/comments":
			writeJSON(w, 403, map[string]string{"message": "API rate limit exceeded " + token})
		default:
			writeJSON(w, 404, map[string]string{"message": "Not Found"})
		}
	})
	c := NewClient(ClientOptions{Access: Access{Tier: api.TierToken}, Host: api2.host(), Owner: "o", Repo: "r",
		Token: token})
	ctx := context.Background()
	issues, err := c.ListIssues(ctx, "open", 100)
	if err != nil || len(issues) != 1 || issues[0].Number != 1 || issues[0].Labels[0] != "bug" {
		t.Fatalf("issues = %+v %v", issues, err)
	}
	pr, err := c.CreatePR(ctx, PRInput{Title: "t", Body: "b", Head: "seelegent/1-bug", Base: "main", Draft: true})
	if err != nil || pr.Number != 7 || !pr.Draft || pr.HeadSha != "abc1234" {
		t.Fatalf("pr = %+v %v", pr, err)
	}
	st, err := c.PRState(ctx, 7)
	if err != nil || st.State != "merged" {
		t.Fatalf("state = %+v %v", st, err)
	}
	logText, err := c.JobLog(ctx, 55)
	if err != nil || strings.Contains(logText, token) || !strings.Contains(logText, "boom") {
		t.Fatalf("log = %q %v", logText, err)
	}
	if tail := LogTail(logText, 100); tail != "error: boom "+Redacted {
		t.Errorf("tail = %q", tail)
	}
	_, err = c.Comment(ctx, 1, "hi")
	var ge *Error
	if !errors.As(err, &ge) || ge.Reason != ReasonRateLimited || strings.Contains(err.Error(), token) {
		t.Fatalf("comment err = %v", err)
	}
	if _, err := c.GetRepo(ctx, "o", "missing"); err != nil {
		t.Fatalf("missing repo: %v", err)
	}
	for _, a := range api2.auths {
		if a != "Bearer "+token {
			t.Errorf("API call without the token: %q", a)
		}
	}
	for _, a := range logSrv.auths {
		if a != "" {
			t.Errorf("redirected log request carried %q", a)
		}
	}
	// Manual tier: no credentials at all, writes refused.
	m := NewClient(ClientOptions{Access: Access{Tier: api.TierManual}, Host: api2.host(), Owner: "o", Repo: "r",
		Token: token})
	if _, err := m.CreatePR(ctx, PRInput{}); !errors.Is(err, ErrManual) {
		t.Fatalf("manual create: %v", err)
	}
	n := len(api2.auths)
	if _, err := m.ListIssues(ctx, "open", 10); err != nil {
		t.Fatal(err)
	}
	if api2.auths[n] != "" {
		t.Errorf("manual tier sent %q", api2.auths[n])
	}
	u := m.CompareURL("main", "seelegent/1-bug", "#1: Bug", "body")
	if !strings.HasPrefix(u, api2.srv.URL+"/o/r/compare/main...seelegent/1-bug?expand=1&title=") {
		t.Errorf("compare url = %s", u)
	}
}

func TestGhClient(t *testing.T) {
	gh := &fakeGh{fn: func(args []string, input []byte) ([]byte, []byte, error) {
		path := ""
		for i, a := range args {
			if i > 0 && args[i-1] != "-X" && args[i-1] != "-H" && args[i-1] != "--hostname" && args[i-1] != "--input" &&
				!strings.HasPrefix(a, "-") && a != "api" {
				path = a
			}
		}
		switch {
		case strings.HasPrefix(path, "repos/o/r/pulls?head="):
			return []byte(`[{"number":3,"html_url":"https://github.com/o/r/pull/3","state":"open","head":{"ref":"seelegent/x","sha":"def5678"}}]`), nil, nil
		case path == "repos/o/r/issues/4/comments":
			if !strings.Contains(string(input), `"body":"hello"`) {
				return nil, []byte("bad input"), errors.New("exit 1")
			}
			return []byte(`{"html_url":"https://github.com/o/r/issues/4#c"}`), nil, nil
		}
		return nil, []byte("gh: Not Found (HTTP 404)"), errors.New("exit status 1")
	}}
	c := NewClient(ClientOptions{Access: Access{Tier: api.TierGh}, Host: DotCom, Owner: "o", Repo: "r", Gh: gh,
		Token: "ghp_unusedunusedunused0000"})
	ctx := context.Background()
	pr, err := c.FindPR(ctx, "seelegent/x")
	if err != nil || pr == nil || pr.Number != 3 {
		t.Fatalf("find = %+v %v", pr, err)
	}
	if u, err := c.Comment(ctx, 4, "hello"); err != nil || u == "" {
		t.Fatalf("comment = %q %v", u, err)
	}
	_, err = c.PRState(ctx, 9)
	if !NotFound(err) {
		t.Fatalf("404 = %v", err)
	}
	for _, args := range gh.calls {
		if args[0] != "api" {
			t.Errorf("gh %v", args)
		}
	}
}

func TestParseChecks(t *testing.T) {
	runs := []byte(`{"check_runs":[
	 {"id":11,"name":"build","status":"completed","conclusion":"failure","html_url":"https://github.com/o/r/runs/11",
	  "details_url":"https://github.com/o/r/actions/runs/500/job/11","app":{"slug":"github-actions"},
	  "output":{"title":"Build failed","summary":"1 error"}},
	 {"id":12,"name":"lint","status":"in_progress","app":{"slug":"github-actions"}}]}`)
	statuses := []byte(`{"statuses":[{"id":1,"state":"success","context":"ci/other","target_url":"https://ci.example/1"}]}`)
	c := ParseChecks(runs, statuses, "abc1234")
	if c.State != api.ChecksFailure || len(c.Runs) != 3 {
		t.Fatalf("checks = %+v", c)
	}
	b := c.Runs[0]
	if b.RunID != 500 || b.JobID != 11 || b.Summary != "Build failed\n1 error" || !IsFailing(b) {
		t.Errorf("build run = %+v", b)
	}
	if c.Runs[1].JobID != 12 || c.Runs[2].Kind != "status" || c.Runs[2].Conclusion != "success" {
		t.Errorf("runs = %+v", c.Runs)
	}
	if ChecksState(nil) != api.ChecksUnknown || ChecksState(c.Runs[1:]) != api.ChecksPending {
		t.Error("states")
	}
	long := strings.Repeat("line\n", 2000) + "last"
	if tail := LogTail(long, 100); len(tail) > 100 || !strings.HasSuffix(tail, "last") || strings.HasPrefix(tail, "ine") {
		t.Errorf("tail = %q", tail)
	}
}
