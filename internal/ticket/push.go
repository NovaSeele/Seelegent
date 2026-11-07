package ticket

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// What a pull request pushes (old bug D5, and the review's B1: a secret a worker committed and deleted again still
// left in the pushed history). Seelegent never pushes the worker's commits. It makes one commit of its own: the
// worktree's tree as the commit step leaves it, with the commit it pushed before as its parent (the base on the
// first push), so every update is a fast-forward and no force is ever needed. When the base moved between pushes
// (main was merged into the worktree), the commit has two parents, the commit pushed before and the new base, and
// GitHub sees a merge. Only what that commit adds is scanned: each entry that differs from all its parents.
//
// Rules on the remote, checked before anything is sent:
//   - the base must be on the remote already: its other commits would leave unscanned otherwise;
//   - the remote branch must still be the commit Seelegent pushed before: a branch someone changed on GitHub is
//     never built on or forced (branch_diverged; the outbox entry fails).

// pushPlan is the commit a push will send.
type pushPlan struct {
	// target is where the push goes (origin's push URL, or "origin").
	target string
	// tip is the remote branch's commit before the push ("" when it has none).
	tip string
	// prev is the commit pushed before ("" on the first push) and prevBase the base it built on.
	prev, prevBase string
	// adopted is set when prev is not what the ticket recorded: "own" (a push of Seelegent's whose recording was
	// lost to a stop or a cancelled request) or "legacy" (the worker's history the previous version pushed).
	adopted string
	// base is the commit of the base branch the pushed commit builds on.
	base    string
	parents []string
	// tree is the tree to push, as the commit step leaves the worktree.
	tree string
}

// pushTarget is where a push of the ticket goes: origin's push URL, or "origin" when it cannot be read.
func (s *Service) pushTarget(ctx context.Context, wt string) string {
	if u, ok := s.gitOK(ctx, wt, "remote", "get-url", "--push", "origin"); ok && u != "" &&
		!strings.HasPrefix(u, "-") && !strings.Contains(u, "::") {
		return u
	}
	return "origin"
}

// remoteRef is the commit target holds at refs/heads/<branch> ("" when none). An unreachable remote is an error.
func (s *Service) remoteRef(ctx context.Context, wt, target, branch string) (string, error) {
	out, err := s.git(ctx, wt, gitx.ExecOptions{Timeout: time.Minute}, "ls-remote", "--heads", target,
		"refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == "refs/heads/"+branch {
			return f[0], nil
		}
	}
	return "", nil
}

// remoteSha is the commit origin's push URL holds for the ticket branch ("" when none or unreachable).
func (s *Service) remoteSha(ctx context.Context, t *ticketRec) string {
	sha, err := s.remoteRef(ctx, t.Worktree, s.pushTarget(ctx, t.Worktree), t.Branch)
	if err != nil {
		return ""
	}
	return sha
}

// hasObject reports whether the repository at dir has the commit sha.
func (s *Service) hasObject(ctx context.Context, dir, sha string) bool {
	_, ok := s.gitOK(ctx, dir, "cat-file", "-e", sha+"^{commit}")
	return ok
}

// ensureObject fetches branch from target when the repository lacks sha (objects only, no local ref).
func (s *Service) ensureObject(ctx context.Context, dir, target, branch, sha string) error {
	if s.hasObject(ctx, dir, sha) {
		return nil
	}
	if _, err := s.git(ctx, dir, gitx.ExecOptions{Timeout: pushTimeout}, "fetch", "--quiet", "--no-tags", target,
		"refs/heads/"+branch); err != nil {
		return err
	}
	if !s.hasObject(ctx, dir, sha) {
		return fmt.Errorf("%w: commit %s is not on the remote branch %s", ErrBadState, short(sha), branch)
	}
	return nil
}

// isAncestor reports whether commit a is an ancestor of (or is) b.
func (s *Service) isAncestor(ctx context.Context, dir, a, b string) bool {
	_, ok := s.gitOK(ctx, dir, "merge-base", "--is-ancestor", a, b)
	return ok
}

