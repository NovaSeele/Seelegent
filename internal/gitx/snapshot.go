package gitx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// The calls below serve the work-at-risk snapshots, the outside-edit diff and the hygiene check (milestone 12).
// They only read: no lock, no index write (GIT_OPTIONAL_LOCKS=0 keeps even `git status` from refreshing it), so a
// snapshot never touches the user's index, stash or refs.

// DirtyFile is a path with uncommitted changes, relative to the repository root with '/' separators.
type DirtyFile struct {
	Path string
	// Status is an api.Git* status (untracked for a new file git does not know yet).
	Status string
	// OldPath is the source of a rename.
	OldPath string
}

// Dirty lists the files of the repository at root with uncommitted changes, untracked files included (ignored
// ones are not), and the HEAD commit ("" in a repository without commits).
func (r *Repos) Dirty(ctx context.Context, root string) (head string, files []DirtyFile, err error) {
	st, err := r.state(ctx, root, false)
	if err != nil {
		return "", nil, err
	}
	if st.head {
		out, _, err := r.run(ctx, root, ReadTimeout, 0, "rev-parse", "--verify", "--quiet", "HEAD")
		if err != nil {
			return "", nil, err
		}
		head = strings.TrimSpace(string(out))
	}
	files = make([]DirtyFile, 0, len(st.files))
	for _, f := range st.files {
		files = append(files, DirtyFile{Path: f.Path, Status: f.Status, OldPath: f.OldPath})
	}
	return head, files, nil
}

// Blob returns the content of rel (relative to root, '/' separators) at commit rev, at most limit bytes (the
// second result is false when rev has no such file). filters applies git's checkout conversion (line endings), as
// the file would look in the working tree.
func (r *Repos) Blob(ctx context.Context, root, rev, rel string, limit int, filters bool) (data []byte, ok bool,
	err error) {
	if rev == "" {
		return nil, false, nil
	}
	if !baseRe.MatchString(rev) {
		return nil, false, fmt.Errorf("%w: revision %q", ErrInvalid, rev)
	}
	args := []string{"cat-file"}
	if filters {
		args = append(args, "--filters")
	} else {
		args = append(args, "blob")
	}
	out, _, err := r.run(ctx, root, ReadTimeout, limit, append(args, rev+":"+rel)...)
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && !ge.Timeout {
			return nil, false, nil // no such path at rev
		}
		return nil, false, err
	}
	return out, true, nil
}

// DiffContent diffs two versions of one file given as content (nil with missing set: the file did not exist) and
// returns the parsed diff with Path rel. It runs `git diff --no-index` on two temporary files outside any
// repository, so no repository configuration (drivers, textconv) applies.
func (r *Repos) DiffContent(ctx context.Context, rel string, before, after []byte, beforeMissing,
	afterMissing bool) (api.FileDiff, error) {
	d := api.FileDiff{Path: rel, Hunks: []api.DiffHunk{}}
	if isBinary(before) || isBinary(after) {
		d.Binary = true
		return d, nil
	}
	dir, err := os.MkdirTemp("", "seelegent-diff-")
	if err != nil {
		return d, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if beforeMissing {
		a = os.DevNull
	} else if err := os.WriteFile(a, before, 0o600); err != nil {
		return d, err
	}
	if afterMissing {
		b = os.DevNull
	} else if err := os.WriteFile(b, after, 0o600); err != nil {
		return d, err
	}
	out, over, err := r.run(ctx, dir, ReadTimeout, maxDiffBytes, "diff", "--no-index", "--no-color", "--no-ext-diff",
		"--no-textconv", "-U3", "--", a, b)
	// git diff --no-index exits 1 when the files differ: a failure is an error without a diff.
	var ge *Error
	if err != nil && (len(out) == 0 || !errors.As(err, &ge) || ge.Timeout) {
		return d, err
	}
	if len(out) == 0 {
		return d, nil
	}
	pd := ParseDiff(string(out), over)
	pd.Path = rel
	return pd, nil
}

// Tracked lists up to n tracked files of the repository at root whose names end with suffix (such as ".go"),
// relative to the root.
func (r *Repos) Tracked(ctx context.Context, root, suffix string, n int) ([]string, error) {
	out, _, err := r.run(ctx, root, ReadTimeout, 4<<20, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" && strings.HasSuffix(strings.ToLower(f), strings.ToLower(suffix)) && len(files) < n {
			files = append(files, f)
		}
	}
	return files, nil
}

// ReadWorktree reads at most limit bytes of a working-tree file the way git stores it (a symbolic link's target,
// never the file it points to); a FIFO, socket or device is an error, and nothing here can block on one.
func ReadWorktree(path string, limit int) ([]byte, error) { return readHead(path, limit) }

// IsBinary reports a NUL byte in the first 8000 bytes, as git does.
func IsBinary(data []byte) bool { return isBinary(data) }
