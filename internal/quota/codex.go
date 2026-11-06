package quota

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/buildinfo"
)

// Codex reader constants (old backends/codex.js).
const (
	// CodexTimeout bounds one app-server session (and the `codex debug models` fallback).
	CodexTimeout = 30 * time.Second
	// codexModelPages caps the pages of model/list that are followed.
	codexModelPages = 10
	// codexDefaultRest is how long a reached rate or spend limit lasts when no window says when it resets.
	codexDefaultRest = time.Hour
	// codexMaxLine caps one JSON-RPC message.
	codexMaxLine = 16 << 20
)

// codexPlans maps plan types to the names shown to the user.
var codexPlans = map[string]string{
	"free": "Free", "go": "Go", "plus": "Plus", "pro": "Pro", "team": "Team", "business": "Business",
	"enterprise": "Enterprise", "edu": "Edu", "education": "Edu",
}

// CodexReader reads the plan, quota windows and model catalog of a codex ChatGPT sign-in through `codex
// app-server` (JSON-RPC 2.0 over standard input and output, one message per line): account/read,
// account/rateLimits/read and model/list. No prompt is sent, so no quota is spent, and token values never leave
// the CLI. API-key accounts are passive. It implements backend.QuotaReader.
type CodexReader struct {
	bin     string
	start   StartFunc
	run     RunFunc
	now     func() time.Time
	timeout time.Duration
}

// NewCodexReader returns the reader for the codex CLI at bin ("" when not installed). start and run nil use the
// real processes; now nil is time.Now.
func NewCodexReader(bin string, start StartFunc, run RunFunc, now func() time.Time) *CodexReader {
	if start == nil {
		start = startProc
	}
	if run == nil {
		run = runCommand
	}
	if now == nil {
		now = time.Now
	}
	return &CodexReader{bin: bin, start: start, run: run, now: now, timeout: CodexTimeout}
}

