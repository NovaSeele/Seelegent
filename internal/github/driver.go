package github

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Driver hands out clients with the current access tier, the stored token and the host list of the settings.
type Driver struct {
	detector *Detector
	tokens   *TokenStore
	hosts    func() []string
	http     *http.Client
	gh       GhRunner
}

// DriverOptions configure a Driver.
type DriverOptions struct {
	Tokens *TokenStore
	Gh     GhRunner
	// EnterpriseHosts returns the githubEnterpriseHosts setting.
	EnterpriseHosts func() []string
	HTTP            *http.Client
	// Now is the clock of the access cache.
	Now func() time.Time
}

// NewDriver returns a Driver.
func NewDriver(o DriverOptions) *Driver {
	if o.EnterpriseHosts == nil {
		o.EnterpriseHosts = func() []string { return nil }
	}
	if o.HTTP == nil {
		o.HTTP = NewHTTPClient()
	}
	return &Driver{detector: NewDetector(o.Gh, o.Tokens, o.Now), tokens: o.Tokens, hosts: o.EnterpriseHosts,
		http: o.HTTP, gh: o.Gh}
}

// Access returns the current access (force: detect again).
func (d *Driver) Access(ctx context.Context, force bool) Access { return d.detector.Access(ctx, force) }

// Hosts returns the hosts that count as GitHub now.
func (d *Driver) Hosts() Hosts { return NewHosts(d.hosts()) }

// Client returns a client for owner/repo on host with access a. The token tier loads the stored token; it goes
// only to host's API, and host is always one of Hosts.
func (d *Driver) Client(_ context.Context, a Access, host Host, owner, repo string) (*Client, error) {
	if _, ok := d.Hosts().ByName(host.Name); !ok {
		return nil, &Error{Reason: ReasonInvalid, Message: "host " + host.Name + " is not a GitHub host"}
	}
	o := ClientOptions{Access: a, Host: host, Owner: owner, Repo: repo, HTTP: d.http, Gh: d.gh}
	if a.Tier == api.TierToken && d.tokens != nil {
		tok, err := d.tokens.Load()
		if err != nil && !errors.Is(err, ErrNoToken) {
			return nil, err
		}
		if tok == "" {
			o.Access.Tier = api.TierManual
		}
		o.Token = tok
	}
	return NewClient(o), nil
}

// Redact removes the stored token and anything token-like from text.
func (d *Driver) Redact(text string) string {
	if d.tokens != nil {
		if tok, err := d.tokens.Load(); err == nil {
			return Redact(text, tok)
		}
	}
	return Redact(text)
}

// HasToken reports whether data holds the stored token.
func (d *Driver) HasToken(data []byte) bool {
	if d.tokens == nil {
		return false
	}
	tok, err := d.tokens.Load()
	return err == nil && len(tok) >= minSecret && bytes.Contains(data, []byte(tok))
}

// SaveToken stores a token and detects the access again; it returns the token's hint.
func (d *Driver) SaveToken(token string) (string, error) {
	if d.tokens == nil {
		return "", errors.New("no token store")
	}
	hint, err := d.tokens.Save(token)
	d.detector.Forget()
	return hint, err
}

// RemoveToken deletes the stored token.
func (d *Driver) RemoveToken() error {
	if d.tokens == nil {
		return nil
	}
	err := d.tokens.Remove()
	d.detector.Forget()
	return err
}
