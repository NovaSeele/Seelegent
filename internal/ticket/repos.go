package ticket

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/github"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/worktree"
)

// Access returns how GitHub is reached (force: detect again now).
func (s *Service) Access(ctx context.Context, force bool) api.GitHubAccess {
	return s.o.GitHub.Access(ctx, force).DTO(s.o.GitHub.Hosts())
}

// SetToken stores a GitHub token (dashboard only) and returns its hint.
func (s *Service) SetToken(token string) (api.GitHubTokenResult, error) {
	hint, err := s.o.GitHub.SaveToken(token)
	if err != nil {
		return api.GitHubTokenResult{}, err
	}
	s.log.Info("github: token stored", "hint", hint)
	return api.GitHubTokenResult{TokenHint: hint}, nil
}

// RemoveToken deletes the stored token (dashboard only).
func (s *Service) RemoveToken() error {
	err := s.o.GitHub.RemoveToken()
	if err == nil {
		s.log.Info("github: token removed")
	}
	return err
}

// repoView is a repository as the API shows it.
func (s *Service) repoView(ctx context.Context, tx *sql.Tx, r *repoRec) (api.Repo, error) {
	g := s.gh(r)
	v := api.Repo{ID: r.ID, Path: r.Path, Name: r.Name, Owner: g.owner, Repo: g.name, GitHub: g.ok(),
		NotGitHub: g.why, RemoteURL: r.RemoteURL, DefaultBranch: r.DefaultBranch, SharedDirs: api.NonNil(r.SharedDirs),
		CreatedAt: api.FormatTime(r.CreatedAt), LastSyncAt: api.FormatTime(r.LastSyncAt), SyncError: r.SyncError}
	if g.ok() {
		v.Host = g.host.Name
	}
	if r.HasOrigin {
		v.Remote = "origin"
	}
	issues, err := listIssueRecs(ctx, tx, r.ID)
	if err != nil {
		return api.Repo{}, err
	}
	for _, it := range issues {
		switch {
		case it.Decision != nil && it.Decision.Action == api.TriageSkip:
			v.Counts.Skipped++
		case it.State == "open" && it.Decision == nil:
			v.Counts.Inbox++
		}
	}
	tickets, err := listTicketRecs(ctx, tx, r.ID, "")
	if err != nil {
		return api.Repo{}, err
	}
	for _, t := range tickets {
		switch t.Status {
		case api.TicketWorking, api.TicketReview:
			v.Counts.Working++
		case api.TicketReady:
			v.Counts.WaitingPr++
		case api.TicketPROpen, api.TicketDone:
			v.Counts.Sent++
		}
	}
	if v.Outbox, err = listEntries(ctx, tx, scopeRepo, r.ID); err != nil {
		return api.Repo{}, err
	}
	return v, nil
}

