package gitx

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// What a task committed since it started (TaskGit's commits and committed files, and one commit's diff). Every
// call here only reads; revisions are hex ids checked before they reach git (never an option), and paths come
// after "--".

// emptyTree256 is the empty tree of a SHA-256 repository (emptyTree is SHA-1's).
const emptyTree256 = "6ef19b41225c5369f1c104d45d8d85efa9b057b53b14b4b9b939dd74decc5321"

// shaRe is a commit id a request may name: 4 to 64 hex digits (an abbreviation or a full SHA-1 or SHA-256 id).
var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// ValidSHA reports whether s may name a commit in a request: 4 to 64 hex digits, so it can never be read as an
// option.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// fullSHARe is a full commit id.
var fullSHARe = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ErrNotInTask is matched when a commit is not one of the task's commits (not in start..HEAD, or no such commit).
var ErrNotInTask = errors.New("the commit is not one of the task's commits")

// ErrNoStart is matched by a StartError.
var ErrNoStart = errors.New("the task's start cannot be used")

// StartError says why a task's commits cannot be read: its start is unknown or unreachable.
type StartError struct {
	// State is api.GitStartUnknown or api.GitStartUnreachable.
	State string
}

func (e *StartError) Error() string {
	if e.State == api.GitStartUnreachable {
		return "the commit the task started from is no longer an ancestor of HEAD (rebased, reset or switched " +
			"away), so the task's commits cannot be told apart"
	}
	return "the task did not record where it started, so its commits cannot be told apart"
}

// Is makes errors.Is(err, ErrNoStart) match.
func (e *StartError) Is(target error) bool { return target == ErrNoStart }

// EmptyTree returns the empty tree of the repository whose ids look like oid.
func EmptyTree(oid string) string {
	if len(oid) == 64 {
		return emptyTree256
	}
	return emptyTree
}

// maxLogBytes bounds the log of the commits since a task's start (all of them, for the files they changed).
const maxLogBytes = 8 << 20

// usableStart returns the task's start when it can be used in the repository at root: a recorded start, or a
// start snapshot's head (Root set) taken in this same repository. A snapshot without a head is not trusted to mean
// "no commit yet" (the snapshot may simply not have recorded it), so it is unknown too.
func usableStart(tf TaskFiles, root string) *TaskStart {
	s := tf.Start
	if s == nil || s.Root == "" {
		return s
	}
	if s.Head == "" {
		return nil
	}
	a, errA := fsutil.Key(s.Root)
	b, errB := fsutil.Key(root)
	if errA != nil || errB != nil || a != b {
		return nil
	}
	return s
}

// history fills the start fields, the commits since the start and the task's files among those they changed.
// touched are the files the task's turns touched (repository-relative) with the last turn of each; cwdRel is the
// task's folder relative to root.
func (r *Repos) history(ctx context.Context, root, cwdRel string, tf TaskFiles, st repoState,
	touched map[string]int, out *api.TaskGit) error {
	out.StartState = api.GitStartUnknown
	start := usableStart(tf, root)
	if start == nil {
		return nil
	}
	tf.Start = start
	out.StartHead, out.StartBranch = tf.Start.Head, tf.Start.Branch
	out.BranchChanged = tf.Start.Branch != "" && st.branch != tf.Start.Branch
	ok, err := r.startReachable(ctx, root, tf.Start.Head, st.head)
	if err != nil {
		return err
	}
	if !ok {
		out.StartState = api.GitStartUnreachable
		return nil
	}
	out.StartState = api.GitStartRecorded
	if !st.head {
		return nil // no commit yet, as at the start
	}
	rng := []string{"HEAD"}
	if tf.Start.Head != "" {
		rng = []string{tf.Start.Head + "..HEAD"}
	}
	cnt, _, err := r.run(ctx, root, ReadTimeout, 0, append(append([]string{"rev-list", "--count"}, rng...), "--")...)
	if err != nil {
		return err
	}
	total, _ := strconv.Atoi(strings.TrimSpace(string(cnt)))
	if total == 0 {
		return nil
	}
	// The list: the newest commits only, each with its file count (git diffs at most MaxTaskCommits trees).
	args := append([]string{"log", "-z", "--no-renames", "--name-only", "--no-color", logFormat,
		"-n", strconv.Itoa(api.MaxTaskCommits)}, rng...)
	logOut, _, err := r.run(ctx, root, ReadTimeout, 4<<20, append(args, "--")...)
	if err != nil {
		return err
	}
	commits, _ := parseLog(logOut, tf.Turns)
	out.Commits = commits
	out.CommitsMore = max(0, total-len(commits))
	names, err := r.turnNames(ctx, root, rng, tf.Turns)
	if err != nil {
		return err
	}
	return r.committedFiles(ctx, root, cwdRel, tf.Start.Head, st.oid, touched, names, out)
}