// treeOf is the tree of a commit.
func (s *Service) treeOf(ctx context.Context, dir, commit string) (string, error) {
	return s.git(ctx, dir, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "rev-parse", "--verify", commit+"^{tree}")
}

// currentBase is the commit of the base branch the worktree builds on: the merge base of HEAD and the local base
// branch when it is at or after the ticket's base (main was merged in), else the ticket's base.
func (s *Service) currentBase(ctx context.Context, t *ticketRec) string {
	mb, ok := s.gitOK(ctx, t.Worktree, "merge-base", "HEAD", "refs/heads/"+t.Base)
	if ok && mb != "" && t.BaseSha != "" && s.isAncestor(ctx, t.Worktree, t.BaseSha, mb) {
		return mb
	}
	return t.BaseSha
}

// baseOnRemote refuses a base with commits the remote's base branch does not have: they would be pushed with the
// ticket's commit, unscanned.
func (s *Service) baseOnRemote(ctx context.Context, t *ticketRec, target, base string) error {
	tip, err := s.remoteRef(ctx, t.Worktree, target, t.Base)
	if err != nil {
		return err
	}
	if tip == "" {
		return &PushError{Reason: "base_not_pushed", Details: map[string]any{"branch": t.Base, "commits": 0},
			Message: fmt.Sprintf("the base branch %s is not on GitHub; push it first", t.Base)}
	}
	if err := s.ensureObject(ctx, t.Worktree, target, t.Base, tip); err != nil {
		return err
	}
	n, err := s.git(ctx, t.Worktree, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "rev-list", "--count", base,
		"--not", tip)
	if err != nil {
		return err
	}
	if count, _ := strconv.Atoi(n); count > 0 {
		return &PushError{Reason: "base_not_pushed", Details: map[string]any{"branch": t.Base, "commits": count},
			Message: fmt.Sprintf("the base has %d commit(s) that are not on GitHub; push %s first", count, t.Base)}
	}
	return nil
}

// divergedErr is a remote branch that is no longer the commit Seelegent pushed.
func divergedErr(branch, prev, tip string) error {
	msg := fmt.Sprintf("the branch %s on GitHub changed since Seelegent pushed it (it is %s, Seelegent pushed %s): "+
		"someone pushed to it or updated it on GitHub; Seelegent never builds on such a branch or forces it", branch,
		short(tip), short(prev))
	if prev == "" {
		msg = fmt.Sprintf("a branch %s already exists on GitHub and Seelegent did not push it; Seelegent never "+
			"builds on such a branch or forces it", branch)
	}
	return &PushError{Reason: "branch_diverged", Message: msg,
		Details: map[string]any{"branch": branch, "remote": tip, "pushed": prev}}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// ownPush finds the push of ticket id that sent sha (its outbox entry, in any state, names the commit before the
// push runs) and the base that commit built on. An outbox that cannot be read is an error, never "not ours" (which
// would refuse the push as branch_diverged).
func (s *Service) ownPush(ctx context.Context, id, sha string) (base string, ok bool, err error) {
	if sha == "" {
		return "", false, nil
	}
	err = s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		list, err := listEntries(ctx, tx, scopeTicket, id)
		if err != nil {
			return err
		}
		for _, e := range list {
			if a, _ := e.Args["sha"].(string); e.Kind == api.OutboxPush && a == sha {
				base, _ = e.Args["base"].(string)
				ok = true
			}
		}
		return nil
	})
	if err != nil {
		return "", false, fmt.Errorf("read the ticket's pushes: %w", err)
	}
	return base, ok, nil
}