// ReadQuota implements backend.QuotaReader.
func (r *CodexReader) ReadQuota(ctx context.Context, prof account.Profile, env []string) (backend.Quota, error) {
	if prof.Kind == account.KindAPIKey {
		return backend.Quota{}, ErrPassive
	}
	if r.bin == "" {
		return backend.Quota{}, fmt.Errorf("codex quota: %w: %w", ErrUnavailable, backend.ErrNotInstalled)
	}
	if _, err := os.Stat(filepath.Join(prof.Home, ".codex", "auth.json")); errors.Is(err, fs.ErrNotExist) {
		return backend.Quota{}, fmt.Errorf("codex quota: %w: no codex sign-in in the profile", ErrNeedsLogin)
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	res, err := r.session(ctx, prof.Home, env)
	if err != nil {
		return backend.Quota{}, fmt.Errorf("codex quota: %w", err)
	}
	if res.models == nil && res.signedIn {
		res.models = r.debugModels(ctx, prof.Home, env)
	}
	q, err := parseCodexStatus(res, r.now())
	if err != nil {
		return backend.Quota{}, fmt.Errorf("codex quota: %w", err)
	}
	return q, nil
}

// codexResults are the answers of one app-server session.
type codexResults struct {
	account    json.RawMessage // account/read result
	rateLimits json.RawMessage // account/rateLimits/read result (nil when it failed)
	models     []string        // nil when model/list failed
	signedIn   bool
}

// rpcMessage is a JSON-RPC message from the server.
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// rpcClient talks to one app-server.
type rpcClient struct {
	w      io.Writer
	mu     sync.Mutex
	nextID int
	resp   chan rpcMessage
	done   chan struct{} // closed when the server's output ends
	quit   chan struct{} // closed when the session is over
}

// session runs initialize, account/read, account/rateLimits/read and model/list (following pages).
func (r *CodexReader) session(ctx context.Context, home string, env []string) (codexResults, error) {
	proc, err := r.start(ctx, []string{r.bin, "app-server"}, home, env)
	if err != nil {
		return codexResults{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer proc.Stop()
	c := &rpcClient{w: proc.Stdin, resp: make(chan rpcMessage, 16), done: make(chan struct{}), quit: make(chan struct{})}
	defer close(c.quit)
	go c.readLoop(proc.Stdout)
	go func() {
		<-ctx.Done()
		proc.Stop()
	}()

	version := buildinfo.Get().Version
	if _, err := c.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "seelegent", "version": version}}); err != nil {
		return codexResults{}, err
	}
	if err := c.notify("initialized"); err != nil {
		return codexResults{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var res codexResults
	if res.account, err = c.call(ctx, "account/read", map[string]any{"refreshToken": false}); err != nil {
		return codexResults{}, err
	}
	var acc codexAccountRead
	if err := json.Unmarshal(res.account, &acc); err != nil {
		return codexResults{}, fmt.Errorf("%w: account/read: %w", ErrFormat, err)
	}
	res.signedIn = acc.Account != nil
	if !res.signedIn {
		return res, nil
	}
	if rl, err := c.call(ctx, "account/rateLimits/read", map[string]any{}); err == nil {
		res.rateLimits = rl
	} else if ctx.Err() != nil {
		return codexResults{}, err
	}
	var models []string
	cursor := ""
	for page := 0; page < codexModelPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.call(ctx, "model/list", params)
		if err != nil {
			if ctx.Err() != nil {
				return codexResults{}, err
			}
			break
		}
		var p codexModelPage
		if json.Unmarshal(raw, &p) != nil {
			break
		}
		if models == nil {
			models = []string{}
		}
		for _, m := range p.Data {
			slug := m.Model
			if slug == "" {
				slug = m.ID
			}
			if !m.Hidden && slug != "" && !contains(models, slug) {
				models = append(models, slug)
			}
		}
		if cursor = p.NextCursor; cursor == "" {
			break
		}
	}
	res.models = models
	return res, nil
}

// readLoop forwards the server's responses until its output ends.
func (c *rpcClient) readLoop(stdout io.Reader) {
	defer close(c.done)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), codexMaxLine)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var m rpcMessage
		if json.Unmarshal(line, &m) != nil || m.Method != "" || len(m.ID) == 0 {
			continue // notifications and requests from the server are not needed
		}
		select {
		case c.resp <- m:
		case <-c.quit:
			return
		}
	}
}

// call sends a request and waits for its response.
func (c *rpcClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, method, err)
	}
	want := strconv.Itoa(id)
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %s: %w", ErrUnavailable, method, ctx.Err())
		case <-c.done:
			// Drain what was read before the output ended.
			select {
			case m := <-c.resp:
				if string(m.ID) == want {
					return m.result(method)
				}
				continue
			default:
			}
			return nil, fmt.Errorf("%w: codex app-server ended before answering %s", ErrUnavailable, method)
		case m := <-c.resp:
			if string(m.ID) == want {
				return m.result(method)
			}
		}
	}
}

// result returns the result of a response or its error.
func (m rpcMessage) result(method string) (json.RawMessage, error) {
	if m.Error != nil {
		return nil, fmt.Errorf("%s refused: %s", method, m.Error.Message)
	}
	if len(m.Result) == 0 || string(m.Result) == "null" {
		return nil, fmt.Errorf("%w: %s returned no result", ErrFormat, method)
	}
	return m.Result, nil
}

// notify sends a notification.
func (c *rpcClient) notify(method string) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

func (c *rpcClient) send(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}

// debugModels runs `codex debug models`, the fallback catalog source (nil when it fails).
func (r *CodexReader) debugModels(ctx context.Context, home string, env []string) []string {
	out, err := r.run(ctx, []string{r.bin, "debug", "models"}, home, env)
	if err != nil || out.Code != 0 {
		return nil
	}
	return modelsFromDebugModels(out.Stdout)
}