// Repos lists the connected repositories.
func (s *Service) Repos(ctx context.Context) ([]api.Repo, error) {
	out := []api.Repo{}
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = out[:0]
		list, err := listRepoRecs(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range list {
			v, err := s.repoView(ctx, tx, r)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// ResolveRepo finds a connected repository by id or by its path (or a folder inside it).
func (s *Service) ResolveRepo(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", invalidf("repo is required")
	}
	var id string
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		list, err := listRepoRecs(ctx, tx)
		if err != nil {
			return err
		}
		for _, r := range list {
			if r.ID == ref {
				id = r.ID
				return nil
			}
		}
		if !filepath.IsAbs(ref) {
			return nil
		}
		k, err := fsutil.Key(ref)
		if err != nil {
			return nil //nolint:nilerr // not a path of a connected repository
		}
		for _, r := range list {
			rk, err := fsutil.Key(r.Path)
			if err == nil && (rk == k || strings.HasPrefix(k, rk+string(filepath.Separator))) {
				id = r.ID
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", notFoundf("no connected repository %s", ref)
	}
	return id, nil
}

// Repo returns one repository.
func (s *Service) Repo(ctx context.Context, id string) (api.Repo, error) {
	var v api.Repo
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		r, err := loadRepo(ctx, tx, id)
		if err != nil {
			return err
		}
		v, err = s.repoView(ctx, tx, r)
		return err
	})
	return v, err
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// remoteInfo reads origin and the default branch of the repository at root.
func (s *Service) remoteInfo(ctx context.Context, root string) (hasOrigin bool, remoteURL, def string) {
	if u, ok := s.gitOK(ctx, root, "remote", "get-url", "origin"); ok && u != "" {
		hasOrigin, remoteURL = true, github.StripCredentials(u)
	}
	if sym, ok := s.gitOK(ctx, root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); ok && sym != "" {
		def = strings.TrimPrefix(sym, "origin/")
	}
	if def == "" {
		for _, b := range []string{"main", "master"} {
			if _, ok := s.gitOK(ctx, root, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); ok {
				def = b
				break
			}
		}
	}
	if def == "" {
		if cur, ok := s.gitOK(ctx, root, "symbolic-ref", "--quiet", "--short", "HEAD"); ok && cur != "" {
			def = cur
		}
	}
	if def == "" {
		def = "main"
	}
	return hasOrigin, remoteURL, def
}

// Connect connects the repository at req.Path, or with Init makes one there first.
func (s *Service) Connect(ctx context.Context, req api.RepoConnectRequest) (api.Repo, error) {
	p := strings.TrimSpace(req.Path)
	if p == "" || !filepath.IsAbs(p) {
		return api.Repo{}, invalidf("path must be an absolute folder")
	}
	if req.Name != "" && !repoNameRe.MatchString(req.Name) {
		return api.Repo{}, invalidf(`name may use letters, digits, ".", "_" and "-" (at most 100)`)
	}
	p = filepath.Clean(p)
	if req.Init {
		if err := s.initRepo(ctx, p, req.FirstCommit == nil || *req.FirstCommit); err != nil {
			return api.Repo{}, err
		}
	}
	if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
		return api.Repo{}, invalidf("folder does not exist: %s", p)
	}
	top, ok := s.gitOK(ctx, p, "rev-parse", "--show-toplevel")
	if !ok || top == "" {
		return api.Repo{}, invalidf("not inside a git work tree: %s", p)
	}
	root, err := fsutil.Canonical(filepath.FromSlash(top))
	if err != nil {
		root = filepath.Clean(filepath.FromSlash(top))
	}
	if base := s.o.WorktreeBase; base != "" {
		if bk, err := fsutil.Key(base); err == nil {
			if rk, err := fsutil.Key(root); err == nil && (rk == bk || strings.HasPrefix(rk, bk+string(filepath.Separator))) {
				return api.Repo{}, invalidf("that folder is a Seelegent ticket worktree; connect the main repository")
			}
		}
	}
	key, err := fsutil.Key(root)
	if err != nil {
		return api.Repo{}, invalidf("%v", err)
	}
	id := repoID(key)
	hasOrigin, remoteURL, def := s.remoteInfo(ctx, root)
	var v api.Repo
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadRepo(ctx, tx, id)
		switch {
		case errors.Is(err, ErrNotFound):
			r = &repoRec{ID: id, Path: root, Name: filepath.Base(root), SharedDirs: []string{}, CreatedAt: s.o.Now(),
				NextLocalID: 1}
		case err != nil:
			return err
		}
		r.HasOrigin, r.RemoteURL, r.DefaultBranch = hasOrigin, remoteURL, def
		if rem, ok := github.ParseRemote(remoteURL); ok && r.Name == filepath.Base(root) {
			r.Name = rem.Repo
		}
		if req.Name != "" {
			r.Name = req.Name
		}
		if err := saveRepo(ctx, tx, r, key); err != nil {
			return err
		}
		v, err = s.repoView(ctx, tx, r)
		return err
	})
	if err != nil {
		return api.Repo{}, err
	}
	s.o.IssuesChanged(id)
	return v, nil
}

// initRepo makes the folder a git repository (branch main) with an empty first commit when asked.
func (s *Service) initRepo(ctx context.Context, p string, firstCommit bool) error {
	if err := os.MkdirAll(p, 0o755); err != nil { //nolint:gosec // the user's own project folder
		return invalidf("make %s: %v", p, err)
	}
	setup := gitx.ExecOptions{Setup: true}
	top, ok := s.gitOK(ctx, p, "rev-parse", "--show-toplevel")
	isRoot := false
	if ok && top != "" {
		a, e1 := fsutil.Key(filepath.FromSlash(top))
		b, e2 := fsutil.Key(p)
		isRoot = e1 == nil && e2 == nil && a == b
	}
	if !isRoot {
		if _, err := s.git(ctx, p, setup, "init", "-q", "-b", "main"); err != nil {
			if _, err := s.git(ctx, p, setup, "init", "-q"); err != nil {
				return err
			}
		}
	}
	if firstCommit {
		if _, ok := s.gitOK(ctx, p, "rev-parse", "--verify", "--quiet", "HEAD"); !ok {
			if _, err := s.git(ctx, p, setup, "commit", "--allow-empty", "-q", "-m", "Initial commit"); err != nil {
				return err
			}
		}
	}
	return nil
}

