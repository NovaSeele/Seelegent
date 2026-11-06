package quota

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// maxOutput caps what is kept of a helper command's output.
const maxOutput = 16 << 20

// waitDelay bounds how long a finished command may keep its output pipes open (a grandchild holding them).
const waitDelay = 2 * time.Second

// Output is what a helper command printed.
type Output struct {
	Stdout []byte
	Stderr []byte
	// Code is the exit code (-1 when the command did not exit normally).
	Code int
}

// RunFunc runs argv in dir with env and an empty standard input until it exits or ctx ends, and returns its
// output. It fails only when the command could not run or ctx ended; a non-zero exit is reported in Code.
// Tests replace it.
type RunFunc func(ctx context.Context, argv []string, dir string, env []string) (Output, error)

// Proc is a running helper process that the caller talks to over its standard input and output.
type Proc struct {
	// Stdin is the process's standard input.
	Stdin io.WriteCloser
	// Stdout is the process's standard output; it ends when the process exits or is stopped.
	Stdout io.Reader
	// Stop ends the process and everything it started, and waits for it. It is safe to call more than once.
	Stop func()
}

// StartFunc starts argv in dir with env for a conversation over standard input and output. Tests replace it.
type StartFunc func(ctx context.Context, argv []string, dir string, env []string) (*Proc, error)

// limitedBuffer keeps the first limit bytes written to it.
type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// command builds the exec.Cmd of a helper; stopping on ctx is done by killing its process tree.
func command(argv []string, dir string, env []string) (*exec.Cmd, error) {
	if len(argv) == 0 || argv[0] == "" {
		return nil, errors.New("empty command")
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // the CLI path comes from backend.Find; the whole tree is killed when ctx ends
	cmd.Dir = dir
	cmd.Env = env
	cmd.WaitDelay = waitDelay
	return cmd, nil
}

// runCommand is the real RunFunc: a hidden process tree, killed as a whole when ctx ends.
func runCommand(ctx context.Context, argv []string, dir string, env []string) (Output, error) {
	cmd, err := command(argv, dir, env)
	if err != nil {
		return Output{Code: -1}, err
	}
	stdout := &limitedBuffer{limit: maxOutput}
	stderr := &limitedBuffer{limit: maxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	tree, err := platform.StartTree(cmd)
	if err != nil {
		return Output{Code: -1}, fmt.Errorf("start %s: %w", argv[0], err)
	}
	defer func() { _ = tree.Close() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = tree.Kill(0)
		case <-done:
		}
	}()
	waitErr := tree.Wait()
	out := Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), Code: cmd.ProcessState.ExitCode()}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		return out, waitErr
	}
	return out, nil
}

// startProc is the real StartFunc: a hidden process tree with piped standard input and output; standard error is
// discarded (it may name paths and is never parsed or stored).
func startProc(_ context.Context, argv []string, dir string, env []string) (*Proc, error) {
	cmd, err := command(argv, dir, env)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	tree, err := platform.StartTree(cmd)
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("start %s: %w", argv[0], err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = stdin.Close()
			_ = tree.Kill(0)
			_ = tree.Wait()
			_ = tree.Close()
		})
	}
	return &Proc{Stdin: stdin, Stdout: stdout, Stop: stop}, nil
}
