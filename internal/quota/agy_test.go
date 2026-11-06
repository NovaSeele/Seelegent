package quota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// agyUsageOK is a recorded `agy -p /usage --output-format json` answer (values changed, shape kept).
const agyUsageOK = `Loading...
{"status":"SUCCESS","command":{"data":{"groups":[` +
	`{"name":"Gemini Models","buckets":[` +
	`{"id":"gemini-weekly","name":"Weekly Limit Remaining","description":"x","window":"weekly","remaining_fraction":0.37,"reset_time":"2026-11-09T19:22:20Z"},` +
	`{"id":"gemini-5h","name":"Five Hour Limit Remaining","window":"5h","remaining_fraction":1,"reset_time":"2026-11-06T15:47:33Z"}]},` +
	`{"name":"Claude and GPT models","buckets":[]}]}}}
`

func TestParseAgyUsage(t *testing.T) {
	tests := []struct {
		name    string
		out     Output
		wantErr error
		groups  int
		windows int
	}{
		{"recorded answer", Output{Stdout: []byte(agyUsageOK)}, nil, 2, 2},
		{"signed out", Output{Stdout: []byte(`{"status":"ERROR","error":"Authentication required"}`)}, ErrNeedsLogin, 0, 0},
		{"signed out in stderr", Output{Stderr: []byte("Error: not logged in"), Code: 1}, ErrNeedsLogin, 0, 0},
		{"sign-in required", Output{Stderr: []byte("Error: authentication required. Please log in again."), Code: 1}, ErrNeedsLogin, 0, 0},
		{"authentication failed", Output{Stdout: []byte(`{"status":"ERROR","error":"Authentication failed"}`)}, ErrNeedsLogin, 0, 0},
		{"no JSON at all", Output{Stdout: []byte("panic: something"), Code: 2}, ErrFormat, 0, 0},
		{"not JSON after brace", Output{Stdout: []byte("{oops")}, ErrFormat, 0, 0},
		{"groups missing", Output{Stdout: []byte(`{"status":"SUCCESS","command":{"data":{}}}`)}, ErrFormat, 0, 0},
		{"groups changed type", Output{Stdout: []byte(`{"status":"SUCCESS","command":{"data":{"groups":{"a":1}}}}`)}, ErrFormat, 0, 0},
		{"remaining changed type", Output{Stdout: []byte(`{"status":"SUCCESS","command":{"data":{"groups":[{"name":"G","buckets":[{"window":"5h","remaining_fraction":"37%"}]}]}}}`)}, ErrFormat, 0, 0},
		{"group without name", Output{Stdout: []byte(`{"status":"SUCCESS","command":{"data":{"groups":[{"buckets":[]}]}}}`)}, ErrFormat, 0, 0},
		{"empty group list", Output{Stdout: []byte(`{"status":"SUCCESS","command":{"data":{"groups":[]}}}`)}, nil, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := parseAgyUsage(tt.out)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !q.Entitled || len(q.Groups) != tt.groups {
				t.Fatalf("quota = %+v", q)
			}
			if tt.groups > 0 && len(q.Groups[0].Buckets) != tt.windows {
				t.Fatalf("buckets = %+v", q.Groups[0].Buckets)
			}
		})
	}
	q, _ := parseAgyUsage(Output{Stdout: []byte(agyUsageOK)})
	b := q.Groups[0].Buckets[0]
	if b.Window != "weekly" || b.Label != "Weekly Limit Remaining" || b.WindowMinutes != 10080 ||
		b.UsedFraction < 0.629 || b.UsedFraction > 0.631 || !b.ResetAt.Equal(time.Date(2026, 11, 9, 19, 22, 20, 0, time.UTC)) {
		t.Fatalf("bucket = %+v", b)
	}
	// A plain failure keeps its message and is neither a format nor a sign-in problem.
	_, err := parseAgyUsage(Output{Stdout: []byte(`{"status":"ERROR","error":{"message":"backend busy"}}`)})
	if err == nil || KindOf(err) != KindFailed || !contains([]string{err.Error()}, "/usage failed: backend busy") {
		t.Fatalf("err = %v", err)
	}
	_, err = parseAgyUsage(Output{Stderr: []byte("line1\nline2\n"), Code: 1})
	if err == nil || err.Error() != "/usage failed: line1 line2" {
		t.Fatalf("err = %v", err)
	}
	_, err = parseAgyUsage(Output{Stdout: []byte(`{"status":"WEIRD"}`)})
	if err == nil || err.Error() != "/usage failed: status WEIRD" {
		t.Fatalf("err = %v", err)
	}
}

