package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// TaskFiles is what gitx needs to know about a task.
type TaskFiles struct {
	// Cwd is the task's folder (absolute).
	Cwd string
	// Baseline lists the files (absolute) that were already dirty when the task started.
	Baseline []string
	// Files are the files the task's turns touched, relative to Cwd with '/' separators, with the last turn that
	// touched each.
	Files map[string]int
	// Start is where the task started; nil when it is not known (see api.GitStartUnknown).
	Start *TaskStart
	// Turns are when the task's turns ran, to tell which turn made a commit.
	Turns []TurnSpan
}

// TaskStart is where a task started: the HEAD commit ("" when the repository had no commit yet) and the branch
// ("" when HEAD was detached or the branch is not known). Root is set only for a start read from a task's first
// start snapshot (tasks from before the start was recorded): the repository the snapshot was taken in. Such a
// start is used only in that same repository and only with a head.
type TaskStart struct {
	Head   string
	Branch string
	Root   string
}

// TurnSpan is when one turn of a task ran; End is zero while it runs.
type TurnSpan struct {
	N          int
	Start, End time.Time
}

// TaskGit returns the repository of a task, the task's uncommitted changes in it, and what the task committed
// since it started. The uncommitted changes are the dirty files the task's turns touched, plus dirty files under
// the task's folder that were not dirty when the task started (a worker's shell commands change files the stream
// never names). Every changed file is in Files (UntrackedDirs is empty): what a commit may name. Suggestion is
// left nil for the caller.
func (r *Repos) TaskGit(ctx context.Context, tf TaskFiles) (api.TaskGit, error) {
	return r.taskGit(ctx, tf, false)
}

// TaskGitView is TaskGit as the Git tab shows it: the files of large untracked folders are grouped into
// UntrackedDirs (collapseUntracked), and only the files shown have their lines counted.
func (r *Repos) TaskGitView(ctx context.Context, tf TaskFiles) (api.TaskGit, error) {
	return r.taskGit(ctx, tf, true)
}

func (r *Repos) taskGit(ctx context.Context, tf TaskFiles, view bool) (api.TaskGit, error) {
	out := api.TaskGit{Files: []api.GitFile{}, UntrackedDirs: []api.GitDir{}, Preexisting: []string{},
		StartState: api.GitStartUnknown, Commits: []api.GitCommit{}, CommittedFiles: []api.GitFile{}}
	root, err := r.Root(ctx, tf.Cwd)
	if errors.Is(err, ErrNotRepo) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	st, err := r.state(ctx, root, true)
	if err != nil {
		return out, err
	}
	out.Repo, out.Root, out.Branch, out.Dirty = true, root, st.branch, len(st.files) > 0
	baseline := map[string]bool{}
	for _, b := range tf.Baseline {
		if rel, ok := relTo(root, b); ok {
			baseline[rel] = true
			out.Preexisting = append(out.Preexisting, rel)
		}
	}
	sort.Strings(out.Preexisting)
	touched := touchedFiles(root, tf)
	cwdRel, _ := relTo(root, tf.Cwd)
	for _, f := range st.files {
		turn, hit := touched[f.Path]
		if !hit && f.OldPath != "" {
			turn, hit = touched[f.OldPath]
		}
		if !hit && (baseline[f.Path] || !under(cwdRel, f.Path)) {
			continue
		}
		f.Turn = turn
		out.Files = append(out.Files, f)
	}
	if view {
		r.collapseView(ctx, root, st, &out)
		stampImages(root, st.heads, out.Files)
	}
	countUntracked(root, out.Files)
	out.Head = st.oid
	// What the task committed is extra: when it cannot be read (git too slow on a big repository, a missing
	// object), the uncommitted files and the commit dialog still work.
	if err := r.history(ctx, root, cwdRel, tf, st, touched, &out); err != nil {
		if ctx.Err() != nil {
			return out, err
		}
		r.git.log.Warn("gitx: the task's commits could not be read", "root", root, "err", err)
		out.HistoryState = api.GitHistoryUnavailable
		out.Commits, out.CommitsMore, out.CommittedFiles, out.OwnCommits = []api.GitCommit{}, 0, []api.GitFile{}, 0
	}
	return out, nil
}

// collapseView groups the large untracked folders of out.Files into out.UntrackedDirs, with their listed files'
// line counts and stamps and the count of their files that are not the task's. Grouping is a convenience of the
// view: when it fails (ls-files timed out on a huge folder), it is logged and every file stays in the list.
func (r *Repos) collapseView(ctx context.Context, root string, st repoState, out *api.TaskGit) {
	collapse := r.collapse
	if collapse == nil {
		collapse = r.collapseUntracked
	}
	files, dirs, err := collapse(ctx, root, out.Files)
	if err != nil {
		if ctx.Err() != nil {
			return // the read is being cancelled; the history read that follows says so
		}
		r.git.log.Warn("gitx: the untracked folders could not be grouped; the view lists every file", "root", root,
			"err", err)
		return
	}
	out.Files, out.UntrackedDirs = files, dirs
	for i := range out.UntrackedDirs {
		d := &out.UntrackedDirs[i]
		countUntracked(root, d.Files)
		stampImages(root, st.heads, d.Files)
		// Every file under such a folder is untracked: those that are not among the task's are someone else's.
		for _, f := range st.files {
			if f.Status == api.GitUntracked && strings.HasPrefix(f.Path, d.Path) {
				d.Others++
			}
		}
		d.Others -= d.Count
	}
}

// touchedFiles maps the files the task's turns touched to repository-relative paths, with the last turn of each.
func touchedFiles(root string, tf TaskFiles) map[string]int {
	touched := map[string]int{}
	for f, turn := range tf.Files {
		if rel, ok := relTo(root, filepath.Join(tf.Cwd, filepath.FromSlash(f))); ok {
			touched[rel] = turn
		}
	}
	return touched
}

