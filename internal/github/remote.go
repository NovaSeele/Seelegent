// Package github is Seelegent's way to GitHub (milestone 15, docs/github.md): reading git remotes, the access tiers
// (the gh CLI, a token pasted in the dashboard, or manual), the stored token, and a small client over `gh api` or the
// REST API for issues, pull requests, comments, repositories and CI checks.
//
// Safety: a host is GitHub only when it is github.com or one of the enterprise hosts listed in the settings (old bug
// D4: any host with "github" in its name used to count, and the token went to it). The API address always comes from
// that list, never from a remote URL, so the token can reach no other host. No shell is used; every error message
// and log line passes through Redact.
package github

import (
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/config"
)

// Remote is the GitHub repository a git remote URL names.
type Remote struct {
	// Host is the lower-case host name without port; Port is the port of an http(s) URL ("" when none).
	Host string
	Port string
	// SSH is true for ssh, git and scp-style remotes (their port is not the web port).
	SSH   bool
	Owner string
	Repo  string
}

// scpRemote matches "[user@]host:owner/repo[.git]" (a host of two or more characters, so "C:/x" is a path).
var scpRemote = regexp.MustCompile(`^(?:[^@\s/]+@)?([^:\s/]{2,}):([^/\s]+)/([^/\s]+?)/?$`)

// schemeRe matches the start of a URL.
var schemeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)

func stripGitSuffix(name string) string {
	name = strings.TrimRight(name, "/")
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".git") {
		name = name[:len(name)-4]
	}
	return name
}

// validName reports whether s is a plain owner or repository name.
func validName(s string) bool {
	if s == "" || s == "." || s == ".." || len(s) > 100 {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.'
		if !ok {
			return false
		}
	}
	return true
}

// ParseRemote reads https://host/owner/repo(.git), ssh://git@host[:port]/owner/repo.git and git@host:owner/repo.git.
// Credentials are ignored. Local paths, file:// URLs and anything that is not exactly owner/repo give false.
func ParseRemote(raw string) (Remote, bool) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return Remote{}, false
	}
	if !schemeRe.MatchString(u) {
		m := scpRemote.FindStringSubmatch(u)
		if len(m) < 3 || strings.HasPrefix(m[2], "/") {
			return Remote{}, false
		}
		repo := stripGitSuffix(m[3])
		if !validName(m[2]) || !validName(repo) {
			return Remote{}, false
		}
		return Remote{Host: strings.ToLower(m[1]), SSH: true, Owner: m[2], Repo: repo}, true
	}
	p, err := url.Parse(u)
	if err != nil || p.Hostname() == "" {
		return Remote{}, false
	}
	scheme := strings.ToLower(p.Scheme)
	switch scheme {
	case "http", "https", "ssh", "git", "git+ssh", "ssh+git":
	default:
		return Remote{}, false
	}
	segs := []string{}
	for _, s := range strings.Split(p.Path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) != 2 {
		return Remote{}, false
	}
	repo := stripGitSuffix(segs[1])
	if !validName(segs[0]) || !validName(repo) {
		return Remote{}, false
	}
	r := Remote{Host: strings.ToLower(p.Hostname()), Owner: segs[0], Repo: repo}
	if scheme == "http" || scheme == "https" {
		r.Port = p.Port()
		if scheme == "https" && r.Port == "443" || scheme == "http" && r.Port == "80" {
			r.Port = ""
		}
	} else {
		r.SSH = true
	}
	return r, true
}

// urlUserinfo matches the user[:password]@ of a URL.
var urlUserinfo = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*://)[^\s/@]+@`)

// StripCredentials returns a remote URL as Seelegent stores and shows it: user:password@ removed from http(s) URLs,
// the password removed from ssh URLs (the user name, such as git@, stays); scp-style remotes are kept.
func StripCredentials(raw string) string {
	u := strings.TrimSpace(raw)
	if !schemeRe.MatchString(u) {
		return u
	}
	p, err := url.Parse(u)
	if err != nil {
		return urlUserinfo.ReplaceAllString(u, "$1")
	}
	if p.User == nil {
		return u
	}
	switch strings.ToLower(p.Scheme) {
	case "ssh", "git+ssh", "ssh+git":
		p.User = url.User(p.User.Username())
	default:
		p.User = nil
	}
	return p.String()
}

// Host is a GitHub host Seelegent talks to.
type Host struct {
	// Name is the host as the settings list it (lower case, with its port when one is given): "github.com",
	// "ghe.example.com", "127.0.0.1:9000".
	Name string
	// Scheme is https, or http for a loopback test server.
	Scheme string
}

// DotCom is github.com.
var DotCom = Host{Name: "github.com", Scheme: "https"}

// IsDotCom reports whether h is github.com.
func (h Host) IsDotCom() bool { return h.Name == DotCom.Name }

// APIBase is the REST API root of the host: https://api.github.com, or <scheme>://<host>/api/v3 for an enterprise
// server.
func (h Host) APIBase() string {
	if h.IsDotCom() {
		return "https://api.github.com"
	}
	return h.Scheme + "://" + h.Name + "/api/v3"
}

// WebBase is the address of the host's web pages.
func (h Host) WebBase() string { return h.Scheme + "://" + h.Name }

// hostname is Name without its port.
func (h Host) hostname() (name, port string) {
	if n, p, err := net.SplitHostPort(h.Name); err == nil {
		return strings.Trim(n, "[]"), p
	}
	return strings.Trim(h.Name, "[]"), ""
}

// Hosts are the hosts that count as GitHub: github.com and the enterprise hosts of the settings.
type Hosts struct {
	list []Host
}

// NewHosts returns github.com plus the valid entries of the githubEnterpriseHosts setting (invalid ones are
// skipped; the settings check refuses them).
func NewHosts(enterprise []string) Hosts {
	h := Hosts{list: []Host{DotCom}}
	for _, e := range enterprise {
		n, err := config.NormalizeGitHubHost(e)
		if err != nil || n.Host == DotCom.Name {
			continue
		}
		h.list = append(h.list, Host{Name: n.Host, Scheme: n.Scheme})
	}
	return h
}

// Names lists the host names.
func (h Hosts) Names() []string {
	out := make([]string, 0, len(h.list))
	for _, x := range h.list {
		out = append(out, x.Name)
	}
	return out
}

// Lookup returns the GitHub host of a remote, or false when the remote is on any other host. An ssh remote matches
// on the host name alone (its port is the ssh port); an http(s) remote also on the port.
func (h Hosts) Lookup(r Remote) (Host, bool) {
	if r.Host == "" {
		return Host{}, false
	}
	if r.Host == "github.com" || r.SSH && r.Host == "ssh.github.com" {
		if r.SSH || r.Port == "" {
			return DotCom, true
		}
		return Host{}, false
	}
	for _, x := range h.list[1:] {
		name, port := x.hostname()
		if name != r.Host {
			continue
		}
		if r.SSH || port == r.Port {
			return x, true
		}
	}
	return Host{}, false
}

// ByName returns the listed host with this name.
func (h Hosts) ByName(name string) (Host, bool) {
	for _, x := range h.list {
		if strings.EqualFold(x.Name, name) {
			return x, true
		}
	}
	return Host{}, false
}
