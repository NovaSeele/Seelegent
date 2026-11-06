package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Polling intervals of the daemon side.
const (
	// pollInterval is how often Tail and Wait look for new output and the exit record.
	pollInterval = 100 * time.Millisecond
	// livenessInterval is how often the launcher lock is probed while no exit record exists.
	livenessInterval = 500 * time.Millisecond
	// defaultReadyTimeout bounds how long Start waits for the launcher to report the worker running.
	defaultReadyTimeout = 30 * time.Second
	// startGrace is how long a launch without a ready record and without a live launcher counts as still starting
	// (a daemon that crashed while starting it) before it is reported lost.
	startGrace = 30 * time.Second
)

// ErrNotLaunch is returned by Attach for a directory that holds no launch spec.
var ErrNotLaunch = errors.New("not a launch directory")

// Options configures Start.
type Options struct {
	// Launcher is the command that runs the launcher; the launch directory is appended as its last argument. For
	// the daemon it is {os.Executable(), "__launch"}.
	Launcher []string
	// Dir is the launch directory. It is created if needed and must not hold an earlier launch.
	Dir string
	// Spec describes the worker.
	Spec Spec
	// Env is the full environment of the worker (it is passed through the launcher); nil means the daemon's.
	Env []string
	// ReadyTimeout bounds the wait for the launcher to start the worker; zero means 30 s.
	ReadyTimeout time.Duration
}

// Handle follows one launch from the daemon. It holds no process handle, so it is equally valid for a launch
// started by an earlier daemon (see Attach). It is safe for concurrent use.
type Handle struct {
	files Files

	mu        sync.Mutex
	lastProbe time.Time
	alive     bool

	treeMu sync.Mutex
	tree   *platform.WorkerTree // set by Start, or by Tree once a record checks out
}