// modelsFromDebugModels reads the slugs listed for the account ({"models":[{slug, visibility, priority}]}).
func modelsFromDebugModels(stdout []byte) []string {
	start, end := bytes.IndexByte(stdout, '{'), bytes.LastIndexByte(stdout, '}')
	if start < 0 || end <= start {
		return nil
	}
	var parsed struct {
		Models []struct {
			Slug       string  `json:"slug"`
			Visibility string  `json:"visibility"`
			Priority   float64 `json:"priority"`
		} `json:"models"`
	}
	if json.Unmarshal(stdout[start:end+1], &parsed) != nil || parsed.Models == nil {
		return nil
	}
	sort.SliceStable(parsed.Models, func(i, j int) bool { return parsed.Models[i].Priority < parsed.Models[j].Priority })
	list := []string{}
	for _, m := range parsed.Models {
		if m.Visibility == "list" && m.Slug != "" && !contains(list, m.Slug) {
			list = append(list, m.Slug)
		}
	}
	return list
}

type codexAccountRead struct {
	Account *struct {
		Type     string `json:"type"`
		Email    string `json:"email"`
		PlanType string `json:"planType"`
	} `json:"account"`
}

type codexRateWindow struct {
	UsedPercent        *float64 `json:"usedPercent"`
	WindowDurationMins *float64 `json:"windowDurationMins"`
	ResetsAt           *float64 `json:"resetsAt"`
}

type codexRateLimits struct {
	RateLimits *struct {
		Primary              *codexRateWindow `json:"primary"`
		Secondary            *codexRateWindow `json:"secondary"`
		PlanType             string           `json:"planType"`
		RateLimitReachedType json.RawMessage  `json:"rateLimitReachedType"`
		SpendControlReached  bool             `json:"spendControlReached"`
	} `json:"rateLimits"`
	OrdinaryUsageAllowed *bool `json:"ordinaryUsageAllowed"`
}

type codexModelPage struct {
	Data []struct {
		ID     string `json:"id"`
		Model  string `json:"model"`
		Hidden bool   `json:"hidden"`
	} `json:"data"`
	NextCursor string `json:"nextCursor"`
}

// parseCodexStatus turns the session's answers into a quota (old parseCodexStatus).
func parseCodexStatus(res codexResults, now time.Time) (backend.Quota, error) {
	var acc codexAccountRead
	if err := json.Unmarshal(res.account, &acc); err != nil {
		return backend.Quota{}, fmt.Errorf("%w: account/read: %w", ErrFormat, err)
	}
	if acc.Account == nil {
		return backend.Quota{}, fmt.Errorf("%w: codex is not signed in to a ChatGPT account", ErrNeedsLogin)
	}
	var rl codexRateLimits
	if res.rateLimits != nil {
		if err := json.Unmarshal(res.rateLimits, &rl); err != nil {
			return backend.Quota{}, fmt.Errorf("%w: account/rateLimits/read: %w", ErrFormat, err)
		}
	}
	if strings.EqualFold(acc.Account.Type, "chatgpt") && rl.RateLimits == nil {
		if res.rateLimits == nil {
			return backend.Quota{}, errors.New("codex app-server returned no rate limits")
		}
		return backend.Quota{}, fmt.Errorf("%w: the rate limits have no rateLimits field", ErrFormat)
	}
	plan := strings.ToLower(acc.Account.PlanType)
	if plan == "" && rl.RateLimits != nil {
		plan = strings.ToLower(rl.RateLimits.PlanType)
	}
	label := planLabel(plan)
	q := backend.Quota{Plan: label, Email: strings.TrimSpace(acc.Account.Email), Entitled: true, Models: res.models,
		Groups: []backend.QuotaGroup{}}

	var buckets []backend.QuotaBucket
	if rl.RateLimits != nil {
		for _, w := range []*codexRateWindow{rl.RateLimits.Primary, rl.RateLimits.Secondary} {
			if b, ok := codexBucket(w); ok {
				buckets = append(buckets, b)
			}
		}
	}
	// Longest window first, like Claude's weekly above its 5-hour window.
	sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].WindowMinutes > buckets[j].WindowMinutes })
	if len(buckets) > 0 {
		q.Groups = append(q.Groups, backend.QuotaGroup{Name: "Codex", Buckets: buckets})
	}

	switch {
	case rl.OrdinaryUsageAllowed != nil && !*rl.OrdinaryUsageAllowed:
		q.Entitled = false
		q.EntitlementError = planText(label, "cannot use Codex")
	case res.models != nil && len(res.models) == 0:
		q.Entitled = false
		q.EntitlementError = planText(label, "has no Codex model it may use")
	}
	if q.Entitled && rl.RateLimits != nil {
		var full []backend.QuotaBucket
		for _, b := range buckets {
			if b.UsedFraction >= 1 {
				full = append(full, b)
			}
		}
		reached := rawTruthy(rl.RateLimits.RateLimitReachedType) || rl.RateLimits.SpendControlReached
		if len(full) > 0 || reached {
			pool := full
			if len(pool) == 0 {
				pool = buckets
			}
			until := time.Time{}
			for _, b := range pool {
				if b.ResetAt.After(now) && b.ResetAt.After(until) {
					until = b.ResetAt
				}
			}
			if until.IsZero() {
				until = now.Add(codexDefaultRest)
			}
			q.ExhaustedUntil = until
			switch {
			case rl.RateLimits.SpendControlReached:
				q.ExhaustedReason = "spend limit reached"
			case len(full) > 0:
				var names []string
				for _, b := range full {
					names = append(names, b.Label)
				}
				q.ExhaustedReason = "quota used up: " + strings.Join(names, ", ")
			default:
				q.ExhaustedReason = "rate limit reached"
			}
		}
	}
	return q, nil
}

