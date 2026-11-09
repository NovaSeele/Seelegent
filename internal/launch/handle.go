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
	"sync/atomic"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Polling intervals of the daemon side.
const (
	// pollInterval is how often Wait first looks for the exit record (and a quarter of it how often Start looks
	// for the ready record).
	pollInterval = 100 * time.Millisecond
	// tailPollInterval is how often Tail looks for new output while the worker writes. A Tail that finds nothing
	// waits twice as long each time, up to maxPollInterval, and goes back to tailPollInterval as soon as it reads
	// something: a quiet worker costs one wake-up a second, not ten.
	tailPollInterval = 100 * time.Millisecond
	// maxPollInterval bounds the backoff of Tail: a line written after a long quiet stretch is read within it.
	maxPollInterval = time.Second
	// ReadDelay bounds how long after the worker writes a line Tail reads it (maxPollInterval). The daemon stamps
	// a worker event when it reads the line, so the event's time may trail what it tells by up to this much.
	ReadDelay = maxPollInterval
	// maxWaitInterval bounds the backoff of Wait.
	maxWaitInterval = 500 * time.Millisecond
	// exitCheckIdle is how often at most Tail looks for the exit record (Exited: a file open and a lock probe)
	// after a poll that read nothing, and exitCheckBusy how often at most while every poll reads lines: a worker
	// that writes is rarely ending, so a busy tail spends little on the check. The worker writes nothing after it
	// ended, so the polls after its last line read nothing: the end of a launch is seen within exitCheckIdle plus
	// one poll interval (at most 1.5 s) after its exit record is written, and every byte written before the record
	// is read (Tail reads until nothing is left once it saw the record). While Tail watches the launcher process
	// (watchLauncher), these looks cost nothing and the launcher's exit wakes Tail at once: the end is then seen
	// right after the launcher exits (it writes the record first), and within watchedCheckInterval when a launcher
	// lingers after writing it.
	exitCheckIdle = 500 * time.Millisecond
	exitCheckBusy = 2 * time.Second
	// watchedCheckInterval is how often Exited still looks at the files and the lock while the launcher process is
	// watched (watchLauncher) and runs: the watch says when the launcher ends, so these looks only catch a launch
	// directory removed under a running launcher (gone) and keep the tree of an attached launch.
	watchedCheckInterval = 5 * time.Second
	// livenessInterval is how often the launcher lock is probed while no exit record exists.
	livenessInterval = 500 * time.Millisecond
	// defaultReadyTimeout bounds how long Start waits for the launcher to report the worker running.
	defaultReadyTimeout = 30 * time.Second
	// startGrace is how long a launch without a ready record and without a live launcher counts as still starting
	// (a daemon that crashed while starting it) before it is reported lost.
	startGrace = 30 * time.Second
	// orphanGrace is how long a launcher whose launch directory was removed gets to stop its worker tree after
	// being asked to (it waits up to platform.DefaultKillGrace for the tree itself) before the daemon kills the
	// launcher and its tree.
	orphanGrace = platform.DefaultKillGrace + 5*time.Second
)

// ErrNotLaunch is returned by Attach for a directory that holds no launch spec.
var ErrNotLaunch = errors.New("not a launch directory")

// ErrGone is returned by Exited, Wait and Tail once the launch directory was removed and its launcher has ended:
// there is nothing left to follow, and waiting for it would never end. Unix lets the directory be removed while
// the launcher runs (it keeps its lock on the unlinked file); the handle then stops the launcher, and with it the
// worker tree, before it reports ErrGone, so no worker goes on writing unseen (see Handle.Exited).
var ErrGone = errors.New("the launch directory is gone")

// errDirGone is launcherAlive's answer when the lock cannot be probed because the launch directory is gone.
var errDirGone = errors.New("the launch directory is gone")

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
	// Started, when set, is called with the handle as soon as the launcher runs, before the worker starts: its
	// process tree (Handle.Tree) is known from then on, so the worker guard sees the worker from its first moment.
	Started func(*Handle)
}