func TestAgyReaderFakeRun(t *testing.T) {
	prof := testProfile(t, account.KindIsolated)
	logDir := filepath.Join(prof.Home, ".gemini", "antigravity-cli")
	writeFile(t, filepath.Join(logDir, "cli.log"), "x applyAuthResult: email=old@example.com, y\n")
	writeFile(t, filepath.Join(logDir, "log", "2.log"), "applyAuthResult: email=first@example.com\napplyAuthResult: email=new@example.com\n")
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(logDir, "log", "2.log"), later, later); err != nil {
		t.Fatal(err)
	}
	var gotArgv []string
	var gotDir string
	var gotEnv []string
	run := func(_ context.Context, argv []string, dir string, env []string) (Output, error) {
		gotArgv, gotDir, gotEnv = argv, dir, env
		return Output{Stdout: []byte(agyUsageOK)}, nil
	}
	r := NewAgyReader("/bin/agy", run)
	q, err := r.ReadQuota(t.Context(), prof, []string{"A=1"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotArgv, []string{"/bin/agy", "-p", "/usage", "--output-format", "json"}) || gotDir != prof.Home ||
		!slices.Equal(gotEnv, []string{"A=1"}) {
		t.Fatalf("ran %q in %q with %q", gotArgv, gotDir, gotEnv)
	}
	if q.Email != "new@example.com" || len(q.Groups) != 2 {
		t.Fatalf("quota = %+v", q)
	}

	failing := NewAgyReader("/bin/agy", func(context.Context, []string, string, []string) (Output, error) {
		return Output{}, context.DeadlineExceeded
	})
	if _, err := failing.ReadQuota(t.Context(), prof, nil); KindOf(err) != KindUnavailable {
		t.Fatalf("run error = %v", err)
	}
	if _, err := NewAgyReader("", run).ReadQuota(t.Context(), prof, nil); !errors.Is(err, backend.ErrNotInstalled) ||
		KindOf(err) != KindUnavailable {
		t.Fatalf("no binary = %v", err)
	}
	if _, err := r.ReadQuota(t.Context(), testProfile(t, account.KindAPIKey), nil); !errors.Is(err, ErrPassive) {
		t.Fatalf("api key = %v", err)
	}
	broken := NewAgyReader("/bin/agy", func(context.Context, []string, string, []string) (Output, error) {
		return Output{Stdout: []byte(`{"status":"SUCCESS","command":"changed"}`)}, nil
	})
	if _, err := broken.ReadQuota(t.Context(), prof, nil); !errors.Is(err, ErrFormat) {
		t.Fatalf("changed format = %v", err)
	}
	if got := newEmailCache().agyEmail(t.TempDir()); got != "" {
		t.Fatalf("email without logs = %q", got)
	}
}

func TestAgyReaderRealProcess(t *testing.T) {
	prof := testProfile(t, account.KindSystem)
	r := NewAgyReader(selfBin(t), nil)
	q, err := r.ReadQuota(t.Context(), prof, fakeEnv("print", "QUOTA_FAKE_STDOUT="+agyUsageOK))
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Groups) != 2 {
		t.Fatalf("quota = %+v", q)
	}
	_, err = r.ReadQuota(t.Context(), prof, fakeEnv("print", "QUOTA_FAKE_STDERR=Authentication required", "QUOTA_FAKE_CODE=3"))
	if !errors.Is(err, ErrNeedsLogin) {
		t.Fatalf("signed out = %v", err)
	}
}

