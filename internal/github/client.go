package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// Limits of the client.
const (
	callTimeout   = 30 * time.Second
	maxJSON       = 8 << 20
	maxLog        = 32 << 20
	maxIssueBody  = 20000
	maxIssueTitle = 300
	// CompareBodyMax caps the body put into a manual compare link.
	CompareBodyMax = 6000
	maxRedirects   = 5
)

// NewHTTPClient returns the HTTP client of the REST tier: a redirect to another host, or to another scheme on the
// same host (https to http would send the token in clear), never carries the Authorization header (a job log
// redirects to a storage host), and at most five redirects are followed.
func NewHTTPClient() *http.Client {
	return &http.Client{CheckRedirect: checkRedirect}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return errors.New("too many redirects")
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) || !strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) {
		req.Header.Del("Authorization")
	}
	return nil
}

// Client calls the GitHub API of one repository on one host, through gh (tier gh) or the REST API (token and
// manual tiers; manual sends no credentials).
type Client struct {
	tier  string
	host  Host
	owner string
	repo  string
	token string
	http  *http.Client
	gh    GhRunner
}

// ClientOptions configure a Client.
type ClientOptions struct {
	Access Access
	Host   Host
	Owner  string
	Repo   string
	// Token is sent only in the token tier, and only to Host's API (Host comes from the settings' host list).
	Token string
	HTTP  *http.Client
	Gh    GhRunner
}

// NewClient returns a Client.
func NewClient(o ClientOptions) *Client {
	c := &Client{tier: o.Access.Tier, host: o.Host, owner: o.Owner, repo: o.Repo, http: o.HTTP, gh: o.Gh}
	if c.tier == api.TierToken {
		c.token = o.Token
	}
	if c.http == nil {
		c.http = NewHTTPClient()
	}
	if c.tier == "" {
		c.tier = api.TierManual
	}
	return c
}

// Tier is the client's access tier.
func (c *Client) Tier() string { return c.tier }

// Redact removes the client's token (and anything token-like) from text.
func (c *Client) Redact(text string) string { return Redact(text, c.token) }

func (c *Client) repoPath(suffix string) (string, error) {
	if c.owner == "" || c.repo == "" {
		return "", fmt.Errorf("%w: the repository has no GitHub owner and name", ErrInvalid)
	}
	return "repos/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + suffix, nil
}

func (c *Client) needWrite(what string) error {
	if c.tier != api.TierGh && c.tier != api.TierToken {
		return fmt.Errorf("%w: %s", ErrManual, what)
	}
	return nil
}

// call runs one API request and returns the response body.
func (c *Client) call(ctx context.Context, method, path string, body any, raw bool) ([]byte, error) {
	var input []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		input = b
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if c.tier == api.TierGh {
		return c.callGh(ctx, method, path, input)
	}
	return c.callREST(ctx, method, path, input, raw)
}

var ghStatusRe = regexp.MustCompile(`HTTP (\d{3})`)

// ghRun runs gh with the client's environment and maps a failure to a redacted *Error.
func (c *Client) ghRun(ctx context.Context, args []string, input []byte) ([]byte, error) {
	out, stderr, err := c.gh.Run(ctx, args, input, ghEnv)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, &Error{Reason: ReasonNetwork, Message: "gh did not answer in time"}
	}
	text := string(stderr) + "\n" + string(out)
	status := 0
	if m := ghStatusRe.FindStringSubmatch(text); m != nil {
		status, _ = strconv.Atoi(m[1])
	}
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = strings.TrimSpace(string(out))
	}
	if msg == "" {
		msg = err.Error()
	}
	msg = oneLine(c.Redact(msg), 500)
	reason := ReasonGitHub
	if status > 0 {
		reason = reasonFor(status, text)
	}
	return nil, &Error{Reason: reason, Status: status, Message: msg}
}

func (c *Client) callGh(ctx context.Context, method, path string, input []byte) ([]byte, error) {
	if c.gh == nil {
		return nil, &Error{Reason: ReasonGitHub, Message: "the gh CLI is not available"}
	}
	args := []string{"api", "-X", method, "-H", "Accept: application/vnd.github+json"}
	if !c.host.IsDotCom() {
		args = append(args, "--hostname", c.host.Name)
	}
	args = append(args, path)
	if input != nil {
		args = append(args, "--input", "-")
	}
	return c.ghRun(ctx, args, input)
}