// codexBucket turns one rate-limit window into a bucket.
func codexBucket(w *codexRateWindow) (backend.QuotaBucket, bool) {
	if w == nil || w.UsedPercent == nil || math.IsNaN(*w.UsedPercent) {
		return backend.QuotaBucket{}, false
	}
	mins := 0
	if w.WindowDurationMins != nil && *w.WindowDurationMins > 0 && *w.WindowDurationMins < math.MaxInt32 {
		mins = int(*w.WindowDurationMins)
	}
	b := backend.QuotaBucket{
		Window: windowKey(mins), Label: windowLabel(mins), WindowMinutes: mins,
		UsedFraction: clamp01(*w.UsedPercent / 100),
	}
	if w.ResetsAt != nil && *w.ResetsAt > 0 {
		v := *w.ResetsAt
		if v < 1e12 { // unix seconds; milliseconds are accepted too
			b.ResetAt = time.Unix(int64(v), 0).UTC()
		} else {
			b.ResetAt = time.UnixMilli(int64(v)).UTC()
		}
	}
	return b, true
}

// windowLabel is the English name of a window length.
func windowLabel(mins int) string {
	switch {
	case mins <= 0:
		return "Quota"
	case mins == 300:
		return "5 hours"
	case mins == 10080:
		return "Weekly"
	case mins%1440 == 0:
		return strconv.Itoa(mins/1440) + " days"
	}
	return strconv.FormatFloat(math.Round(float64(mins)/6)/10, 'f', -1, 64) + " hours"
}

// planLabel returns the display name of a plan type ("" for none).
func planLabel(plan string) string {
	p := strings.TrimSpace(strings.ToLower(plan))
	if p == "" {
		return ""
	}
	if l, ok := codexPlans[p]; ok {
		return l
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// planText builds an entitlement message.
func planText(label, what string) string {
	if label == "" {
		return "this ChatGPT account " + what
	}
	return "the ChatGPT " + label + " plan " + what
}

// rawTruthy reports whether a JSON value is set (not null, false, "" or 0).
func rawTruthy(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", "false", `""`, "0":
		return false
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