// Commit commits exactly files (repository-relative, each one of the task's changed files) with message: new
// files are added first, then `git commit --only` takes those paths from the working tree, so changes the user
// staged before stay staged and out of the commit. A renamed file brings its old path, so the deletion goes
// with the addition.
func (r *Repos) Commit(ctx context.Context, tf TaskFiles, message string, files []string) (api.CommitResult, error) {
	if strings.TrimSpace(message) == "" {
		return api.CommitResult{}, fmt.Errorf("%w: the commit message is empty", ErrInvalid)
	}
	if len(files) == 0 {
		return api.CommitResult{}, fmt.Errorf("%w: no files to commit", ErrInvalid)
	}
	tg, err := r.TaskGit(ctx, tf)
	if err != nil {
		return api.CommitResult{}, err
	}
	if !tg.Repo {
		return api.CommitResult{}, fmt.Errorf("%w: the task's folder is not in a git repository", ErrInvalid)
	}
	allowed := map[string]api.GitFile{}
	for _, f := range tg.Files {
		allowed[f.Path] = f
	}
	var add, paths []string
	seen := map[string]bool{}
	for _, p := range files {
		rel, err := CleanPath(tg.Root, p)
		if err != nil {
			return api.CommitResult{}, err
		}
		f, ok := allowed[rel]
		if !ok {
			return api.CommitResult{}, fmt.Errorf("%w: %s is not one of the task's changed files", ErrInvalid, p)
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		paths = append(paths, rel)
		switch f.Status {
		case api.GitUntracked, api.GitAdded:
			add = append(add, rel)
		case api.GitRenamed:
			add = append(add, rel)
			if f.OldPath != "" && !seen[f.OldPath] {
				seen[f.OldPath] = true
				paths = append(paths, f.OldPath)
			}
		}
	}
	unlock, err := r.Lock(ctx, tg.Root)
	if err != nil {
		return api.CommitResult{}, err
	}
	defer unlock()
	defer r.forget(tg.Root)
	if len(add) > 0 {
		args := append([]string{"--literal-pathspecs", "add", "--"}, add...)
		if _, _, err := r.run(ctx, tg.Root, ReadTimeout, 0, args...); err != nil {
			return api.CommitResult{}, err
		}
	}
	args := append([]string{"--literal-pathspecs", "commit", "--only", "-m", message, "--"}, paths...)
	if _, _, err := r.run(ctx, tg.Root, CommitTimeout, 0, args...); err != nil {
		return api.CommitResult{}, err
	}
	sha, _, err := r.run(ctx, tg.Root, ReadTimeout, 0, "rev-parse", "HEAD")
	if err != nil {
		return api.CommitResult{}, err
	}
	stat, _, err := r.run(ctx, tg.Root, ReadTimeout, 0, "show", "--shortstat", "--format=", "--no-ext-diff",
		"--no-textconv", "HEAD")
	if err != nil {
		return api.CommitResult{}, err
	}
	return api.CommitResult{SHA: strings.TrimSpace(string(sha)), Summary: strings.TrimSpace(string(stat))}, nil
}

// forget drops the cached branch answer of root after a write.
func (r *Repos) forget(root string) {
	r.mu.Lock()
	delete(r.branches, root)
	r.mu.Unlock()
}

// SuggestMessage builds a commit message from a task's title and the worker's last answer: the title as the
// subject (at most 72 characters) and the first paragraph of the answer, plus the files when there are few.
func SuggestMessage(title, response string, files []api.GitFile) string {
	subject := strings.TrimSpace(strings.SplitN(strings.TrimSpace(title), "\n", 2)[0])
	if subject == "" {
		subject = "Update files"
	}
	if r := []rune(subject); len(r) > 72 {
		subject = strings.TrimSpace(string(r[:71])) + "…"
	}
	var body []string
	if para := firstParagraph(response); para != "" {
		body = append(body, para)
	}
	if n := len(files); n > 0 && n <= 10 {
		var list []string
		for _, f := range files {
			list = append(list, "- "+f.Path)
		}
		body = append(body, strings.Join(list, "\n"))
	}
	if len(body) == 0 {
		return subject
	}
	return subject + "\n\n" + strings.Join(body, "\n\n")
}

// firstParagraph returns the first paragraph of text that is not a markdown heading, at most 500 characters.
func firstParagraph(text string) string {
	for _, p := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		p = strings.TrimSpace(p)
		if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "```") {
			continue
		}
		if r := []rune(p); len(r) > 500 {
			p = strings.TrimSpace(string(r[:499])) + "…"
		}
		return p
	}
	return ""
}

// relTo returns p relative to root with '/' separators when p lies inside root (compared through fsutil).
func relTo(root, p string) (string, bool) {
	kr, err := fsutil.Key(root)
	if err != nil {
		return "", false
	}
	kp, err := fsutil.Key(p)
	if err != nil {
		return "", false
	}
	if kp == kr {
		return "", true
	}
	if !strings.HasPrefix(kp, strings.TrimSuffix(kr, "/")+"/") {
		return "", false
	}
	// The key may be lower-cased; take the same number of characters from the canonical path.
	c, err := fsutil.Canonical(p)
	if err != nil {
		return "", false
	}
	c = filepath.ToSlash(c)
	return c[len(c)-(len(kp)-len(strings.TrimSuffix(kr, "/"))-1):], true
}

// under reports whether path lies in dir (both repository-relative; "" is the root).
func under(dir, path string) bool {
	return dir == "" || path == dir || strings.HasPrefix(path, dir+"/")
}
