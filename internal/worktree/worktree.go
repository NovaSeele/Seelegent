// Package worktree makes and removes the git worktrees Seelegent owns (contest variants now, tickets in milestone
// 15) and gives each one the environment of the main checkout (contract §6.3): the gitignored files listed in
// `.worktreeinclude` are copied, the shared folders (such as node_modules) are linked, and everything left out is
// reported with a reason.
//
// Safety: worktrees live under one base folder of the data directory and are detached (no branch). Removal never
// forces: the shared-folder links are removed first, without following them, then everything in the worktree is
// committed inside it (its own index; old bug D21: the links used to be committed when a worker had changed
// .gitignore), and only then `git worktree remove` runs, which refuses a dirty worktree. Git runs through the safe
// runner of gitx.
package worktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Limits of the worktree environment.
const (
	MaxInclude   = 200
	MaxShared    = 20
	MaxCopyBytes = 50 << 20
	maxRelLen    = 300
)

// IncludeFile is the file of the main checkout that lists the gitignored files a worktree gets a copy of.
const IncludeFile = ".worktreeinclude"

// ErrInvalid is matched by a bad shared-folder list.
var ErrInvalid = errors.New("invalid worktree setting")

// Git runs the safe git commands (gitx.Repos.Exec).
type Git interface {
	Exec(ctx context.Context, dir string, opt gitx.ExecOptions, args ...string) ([]byte, error)
}

// Trees makes and removes the worktrees under Base.
type Trees struct {
	Git  Git
	Base string
}

// RelPath checks a repository-relative path from user input: '/' separators, no "./", one leading "/" allowed
// (gitignore style), no trailing "/", nothing outside the repository or inside .git. It returns the clean path, or
// "" and the reason.
func RelPath(raw string) (rel, reason string) {
	s := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	switch {
	case strings.HasPrefix(s, "./"):
		s = s[2:]
	case strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//"):
		s = s[1:]
	}
	s = strings.TrimRight(s, "/")
	if s == "" || s == "." {
		return "", "empty path"
	}
	if len(s) > maxRelLen {
		return "", "path is too long"
	}
	if strings.HasPrefix(s, "/") || filepath.IsAbs(s) || (len(s) > 1 && s[1] == ':') {
		return "", "absolute path; use a repo-relative path"
	}
	for _, r := range s {
		if r < 0x20 || r == ':' {
			return "", "unsupported character in path"
		}
	}
	var segs []string
	for _, seg := range strings.Split(s, "/") {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", "outside the repo (..)"
		}
		segs = append(segs, seg)
	}
	if len(segs) == 0 {
		return "", "empty path"
	}
	if strings.EqualFold(segs[0], ".git") {
		return "", "inside .git"
	}
	return strings.Join(segs, "/"), ""
}

func relKey(rel string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(rel)
	}
	return rel
}

// ParseInclude reads a .worktreeinclude: one literal repository-relative path per line, # comments. Globs and
// negations are skipped with a reason.
func ParseInclude(text string) (paths []string, skipped []api.EnvSkip) {
	seen := map[string]bool{}
	text = strings.TrimPrefix(text, "\ufeff")
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "!") {
			skipped = append(skipped, api.EnvSkip{Path: line, Reason: "negation is not supported"})
			continue
		}
		if strings.ContainsAny(line, "*?[]") {
			skipped = append(skipped, api.EnvSkip{Path: line, Reason: "glob patterns are not supported; list literal paths"})
			continue
		}
		rel, why := RelPath(line)
		if rel == "" {
			skipped = append(skipped, api.EnvSkip{Path: line, Reason: why})
			continue
		}
		if seen[relKey(rel)] {
			continue
		}
		seen[relKey(rel)] = true
		if len(paths) >= MaxInclude {
			skipped = append(skipped, api.EnvSkip{Path: rel, Reason: fmt.Sprintf("more than %d entries", MaxInclude)})
			continue
		}
		paths = append(paths, rel)
	}
	return paths, skipped
}

