package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// cancelPoll is how often the launcher looks for the cancel file.
const cancelPoll = 200 * time.Millisecond

// lockWait is how long the launcher waits for the launch lock.
const lockWait = 2 * time.Second

// ErrAlreadyRunning is returned by Run when another launcher holds the launch directory.
var ErrAlreadyRunning = errors.New("a launcher is already running for this launch")

// ErrAlreadyFinished is returned by Run when the launch already has an exit record.
var ErrAlreadyFinished = errors.New("launch already finished")

// Run is the launcher: the body of `seelegent __launch <dir>`. It runs the worker described by dir's spec to the
// end (see the package documentation) and writes the exit record. Its own environment becomes the worker's. When
// ctx ends the worker tree is stopped as if it had been canceled. Run returns an error only when it could not run
// the launch at all; how the worker ended is in the exit record.
func Run(ctx context.Context, dir string) error {
	f := FilesIn(dir)
	// A daemon probing liveness holds the lock for a moment: wait a little before giving up.
	lockCtx, cancel := context.WithTimeout(ctx, lockWait)
	lock, err := fsutil.Lock(lockCtx, f.Lock)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("launch %s: %w", dir, ErrAlreadyRunning)
	}
	if err != nil {
		return fmt.Errorf("launch %s: %w", dir, err)
	}
	defer func() { _ = lock.Unlock() }()

	if exists(f.Exit) {
		return fmt.Errorf("launch %s: %w", dir, ErrAlreadyFinished)
	}
	var sf specFile
	if err := readJSON(f.Spec, &sf); err != nil {
		ex := Exit{Reason: ReasonStartFailed, Code: -1, Error: "read spec: " + err.Error(), EndedAt: time.Now().UTC()}
		return errors.Join(err, writeJSON(f.Exit, ex))
	}
	ex := supervise(ctx, f, sf)
	if err := writeJSON(sf.Exit, ex); err != nil {
		return fmt.Errorf("launch %s: write exit record: %w", dir, err)
	}
	return nil
}

// supervise starts the worker, follows it until it ends and returns the exit record.
func supervise(ctx context.Context, f Files, sf specFile) Exit {
	failed := func(err error) Exit {
		appendLine(sf.Stderr, "launcher: "+err.Error())
		return Exit{Reason: ReasonStartFailed, Code: -1, Error: err.Error(), EndedAt: time.Now().UTC()}
	}
	if len(sf.Argv) == 0 {
		return failed(errors.New("empty command"))
	}
	name := sf.Job
	if name == "" { // a spec written before trees had names
		var err error
		if name, err = platform.NewTreeName(); err != nil {
			return failed(err)
		}
	}
	cmd, closeFiles, err := workerCommand(sf)
	if err != nil {
		return failed(err)
	}
	tree, err := platform.StartWorkerTreeWith(cmd, name, sf.Tree)
	closeFiles()
	if err != nil {
		return failed(fmt.Errorf("start %s: %w", sf.Argv[0], err))
	}
	defer func() { _ = tree.Close() }()
	if refused := tree.Unapplied(); len(refused) > 0 {
		appendLine(sf.Stderr, "launcher: the system refused these limits of the worker process tree, which runs "+
			"without them: "+strings.Join(refused, ", "))
	}
	started := time.Now().UTC()
	wt := tree.WorkerTree(f.Dir)
	ready := Ready{LauncherPID: os.Getpid(), LauncherStart: wt.RootStart, Job: wt.Job, WorkerPID: tree.Pid(), StartedAt: started}
	if err := writeJSON(f.Ready, ready); err != nil {
		// Without the record a restarted daemon cannot know the worker's tree: do not run it unseen.
		ex := failed(fmt.Errorf("write ready record: %w", err))
		ex.StartedAt = started
		return ex // the deferred Close stops the tree
	}

	waited := make(chan struct{})
	go func() {
		_ = tree.Wait() // the exit code comes from tree.ExitCode
		close(waited)
	}()

	reason := ReasonExited
	memHit := false
	var timeout <-chan time.Time
	if sf.Timeout > 0 {
		t := time.NewTimer(sf.Timeout)
		defer t.Stop()
		timeout = t.C
	}
	ticker := time.NewTicker(cancelPoll)
	defer ticker.Stop()
	grace := sf.KillGrace
	if grace <= 0 {
		grace = platform.DefaultKillGrace
	}
	stop := func(r Reason, msg string) {
		reason = r
		appendLine(sf.Stderr, "launcher: "+msg)
		if err := tree.Kill(grace); err != nil {
			appendLine(sf.Stderr, "launcher: stop worker: "+err.Error())
		}
	}

loop:
	for {
		select {
		case <-waited:
			break loop
		case <-ticker.C:
			if !memHit && tree.Poll() {
				memHit = true
				noteMemoryLimit(sf)
			}
			if reason == ReasonExited && exists(sf.Cancel) {
				stop(ReasonCanceled, "canceled; stopping the worker process tree")
			}
		case <-timeout:
			if reason == ReasonExited {
				stop(ReasonTimeout, fmt.Sprintf("timeout after %s; stopping the worker process tree", sf.Timeout))
			}
		case <-ctx.Done():
			if reason == ReasonExited {
				stop(ReasonCanceled, "launcher stopped; stopping the worker process tree")
			}
			ctx = context.Background() // stop once; keep waiting for the worker
		}
	}
	// Read before Close: the job (and its notifications) go with it.
	if !memHit && tree.MemoryLimitHit() {
		memHit = true
		noteMemoryLimit(sf)
	}
	// Anything the worker left behind (background commands) ends with the turn.
	if err := tree.Close(); err != nil {
		appendLine(sf.Stderr, "launcher: stop leftover processes: "+err.Error())
	}
	return Exit{Reason: reason, Code: tree.ExitCode(), StartedAt: started, MemoryLimitHit: memHit,
		EndedAt: time.Now().UTC()}
}

// noteMemoryLimit says in stderr.txt that the worker tree ran into its memory cap.
func noteMemoryLimit(sf specFile) {
	appendLine(sf.Stderr, fmt.Sprintf("launcher: the worker process tree reached its memory limit (%d MB)",
		sf.Tree.MemoryLimitMB))
}

// workerCommand builds the worker command with its files. closeFiles releases the launcher's copies of the files
// once the worker has inherited them.
func workerCommand(sf specFile) (*exec.Cmd, func(), error) {
	var opened []*os.File
	closeAll := func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}
	open := func(path string, flag int) (*os.File, error) {
		f, err := os.OpenFile(path, flag, filePerm)
		if err != nil {
			return nil, err
		}
		opened = append(opened, f)
		return f, nil
	}
	const appendFlags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	stdout, err := open(sf.Stream, appendFlags)
	if err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("open stream: %w", err)
	}
	stderr, err := open(sf.Stderr, appendFlags)
	if err != nil {
		closeAll()
		return nil, nil, fmt.Errorf("open stderr: %w", err)
	}
	// No context here: exec would kill only the root process, while supervise stops the whole tree.
	cmd := exec.Command(sf.Argv[0], sf.Argv[1:]...) //nolint:gosec,noctx // the worker command is the purpose of the launcher
	cmd.Dir = sf.Dir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if sf.StdinPath != "" {
		stdin, err := open(sf.StdinPath, os.O_RDONLY)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("open stdin: %w", err)
		}
		cmd.Stdin = stdin
	}
	return cmd, closeAll, nil
}

// appendLine appends a line to path, ignoring errors: it only feeds diagnostics.
func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, filePerm)
	if err != nil {
		return
	}
	_, _ = f.WriteString("\n" + line + "\n")
	_ = f.Close()
}
