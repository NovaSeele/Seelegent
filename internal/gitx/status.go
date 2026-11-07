package gitx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// emptyTree is git's id of the empty tree, the base of a diff in a repository without commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Status returns the state of the repository that contains cwd; {repo: false} outside git.
func (r *Repos) Status(ctx context.Context, cwd string) (api.RepoStatus, error) {
	root, err := r.Root(ctx, cwd)
	if errors.Is(err, ErrNotRepo) {
		return api.RepoStatus{Repo: false}, nil
	}
	if err != nil {
		return api.RepoStatus{}, err
	}
	st, err := r.state(ctx, root, true)
	if err != nil {
		return api.RepoStatus{}, err
	}
	return api.RepoStatus{Repo: true, Root: root, Branch: st.branch, Detached: st.detached,
		Dirty: len(st.files) > 0, Files: api.NonNil(st.files)}, nil
}

// Branch returns the branch of the repository that contains cwd, or nil outside git. Answers are cached for 10 s
// per repository.
func (r *Repos) Branch(ctx context.Context, cwd string) (*api.BranchInfo, error) {
	root, err := r.Root(ctx, cwd)
	if errors.Is(err, ErrNotRepo) {
		return nil, nil //nolint:nilnil // nil means "not a repository", as the API's null
	}
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	e, ok := r.branches[root]
	r.mu.Unlock()
	if ok && r.now().Sub(e.at) < branchCacheTTL {
		return e.info, nil
	}
	st, err := r.state(ctx, root, false)
	if err != nil {
		return nil, err
	}
	info := &api.BranchInfo{Branch: st.branch, Detached: st.detached, Dirty: len(st.files) > 0}
	r.mu.Lock()
	r.branches[root] = branchEntry{at: r.now(), info: info}
	r.mu.Unlock()
	return info, nil
}

type repoState struct {
	branch   string
	detached bool
	head     bool // the repository has a commit
	files    []api.GitFile
}

// state reads `git status` of the repository at root, with line counts when numstat is set.
func (r *Repos) state(ctx context.Context, root string, numstat bool) (repoState, error) {
	out, _, err := r.run(ctx, root, ReadTimeout, 0, "status", "--porcelain=v2", "--branch", "-z",
		"--untracked-files=all")
	if err != nil {
		return repoState{}, err
	}
	st := parseStatus(out)
	if !numstat || len(st.files) == 0 {
		return st, nil
	}
	base := "HEAD"
	if !st.head {
		base = emptyTree
	}
	diff, _, err := r.run(ctx, root, ReadTimeout, 0, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff",
		base, "--")
	if err != nil {
		return repoState{}, err
	}
	stats := parseNumstat(diff)
	for i := range st.files {
		f := &st.files[i]
		if f.Status == api.GitUntracked {
			f.Additions, f.Binary = countLines(filepath.Join(root, filepath.FromSlash(f.Path)))
			continue
		}
		if s, ok := stats[f.Path]; ok {
			f.Additions, f.Deletions, f.Binary = s.add, s.del, s.binary
		}
	}
	return st, nil
}

// parseStatus reads `git status --porcelain=v2 --branch -z`. Paths are relative to the repository root.
func parseStatus(out []byte) repoState {
	var st repoState
	recs := strings.Split(string(out), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			switch {
			case strings.HasPrefix(rec, "# branch.head "):
				b := strings.TrimPrefix(rec, "# branch.head ")
				if b == "(detached)" {
					st.detached = true
				} else {
					st.branch = b
				}
			case strings.HasPrefix(rec, "# branch.oid "):
				st.head = strings.TrimPrefix(rec, "# branch.oid ") != "(initial)"
			}
		case '1':
			if f := strings.SplitN(rec, " ", 9); len(f) == 9 {
				st.files = append(st.files, api.GitFile{Path: f[8], Status: statusOf(f[1])})
			}
		case '2':
			if f := strings.SplitN(rec, " ", 10); len(f) == 10 {
				gf := api.GitFile{Path: f[9], Status: api.GitRenamed}
				if i+1 < len(recs) {
					i++
					gf.OldPath = recs[i]
				}
				if strings.HasPrefix(f[8], "C") { // a copy keeps its source
					gf.Status, gf.OldPath = api.GitAdded, ""
				}
				st.files = append(st.files, gf)
			}
		case 'u':
			if f := strings.SplitN(rec, " ", 11); len(f) == 11 {
				st.files = append(st.files, api.GitFile{Path: f[10], Status: api.GitModified})
			}
		case '?':
			st.files = append(st.files, api.GitFile{Path: strings.TrimPrefix(rec, "? "), Status: api.GitUntracked})
		}
	}
	sort.Slice(st.files, func(i, j int) bool { return st.files[i].Path < st.files[j].Path })
	return st
}

// statusOf maps the XY code of a changed entry (index, worktree).
func statusOf(xy string) string {
	switch {
	case strings.ContainsRune(xy, 'D'):
		return api.GitDeleted
	case strings.ContainsRune(xy, 'A'):
		return api.GitAdded
	}
	return api.GitModified
}

type lineStat struct {
	add, del int
	binary   bool
}

// parseNumstat reads `git diff --numstat -z --no-renames`: "added\tdeleted\tpath" records, "-" for binary files.
func parseNumstat(out []byte) map[string]lineStat {
	stats := map[string]lineStat{}
	for _, rec := range strings.Split(string(out), "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "-" {
			stats[f[2]] = lineStat{binary: true}
			continue
		}
		add, _ := strconv.Atoi(f[0])
		del, _ := strconv.Atoi(f[1])
		stats[f[2]] = lineStat{add: add, del: del}
	}
	return stats
}

// countLines counts the lines of an untracked file (up to 1 MiB); a file with a NUL byte is binary, and a file
// that is neither regular nor a symbolic link (a FIFO, a socket, a device) has 0 lines.
func countLines(path string) (lines int, binary bool) {
	data, err := readHead(path, maxDiffBytes)
	if err != nil {
		return 0, false
	}
	if isBinary(data) {
		return 0, true
	}
	if len(data) == 0 {
		return 0, false
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n, false
}

// readHead reads at most limit bytes of an untracked file the way git stores it: a regular file's content or a
// symbolic link's target (never the file the link points to). Anything else, such as a FIFO, a socket or a device,
// is fsutil.ErrNotRegular; nothing here can block on a special file (a FIFO without a writer would hang a plain
// open forever).
func readHead(path string, limit int) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, err
		}
		return []byte(target[:min(len(target), limit)]), nil
	}
	data, _, err := fsutil.ReadRegular(path, int64(limit))
	return data, err
}

// isBinary reports a NUL byte in the first 8000 bytes, as git does.
func isBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}
