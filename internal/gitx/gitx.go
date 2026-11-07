// Package gitx runs the git commands of the HTTP API (contract §3.3): repository status, file diffs (parsed, never
// sent as unified text), a task's changed files and commits of exactly the files the user picked.
//
// Every git call is exec with an argument list, never a shell (Binary never picks a Windows batch shim); paths from users or workers are passed after "--"
// with literal pathspecs, so `pages/[id].vue` or `:!x` are plain names. Read commands time out after 20 s and a
// commit after 5 min; on timeout the whole process tree is killed (hooks start children). Writes run under a
// per-repository mutex keyed by fsutil.Key(root), which every git-writing feature shares (Repos.Lock).
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Timeouts of git commands.
const (
	ReadTimeout   = 20 * time.Second
	CommitTimeout = 5 * time.Minute
	// branchCacheTTL is how long Branch answers from its cache (GET /git/branches).
	branchCacheTTL = 10 * time.Second
	// maxDiffBytes is the diff size above which a FileDiff is truncated.
	maxDiffBytes = 1 << 20
)

// ErrNotRepo is returned when a folder is not inside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// ErrInvalid is matched by errors about the request (a bad path, a file outside the task, an empty message).
var ErrInvalid = errors.New("invalid git request")

// Error is a git command that failed.
type Error struct {
	// Args are the git arguments (without the -c options).
	Args []string
	// Stderr is git's error output, trimmed.
	Stderr string
	// Timeout is true when the command ran out of time and was killed.
	Timeout bool
	// IndexLock is true when git reported .git/index.lock: another git process runs, or one crashed and left the
	// file behind. The daemon never deletes it.
	IndexLock bool
	Err       error
}

func (e *Error) Error() string {
	op := "git"
	if len(e.Args) > 0 {
		op += " " + e.Args[0]
	}
	switch {
	case e.Timeout:
		return op + ": timed out"
	case e.IndexLock:
		return op + ": .git/index.lock exists (another git command runs, or one crashed and left it behind; " +
			"remove it yourself when no git command is running)"
	case e.Stderr != "":
		return op + ": " + firstLines(e.Stderr, 5)
	}
	return fmt.Sprintf("%s: %v", op, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// Repos runs git for the daemon. It holds the per-repository write locks and the branch cache.
type Repos struct {
	git *Binary
	now func() time.Time

	mu       sync.Mutex
	locks    map[string]*sync.Mutex
	branches map[string]branchEntry
}

type branchEntry struct {
	at   time.Time
	info *api.BranchInfo
}

// New returns a Repos that runs the git of bin (the composition root shares one Binary; NewBinary(nil) when nil).
// now is the clock of the branch cache (time.Now when nil).
func New(now func() time.Time, bin *Binary) *Repos {
	if now == nil {
		now = time.Now
	}
	if bin == nil {
		bin = NewBinary(nil)
	}
	return &Repos{git: bin, now: now, locks: map[string]*sync.Mutex{}, branches: map[string]branchEntry{}}
}

// Lock takes the write lock of the repository at root and returns its unlock function. Every feature that writes
// to a repository (commits, snapshots, gates, pull requests) holds it.
func (r *Repos) Lock(ctx context.Context, root string) (func(), error) {
	k, err := fsutil.Key(root)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	m := r.locks[k]
	if m == nil {
		m = &sync.Mutex{}
		r.locks[k] = m
	}
	r.mu.Unlock()
	got := make(chan struct{})
	go func() {
		m.Lock()
		close(got)
	}()
	select {
	case <-got:
		return m.Unlock, nil
	case <-ctx.Done():
		go func() { <-got; m.Unlock() }()
		return nil, ctx.Err()
	}
}

// run runs git in dir and returns its standard output. Output beyond limit bytes (0: no limit) is dropped and
// reported by the second result.
func (r *Repos) run(ctx context.Context, dir string, timeout time.Duration, limit int, args ...string) (stdout []byte,
	truncated bool, err error) {
	git, err := r.git.Path()
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.fsmonitor=false", "-c", "core.quotepath=off", "-c", "color.ui=false"},
		args...)
	cmd := exec.Command(git, full...) //nolint:noctx // the tree is killed when ctx ends
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_LITERAL_PATHSPECS=1", "LC_ALL=C",
		"GIT_OPTIONAL_LOCKS=0")
	out := &limitedBuffer{limit: limit}
	errBuf := &limitedBuffer{limit: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = errBuf
	tree, err := platform.StartTree(cmd)
	if err != nil {
		return nil, false, &Error{Args: args, Err: err}
	}
	defer func() { _ = tree.Close() }()
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = tree.Kill(time.Second)
		<-done //nolint:errcheck // the tree was killed; its exit error adds nothing
		return nil, false, &Error{Args: args, Timeout: true, Err: ctx.Err()}
	}
	if err != nil {
		msg := strings.TrimSpace(errBuf.buf.String())
		return out.buf.Bytes(), out.over, &Error{Args: args, Stderr: msg, Err: err,
			IndexLock: strings.Contains(msg, "index.lock")}
	}
	return out.buf.Bytes(), out.over, nil
}

// limitedBuffer keeps the first limit bytes written to it (all when limit is 0).
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
	over  bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit > 0 {
		room := b.limit - b.buf.Len()
		if room <= 0 {
			b.over = b.over || len(p) > 0
			return len(p), nil
		}
		if len(p) > room {
			b.buf.Write(p[:room])
			b.over = true
			return len(p), nil
		}
	}
	b.buf.Write(p)
	return len(p), nil
}

// Root returns the top folder of the repository that contains dir, or ErrNotRepo.
func (r *Repos) Root(ctx context.Context, dir string) (string, error) {
	out, _, err := r.run(ctx, dir, ReadTimeout, 0, "rev-parse", "--show-toplevel")
	if err != nil {
		var ge *Error
		if errors.As(err, &ge) && strings.Contains(strings.ToLower(ge.Stderr), "not a git repository") {
			return "", ErrNotRepo
		}
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", ErrNotRepo
	}
	return fsutil.Canonical(root)
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " / ")
}
