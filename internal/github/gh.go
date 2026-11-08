package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/NovaSeele/Seelegent/internal/platform"
)

// ErrNoGh is matched when the gh CLI is not installed.
var ErrNoGh = errors.New("the gh CLI is not installed")

// GhRunner runs one gh command (never through a shell) and returns its output. A non-zero exit is an error that
// still carries the output.
type GhRunner interface {
	Run(ctx context.Context, args []string, input []byte, env []string) (stdout, stderr []byte, err error)
}

// ExecGh runs the gh executable that Path finds ("" when gh is not installed) with Env (nil: the daemon's
// environment) and the extra variables of each call, in a hidden process tree that is killed when ctx ends.
type ExecGh struct {
	Path func() string
	Env  func() []string
}

// maxGhOutput caps what a gh command may print (a job log is the largest).
const maxGhOutput = 32 << 20

// Run implements GhRunner.
func (g ExecGh) Run(ctx context.Context, args []string, input []byte, env []string) (stdout, stderr []byte,
	err error) {
	path := ""
	if g.Path != nil {
		path = g.Path()
	}
	if path == "" {
		return nil, nil, ErrNoGh
	}
	cmd := exec.Command(path, args...) //nolint:noctx // the tree is killed when ctx ends
	base := []string(nil)
	if g.Env != nil {
		base = g.Env()
	} else {
		base = cmd.Environ()
	}
	cmd.Env = append(append([]string{}, base...), env...)
	var out, errOut capped
	out.max, errOut.max = maxGhOutput, 64<<10
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	tree, err := platform.StartTreeWith(cmd, platform.TreeOptions{ShareConsole: true}) // a fixed helper program
	if err != nil {
		return nil, nil, fmt.Errorf("start gh: %w", err)
	}
	defer func() { _ = tree.Close() }()
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = tree.Kill(time.Second)
		<-done //nolint:errcheck // killed; the exit error adds nothing
		return out.buf.Bytes(), errOut.buf.Bytes(), ctx.Err()
	}
	return out.buf.Bytes(), errOut.buf.Bytes(), err
}

// capped keeps the first max bytes written to it.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}
