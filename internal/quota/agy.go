package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// agy reader limits (old backends/agy.js).
const (
	// AgyTimeout bounds one `agy -p /usage` run.
	AgyTimeout = 90 * time.Second
	// agyLogTail is how much of the end of each agy log is searched for the signed-in email.
	agyLogTail = 4 << 20
)

// agyEmail finds the signed-in email in agy's logs ("applyAuthResult: email=<email>").
var agyEmail = regexp.MustCompile(`applyAuthResult: email=([^,\s]+)`)

// agyAuthText recognizes wording that asks for a sign-in; it always means a signed-out agy, whatever else the
// output says.
var agyAuthText = regexp.MustCompile(`(?i)authentication required|not (logged|signed) in|(log|sign) ?in required|` +
	`please (log|sign) ?in|(log|sign) back in|(log|sign) ?in again|authentication expired`)

// agyAuthFailedText is the bare "authentication failed". agy also prints it when the network or a proxy breaks the
// token exchange ("authentication failed: connection reset"), so it means "signed out" only when agyNetworkText
// does not match.
var agyAuthFailedText = regexp.MustCompile(`(?i)authentication failed`)

// agyNetworkText recognizes network, proxy and TLS failures; they veto only agyAuthFailedText.
var agyNetworkText = regexp.MustCompile(`(?i)connection (reset|refused|closed|aborted|timed out)|timed? ?out|` +
	`econn(reset|refused|aborted)|etimedout|enotfound|eai_again|socket hang up|network|proxy|\bdns\b|\btls\b|` +
	`certificate|x509|unreachable|no such host|temporar(y|ily)|\b50[234]\b`)

// AgyReader reads the quota of an agy account with `agy -p /usage --output-format json`, run in the account's
// profile. It implements backend.QuotaReader.
type AgyReader struct {
	bin     string
	run     RunFunc
	timeout time.Duration
	emails  *emailCache
}

// NewAgyReader returns the reader for the agy CLI at bin ("" when not installed). run nil runs the real command.
func NewAgyReader(bin string, run RunFunc) *AgyReader {
	if run == nil {
		run = runCommand
	}
	return &AgyReader{bin: bin, run: run, timeout: AgyTimeout, emails: newEmailCache()}
}