func (c *Client) callREST(ctx context.Context, method, path string, input []byte, raw bool) ([]byte, error) {
	var rd io.Reader
	if input != nil {
		rd = bytes.NewReader(input)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.host.APIBase()+"/"+path, rd)
	if err != nil {
		return nil, &Error{Reason: ReasonGitHub, Message: c.Redact(err.Error())}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "seelegent")
	if c.tier == api.TierToken && c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Reason: ReasonNetwork, Message: oneLine(c.Redact(err.Error()), 500)}
	}
	defer func() { _ = resp.Body.Close() }()
	limit := int64(maxJSON)
	if raw {
		limit = maxLog
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, &Error{Reason: ReasonNetwork, Message: oneLine(c.Redact(err.Error()), 500)}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return data, nil
	}
	var e struct {
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(data, &e) == nil && e.Message != "" {
		msg = e.Message
	} else {
		msg = strings.TrimSpace(string(data))
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	msg = oneLine(c.Redact(msg), 500)
	reason := reasonFor(resp.StatusCode, msg)
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		reason = ReasonRateLimited
	}
	return nil, &Error{Reason: reason, Status: resp.StatusCode, Message: msg}
}

func (c *Client) callJSON(ctx context.Context, method, path string, body, out any) error {
	data, err := c.call(ctx, method, path, body, false)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return &Error{Reason: ReasonGitHub, Message: "unreadable answer"}
	}
	return nil
}

// IssueData is a GitHub issue as the API gives it, trimmed.
type IssueData struct {
	Number    int
	Title     string
	Body      string
	Labels    []string
	Author    string
	URL       string
	State     string
	UpdatedAt string
}

type ghIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	HTMLURL     string          `json:"html_url"`
	State       string          `json:"state"`
	UpdatedAt   string          `json:"updated_at"`
	PullRequest json.RawMessage `json:"pull_request"`
}

