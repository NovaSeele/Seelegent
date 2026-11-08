package ticket

import (
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// serveGitOverHTTP makes the fake GitHub serve e.origin as https-like smart HTTP at /octo/demo.git (git
// http-backend), answering 401 to a request without the stored token as GitHub would to a machine with no git
// credentials, and points origin's push URL there.
func serveGitOverHTTP(t *testing.T, e *env) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	run(t, e.origin, "config", "http.receivepack", "true")
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Root: "/octo/demo.git",
		Env:        []string{"GIT_PROJECT_ROOT=" + e.origin, "GIT_HTTP_EXPORT_ALL=1"},
		InheritEnv: []string{"PATH", "SYSTEMROOT", "HOME", "USERPROFILE", "TEMP", "TMP"}}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+testToken))
	e.gh.mu.Lock()
	e.gh.git = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	})
	e.gh.mu.Unlock()
	run(t, e.repo, "config", "remote.origin.pushurl", e.gh.srv.URL+"/octo/demo.git")
}

// TestTokenTierPushesWithStoredToken is old bug D18: a token-tier repository pushed with a plain `git push`, which
// fails on a machine without git credentials for GitHub. The push now carries the stored token for that one
// command, and nowhere else: not in the repository's git config, not in the ticket.
func TestTokenTierPushesWithStoredToken(t *testing.T) {
	e := setupFlow(t)
	serveGitOverHTTP(t, e)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Token push")
	write(t, tk.Worktree+"/src/cache.go", "package src\n\nvar ttl = 60\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)

	tk, err := e.s.PR(ctx, tk.ID, api.PRRequest{})
	if err != nil {
		t.Fatalf("pr: %v (ticket error %q)", err, tk.Error)
	}
	sha := e.gh.originSha(tk.Branch)
	if sha == "" || tk.PR == nil || tk.PR.HeadSha != sha {
		t.Fatalf("origin has %q, pr %+v", sha, tk.PR)
	}
	cfg := run(t, e.repo, "config", "--list", "--show-origin")
	if strings.Contains(cfg, testToken) || strings.Contains(strings.ToLower(cfg), "extraheader") {
		t.Fatalf("the token reached the git config:\n%s", cfg)
	}
	data, err := os.ReadFile(e.repo + "/.git/config")
	if err != nil || strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte("x-access-token:"+
		testToken))) {
		t.Fatalf("the repository config holds the token (%v)", err)
	}
}

// TestTokenTierBadTokenIsGitAuth: a token GitHub refuses fails the push with the cause git_auth, and the error the
// lead reads does not hold the token.
func TestTokenTierBadTokenIsGitAuth(t *testing.T) {
	e := setupFlow(t)
	serveGitOverHTTP(t, e)
	ctx := t.Context()
	tk, task := readyTicket(t, e, "Bad token")
	write(t, tk.Worktree+"/src/cache.go", "package src\n\nvar ttl = 61\n")
	e.tasks.accept(task, "ok")
	waitTicket(t, e.s, tk.ID, api.TicketReady)
	const wrong = "ghp_WrongTokenWrongToken0123456789abcd"
	if _, err := e.s.SetToken(wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.PR(ctx, tk.ID, api.PRRequest{}); err == nil {
		t.Fatal("pr with a refused token succeeded")
	}
	got, err := e.s.Ticket(ctx, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ErrorCause != api.CauseGitAuth {
		t.Fatalf("cause = %q, want git_auth (error %q)", got.ErrorCause, got.Error)
	}
	if strings.Contains(got.Error, wrong) {
		t.Fatalf("error holds the token: %q", got.Error)
	}
	if e.gh.originSha(tk.Branch) != "" {
		t.Fatal("the branch was pushed")
	}
}
