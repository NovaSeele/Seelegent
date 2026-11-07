package github

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
)

// Error reasons of a failed GitHub call.
const (
	ReasonAuth        = "auth"
	ReasonRateLimited = "rate_limited"
	ReasonForbidden   = "forbidden"
	ReasonNotFound    = "not_found"
	ReasonInvalid     = "invalid"
	ReasonNetwork     = "network"
	ReasonGitHub      = "github"
)

var (
	// ErrUpstream is matched by every failed GitHub call (*Error).
	ErrUpstream = errors.New("GitHub request failed")
	// ErrManual is matched when an action needs the gh CLI or a token and neither is there.
	ErrManual = errors.New("this needs the gh CLI signed in or a GitHub token")
)

// Error is a GitHub call that failed. Message is redacted.
type Error struct {
	Reason  string
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("GitHub: %s (HTTP %d)", e.Message, e.Status)
	}
	return "GitHub: " + e.Message
}

// Is makes errors.Is(err, ErrUpstream) true.
func (e *Error) Is(target error) bool { return target == ErrUpstream }

// APIDetails are the error details of the HTTP API.
func (e *Error) APIDetails() map[string]any {
	d := map[string]any{"source": "github", "reason": e.Reason}
	if e.Status > 0 {
		d["status"] = e.Status
	}
	return d
}

// NotFound reports whether err is a GitHub 404.
func NotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == ReasonNotFound
}

// reasonFor maps an HTTP status to an error reason.
func reasonFor(status int, message string) string {
	switch status {
	case 401:
		return ReasonAuth
	case 429:
		return ReasonRateLimited
	case 403:
		if strings.Contains(strings.ToLower(message), "rate limit") {
			return ReasonRateLimited
		}
		return ReasonForbidden
	case 404:
		return ReasonNotFound
	case 422:
		return ReasonInvalid
	}
	return ReasonGitHub
}

// Access is how GitHub is reached right now.
type Access struct {
	Tier      string
	GhVersion string
	GhUser    string
	TokenHint string
	CheckedAt time.Time
}

// CanWrite reports whether the tier can call the API with an identity (gh or token).
func (a Access) CanWrite() bool { return a.Tier == api.TierGh || a.Tier == api.TierToken }

// DTO is the access as the API shows it, with the hosts that count as GitHub.
func (a Access) DTO(hosts Hosts) api.GitHubAccess {
	return api.GitHubAccess{Tier: a.Tier, GhVersion: a.GhVersion, GhUser: a.GhUser, TokenHint: a.TokenHint,
		Hosts: hosts.Names(), CheckedAt: api.FormatTime(a.CheckedAt)}
}

// accessTTL is how long a detected access is reused.
const accessTTL = 60 * time.Second

// ghEnv are the variables of every gh child: no prompts, no color codes, no update check.
var ghEnv = []string{"GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1", "GH_SPINNER_DISABLED=1"}

var ghVersionRe = regexp.MustCompile(`(?i)gh version\s+(\S+)`)

// Detector finds the access tier: gh when the gh CLI is installed and signed in, else token when one is stored,
// else manual. The result is kept for a minute. One detection runs at a time (G10: the lock was held while gh ran,
// up to 25 s, so every GitHub call waited): callers that have a cached value get it meanwhile, the others wait for
// that detection.
type Detector struct {
	gh     GhRunner
	tokens *TokenStore
	now    func() time.Time

	mu     sync.Mutex
	cached Access
	ok     bool
	flight *detection
	// gen changes on Forget: a detection started before it does not fill the cache.
	gen int
}

// detection is a detection in progress; done closes when a holds its result.
type detection struct {
	done chan struct{}
	a    Access
}

// NewDetector returns a Detector.
func NewDetector(gh GhRunner, tokens *TokenStore, now func() time.Time) *Detector {
	if now == nil {
		now = time.Now
	}
	return &Detector{gh: gh, tokens: tokens, now: now}
}

// Access returns the current access (force: detect again now, or wait for the detection already running). A
// detection that a Forget overtook is not answered: the caller detects again (or waits for the newer detection).
func (d *Detector) Access(ctx context.Context, force bool) Access {
	for {
		a, gen := d.access(ctx, force)
		d.mu.Lock()
		current := d.gen == gen
		d.mu.Unlock()
		if current || ctx.Err() != nil {
			return a
		}
	}
}

// access is one attempt of Access; gen is the generation the answer belongs to.
func (d *Detector) access(ctx context.Context, force bool) (a Access, gen int) {
	d.mu.Lock()
	gen = d.gen
	if d.ok && !force && d.now().Sub(d.cached.CheckedAt) < accessTTL {
		a = d.cached
		d.mu.Unlock()
		return a, gen
	}
	if f := d.flight; f != nil {
		if d.ok && !force {
			a = d.cached // stale by less than the detection takes
			d.mu.Unlock()
			return a, gen
		}
		d.mu.Unlock()
		select {
		case <-f.done:
			return f.a, gen
		case <-ctx.Done():
			return Access{Tier: api.TierManual, CheckedAt: d.now()}, gen
		}
	}
	f := &detection{done: make(chan struct{})}
	d.flight = f
	d.mu.Unlock()
	// Detached from the caller: the callers waiting for this detection must not get a cancelled one.
	f.a = d.detect(context.WithoutCancel(ctx))
	d.mu.Lock()
	if d.gen == gen {
		d.cached, d.ok = f.a, true
	}
	if d.flight == f {
		d.flight = nil
	}
	d.mu.Unlock()
	close(f.done)
	return f.a, gen
}

// detect asks gh and the token store.
func (d *Detector) detect(ctx context.Context) Access {
	a := Access{Tier: api.TierManual, CheckedAt: d.now()}
	if d.gh != nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, _, err := d.gh.Run(cctx, []string{"--version"}, nil, ghEnv)
		cancel()
		if err == nil {
			if m := ghVersionRe.FindSubmatch(out); m != nil {
				a.GhVersion = string(m[1])
			} else {
				a.GhVersion = oneLine(string(out), 40)
			}
			cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			user, _, err := d.gh.Run(cctx, []string{"api", "user", "--jq", ".login"}, nil, ghEnv)
			cancel()
			if login := strings.TrimSpace(firstLine(string(user))); err == nil && login != "" {
				a.GhUser, a.Tier = oneLine(login, 100), api.TierGh
			}
		}
	}
	if d.tokens != nil {
		if tok, err := d.tokens.Load(); err == nil {
			a.TokenHint = account.KeyHint(tok)
			if a.Tier != api.TierGh {
				a.Tier = api.TierToken
			}
		}
	}
	return a
}

// Forget drops the cached access (after the token changed); a detection still running does not fill it again.
func (d *Detector) Forget() {
	d.mu.Lock()
	d.ok = false
	d.gen++
	d.flight = nil
	d.mu.Unlock()
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