// Handle follows one launch from the daemon. It is equally valid for a launch started by an earlier daemon (see
// Attach): the files and the launcher lock are what it reads. It is safe for concurrent use.
//
// # One wait per launcher
//
// A wait for a process blocks an OS thread of the daemon (WaitForSingleObject on Windows, waitid or wait4 on Unix),
// and the Go runtime never gives a thread back, so each running launcher costs one thread and its handles for every
// wait on it. There is one:
//
//   - A handle from Start owns the launcher as a child: one goroutine calls cmd.Wait, which reaps the launcher on
//     Unix (no zombie) and closes the process handle on Windows. It closes reaped when it returns, and that channel
//     is everything else's news of the launcher's end: Start's look for a launcher that died before the worker ran
//     (reapErr says how it ended) and the tails' watch (watchLauncher). platform.WatchProcess is never used for it,
//     so on Unix nothing but cmd.Wait waits for the child and no pidfd poll can race its wait4.
//   - A handle from Attach (after a daemon restart) has no child to wait for: the launcher belongs to an earlier
//     daemon. Its tails watch the launcher with platform.WatchProcess, which never reaps (a process that is not a
//     child cannot be reaped), and fall back to polling where the system cannot watch (noWatch).
//
// Either way the channel only says when to look at the files: the exit record, the ready record and the launcher
// lock stay the answer. A wait that fails also closes reaped, which merely makes Exited look at the files at every
// call from then on.
type Handle struct {
	files Files

	// reaped is closed once cmd.Wait on the launcher returned (a handle from Start; nil for Attach); reapErr is
	// what it returned, readable once reaped is closed.
	reaped  chan struct{}
	reapErr error

	mu        sync.Mutex
	lastProbe time.Time
	alive     bool

	exitMu sync.Mutex
	exit   *Exit // the exit record once found: it never changes, so it is read only once
	// ready is set once the ready record was seen: it is never removed while the launch directory exists, so
	// Exited stops looking for it.
	ready atomic.Bool

	// watch waits on the launcher process while a tail runs (watchLauncher); lastFull is when Exited last looked
	// at the files while the watch said the launcher runs.
	watchMu  sync.Mutex
	watch    *launcherWatch
	lastFull time.Time
	// noWatch is set once the system could not wait for the launcher (any WatchProcess error: no pidfd, a
	// seccomp EPERM, a BSD): the tails poll from then on instead of trying again at every look.
	noWatch bool

	treeMu     sync.Mutex
	tree       *platform.WorkerTree // set by Start, or by Tree once a record checks out
	seen       *Ready               // the last ready record read, not checked: only ever used to keep waiting
	lastVerify time.Time

	stopMu    sync.Mutex
	stopAsked time.Time      // when a launcher whose directory was removed was asked to stop
	orphans   map[int]uint64 // what is known of that launcher's tree: process id -> start time
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
	// No console: the launcher starts the worker through platform.StartWorkerTreeWith, which gives the worker a
	// hidden console of its own, so a console host for the launcher itself would be idle memory.
	cmd, err := platform.StartDetachedNoConsole(func() *exec.Cmd {
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
	h := &Handle{files: f, tree: &platform.WorkerTree{ID: f.Dir, RootPID: launcher, RootStart: start, Job: job},
		reaped: make(chan struct{})}
	// The only wait on the launcher (see Handle): it reaps it if it ends while this daemon still runs.
	go func() {
		h.reapErr = cmd.Wait()
		close(h.reaped)
	}()
	if opts.Started != nil {
		opts.Started(h)
	}

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
		case <-h.reaped:
			if exists(f.Ready) || exists(f.Exit) {
				return h, nil
			}
			return nil, fmt.Errorf("start launch: launcher exited before starting the worker: %w", h.reapErr)
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
	alive, err := h.launcherAlive()
	if err != nil {
		return unknown, true, false // the folder is gone: nothing to check the launcher against
	}
	if !ready {
		if exists(h.files.Exit) || !alive {
			return nil, false, false
		}
		if sf.Job != "" {
			return &platform.WorkerTree{ID: h.files.Dir, Job: sf.Job}, true, false // starting (Windows)
		}
		return unknown, true, false
	}
	if !alive {
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
// ReasonLost (and the record is written so later readers agree). Once a record was found, the handle keeps it.
//
// While a tail watches the launcher process (watchLauncher) and it runs, the launch has not ended (the launcher
// writes the exit record before it exits): Exited then answers without reading anything, and looks at the files
// and the lock only every watchedCheckInterval. The exit record stays the answer; the watch only says when to
// look.
func (h *Handle) Exited() (Exit, bool, error) {
	h.exitMu.Lock()
	known := h.exit
	h.exitMu.Unlock()
	if known != nil {
		return *known, true, nil
	}
	if h.launcherRuns() {
		return Exit{}, false, nil
	}
	ex, ok, err := h.exited()
	if err == nil && ok {
		h.exitMu.Lock()
		h.exit = &ex
		h.exitMu.Unlock()
	}
	return ex, ok, err
}

// exited is Exited without the kept record.
func (h *Handle) exited() (Exit, bool, error) {
	ex, ok, err := readExit(h.files.Exit)
	if err != nil || ok {
		return ex, ok, err
	}
	if !h.ready.Load() && !h.readyExists() {
		// Still starting: do not even probe the lock, the launcher may be about to take it.
		if st, err := os.Stat(h.files.Spec); err == nil && time.Since(st.ModTime()) < startGrace {
			return Exit{}, false, nil
		}
	}
	alive, err := h.launcherAlive()
	if errors.Is(err, errDirGone) {
		return h.gone()
	}
	if err != nil {
		return Exit{}, false, err
	}
	if alive {
		h.keepTree()
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

// launcherWatch is one wait on the launcher process, shared by the tails of a handle.
type launcherWatch struct {
	done   <-chan struct{}
	refs   int
	cancel context.CancelFunc
}

// watchLauncher starts (or joins) a wait on the launcher process; release ends the caller's share, and the wait
// ends with the last one. A handle from Start shares its one wait, cmd.Wait (reaped), and starts none. done is nil
// when the launcher cannot be waited for (its tree is not known or not checked yet, or the system cannot wait for
// it): the caller looks for the end by polling then, and may try again later. A launcher the system could not wait
// for once is not tried again (noWatch).
func (h *Handle) watchLauncher() (done <-chan struct{}, release func()) {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	if h.watch == nil && h.reaped != nil {
		h.watch = &launcherWatch{done: h.reaped, cancel: func() {}}
	}
	if h.watch == nil && h.noWatch {
		return nil, func() {}
	}
	if h.watch == nil {
		tree, ok := h.Tree()
		if !ok || tree.RootPID <= 0 || tree.RootStart == 0 {
			return nil, func() {}
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := platform.WatchProcess(ctx, tree.RootPID, tree.RootStart)
		if err != nil {
			cancel()
			h.noWatch = true
			return nil, func() {}
		}
		h.watch = &launcherWatch{done: ch, cancel: cancel}
	}
	w := h.watch
	w.refs++
	var once sync.Once
	return w.done, func() {
		once.Do(func() {
			h.watchMu.Lock()
			defer h.watchMu.Unlock()
			w.refs--
			if w.refs == 0 {
				w.cancel()
				if h.watch == w {
					h.watch = nil
				}
			}
		})
	}
}

// launcherRuns reports whether a watch says the launcher still runs and the files were looked at within
// watchedCheckInterval; when it is time to look again, it counts the look as done.
func (h *Handle) launcherRuns() bool {
	h.watchMu.Lock()
	defer h.watchMu.Unlock()
	if h.watch == nil {
		return false
	}
	select {
	case <-h.watch.done:
		return false
	default:
	}
	if now := time.Now(); h.lastFull.IsZero() || now.Sub(h.lastFull) >= watchedCheckInterval {
		h.lastFull = now
		return false
	}
	return true
}

// readyExists reports whether the ready record exists, and keeps a yes.
func (h *Handle) readyExists() bool {
	if !exists(h.files.Ready) {
		return false
	}
	h.ready.Store(true)
	return true
}

// keepTree makes an attached handle learn its launcher while the launch directory still exists, so that a
// directory removed later still leaves the daemon a launcher it can stop (see gone). Like Tree, it keeps only a
// record that checks out; the last record read is kept apart, unchecked, in seen.
func (h *Handle) keepTree() {
	h.treeMu.Lock()
	defer h.treeMu.Unlock()
	if h.tree != nil || (!h.lastVerify.IsZero() && time.Since(h.lastVerify) < livenessInterval) {
		return
	}
	h.lastVerify = time.Now()
	if r, ok := h.Ready(); ok {
		h.seen = &r
	}
	if t, _, verified := h.loadTree(); verified {
		h.tree = t
	}
}

// gone is Exited for a launch whose directory was removed. Windows refuses to remove it while the launcher runs,
// but Unix allows it: the launcher keeps its lock on the unlinked file and the worker keeps running. Reporting
// the launch ended then would let the task fail and free its lane while the worker goes on writing (M18 review
// N2). So the tree of a launcher the daemon knows (from Start, or from a record that checked out) is stopped
// first: the launcher is asked to stop it, and whatever of it still runs after orphanGrace, or after the launcher
// ended, is killed. A launcher known only from a record that did not check out is never signaled (the record
// may name another process): while that process runs, the launch counts as running. ErrGone comes only once
// nothing known of the launch runs.
func (h *Handle) gone() (Exit, bool, error) {
	h.treeMu.Lock()
	tree, seen := h.tree, h.seen
	h.treeMu.Unlock()
	switch {
	case tree != nil && tree.RootPID > 0:
		if h.stopOrphan(tree) {
			return Exit{}, false, nil
		}
	case tree == nil && seen != nil && seen.LauncherPID > 0 &&
		platform.ProcessRunning(seen.LauncherPID, seen.LauncherStart):
		return Exit{}, false, nil
	}
	return Exit{}, false, fmt.Errorf("launch %s: %w", h.files.Dir, ErrGone)
}

// stopOrphan stops the tree of a launcher whose launch directory was removed and reports whether any of it still
// runs. While the launcher runs, its tree is listed (with start times, so a reused id is never taken for it) and
// the launcher is asked once to stop it; a launcher that has not ended after orphanGrace is killed with what was
// listed of its tree. Processes listed earlier that outlive the launcher are killed as well.
func (h *Handle) stopOrphan(tree *platform.WorkerTree) bool {
	h.stopMu.Lock()
	defer h.stopMu.Unlock()
	launcher := platform.ProcessRunning(tree.RootPID, tree.RootStart)
	if h.orphans == nil {
		h.orphans = map[int]uint64{}
	}
	if launcher {
		h.orphans[tree.RootPID] = tree.RootStart
		pids, _ := tree.PIDs()
		for _, pid := range pids {
			if _, ok := h.orphans[pid]; !ok {
				if start, err := platform.ProcessStartTime(pid); err == nil {
					h.orphans[pid] = start
				}
			}
		}
	}
	for pid, start := range h.orphans {
		if !platform.ProcessRunning(pid, start) {
			delete(h.orphans, pid)
		}
	}
	if len(h.orphans) == 0 {
		return false
	}
	switch {
	case h.stopAsked.IsZero() && launcher:
		h.stopAsked = time.Now()
		_ = askStop(tree.RootPID) // a launcher that does not stop is killed after orphanGrace
	case !launcher || time.Since(h.stopAsked) >= orphanGrace:
		for pid := range h.orphans {
			_ = forceStop(pid) // one that has just ended is dropped on the next call
		}
	}
	return true
}

// launcherAlive probes the launcher lock, at most every livenessInterval. It fails with errDirGone when the launch
// directory no longer exists: the lock cannot be probed then, and "cannot tell" would mean waiting forever.
func (h *Handle) launcherAlive() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.lastProbe.IsZero() && time.Since(h.lastProbe) < livenessInterval {
		return h.alive, nil
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
		if _, serr := os.Stat(h.files.Dir); errors.Is(serr, os.ErrNotExist) {
			h.lastProbe = time.Time{}
			return false, errDirGone
		}
		h.alive = true // cannot tell: never declare a running worker lost
	}
	return h.alive, nil
}

// Wait waits until the launch has ended and returns its exit record. It looks every pollInterval at first, then
// less often, up to every maxWaitInterval.
func (h *Handle) Wait(ctx context.Context) (Exit, error) {
	poll := backoff{min: pollInterval, max: maxWaitInterval}
	for {
		ex, ok, err := h.Exited()
		if err != nil {
			return Exit{}, err
		}
		if ok {
			return ex, nil
		}
		if err := sleep(ctx, poll.next()); err != nil {
			return Exit{}, fmt.Errorf("wait for launch: %w", err)
		}
	}
}

// sleep waits for d or until ctx ends (its cause is returned).
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
		return nil
	}
}

// backoff is a polling interval that starts at min, doubles after every next up to max, and starts over at min
// after reset.
type backoff struct {
	min, max time.Duration
	cur      time.Duration // the next interval; zero means min
}

// next returns the interval to wait now and doubles the following one.
func (b *backoff) next() time.Duration {
	d := max(b.cur, b.min)
	b.cur = min(2*d, b.max)
	return d
}

// reset makes the next interval min again.
func (b *backoff) reset() { b.cur = 0 }

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