func TestRunCommand(t *testing.T) {
	out, err := runCommand(t.Context(), []string{selfBin(t)}, t.TempDir(),
		fakeEnv("print", "QUOTA_FAKE_STDOUT=hello", "QUOTA_FAKE_STDERR=oops", "QUOTA_FAKE_CODE=4"))
	if err != nil || string(out.Stdout) != "hello" || string(out.Stderr) != "oops" || out.Code != 4 {
		t.Fatalf("out = %q %q %d, %v", out.Stdout, out.Stderr, out.Code, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := runCommand(ctx, []string{selfBin(t)}, t.TempDir(), fakeEnv("hang")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hang = %v", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatal("the hanging command was not stopped")
	}
	if _, err := runCommand(t.Context(), []string{filepath.Join(t.TempDir(), "missing")}, "", nil); err == nil {
		t.Fatal("missing binary ran")
	}
	if _, err := runCommand(t.Context(), nil, "", nil); err == nil {
		t.Fatal("empty command ran")
	}
	var b limitedBuffer
	b.limit = 3
	if n, _ := b.Write([]byte("abcdef")); n != 6 || b.String() != "abc" {
		t.Fatalf("limited buffer = %q", b.String())
	}
}

// A definite sign-in phrase means signed out even when the output also mentions the network (re-review finding 1).
func TestAgySignInPhraseBeatsNetworkText(t *testing.T) {
	cases := []Output{
		{Stdout: []byte(`{"error":"Authentication required"}`), Stderr: []byte("using proxy from HTTPS_PROXY"), Code: 1},
		{Stderr: []byte("not logged in. Please log in again.\nnetwork: online"), Code: 1},
		{Stderr: []byte("[warn] request timed out once, retrying\nError: authentication required"), Code: 1},
	}
	for _, out := range cases {
		if _, err := parseAgyUsage(out); !errors.Is(err, ErrNeedsLogin) {
			t.Errorf("stdout %q stderr %q: err = %v, want signed out", out.Stdout, out.Stderr, err)
		}
	}
}

func TestAgyNetworkErrorIsNotSignedOut(t *testing.T) {
	for _, text := range []string{
		"authentication failed: connection reset",
		"Authentication failed: read ECONNRESET",
		"authentication failed (proxy returned 502)",
		"authentication failed: tls handshake timeout",
	} {
		_, err := parseAgyUsage(Output{Stderr: []byte(text), Code: 1})
		if err == nil || errors.Is(err, ErrNeedsLogin) || KindOf(err) != KindFailed {
			t.Errorf("%q: err = %v (kind %s), want a plain failure", text, err, KindOf(err))
		}
	}
}

func TestAgyEmailCache(t *testing.T) {
	home := t.TempDir()
	logDir := filepath.Join(home, ".gemini", "antigravity-cli", "log")
	older, newer := filepath.Join(logDir, "1.log"), filepath.Join(logDir, "2.log")
	writeFile(t, older, "applyAuthResult: email=old@example.com\n")
	writeFile(t, newer, "nothing here\n")
	base := time.Now().Add(-time.Hour)
	setTime := func(p string, at time.Time) {
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}
	setTime(older, base)
	setTime(newer, base.Add(time.Minute))
	c := newEmailCache()
	// The newest log names no email: the next one is searched.
	if got := c.agyEmail(home); got != "old@example.com" {
		t.Fatalf("email = %q", got)
	}
	// Same size and time: the cached answer is used, the file is not read again.
	writeFile(t, older, "applyAuthResult: email=xyz@example.com\n")
	setTime(older, base)
	if got := c.agyEmail(home); got != "old@example.com" {
		t.Fatalf("unchanged file read again: %q", got)
	}
	// A changed log is read again.
	setTime(older, base.Add(2*time.Minute))
	if got := c.agyEmail(home); got != "xyz@example.com" {
		t.Fatalf("changed file not read: %q", got)
	}
	// Another home's entries are kept; a deleted log's entry is dropped at the next lookup of its home.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, ".gemini", "antigravity-cli", "cli.log"), "applyAuthResult: email=b@example.com\n")
	if got := c.agyEmail(other); got != "b@example.com" {
		t.Fatalf("other home email = %q", got)
	}
	if err := os.Remove(older); err != nil {
		t.Fatal(err)
	}
	if got := c.agyEmail(home); got != "" {
		t.Fatalf("email after the log was deleted = %q", got)
	}
	c.mu.Lock()
	n := len(c.files)
	c.mu.Unlock()
	if n != 2 { // the other home's cli.log and this home's 2.log
		t.Fatalf("%d cache entries, want 2", n)
	}
}
