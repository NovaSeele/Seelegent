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

// tail implements Handle.Tail for one file.
func (h *Handle) tail(ctx context.Context, path string, offset int64, fn func([]byte, int64) error) error {
	if offset < 0 {
		return fmt.Errorf("tail %s: negative offset %d", path, offset)
	}
	t := tailer{path: path, next: offset, fn: fn, buf: make([]byte, tailChunk)}
	defer t.close()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// Look at the end state before reading, so that bytes written before the exit record are always read.
		_, ended, err := h.Exited()
		if err != nil {
			return err
		}
		read, err := t.readAvailable()
		if err != nil {
			return err
		}
		if read > 0 {
			continue // there may be more right away
		}
		if ended {
			return t.flush()
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("tail: %w", context.Cause(ctx))
		case <-ticker.C:
		}
	}
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