// logFormat is the commit header parseLog reads.
const logFormat = "--format=%x1e%H%x1f%an%x1f%at%x1f%ct%x1f%s"

// turnCommits returns the commits of the range made during one of the task's turns (by committer time), newest
// first. Listing them diffs no tree; the walk stops before the first turn's start (--max-age) and skips commits
// after the last turn's end (--min-age), so its cost follows the task's span, not the range's length.
func (r *Repos) turnCommits(ctx context.Context, root string, rng []string, turns []TurnSpan) ([]string, error) {
	if len(turns) == 0 {
		return nil, nil
	}
	first, last, open := turns[0].Start, turns[0].End, false
	for _, s := range turns {
		if s.Start.Before(first) {
			first = s.Start
		}
		if s.End.IsZero() {
			open = true
		} else if s.End.After(last) {
			last = s.End
		}
	}
	args := []string{"rev-list", "--timestamp", "--max-age=" + strconv.FormatInt(first.Unix(), 10)}
	if !open {
		args = append(args, "--min-age="+strconv.FormatInt(last.Unix()+1, 10))
	}
	out, _, err := r.run(ctx, root, ReadTimeout, maxLogBytes, append(append(args, rng...), "--")...)
	if err != nil {
		return nil, err
	}
	var shas []string
	for _, line := range strings.Split(string(out), "\n") {
		ts, sha, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || !fullSHARe.MatchString(sha) {
			continue
		}
		ct, _ := strconv.ParseInt(ts, 10, 64)
		if turnAt(turns, time.Unix(ct, 0)) > 0 {
			shas = append(shas, sha)
		}
	}
	return shas, nil
}

// turnNames reads the files the commits made during the task's turns changed, with the newest turn of each: git
// diffs the trees of those commits only (`log --no-walk --stdin`). A log past maxLogBytes keeps what it read.
func (r *Repos) turnNames(ctx context.Context, root string, rng []string, turns []TurnSpan) (map[string]int, error) {
	shas, err := r.turnCommits(ctx, root, rng, turns)
	if err != nil || len(shas) == 0 {
		return map[string]int{}, err
	}
	out, _, err := r.runIn(ctx, root, nil, []byte(strings.Join(shas, "\n")+"\n"), ReadTimeout, maxLogBytes,
		"log", "--no-walk=unsorted", "--stdin", "-z", "--no-renames", "--name-only", "--no-color", logFormat, "--")
	if err != nil {
		return nil, err
	}
	_, names := parseLog(out, turns)
	return names, nil
}

// startReachable reports whether start ("" for a repository that had no commit) is an ancestor of HEAD. hasHead
// says whether the repository has a commit now.
func (r *Repos) startReachable(ctx context.Context, root, start string, hasHead bool) (bool, error) {
	if start == "" {
		return true, nil
	}
	if !hasHead || !fullSHARe.MatchString(start) {
		return false, nil
	}
	return r.isAncestor(ctx, root, start, "HEAD")
}