// UpdateRepo sets the repository's shared folders.
func (s *Service) UpdateRepo(ctx context.Context, id string, patch api.RepoPatch) (api.Repo, error) {
	shared, err := worktree.NormalizeShared(patch.SharedDirs)
	if err != nil {
		return api.Repo{}, invalidf("%v", err)
	}
	var v api.Repo
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadRepo(ctx, tx, id)
		if err != nil {
			return err
		}
		key, err := fsutil.Key(r.Path)
		if err != nil {
			return err
		}
		r.SharedDirs = shared
		if err := saveRepo(ctx, tx, r, key); err != nil {
			return err
		}
		v, err = s.repoView(ctx, tx, r)
		return err
	})
	if err != nil {
		return api.Repo{}, err
	}
	s.o.IssuesChanged(id)
	return v, nil
}

// SharedDirs returns the shared folders of the connected repository at root (contest worktrees use them too).
func (s *Service) SharedDirs(root string) []string {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	k, err := fsutil.Key(root)
	if err != nil {
		return nil
	}
	r, err := s.getRepo(ctx, repoID(k))
	if err != nil {
		return nil
	}
	return r.SharedDirs
}

// Disconnect forgets a repository, its inbox and its finished tickets. Refused while a ticket is in progress or
// still has its worktree.
func (s *Service) Disconnect(ctx context.Context, id string) error {
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := loadRepo(ctx, tx, id); err != nil {
			return err
		}
		tickets, err := listTicketRecs(ctx, tx, id, "")
		if err != nil {
			return err
		}
		var busy []string
		for _, t := range tickets {
			if !finished(t.Status) || !t.WorktreeRemoved && t.Worktree != "" && t.BaseSha != "" {
				busy = append(busy, t.ID+" ("+t.Status+")")
			}
		}
		if len(busy) > 0 {
			return badStatef("the repository has tickets in progress or with a worktree: %s; drop and clean them up "+
				"first", strings.Join(busy, ", "))
		}
		for _, q := range []string{
			`DELETE FROM ticket_log WHERE ticket_id IN (SELECT id FROM tickets WHERE repo_id = ?)`,
			`DELETE FROM github_outbox WHERE scope = 'ticket' AND ref IN (SELECT id FROM tickets WHERE repo_id = ?)`,
			`DELETE FROM tickets WHERE repo_id = ?`,
			`DELETE FROM github_issues WHERE repo_id = ?`,
			`DELETE FROM github_outbox WHERE scope = 'repo' AND ref = ?`,
			`DELETE FROM github_repos WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.o.IssuesChanged(id)
	}
	return err
}

// CreateRemote creates the GitHub repository of a connected repository that has no origin yet (gh or token tier)
// on github.com, adds it as origin and pushes the default branch once. Write-ahead like the ticket outbox.
func (s *Service) CreateRemote(ctx context.Context, id string, req api.RemoteRequest) (api.Repo, error) {
	unlock, err := s.locks.lock(ctx, "repo:"+id)
	if err != nil {
		return api.Repo{}, err
	}
	defer unlock()
	r, err := s.getRepo(ctx, id)
	if err != nil {
		return api.Repo{}, err
	}
	if s.gh(r).ok() {
		return api.Repo{}, badStatef("the repository is already on GitHub")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = r.Name
	}
	if !repoNameRe.MatchString(name) {
		return api.Repo{}, invalidf(`name may use letters, digits, ".", "_" and "-" (at most 100)`)
	}
	private := req.Private == nil || *req.Private
	access := s.o.GitHub.Access(ctx, true)
	if !access.CanWrite() {
		return api.Repo{}, github.ErrManual
	}
	var entry api.OutboxEntry
	retry := false
	now := api.FormatTime(s.o.Now())
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		list, err := listEntries(ctx, tx, scopeRepo, id)
		if err != nil {
			return err
		}
		var open *api.OutboxEntry
		for i := range list {
			if e := list[i]; e.Kind == api.OutboxCreateRemote && (e.Status == api.OutboxPending || e.Status == api.OutboxUnknown ||
				e.Status == api.OutboxFailed) {
				open = &list[i]
			}
		}
		if r.HasOrigin && open == nil {
			return badStatef("the repository already has an origin remote")
		}
		if open != nil {
			entry, retry = *open, open.Status == api.OutboxUnknown || open.Status == api.OutboxFailed
		} else {
			entry = api.OutboxEntry{Kind: api.OutboxCreateRemote, Result: map[string]any{}}
		}
		entry.Args = map[string]any{"name": name, "private": private}
		entry.Status, entry.At, entry.Error = api.OutboxRunning, now, ""
		entry.ID, err = saveEntry(ctx, tx, scopeRepo, id, entry)
		return err
	})
	if err != nil {
		return api.Repo{}, err
	}
	finish := func(result map[string]any, ferr error) {
		_ = s.o.DB.Write(context.WithoutCancel(ctx), func(tx *sql.Tx) error {
			if ferr != nil {
				entry.Status, entry.Error = api.OutboxFailed, s.redactErr(ferr, 600)
			} else {
				entry.Status, entry.Result, entry.DoneAt = api.OutboxDone, result, api.FormatTime(s.o.Now())
			}
			_, err := saveEntry(ctx, tx, scopeRepo, id, entry)
			return err
		})
		s.o.IssuesChanged(id)
	}
	client, err := s.o.GitHub.Client(ctx, access, github.DotCom, "", "")
	if err != nil {
		finish(nil, err)
		return api.Repo{}, err
	}
	var created *github.RepoInfo
	if retry {
		// An earlier attempt may have created it before the daemon stopped: adopt it instead of failing.
		user := access.GhUser
		if user == "" {
			user, _ = client.CurrentUser(ctx)
		}
		if user != "" {
			if created, err = client.GetRepo(ctx, user, name); err != nil {
				finish(nil, err)
				return api.Repo{}, err
			}
		}
	}
	if created == nil {
		info, err := client.CreateRepo(ctx, name, private)
		if err != nil {
			finish(nil, err)
			return api.Repo{}, err
		}
		created = &info
	}
	cloneURL := github.StripCredentials(created.CloneURL)
	if cloneURL == "" {
		cloneURL = "https://github.com/" + created.Owner + "/" + created.Repo + ".git"
	}
	if _, ok := s.gitOK(ctx, r.Path, "remote", "get-url", "origin"); !ok {
		if _, err := s.git(ctx, r.Path, gitx.ExecOptions{Setup: true}, "remote", "add", "origin", "--", cloneURL); err != nil {
			finish(nil, err)
			return api.Repo{}, err
		}
	}
	b := r.DefaultBranch
	if _, ok := s.gitOK(ctx, r.Path, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); ok && gitx.ValidBranch(b) {
		// The first publish of a new, empty repository: the only push of a branch other than a ticket's.
		if _, err := s.git(ctx, r.Path, gitx.ExecOptions{PushBranch: b, Timeout: 5 * time.Minute}, "push", "origin",
			"refs/heads/"+b+":refs/heads/"+b); err != nil {
			finish(nil, err)
			return api.Repo{}, err
		}
	}
	finish(map[string]any{"owner": created.Owner, "repo": created.Repo, "url": created.URL}, nil)
	hasOrigin, remoteURL, def := s.remoteInfo(ctx, r.Path)
	var v api.Repo
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		cur, err := loadRepo(ctx, tx, id)
		if err != nil {
			return err
		}
		key, err := fsutil.Key(cur.Path)
		if err != nil {
			return err
		}
		cur.HasOrigin, cur.RemoteURL, cur.DefaultBranch = hasOrigin, remoteURL, def
		if err := saveRepo(ctx, tx, cur, key); err != nil {
			return err
		}
		v, err = s.repoView(ctx, tx, cur)
		return err
	})
	if err != nil {
		return api.Repo{}, err
	}
	s.o.IssuesChanged(id)
	return v, nil
}