// planPush works out the commit a push of the ticket sends: its parents, after checking the remote branch and the
// base. tree is filled in by snapshot.
func (s *Service) planPush(ctx context.Context, t *ticketRec) (*pushPlan, error) {
	p := &pushPlan{target: s.pushTarget(ctx, t.Worktree), prev: t.PushedSha}
	tip, err := s.remoteRef(ctx, t.Worktree, p.target, t.Branch)
	if err != nil {
		return nil, err
	}
	p.tip = tip
	pushedBase := t.PushedBase
	if tip != p.prev {
		base, own, err := s.ownPush(ctx, t.ID, tip)
		if err != nil {
			return nil, err
		}
		switch {
		case own:
			// Seelegent's own commit: a push whose recording was lost (the request went away or the daemon stopped
			// after GitHub took it). The outbox wrote its sha before the push.
			p.prev, pushedBase, p.adopted = tip, base, "own"
		case p.prev == "" && s.isAncestor(ctx, t.Worktree, tip, "HEAD"):
			// A branch the version before this one pushed (the worker's history, already public): build on it.
			p.prev, p.adopted = tip, "legacy"
		default:
			return nil, divergedErr(t.Branch, p.prev, tip)
		}
	}
	if p.prev != "" {
		if err := s.ensureObject(ctx, t.Worktree, p.target, t.Branch, p.prev); err != nil {
			return nil, err
		}
		if pushedBase == "" {
			pushedBase = t.BaseSha
		}
	}
	p.prevBase = pushedBase
	p.base = s.currentBase(ctx, t)
	switch {
	case p.prev == "":
		p.parents = []string{p.base}
	case p.base != pushedBase && !s.isAncestor(ctx, t.Worktree, p.base, p.prev):
		p.parents = []string{p.prev, p.base}
	default:
		p.parents = []string{p.prev}
		p.base = pushedBase
	}
	if p.prev == "" || len(p.parents) == 2 {
		if err := s.baseOnRemote(ctx, t, p.target, p.base); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// snapshot fills in the tree the push sends: the worktree as the commit step will leave it (HEAD plus the
// uncommitted files git does not ignore), written through a temporary index; the shared-folder links stay out.
func (s *Service) snapshot(ctx context.Context, t *ticketRec, p *pushPlan) error {
	tmp, err := os.MkdirTemp("", "seelegent-push-")
	if err != nil {
		return s.scanErr(err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	var exclude []string
	if t.Env != nil {
		exclude = t.Env.Linked
	}
	tree, err := s.o.Git.SnapshotTree(ctx, t.Worktree, tmp, exclude...)
	if err != nil {
		return s.scanErr(err)
	}
	p.tree = tree
	return nil
}

// added lists the blobs the pushed commit adds: the entries of its tree that differ from every parent.
func (s *Service) added(ctx context.Context, wt string, p *pushPlan) ([]object, error) {
	var sets []map[string]object
	for _, parent := range p.parents {
		set, err := s.changedObjects(ctx, wt, parent, p.tree)
		if err != nil {
			return nil, err
		}
		sets = append(sets, set)
	}
	var out []object
	for path, o := range sets[0] {
		inAll := true
		for _, other := range sets[1:] {
			if _, ok := other[path]; !ok {
				inAll = false
			}
		}
		if inAll {
			out = append(out, o)
		}
	}
	return out, nil
}

// commitTree makes the commit the push sends. The repository's identity is used; without one, Seelegent's.
func (s *Service) commitTree(ctx context.Context, wt string, p *pushPlan, message string) (string, error) {
	args := []string{"commit-tree", p.tree}
	for _, parent := range p.parents {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	opt := gitx.ExecOptions{Timeout: gitx.ReadTimeout}
	out, err := s.git(ctx, wt, opt, append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	var ge *GitError
	if err != nil && errors.As(err, &ge) && noIdentity(ge.Message) {
		out, err = s.git(ctx, wt, opt, append(gitx.Identity(), args...)...)
	}
	return out, err
}

// noIdentity reports whether git refused to commit for want of a name or an email.
func noIdentity(stderr string) bool {
	return strings.Contains(stderr, "Please tell me who you are") || strings.Contains(stderr, "empty ident") ||
		strings.Contains(stderr, "unable to auto-detect email address")
}