// isAncestor runs `git merge-base --is-ancestor a b`: exit 0 is yes, exit 1 no, and a missing commit (128) is no
// too; a timeout or a git that cannot run is an error.
func (r *Repos) isAncestor(ctx context.Context, root, a, b string) (bool, error) {
	_, _, err := r.run(ctx, root, ReadTimeout, 0, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ge *Error
	if errors.As(err, &ge) && !ge.Timeout && ExitCode(err) > 0 {
		return false, nil
	}
	return false, err
}

// parseLog reads `git log -z --name-only --format=%x1e%H%x1f%an%x1f%at%x1f%ct%x1f%s`: the commits, and for each
// file a commit made during one of the task's turns changed, the newest such turn. Files only commits outside the
// turns changed are not in names.
func parseLog(out []byte, turns []TurnSpan) (commits []api.GitCommit, names map[string]int) {
	commits = []api.GitCommit{}
	names = map[string]int{}
	for _, rec := range strings.Split(string(out), "\x1e") {
		if rec == "" {
			continue
		}
		header, rest, _ := strings.Cut(rec, "\x00")
		f := strings.SplitN(header, "\x1f", 5)
		if len(f) != 5 {
			continue
		}
		at, _ := strconv.ParseInt(f[2], 10, 64)
		ct, _ := strconv.ParseInt(f[3], 10, 64)
		c := api.GitCommit{SHA: f[0], Author: f[1], AuthorTime: api.FormatTime(time.Unix(at, 0)), Subject: f[4],
			Turn: turnAt(turns, time.Unix(ct, 0))}
		c.OutsideTurns = c.Turn == 0
		for _, name := range strings.Split(strings.TrimPrefix(rest, "\n"), "\x00") {
			if name == "" {
				continue
			}
			c.FileCount++
			if c.Turn > 0 {
				names[name] = max(names[name], c.Turn)
			}
		}
		commits = append(commits, c)
	}
	return commits, names
}

// turnAt returns the turn that ran at t (commit times have whole seconds), or 0.
func turnAt(turns []TurnSpan, t time.Time) int {
	n := 0
	for _, s := range turns {
		if t.Before(s.Start.Truncate(time.Second)) || (!s.End.IsZero() && t.After(s.End)) {
			continue
		}
		n = max(n, s.N)
	}
	return n
}

// committedFiles lists the task's files among those changed between start and HEAD, each counted from start to
// the working tree, with the newest turn that touched or committed it. A file is the task's when its turns touched
// it, or when a commit made during one of its turns (names) changed it and it lies under the task's folder, the
// same rule as the uncommitted files; a file only commits outside the turns changed is someone else's.
func (r *Repos) committedFiles(ctx context.Context, root, cwdRel, start, head string,
	touched, names map[string]int, out *api.TaskGit) error {
	base := start
	if base == "" {
		base = EmptyTree(head)
	}
	ns, _, err := r.run(ctx, root, ReadTimeout, 0, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff",
		"--no-textconv", base, "HEAD", "--")
	if err != nil {
		return err
	}
	var files []api.GitFile
	size := 0
	for p, s := range parseNameStatus(ns) {
		_, hit := touched[p]
		if _, ours := names[p]; !hit && (!ours || !under(cwdRel, p)) {
			continue
		}
		files = append(files, api.GitFile{Path: p, Status: s, Turn: max(touched[p], names[p])})
		size += len(p) + 1
	}
	if len(files) == 0 {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	// The counts against the working tree, for those files only when they fit on a command line (a whole-repo diff
	// of a big repository is slow); paths are literal (GIT_LITERAL_PATHSPECS).
	args := []string{"diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, "--"}
	if len(files) <= maxNumstatPaths && size <= maxNumstatChars {
		for _, f := range files {
			args = append(args, f.Path)
		}
	}
	num, _, err := r.run(ctx, root, ReadTimeout, 0, args...)
	if err != nil {
		return err
	}
	stats := parseNumstat(num)
	for i, f := range files {
		if ls, ok := stats[f.Path]; ok {
			files[i].Additions, files[i].Deletions, files[i].Binary = ls.add, ls.del, ls.binary
		}
	}
	out.CommittedFiles = files
	return nil
}

// The numstat of committedFiles names the files when at most this many, this long in all (a Windows command line
// holds 32767 characters).
const (
	maxNumstatPaths = 200
	maxNumstatChars = 16 << 10
)

// parseNameStatus reads `git diff --name-status -z --no-renames`: "X\0path\0" pairs.
func parseNameStatus(out []byte) map[string]string {
	status := map[string]string{}
	f := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(f); i += 2 {
		if f[i] == "" || f[i+1] == "" {
			continue
		}
		switch f[i][0] {
		case 'A':
			status[f[i+1]] = api.GitAdded
		case 'D':
			status[f[i+1]] = api.GitDeleted
		default:
			status[f[i+1]] = api.GitModified
		}
	}
	return status
}

// TaskCommit returns one of the task's commits (sha: 4 to 64 hex digits) with its files and diffs against its first
// parent. A commit outside start..HEAD, or one git does not know, is ErrNotInTask; a task whose start is unknown or
// unreachable is a StartError (ErrNoStart).
func (r *Repos) TaskCommit(ctx context.Context, tf TaskFiles, sha string) (api.GitCommitDetail, error) {
	var d api.GitCommitDetail
	if !shaRe.MatchString(sha) {
		return d, fmt.Errorf("%w: commit %q must be 4 to 64 hex digits", ErrInvalid, sha)
	}
	root, err := r.Root(ctx, tf.Cwd)
	if errors.Is(err, ErrNotRepo) {
		return d, fmt.Errorf("%w: the task's folder is not in a git repository", ErrInvalid)
	}
	if err != nil {
		return d, err
	}
	st, err := r.state(ctx, root, false)
	if err != nil {
		return d, err
	}
	if tf.Start = usableStart(tf, root); tf.Start == nil {
		return d, &StartError{State: api.GitStartUnknown}
	}
	ok, err := r.startReachable(ctx, root, tf.Start.Head, st.head)
	if err != nil {
		return d, err
	}
	if !ok {
		return d, &StartError{State: api.GitStartUnreachable}
	}
	full, err := r.resolveCommit(ctx, root, sha)
	if err != nil {
		return d, err
	}
	if in, err := r.inRange(ctx, root, full, tf.Start.Head, st.head); err != nil || !in {
		if err == nil {
			err = fmt.Errorf("%w: %s", ErrNotInTask, sha)
		}
		return d, err
	}
	return r.commitDetail(ctx, root, full, tf.Turns)
}

// resolveCommit returns the full id of the commit sha names (already checked as hex), or ErrNotInTask when there
// is none or the abbreviation is ambiguous.
func (r *Repos) resolveCommit(ctx context.Context, root, sha string) (string, error) {
	out, _, err := r.run(ctx, root, ReadTimeout, 0, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && !ge.Timeout && ExitCode(err) > 0 {
			return "", fmt.Errorf("%w: %s", ErrNotInTask, sha)
		}
		return "", err
	}
	full := strings.ToLower(strings.TrimSpace(string(out)))
	if !fullSHARe.MatchString(full) {
		return "", fmt.Errorf("%w: %s", ErrNotInTask, sha)
	}
	return full, nil
}

// inRange reports whether commit lies in start..HEAD (every commit of HEAD when start is "").
func (r *Repos) inRange(ctx context.Context, root, commit, start string, hasHead bool) (bool, error) {
	if !hasHead {
		return false, nil
	}
	if ok, err := r.isAncestor(ctx, root, commit, "HEAD"); err != nil || !ok {
		return false, err
	}
	if start == "" {
		return true, nil
	}
	before, err := r.isAncestor(ctx, root, commit, start)
	return !before, err
}

// commitDetail reads commit (a full id) against its first parent.
func (r *Repos) commitDetail(ctx context.Context, root, commit string, turns []TurnSpan) (api.GitCommitDetail,
	error) {
	var d api.GitCommitDetail
	info, _, err := r.run(ctx, root, ReadTimeout, 64<<10, "show", "-s", "--no-color",
		"--format=%H%x1f%P%x1f%an%x1f%at%x1f%ct%x1f%s%x1f%b", commit, "--")
	if err != nil {
		return d, err
	}
	f := strings.SplitN(strings.TrimRight(string(info), "\n"), "\x1f", 7)
	if len(f) != 7 {
		return d, fmt.Errorf("%w: unexpected git show output for %s", ErrNotInTask, commit)
	}
	at, _ := strconv.ParseInt(f[3], 10, 64)
	ct, _ := strconv.ParseInt(f[4], 10, 64)
	d.SHA, d.Author, d.AuthorTime, d.Subject = f[0], f[2], api.FormatTime(time.Unix(at, 0)), f[5]
	d.Turn = turnAt(turns, time.Unix(ct, 0))
	d.OutsideTurns = d.Turn == 0
	d.Body = strings.TrimSpace(f[6])
	base := EmptyTree(commit)
	if parents := strings.Fields(f[1]); len(parents) > 0 {
		d.Parent, base = parents[0], parents[0]
	}
	ns, _, err := r.run(ctx, root, ReadTimeout, 0, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff",
		"--no-textconv", base, commit, "--")
	if err != nil {
		return d, err
	}
	num, _, err := r.run(ctx, root, ReadTimeout, 0, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff",
		"--no-textconv", base, commit, "--")
	if err != nil {
		return d, err
	}
	stats := parseNumstat(num)
	d.Files = []api.GitFile{}
	for p, s := range parseNameStatus(ns) {
		gf := api.GitFile{Path: p, Status: s, Turn: d.Turn}
		if ls, ok := stats[p]; ok {
			gf.Additions, gf.Deletions, gf.Binary = ls.add, ls.del, ls.binary
		}
		d.Files = append(d.Files, gf)
	}
	sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Path < d.Files[j].Path })
	d.FileCount = len(d.Files)
	patch, over, err := r.run(ctx, root, ReadTimeout, maxDiffBytes, "diff", "--no-color", "--no-ext-diff",
		"--no-textconv", "--no-renames", "-U3", base, commit, "--")
	if err != nil {
		return d, err
	}
	d.Diffs, d.Truncated = SplitDiff(string(patch), over), over
	return d, nil
}
