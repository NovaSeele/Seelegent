package launch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Read sizes of Tail.
const (
	// tailChunk is how much is read at a time.
	tailChunk = 64 << 10
	// maxLine caps one delivered line; a longer run of bytes without a line break is delivered in pieces.
	maxLine = 32 << 20
)

// tail implements Handle.Tail for one file. Every poll reads whatever is there (readAvailable reads to the end of
// the file); it polls every tailPollInterval while lines come and backs off up to maxPollInterval while none do. It
// looks for the end of the launch at most every exitCheckIdle after a poll that read nothing and every
// exitCheckBusy while lines keep coming (see the constants for the bounds). Once it saw the exit record it reads
// until nothing is left: every byte written before the record is read.
//
// It also watches the launcher process (watchLauncher): while it runs, the checks cost nothing (Exited answers
// from the watch), and its end wakes the tail at once, so the end of a launch is seen right after the launcher
// exits. Without a watch the bounds of the constants apply.
func (h *Handle) tail(ctx context.Context, path string, offset int64, fn func([]byte, int64) error) error {
	if offset < 0 {
		return fmt.Errorf("tail %s: negative offset %d", path, offset)
	}
	t := tailer{path: path, next: offset, fn: fn, buf: make([]byte, tailChunk)}
	defer t.close()
	var launcherDone <-chan struct{}
	release := func() {}
	defer func() { release() }() //nolint:gocritic // release is replaced once the watch starts
	p := newTailPace()
	ended, fired := false, false
	for {
		read, err := t.readAvailable()
		if err != nil {
			return err
		}
		if ended {
			if read == 0 {
				return t.flush()
			}
			continue
		}
		now := time.Now()
		woke := false
		if launcherDone != nil && !fired {
			select {
			case <-launcherDone:
				fired, woke = true, true // look now
			default:
			}
		}
		if woke || p.checkDue(now, read == 0) {
			if launcherDone == nil {
				launcherDone, release = h.watchLauncher()
			}
			_, done, err := h.Exited()
			if err != nil {
				return err
			}
			if ended = done; ended {
				continue // read what was written before the record
			}
		}
		if read > 0 {
			p.poll.reset()
		}
		wake := launcherDone
		if fired {
			wake = nil
		}
		if err := sleepOr(ctx, p.poll.next(), wake); err != nil {
			return fmt.Errorf("tail: %w", err)
		}
	}
}

// sleepOr is sleep that also ends when wake is closed.
func sleepOr(ctx context.Context, d time.Duration, wake <-chan struct{}) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-t.C:
	case <-wake:
	}
	return nil
}

// tailPace paces a tail: poll is its wait while nothing comes, and checkDue says when to look for the exit record.
type tailPace struct {
	poll       backoff
	idle, busy time.Duration
	lastCheck  time.Time // zero: never looked
}

func newTailPace() tailPace {
	return tailPace{poll: backoff{min: tailPollInterval, max: maxPollInterval}, idle: exitCheckIdle,
		busy: exitCheckBusy}
}

// checkDue reports whether to look for the exit record at now (the first time always) after a poll that read
// nothing (idle) or read lines and, if so, counts the look as done.
func (p *tailPace) checkDue(now time.Time, idle bool) bool {
	every := p.busy
	if idle {
		every = p.idle
	}
	if !p.lastCheck.IsZero() && now.Sub(p.lastCheck) < every {
		return false
	}
	p.lastCheck = now
	return true
}

// tailer reads one growing file and delivers its lines.
type tailer struct {
	path    string
	f       *os.File
	pending []byte // bytes after the last delivered line
	next    int64  // offset of pending[0]
	fn      func([]byte, int64) error
	buf     []byte
}

// readAvailable reads what has been appended since the last call and delivers the complete lines.
func (t *tailer) readAvailable() (int, error) {
	if t.f == nil {
		f, err := os.Open(t.path)
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		if err != nil {
			return 0, fmt.Errorf("tail %s: %w", t.path, err)
		}
		t.f = f
	}
	total := 0
	for {
		n, rerr := t.f.ReadAt(t.buf, t.next+int64(len(t.pending)))
		if n > 0 {
			total += n
			t.pending = append(t.pending, t.buf[:n]...)
			var err error
			if t.pending, t.next, err = deliverLines(t.pending, t.next, t.fn); err != nil {
				return total, err
			}
		}
		if errors.Is(rerr, io.EOF) || (rerr == nil && n == 0) {
			return total, nil
		}
		if rerr != nil {
			return total, fmt.Errorf("tail %s: %w", t.path, rerr)
		}
	}
}

// flush delivers a last line that has no line break.
func (t *tailer) flush() error {
	if len(t.pending) == 0 {
		return nil
	}
	line := bytes.TrimSuffix(t.pending, []byte("\r"))
	t.next += int64(len(t.pending))
	t.pending = nil
	return t.fn(line, t.next)
}

func (t *tailer) close() {
	if t.f != nil {
		_ = t.f.Close()
	}
}

// deliverLines calls fn for every complete line in pending (which starts at byte offset next) and returns what is
// left and the offset where it starts. A run longer than maxLine without a line break is delivered as it is.
func deliverLines(pending []byte, next int64, fn func([]byte, int64) error) (rest []byte, offset int64, err error) {
	for {
		i := bytes.IndexByte(pending, '\n')
		line, after := pending, pending[:0]
		switch {
		case i >= 0:
			line, after = bytes.TrimSuffix(pending[:i], []byte("\r")), pending[i+1:]
		case len(pending) < maxLine:
			return pending, next, nil
		}
		next += int64(len(pending) - len(after))
		if err := fn(line, next); err != nil {
			return after, next, err
		}
		pending = after
	}
}
