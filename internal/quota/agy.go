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
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
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

// agyAuthText recognizes a signed-out agy in its output.
var agyAuthText = regexp.MustCompile(`(?i)authentication required|not logged in|authentication (expired|failed)|log back in`)

// AgyReader reads the quota of an agy account with `agy -p /usage --output-format json`, run in the account's
// profile. It implements backend.QuotaReader.
type AgyReader struct {
	bin     string
	run     RunFunc
	timeout time.Duration
}

// NewAgyReader returns the reader for the agy CLI at bin ("" when not installed). run nil runs the real command.
func NewAgyReader(bin string, run RunFunc) *AgyReader {
	if run == nil {
		run = runCommand
	}
	return &AgyReader{bin: bin, run: run, timeout: AgyTimeout}
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
	q.Email = agyReadEmail(prof.Home)
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
		if agyAuthText.MatchString(text) {
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

// agyReadEmail returns the email of the newest sign-in in agy's logs under home ("" when none).
func agyReadEmail(home string) string {
	dir := filepath.Join(home, ".gemini", "antigravity-cli")
	files := []string{filepath.Join(dir, "cli.log")}
	if entries, err := os.ReadDir(filepath.Join(dir, "log")); err == nil {
		for _, e := range entries {
			if e.Type().IsRegular() {
				files = append(files, filepath.Join(dir, "log", e.Name()))
			}
		}
	}
	var (
		email  string
		newest time.Time
	)
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil || !st.Mode().IsRegular() {
			continue
		}
		data, err := readTail(f, agyLogTail)
		if err != nil {
			continue
		}
		m := agyEmail.FindAllSubmatch(data, -1)
		if len(m) == 0 {
			continue
		}
		if email == "" || !st.ModTime().Before(newest) {
			email, newest = string(m[len(m)-1][1]), st.ModTime()
		}
	}
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