// ListIssues returns up to limit issues in state (open, closed or all), newest change first; pull requests are
// left out.
func (c *Client) ListIssues(ctx context.Context, state string, limit int) ([]IssueData, error) {
	limit = min(max(limit, 1), 1000)
	out := []IssueData{}
	for page := 1; len(out) < limit && page <= 10; page++ {
		per := min(100, limit)
		p, err := c.repoPath(fmt.Sprintf("/issues?state=%s&per_page=%d&page=%d&sort=updated&direction=desc",
			url.QueryEscape(state), per, page))
		if err != nil {
			return nil, err
		}
		var items []ghIssue
		if err := c.callJSON(ctx, http.MethodGet, p, nil, &items); err != nil {
			return nil, err
		}
		for ix := range items {
			it := &items[ix]
			if len(it.PullRequest) > 0 && string(it.PullRequest) != "null" {
				continue
			}
			d := IssueData{Number: it.Number, Title: oneLine(it.Title, maxIssueTitle), Body: clipRunes(it.Body, maxIssueBody),
				Labels: []string{}, URL: httpURL(it.HTMLURL), State: "open", UpdatedAt: it.UpdatedAt}
			if it.State == "closed" {
				d.State = "closed"
			}
			if it.User != nil {
				d.Author = oneLine(it.User.Login, 100)
			}
			for _, l := range it.Labels {
				if l.Name != "" && len(d.Labels) < 30 {
					d.Labels = append(d.Labels, oneLine(l.Name, 100))
				}
			}
			out = append(out, d)
		}
		if len(items) < per {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type ghPR struct {
	Number   int    `json:"number"`
	HTMLURL  string `json:"html_url"`
	State    string `json:"state"`
	Draft    bool   `json:"draft"`
	Merged   bool   `json:"merged"`
	MergedAt string `json:"merged_at"`
	Head     struct {
		Ref string `json:"ref"`
		Sha string `json:"sha"`
	} `json:"head"`
}

func (p ghPR) dto() api.TicketPR {
	st := "open"
	switch {
	case p.Merged || p.MergedAt != "":
		st = "merged"
	case p.State == "closed":
		st = "closed"
	}
	return api.TicketPR{Number: p.Number, URL: httpURL(p.HTMLURL), State: st, Draft: p.Draft, HeadSha: p.Head.Sha}
}

// FindPR returns the pull request whose head is branch of the repository's owner, or nil.
func (c *Client) FindPR(ctx context.Context, branch string) (*api.TicketPR, error) {
	p, err := c.repoPath("/pulls?head=" + url.QueryEscape(c.owner+":"+branch) + "&state=all&per_page=10")
	if err != nil {
		return nil, err
	}
	var items []ghPR
	if err := c.callJSON(ctx, http.MethodGet, p, nil, &items); err != nil {
		return nil, err
	}
	for _, it := range items {
		if it.Head.Ref == "" || it.Head.Ref == branch {
			pr := it.dto()
			return &pr, nil
		}
	}
	return nil, nil
}

// PRInput is a new pull request.
type PRInput struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
	Draft bool   `json:"draft"`
}

// CreatePR opens a pull request (gh or token tier).
func (c *Client) CreatePR(ctx context.Context, in PRInput) (api.TicketPR, error) {
	if err := c.needWrite("opening a pull request"); err != nil {
		return api.TicketPR{}, err
	}
	p, err := c.repoPath("/pulls")
	if err != nil {
		return api.TicketPR{}, err
	}
	var pr ghPR
	if err := c.callJSON(ctx, http.MethodPost, p, in, &pr); err != nil {
		return api.TicketPR{}, err
	}
	return pr.dto(), nil
}

// PRState reads pull request n.
func (c *Client) PRState(ctx context.Context, n int) (api.TicketPR, error) {
	p, err := c.repoPath("/pulls/" + strconv.Itoa(n))
	if err != nil {
		return api.TicketPR{}, err
	}
	var pr ghPR
	if err := c.callJSON(ctx, http.MethodGet, p, nil, &pr); err != nil {
		return api.TicketPR{}, err
	}
	return pr.dto(), nil
}

// Comment comments on issue n and returns the comment's link.
func (c *Client) Comment(ctx context.Context, n int, body string) (string, error) {
	if err := c.needWrite("commenting"); err != nil {
		return "", err
	}
	p, err := c.repoPath("/issues/" + strconv.Itoa(n) + "/comments")
	if err != nil {
		return "", err
	}
	var out struct {
		HTMLURL string `json:"html_url"`
	}
	if err := c.callJSON(ctx, http.MethodPost, p, map[string]string{"body": body}, &out); err != nil {
		return "", err
	}
	return httpURL(out.HTMLURL), nil
}

// FindComment returns the link of a comment on issue n, updated at or after since, whose body holds marker (""
// when there is none). It reads at most 300 comments.
func (c *Client) FindComment(ctx context.Context, n int, marker string, since time.Time) (string, error) {
	for page := 1; page <= 3; page++ {
		p, err := c.repoPath(fmt.Sprintf("/issues/%d/comments?per_page=100&page=%d&since=%s", n, page,
			url.QueryEscape(since.UTC().Format(time.RFC3339))))
		if err != nil {
			return "", err
		}
		var list []struct {
			Body    string `json:"body"`
			HTMLURL string `json:"html_url"`
		}
		if err := c.callJSON(ctx, http.MethodGet, p, nil, &list); err != nil {
			return "", err
		}
		for _, cm := range list {
			if strings.Contains(cm.Body, marker) {
				return httpURL(cm.HTMLURL), nil
			}
		}
		if len(list) < 100 {
			break
		}
	}
	return "", nil
}

// CurrentUser is the login of the signed-in identity.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	var u struct {
		Login string `json:"login"`
	}
	if err := c.callJSON(ctx, http.MethodGet, "user", nil, &u); err != nil {
		return "", err
	}
	return u.Login, nil
}

// RepoInfo is a GitHub repository.
type RepoInfo struct {
	Owner    string
	Repo     string
	URL      string
	CloneURL string
	Private  bool
}

type ghRepo struct {
	Name    string `json:"name"`
	Private bool   `json:"private"`
	Owner   struct {
		Login string `json:"login"`
	} `json:"owner"`
	HTMLURL  string `json:"html_url"`
	CloneURL string `json:"clone_url"`
}

func (r ghRepo) info() RepoInfo {
	return RepoInfo{Owner: r.Owner.Login, Repo: r.Name, URL: httpURL(r.HTMLURL), CloneURL: httpURL(r.CloneURL),
		Private: r.Private}
}

// Branch is a branch of a repository and its commit.
type Branch struct {
	Name string
	Sha  string
}

// Branches lists the branches of owner/name (at most 100: enough to tell an empty repository from one in use).
func (c *Client) Branches(ctx context.Context, owner, name string) ([]Branch, error) {
	var list []struct {
		Name   string `json:"name"`
		Commit struct {
			Sha string `json:"sha"`
		} `json:"commit"`
	}
	p := "repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name) + "/branches?per_page=100"
	if err := c.callJSON(ctx, http.MethodGet, p, nil, &list); err != nil {
		return nil, err
	}
	out := make([]Branch, 0, len(list))
	for _, b := range list {
		out = append(out, Branch{Name: b.Name, Sha: b.Commit.Sha})
	}
	return out, nil
}