// agyAnswer is the JSON line agy prints for /usage.
type agyAnswer struct {
	Status  string          `json:"status"`
	Error   json.RawMessage `json:"error"`
	Command *struct {
		Data *struct {
			Groups []json.RawMessage `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// agyGroup is one quota group of the answer; its buckets are decoded one by one so one odd bucket does not hide
// the others.
type agyGroup struct {
	Name    string            `json:"name"`
	Buckets []json.RawMessage `json:"buckets"`
}

type agyBucket struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Window            string   `json:"window"`
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
}

// ReadQuota implements backend.QuotaReader.
func (r *AgyReader) ReadQuota(ctx context.Context, prof account.Profile, env []string) (backend.Quota, error) {
	if prof.Kind == account.KindAPIKey {
		return backend.Quota{}, ErrPassive
	}
	if r.bin == "" {
		return backend.Quota{}, fmt.Errorf("agy quota: %w: %w", ErrUnavailable, backend.ErrNotInstalled)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	out, err := r.run(ctx, []string{r.bin, "-p", "/usage", "--output-format", "json"}, prof.Home, env)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("agy quota: %w: %w", ErrUnavailable, err)
	}
	q, err := parseAgyUsage(out)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("agy quota: %w", err)
	}
	q.Email = r.emails.agyEmail(prof.Home)
	return q, nil
}

// parseAgyUsage turns the output of `agy -p /usage --output-format json` into a quota.
func parseAgyUsage(out Output) (backend.Quota, error) {
	line := lastJSONLine(out.Stdout)
	var ans agyAnswer
	if line != nil {
		if err := json.Unmarshal(line, &ans); err != nil {
			return backend.Quota{}, fmt.Errorf("%w: %w", ErrFormat, err)
		}
	}
	if line == nil || ans.Status != "SUCCESS" {
		msg := jsonText(ans.Error)
		text := string(out.Stdout) + "\n" + string(out.Stderr) + "\n" + msg
		if agySignedOut(text) {
			return backend.Quota{}, fmt.Errorf("%w: agy is not signed in", ErrNeedsLogin)
		}
		if msg == "" {
			msg = lastLines(string(out.Stderr), 3)
		}
		if line == nil && msg == "" {
			return backend.Quota{}, fmt.Errorf("%w: no JSON answer (exit code %d)", ErrFormat, out.Code)
		}
		if msg == "" {
			msg = "status " + ans.Status
		}
		return backend.Quota{}, fmt.Errorf("/usage failed: %s", msg)
	}
	if ans.Command == nil || ans.Command.Data == nil || ans.Command.Data.Groups == nil {
		return backend.Quota{}, fmt.Errorf("%w: no command.data.groups in the answer", ErrFormat)
	}
	q := backend.Quota{Entitled: true, Groups: []backend.QuotaGroup{}}
	buckets := 0
	for _, raw := range ans.Command.Data.Groups {
		var g agyGroup
		if err := json.Unmarshal(raw, &g); err != nil || g.Name == "" {
			continue
		}
		qg := backend.QuotaGroup{Name: g.Name, Buckets: []backend.QuotaBucket{}}
		for _, rb := range g.Buckets {
			var b agyBucket
			if err := json.Unmarshal(rb, &b); err != nil || b.RemainingFraction == nil {
				continue
			}
			label := b.Name
			if label == "" {
				label = b.ID
			}
			key := b.Window
			if key == "" {
				key = "window"
			}
			qg.Buckets = append(qg.Buckets, backend.QuotaBucket{
				Window: key, Label: label, WindowMinutes: windowMinutes(b.Window),
				UsedFraction: clamp01(1 - *b.RemainingFraction), ResetAt: parseTime(b.ResetTime),
			})
			buckets++
		}
		q.Groups = append(q.Groups, qg)
	}
	if len(ans.Command.Data.Groups) > 0 && len(q.Groups) == 0 {
		return backend.Quota{}, fmt.Errorf("%w: no quota group could be read", ErrFormat)
	}
	if buckets == 0 && hasBuckets(ans.Command.Data.Groups) {
		return backend.Quota{}, fmt.Errorf("%w: no quota window could be read", ErrFormat)
	}
	return q, nil
}

// agySignedOut reports whether agy's output says the account must sign in: a definite sign-in phrase always does;
// a bare "authentication failed" does unless the output also names a network, proxy or TLS failure.
func agySignedOut(text string) bool {
	return agyAuthText.MatchString(text) ||
		(agyAuthFailedText.MatchString(text) && !agyNetworkText.MatchString(text))
}

// hasBuckets reports whether any raw group lists buckets (so none being readable means the format changed).
func hasBuckets(groups []json.RawMessage) bool {
	for _, raw := range groups {
		var g agyGroup
		if json.Unmarshal(raw, &g) == nil && len(g.Buckets) > 0 {
			return true
		}
	}
	return false
}

// emailCache remembers the email found in each agy log by (path, size, modification time), so a refresh does
// not read megabytes of unchanged logs again. Entries of logs that are gone (deleted or rotated away) are dropped
// at the next lookup of their home. It is safe for concurrent use.
type emailCache struct {
	mu    sync.Mutex
	files map[string]emailEntry // by fsutil key of the log path
}

type emailEntry struct {
	home  string // fsutil key of the profile home the log belongs to
	size  int64
	mtime time.Time
	email string
}

func newEmailCache() *emailCache { return &emailCache{files: map[string]emailEntry{}} }

// pathKey returns the cache key of a path.
func pathKey(p string) string {
	if k, err := fsutil.Key(p); err == nil {
		return k
	}
	return p
}

// agyEmail returns the email of the newest sign-in in agy's logs under home ("" when none). Logs are searched
// newest first and the search stops at the first log that names an email, so usually only one file is read, and
// only when it changed since the last refresh.
func (c *emailCache) agyEmail(home string) string {
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	paths := []string{filepath.Join(dir, "cli.log")}
	if entries, err := os.ReadDir(filepath.Join(dir, "log")); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				paths = append(paths, filepath.Join(dir, "log", e.Name()))
			}
		}
	}
	type logFile struct {
		path, key string
		st        os.FileInfo
	}
	homeKey := pathKey(home)
	listed := map[string]bool{}
	var files []logFile
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			k := pathKey(p)
			listed[k] = true
			files = append(files, logFile{p, k, st})
		}
	}
	c.mu.Lock()
	for k, e := range c.files {
		if e.home == homeKey && !listed[k] {
			delete(c.files, k)
		}
	}
	c.mu.Unlock()
	sort.SliceStable(files, func(i, j int) bool { return files[i].st.ModTime().After(files[j].st.ModTime()) })
	for _, f := range files {
		if email := c.fileEmail(homeKey, f.key, f.path, f.st); email != "" {
			return email
		}
	}
	return ""
}

// fileEmail returns the last email named in the tail of one log, from the cache while the file is unchanged.
func (c *emailCache) fileEmail(homeKey, key, path string, st os.FileInfo) string {
	c.mu.Lock()
	e, ok := c.files[key]
	c.mu.Unlock()
	if ok && e.size == st.Size() && e.mtime.Equal(st.ModTime()) {
		return e.email
	}
	data, err := readTail(path, agyLogTail)
	if err != nil {
		return ""
	}
	email := ""
	if m := agyEmail.FindAllSubmatch(data, -1); len(m) > 0 {
		email = string(m[len(m)-1][1])
	}
	c.mu.Lock()
	c.files[key] = emailEntry{home: homeKey, size: st.Size(), mtime: st.ModTime(), email: email}
	c.mu.Unlock()
	return email
}

// readTail returns at most the last n bytes of a file.
func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > n {
		if _, err := f.Seek(st.Size()-n, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(f, n))
}

// lastJSONLine returns the last line of out that starts with '{' (nil when none).
func lastJSONLine(out []byte) []byte {
	lines := bytes.Split(out, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		l := bytes.TrimSpace(lines[i])
		if len(l) > 0 && l[0] == '{' {
			return l
		}
	}
	return nil
}

// jsonText returns a JSON string value, or the compact JSON of any other value ("" for none or null).
func jsonText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return string(raw)
}

// lastLines returns the last n non-empty lines of s joined by spaces.
func lastLines(s string, n int) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			keep = append(keep, l)
		}
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return strings.Join(keep, " ")
}

// parseTime reads an RFC 3339 time (zero when empty or invalid).
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