// Start writes the spec into opts.Dir, starts the launcher detached and waits until the worker runs (or has
// already ended, for example because it could not be started: the exit record then says why).
func Start(ctx context.Context, opts Options) (*Handle, error) {
	if len(opts.Launcher) == 0 {
		return nil, errors.New("start launch: no launcher command")
	}
	if len(opts.Spec.Argv) == 0 {
		return nil, errors.New("start launch: empty worker command")
	}
	f := FilesIn(opts.Dir)
	if err := os.MkdirAll(f.Dir, dirPerm); err != nil {
		return nil, fmt.Errorf("start launch: %w", err)
	}
	if exists(f.Spec) {
		return nil, fmt.Errorf("start launch: %s already holds a launch", f.Dir)
	}
	job, err := platform.NewTreeName()
	if err != nil {
		return nil, fmt.Errorf("start launch: %w", err)
	}
	sf := specFile{Spec: opts.Spec, Stream: f.Stream, Stderr: f.Stderr, Exit: f.Exit, Cancel: f.Cancel, Job: job}
	if err := writeJSON(f.Spec, sf); err != nil {
		return nil, fmt.Errorf("start launch: %w", err)
	}
	argv := append(append([]string(nil), opts.Launcher...), f.Dir)
	cmd, err := platform.StartDetached(func() *exec.Cmd {
		c := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // the launcher must outlive ctx and the daemon
		c.Env = opts.Env
		c.Dir = f.Dir
		return c
	})
	if err != nil {
		return nil, fmt.Errorf("start launcher: %w", err)
	}
	// The tree is known now, from the launcher this daemon started: no file a worker could rewrite is involved,
	// and a worker cannot connect before its tree is known.
	launcher := cmd.Process.Pid
	start, _ := platform.ProcessStartTime(launcher)
	h := &Handle{files: f, tree: &platform.WorkerTree{ID: f.Dir, RootPID: launcher, RootStart: start, Job: job}}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // reap the launcher if it ends while this daemon still runs

	timeout := opts.ReadyTimeout
	if timeout <= 0 {
		timeout = defaultReadyTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval / 4)
	defer ticker.Stop()
	for {
		if exists(f.Ready) || exists(f.Exit) {
			return h, nil
		}
		select {
		case err := <-exited:
			if exists(f.Ready) || exists(f.Exit) {
				return h, nil
			}
			return nil, fmt.Errorf("start launch: launcher exited before starting the worker: %w", err)
		case <-ctx.Done():
			// Ask a launcher that is still starting to stop the worker as soon as it runs.
			_ = h.Cancel()
			return nil, fmt.Errorf("start launch: waiting for the launcher: %w", context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// Attach returns a handle for the existing launch in dir, typically after a daemon restart.
func Attach(dir string) (*Handle, error) {
	f := FilesIn(dir)
	if !exists(f.Spec) {
		return nil, fmt.Errorf("attach %s: %w", dir, ErrNotLaunch)
	}
	return &Handle{files: f}, nil
}

// Files returns the paths of the launch.
func (h *Handle) Files() Files { return h.files }

// Ready returns the ready record once the worker has started.
func (h *Handle) Ready() (Ready, bool) {
	var r Ready
	if err := readJSON(h.files.Ready, &r); err != nil {
		return Ready{}, false
	}
	return r, true
}

// Tree describes the worker's process tree for platform.SocketOwnerInTrees and platform.WorkerTree.PIDs; the
// result is a copy whose ID is the launch directory (callers that want another id, the task id, set it).
//
// A handle from Start knows the tree from the moment the launcher runs (its process id and start time, and the
// job name it chose), before the worker starts, and never reads it from files a worker could rewrite. A handle
// from Attach reads launch.json and ready.json once and keeps the result when it checks out: the job names in
// both files agree, the launcher process still runs with the recorded start time and, on Linux, it is the
// process holding the launch lock (/proc/locks). A record that does not check out while the launcher runs (or a
// running launcher without a ready record yet) gives a tree with neither root nor job: listing it fails, so the
// worker guard fails closed. ok is false when the launch has no running launcher.
func (h *Handle) Tree() (tree *platform.WorkerTree, ok bool) {
	h.treeMu.Lock()
	defer h.treeMu.Unlock()
	if h.tree == nil {
		t, ok, verified := h.loadTree()
		if !verified {
			return t, ok
		}
		h.tree = t
	}
	c := *h.tree
	return &c, true
}

// loadTree reads the tree of an attached launch. verified says whether the result may be kept.
func (h *Handle) loadTree() (t *platform.WorkerTree, ok, verified bool) {
	unknown := &platform.WorkerTree{ID: h.files.Dir} // lists nothing: callers fail closed
	var sf specFile
	if err := readJSON(h.files.Spec, &sf); err != nil {
		return unknown, true, false
	}
	r, ready := h.Ready()
	if !ready {
		if exists(h.files.Exit) || !h.launcherAlive() {
			return nil, false, false
		}
		if sf.Job != "" {
			return &platform.WorkerTree{ID: h.files.Dir, Job: sf.Job}, true, false // starting (Windows)
		}
		return unknown, true, false
	}
	if !h.launcherAlive() {
		return nil, false, false
	}
	if r.Job != sf.Job || r.LauncherPID <= 0 {
		return unknown, true, false
	}
	if start, err := platform.ProcessStartTime(r.LauncherPID); err != nil || start != r.LauncherStart {
		return unknown, true, false
	}
	if holders, err := platform.LockHolders(h.files.Lock); err == nil && !slices.Contains(holders, r.LauncherPID) {
		return unknown, true, false
	}
	return &platform.WorkerTree{ID: h.files.Dir, RootPID: r.LauncherPID, RootStart: r.LauncherStart, Job: r.Job}, true, true
}

// Cancel asks the launcher to stop the whole worker process tree. It returns at once; Wait reports the end. It is
// safe to call more than once and after the launch ended.
func (h *Handle) Cancel() error {
	if exists(h.files.Cancel) {
		return nil
	}
	if err := fsutil.WriteFileAtomic(h.files.Cancel, []byte("cancel\n"), filePerm); err != nil {
		return fmt.Errorf("cancel launch: %w", err)
	}
	return nil
}

// Exited returns the exit record if the launch has ended. A launcher that died without writing one is reported as
// ReasonLost (and the record is written so later readers agree).
func (h *Handle) Exited() (Exit, bool, error) {
	ex, ok, err := readExit(h.files.Exit)
	if err != nil || ok {
		return ex, ok, err
	}
	if !exists(h.files.Ready) {
		// Still starting: do not even probe the lock, the launcher may be about to take it.
		if st, err := os.Stat(h.files.Spec); err == nil && time.Since(st.ModTime()) < startGrace {
			return Exit{}, false, nil
		}
	}
	if h.launcherAlive() {
		return Exit{}, false, nil
	}
	// The launcher may have written its record just before releasing the lock.
	if ex, ok, err := readExit(h.files.Exit); err != nil || ok {
		return ex, ok, err
	}
	lost := Exit{Reason: ReasonLost, Code: -1, Error: "the launcher stopped without an exit record", EndedAt: time.Now().UTC()}
	if r, ok := h.Ready(); ok {
		lost.StartedAt = r.StartedAt
	}
	if err := writeJSON(h.files.Exit, lost); err != nil {
		return Exit{}, false, fmt.Errorf("record lost launch: %w", err)
	}
	return lost, true, nil
}

// launcherAlive probes the launcher lock, at most every livenessInterval.
func (h *Handle) launcherAlive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.lastProbe.IsZero() && time.Since(h.lastProbe) < livenessInterval {
		return h.alive
	}
	h.lastProbe = time.Now()
	lock, err := fsutil.TryLock(h.files.Lock)
	switch {
	case err == nil:
		_ = lock.Unlock()
		h.alive = false
	case errors.Is(err, fsutil.ErrLocked):
		h.alive = true
	default:
		h.alive = true // cannot tell: never declare a running worker lost
	}
	return h.alive
}

// Wait waits until the launch has ended and returns its exit record.
func (h *Handle) Wait(ctx context.Context) (Exit, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		ex, ok, err := h.Exited()
		if err != nil {
			return Exit{}, err
		}
		if ok {
			return ex, nil
		}
		select {
		case <-ctx.Done():
			return Exit{}, fmt.Errorf("wait for launch: %w", context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

// Tail follows the worker's stdout from byte offset (0 for the start, or the last committed offset after a daemon
// restart) and calls fn for each line, without its line break, together with the offset just after that line: the
// value to commit so that a later Tail resumes exactly after it. A last line without a line break is delivered
// once the launch has ended. The line slice is only valid during the call. Tail returns nil when the launch has
// ended and every byte was delivered, fn's error if fn fails, or the context error.
func (h *Handle) Tail(ctx context.Context, offset int64, fn func(line []byte, next int64) error) error {
	return h.tail(ctx, h.files.Stream, offset, fn)
}

// TailStderr is Tail for the worker's stderr (which also carries the launcher's own messages).
func (h *Handle) TailStderr(ctx context.Context, offset int64, fn func(line []byte, next int64) error) error {
	return h.tail(ctx, h.files.Stderr, offset, fn)
}

// StderrTail returns at most the last n bytes of the worker's stderr.
func (h *Handle) StderrTail(n int64) (string, error) {
	f, err := os.Open(h.files.Stderr)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read stderr: %w", err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("read stderr: %w", err)
	}
	from := max(st.Size()-n, 0)
	buf := make([]byte, st.Size()-from)
	if _, err := f.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read stderr: %w", err)
	}
	return strings.ToValidUTF8(string(buf), ""), nil
}