// GetRepo reads owner/name; nil when it does not exist.
func (c *Client) GetRepo(ctx context.Context, owner, name string) (*RepoInfo, error) {
	var r ghRepo
	err := c.callJSON(ctx, http.MethodGet, "repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name), nil, &r)
	if NotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := r.info()
	return &info, nil
}

// CreateRepo creates a repository of the signed-in user (gh or token tier), without any first commit.
func (c *Client) CreateRepo(ctx context.Context, name string, private bool) (RepoInfo, error) {
	if err := c.needWrite("creating a repository"); err != nil {
		return RepoInfo{}, err
	}
	var r ghRepo
	body := map[string]any{"name": name, "private": private, "auto_init": false}
	if err := c.callJSON(ctx, http.MethodPost, "user/repos", body, &r); err != nil {
		return RepoInfo{}, err
	}
	return r.info(), nil
}

// CompareURL is the web page that opens a pull request of head into base with title and body filled in (the
// manual tier's way to open one).
func (c *Client) CompareURL(base, head, title, body string) string {
	q := []string{"expand=1"}
	if title != "" {
		q = append(q, "title="+url.QueryEscape(title))
	}
	if body != "" {
		q = append(q, "body="+url.QueryEscape(clipRunes(body, CompareBodyMax)))
	}
	return c.host.WebBase() + "/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/compare/" +
		escapeRef(base) + "..." + escapeRef(head) + "?" + strings.Join(q, "&")
}

// PRURL is the web page of pull request n.
func (c *Client) PRURL(n int) string {
	return c.host.WebBase() + "/" + url.PathEscape(c.owner) + "/" + url.PathEscape(c.repo) + "/pull/" + strconv.Itoa(n)
}

func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)

// Checks reads the check runs and the commit statuses of commit sha. A failure to read the statuses leaves only
// the check runs (unless it is an auth or rate-limit error).
func (c *Client) Checks(ctx context.Context, sha string) (api.TicketChecks, error) {
	if !shaRe.MatchString(sha) {
		return api.TicketChecks{}, fmt.Errorf("%w: a commit sha is required", ErrInvalid)
	}
	p, err := c.repoPath("/commits/" + sha + "/check-runs?per_page=100")
	if err != nil {
		return api.TicketChecks{}, err
	}
	runs, err := c.call(ctx, http.MethodGet, p, nil, false)
	if err != nil {
		return api.TicketChecks{}, err
	}
	sp, _ := c.repoPath("/commits/" + sha + "/status?per_page=100")
	statuses, err := c.call(ctx, http.MethodGet, sp, nil, false)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && (ge.Reason == ReasonAuth || ge.Reason == ReasonRateLimited) {
			return api.TicketChecks{}, err
		}
		statuses = nil
	}
	return ParseChecks(runs, statuses, sha), nil
}

// JobLog returns the full log of a GitHub Actions job (gh or token tier).
func (c *Client) JobLog(ctx context.Context, jobID int64) (string, error) {
	if err := c.needWrite("reading CI logs"); err != nil {
		return "", err
	}
	if jobID <= 0 {
		return "", fmt.Errorf("%w: a job id is required", ErrInvalid)
	}
	p, err := c.repoPath("/actions/jobs/" + strconv.FormatInt(jobID, 10) + "/logs")
	if err != nil {
		return "", err
	}
	data, err := c.call(ctx, http.MethodGet, p, nil, true)
	if err == nil || c.tier != api.TierGh {
		return c.Redact(string(data)), err
	}
	var ge *Error
	if errors.As(err, &ge) && (ge.Reason == ReasonAuth || ge.Reason == ReasonRateLimited) {
		return "", err
	}
	slug := c.owner + "/" + c.repo
	if !c.host.IsDotCom() {
		slug = c.host.Name + "/" + slug
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	out, err := c.ghRun(ctx, []string{"run", "view", "--job", strconv.FormatInt(jobID, 10), "--log-failed", "-R", slug}, nil)
	return c.Redact(string(out)), err
}

// httpURL returns u when it is an http(s) URL without spaces, else "".
func httpURL(u string) string {
	if (strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) && !strings.ContainsAny(u, " \t\r\n") {
		return clipRunes(u, 1000)
	}
	return ""
}

// clipRunes cuts s to at most n runes.
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
