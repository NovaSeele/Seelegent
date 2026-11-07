package ticket

import (
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/task"
)

func TestNameReason(t *testing.T) {
	refused := []string{".env", "app/.env.local", "nul", "sub/COM1.txt", "id_rsa", "keys/server.PEM", "a.pfx",
		".git-credentials", "_netrc", ".pypirc", "x/y.key"}
	for _, p := range refused {
		if nameReason(p) == "" {
			t.Errorf("%s is allowed", p)
		}
	}
	allowed := []string{".env.example", "env.go", "src/null.go", "docs/key.md", "id_rsa.pub", "console.log",
		"README.md", ".envrc.md"}
	for _, p := range allowed {
		if why := nameReason(p); why != "" {
			t.Errorf("%s is refused: %s", p, why)
		}
	}
}

func TestScanBytes(t *testing.T) {
	secrets := []string{
		"x = github_pat_11ABCDEFG0123456789_abcdefghijklmnop",
		"ghp_" + strings.Repeat("a1", 18),
		"aws AKIAABCDEFGHIJKLMNOP end",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc",
		"-----BEGIN PRIVATE KEY-----",
		"xoxb-1234567890-abcdef",
		"AIza" + strings.Repeat("B", 35),
		"sk-ant-" + strings.Repeat("c", 30),
	}
	for _, s := range secrets {
		if scanBytes([]byte(s), nil) == "" {
			t.Errorf("not found: %q", s)
		}
	}
	for _, s := range []string{"ghp_short", "AKIA lowercase", "-----BEGIN PUBLIC KEY-----", "plain text"} {
		if why := scanBytes([]byte(s), nil); why != "" {
			t.Errorf("%q: %s", s, why)
		}
	}
	stored := func(b []byte) bool { return strings.Contains(string(b), "custom-secret-value") }
	if scanBytes([]byte("v=custom-secret-value"), stored) == "" {
		t.Error("stored token not found")
	}
}

func TestScrubLocal(t *testing.T) {
	text := "Changed C:\\Work\\app\\src\\a.go and C:/Work/app/src/b.go, see [log](file:///C:/Users/me/x/run.log) " +
		"and C:\\Users\\me\\notes.txt and file:///tmp/out%20put.txt"
	got := scrubLocal(text, []string{`C:\Work\app`}, `C:\Users\me`)
	for _, bad := range []string{"C:\\Work", "C:/Work", "file:", "Users\\me"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q left in %q", bad, got)
		}
	}
	for _, want := range []string{"src\\a.go", "src/b.go", "log", "~\\notes.txt", "out put.txt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q missing from %q", want, got)
		}
	}
}

func TestSlugifyAndFences(t *testing.T) {
	cases := map[string]string{
		"Cache never expires!":      "cache-never-expires",
		"  Ünïcödé — títle  ":       "unicode-title",
		"日本語":                       "",
		strings.Repeat("abc ", 20):  "abc-abc-abc-abc-abc-abc-abc-abc-abc-abc",
		"fix: `rm -rf /` in script": "fix-rm-rf-in-script",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
	block := issueBlock(api.Issue{Key: "gh#3", Number: 3, Title: "x", Body: "a\n~~~~\nb\n```\nc"}, 1000)
	if strings.Count(block, "~~~~") != 2 {
		t.Fatalf("the body can close the fence:\n%s", block)
	}
}

func TestDeriveStatus(t *testing.T) {
	done := func(v string) api.TaskSummary {
		s := api.TaskSummary{Status: task.StatusDone}
		if v != "" {
			s.Review = &api.Review{Verdict: v}
		}
		return s
	}
	cases := []struct {
		tasks []api.TaskSummary
		want  string
	}{
		{nil, api.TicketWorking},
		{[]api.TaskSummary{{Status: api.StatusRunning}}, api.TicketWorking},
		{[]api.TaskSummary{done("")}, api.TicketReview},
		{[]api.TaskSummary{done(api.VerdictAccepted)}, api.TicketReady},
		{[]api.TaskSummary{done(api.VerdictAccepted), done("")}, api.TicketReview},
		{[]api.TaskSummary{done(api.VerdictAccepted), {Status: api.StatusRunning}}, api.TicketWorking},
	}
	for i, c := range cases {
		if got := deriveStatus(c.tasks); got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	for in, want := range map[string]string{"3": "gh#3", "#3": "gh#3", "gh#03": "gh#3", "local#2": "local#2"} {
		if got, err := NormalizeKey(in); err != nil || got != want {
			t.Errorf("NormalizeKey(%q) = %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "gh#0", "x#1", "-1", "gh#a"} {
		if _, err := NormalizeKey(bad); err == nil {
			t.Errorf("NormalizeKey(%q) accepted", bad)
		}
	}
}