// NormalizeShared checks a shared-folder list: repository-relative folders, no globs, at most MaxShared,
// duplicates dropped.
func NormalizeShared(list []string) ([]string, error) {
	if len(list) > MaxShared {
		return nil, fmt.Errorf("%w: at most %d shared folders", ErrInvalid, MaxShared)
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range list {
		if strings.ContainsAny(raw, "*?[]!") {
			return nil, fmt.Errorf("%w: shared folder %q: globs and negations are not supported", ErrInvalid, raw)
		}
		rel, why := RelPath(raw)
		if rel == "" {
			return nil, fmt.Errorf("%w: shared folder %q: %s", ErrInvalid, raw, why)
		}
		if !seen[relKey(rel)] {
			seen[relKey(rel)] = true
			out = append(out, rel)
		}
	}
	return out, nil
}

// inside reports whether path is base or below it (both canonical).
func inside(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func canonical(p string) string {
	if c, err := fsutil.Canonical(p); err == nil {
		return c
	}
	return filepath.Clean(p)
}

// owned checks that wt is a folder below Base (never Base itself, never the main checkout).
func (t *Trees) owned(wt string) (string, error) {
	base := canonical(t.Base)
	w := canonical(wt)
	if w == base || !inside(base, w) {
		return "", fmt.Errorf("worktree %s is not below %s", wt, t.Base)
	}
	return w, nil
}

// Add makes a detached worktree of the repository at root at dir (below Base, not existing yet) on commit, and
// returns its canonical path.
func (t *Trees) Add(ctx context.Context, root, dir, commit string) (string, error) {
	if _, err := t.owned(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", fmt.Errorf("worktree folder: %w", err)
	}
	if _, err := t.Git.Exec(ctx, root, gitx.ExecOptions{}, "worktree", "add", "--detach", dir, commit); err != nil {
		return "", err
	}
	return canonical(dir), nil
}

// Setup gives the worktree wt of the repository at root its environment: a copy of each gitignored file listed in
// root/.worktreeinclude and a link to each shared folder. It never fails: every problem is a skipped entry.
func (t *Trees) Setup(ctx context.Context, root, wt string, shared []string) api.WorktreeEnv {
	env := api.WorktreeEnv{Copied: []string{}, Linked: []string{}, Skipped: []api.EnvSkip{}}
	root = canonical(root)
	data, err := os.ReadFile(filepath.Join(root, IncludeFile))
	switch {
	case err == nil:
		paths, skipped := ParseInclude(string(data))
		env.Skipped = append(env.Skipped, skipped...)
		for _, rel := range paths {
			if why := t.copyInclude(ctx, root, wt, rel); why != "" {
				env.Skipped = append(env.Skipped, api.EnvSkip{Path: rel, Reason: why})
			} else {
				env.Copied = append(env.Copied, rel)
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		env.Skipped = append(env.Skipped, api.EnvSkip{Path: IncludeFile, Reason: "unreadable: " + err.Error()})
	}
	for _, raw := range shared {
		rel, why := RelPath(raw)
		if rel == "" {
			env.Skipped = append(env.Skipped, api.EnvSkip{Path: raw, Reason: why})
			continue
		}
		if why := t.linkShared(ctx, root, wt, rel); why != "" {
			env.Skipped = append(env.Skipped, api.EnvSkip{Path: rel, Reason: why})
		} else {
			env.Linked = append(env.Linked, rel)
		}
	}
	return env
}

func (t *Trees) tracked(ctx context.Context, root, rel string) bool {
	_, err := t.Git.Exec(ctx, root, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "ls-files", "--error-unmatch", "--",
		rel)
	return err == nil
}

func (t *Trees) ignored(ctx context.Context, root, rel string) bool {
	// check-ignore refuses literal pathspecs; rel has no magic or glob characters (RelPath, ParseInclude).
	_, err := t.Git.Exec(ctx, root, gitx.ExecOptions{Timeout: gitx.ReadTimeout, Env: []string{"GIT_LITERAL_PATHSPECS=0"}},
		"check-ignore", "-q", "--", rel)
	return err == nil
}

// showsInStatus reports whether git in the worktree sees rel as a new file (so `git add -A` would take it).
func (t *Trees) showsInStatus(ctx context.Context, wt, rel string) bool {
	out, err := t.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain",
		"--untracked-files=normal", "--", rel)
	return err != nil || strings.TrimSpace(string(out)) != ""
}

// dest is the place of rel inside the worktree, its parent folders made and checked to stay inside it.
func dest(wt, rel string) (path, reason string) {
	d := filepath.Join(wt, filepath.FromSlash(rel))
	if _, err := os.Lstat(d); err == nil {
		return "", "already exists in the worktree"
	}
	if err := os.MkdirAll(filepath.Dir(d), 0o700); err != nil {
		return "", "cannot make its folder: " + err.Error()
	}
	if !inside(canonical(wt), canonical(filepath.Dir(d))) {
		return "", "outside the worktree"
	}
	return d, ""
}

func (t *Trees) copyInclude(ctx context.Context, root, wt, rel string) string {
	src := filepath.Join(root, filepath.FromSlash(rel))
	if _, err := os.Lstat(src); err != nil {
		return "missing in the main checkout"
	}
	if !inside(root, canonical(src)) {
		return "outside the repo"
	}
	st, err := os.Stat(src)
	switch {
	case err != nil:
		return "unreadable: " + err.Error()
	case st.IsDir():
		return "is a folder; list files here, or add the folder to the shared folders"
	case !st.Mode().IsRegular():
		return "not a regular file"
	case st.Size() > MaxCopyBytes:
		return "larger than 50 MB"
	}
	if t.tracked(ctx, root, rel) {
		return "tracked by git (the worktree already has it)"
	}
	if !t.ignored(ctx, root, rel) {
		return "not gitignored (it would be committed)"
	}
	d, why := dest(wt, rel)
	if d == "" {
		return why
	}
	if err := copyFile(src, d); err != nil {
		return "copy failed: " + err.Error()
	}
	if t.showsInStatus(ctx, wt, rel) {
		_ = os.Remove(d)
		return "not ignored in the worktree (it would be committed)"
	}
	return ""
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, MaxCopyBytes+1)); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

func (t *Trees) linkShared(ctx context.Context, root, wt, rel string) string {
	src := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Stat(src)
	if err != nil {
		return "missing in the main checkout"
	}
	if !st.IsDir() {
		return "not a folder"
	}
	if !inside(root, canonical(src)) {
		return "outside the repo"
	}
	if t.tracked(ctx, root, rel) {
		return "tracked by git"
	}
	if !t.ignored(ctx, root, rel) {
		return "not gitignored"
	}
	d, why := dest(wt, rel)
	if d == "" {
		return why
	}
	if err := platform.LinkInto(src, d); err != nil {
		return "link failed: " + err.Error()
	}
	if t.showsInStatus(ctx, wt, rel) {
		_, _ = platform.RemoveLink(d)
		return "the link is not ignored by git in the worktree (write the ignore pattern without a trailing slash, " +
			"e.g. \"node_modules\")"
	}
	return ""
}

// RemoveLinks removes the shared-folder links of a worktree without following them. A path that is no longer a link
// (the worker replaced it) is left and reported.
func RemoveLinks(wt string, env *api.WorktreeEnv) (removed, notLink []string, err error) {
	if env == nil {
		return nil, nil, nil
	}
	for _, raw := range env.Linked {
		rel, _ := RelPath(raw)
		if rel == "" {
			continue
		}
		res, rerr := platform.RemoveLink(filepath.Join(wt, filepath.FromSlash(rel)))
		switch {
		case rerr != nil:
			err = errors.Join(err, rerr)
		case res == platform.LinkRemoved:
			removed = append(removed, rel)
		case res == platform.LinkNotLink:
			notLink = append(notLink, rel)
		}
	}
	return removed, notLink, err
}

// CommitAll commits everything in the owned worktree wt (inside it, with its own index) and returns HEAD. The
// shared-folder links must be gone first (Seal).
func (t *Trees) CommitAll(ctx context.Context, wt, message string) (string, error) {
	w, err := t.owned(wt)
	if err != nil {
		return "", err
	}
	if !t.Working(ctx, w) {
		return "", fmt.Errorf("worktree %s is no longer a git worktree", wt)
	}
	opt := gitx.ExecOptions{Owned: true}
	if _, err := t.Git.Exec(ctx, w, opt, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	st, err := t.Git.Exec(ctx, w, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(st)) != "" {
		args := append(gitx.Identity(), "commit", "--no-verify", "-q", "-m", message)
		if _, err := t.Git.Exec(ctx, w, opt, args...); err != nil {
			return "", err
		}
	}
	out, err := t.Git.Exec(ctx, w, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Seal removes the shared-folder links of wt and then commits everything in it: the worktree's final state as a
// commit, with no link in it. It refuses when a linked path is still a link afterwards.
func (t *Trees) Seal(ctx context.Context, wt string, env *api.WorktreeEnv, message string) (string, error) {
	if _, err := t.owned(wt); err != nil {
		return "", err
	}
	if _, _, err := RemoveLinks(wt, env); err != nil {
		return "", fmt.Errorf("remove shared-folder links: %w", err)
	}
	return t.CommitAll(ctx, wt, message)
}

// Remove seals the worktree wt and removes it with plain `git worktree remove` (never --force). A worktree whose
// folder is gone is pruned. It returns the final commit ("" when the folder was gone).
func (t *Trees) Remove(ctx context.Context, root, wt string, env *api.WorktreeEnv, message string) (string, error) {
	if _, err := t.owned(wt); err != nil {
		return "", err
	}
	if _, err := os.Lstat(wt); errors.Is(err, os.ErrNotExist) {
		_, perr := t.Git.Exec(ctx, root, gitx.ExecOptions{}, "worktree", "prune")
		return "", perr
	}
	if _, _, err := RemoveLinks(wt, env); err != nil {
		return "", fmt.Errorf("remove shared-folder links: %w", err)
	}
	// A removal that stopped partway may have taken .git: git would then run in whatever repository lies above.
	if !t.Working(ctx, wt) {
		return "", fmt.Errorf("worktree %s is no longer a git worktree (an earlier removal stopped partway)", wt)
	}
	// Links a worker made itself go too: removing the folder must never reach through one.
	if err := removeStrayLinks(wt, t.trackedLinks(ctx, wt)); err != nil {
		return "", err
	}
	commit, err := t.CommitAll(ctx, wt, message)
	if err != nil {
		return "", err
	}
	if _, err := t.Git.Exec(ctx, root, gitx.ExecOptions{}, "worktree", "remove", wt); err != nil {
		return commit, err
	}
	return commit, nil
}

// Working reports whether wt is still a git worktree of its own: its .git entry is there and git, run inside it,
// names wt itself as the top of the working tree (never a repository above it).
func (t *Trees) Working(ctx context.Context, wt string) bool {
	if _, err := os.Lstat(filepath.Join(wt, ".git")); err != nil {
		return false
	}
	out, err := t.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	same, err := fsutil.SamePath(strings.TrimSpace(string(out)), wt)
	return err == nil && same
}

// Discard deletes the folder of a worktree git no longer knows as one (a `git worktree remove` that stopped
// partway), once its changes are kept elsewhere, then prunes git's record of it. wt must be owned and lie strictly
// inside scope (the contest's folder under Base). The shared-folder links and every other link go first, as
// themselves; the folder is then deleted entry by entry without ever following a link or junction (a linked
// node_modules points into the user's real folder).
func (t *Trees) Discard(ctx context.Context, root, wt, scope string, env *api.WorktreeEnv) error {
	if _, err := t.owned(wt); err != nil {
		return err
	}
	if _, err := t.owned(scope); err != nil {
		return err
	}
	inScope, err := fsutil.Within(scope, wt)
	if err != nil {
		return err
	}
	if same, _ := fsutil.SamePath(scope, wt); !inScope || same {
		return fmt.Errorf("worktree %s is not inside %s", wt, scope)
	}
	fi, err := os.Lstat(wt)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case platform.IsLink(fi):
		if _, err := platform.RemoveLink(wt); err != nil {
			return err
		}
	default:
		if _, _, err := RemoveLinks(wt, env); err != nil {
			return fmt.Errorf("remove shared-folder links: %w", err)
		}
		if err := removeStrayLinks(wt, nil); err != nil {
			return err
		}
		if err := removeTree(wt); err != nil {
			return err
		}
	}
	_, err = t.Git.Exec(ctx, root, gitx.ExecOptions{}, "worktree", "prune")
	return err
}

// removeTree deletes p and everything below it. A link or junction (any reparse point) is removed as itself and
// never walked into; a read-only file is made writable first.
func removeTree(p string) error {
	n := 0
	var walk func(p string) error
	walk = func(p string) error {
		n++
		if n > maxWalk {
			return fmt.Errorf("more than %d entries; remove it by hand", maxWalk)
		}
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if platform.IsLink(fi) {
			_, err := platform.RemoveLink(p)
			return err
		}
		if fi.IsDir() {
			if fi.Mode().Perm()&0o700 != 0o700 {
				// A read-only folder (what stops `git worktree remove`) cannot lose its entries.
				_ = os.Chmod(p, 0o700) //nolint:gosec // owner-only, on a folder about to be deleted
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return err
			}
			for _, e := range entries {
				if err := walk(filepath.Join(p, e.Name())); err != nil {
					return err
				}
			}
		}
		if err := os.Remove(p); err != nil {
			if cerr := os.Chmod(p, 0o600); cerr != nil {
				return err
			}
			return os.Remove(p)
		}
		return nil
	}
	return walk(p)
}

// maxWalk bounds the entries removeStrayLinks looks at.
const maxWalk = 500_000

// trackedLinks are the symbolic links the index of wt tracks (mode 120000), by slash-separated path: git made them,
// so they stay (removing one would record its deletion in the final commit).
func (t *Trees) trackedLinks(ctx context.Context, wt string) map[string]bool {
	out, err := t.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "ls-files", "-s", "-z")
	if err != nil {
		return nil
	}
	links := map[string]bool{}
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if ok && strings.HasPrefix(meta, "120000 ") {
			links[path] = true
		}
	}
	return links
}

// removeStrayLinks removes every symbolic link and junction below wt as itself, never walking into one, except the
// tracked links in keep.
func removeStrayLinks(wt string, keep map[string]bool) error {
	n := 0
	var errs error
	walkErr := filepath.WalkDir(wt, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == wt {
				return err
			}
			return nil //nolint:nilerr // an unreadable entry is left; git worktree remove reports it
		}
		n++
		if n > maxWalk {
			return fmt.Errorf("worktree %s has more than %d entries; remove it by hand", wt, maxWalk)
		}
		if p == wt || d.Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 && !d.IsDir() {
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil || !platform.IsLink(fi) {
			return nil //nolint:nilerr // not a link: walked normally
		}
		if rel, rerr := filepath.Rel(wt, p); rerr == nil && keep[filepath.ToSlash(rel)] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if _, err := platform.RemoveLink(p); err != nil {
			errs = errors.Join(errs, err)
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return errors.Join(walkErr, errs)
}

// RemoveEmpty removes dir when it is an empty folder below Base.
func (t *Trees) RemoveEmpty(dir string) {
	if _, err := t.owned(dir); err == nil {
		_ = os.Remove(dir) // non-recursive: fails when anything is left
	}
}
