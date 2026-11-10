package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"syscall"
)

// sessionGone reports whether r is one of the SDK's errors about a session that simply ended: "server run
// cancelled" when the daemon stops the session (context canceled), or "server session ended with error" when the
// host closed its end of the connection (EOF, a reset or aborted connection, a closed pipe). Both are the same
// event as the SDK's Info "server session ended", which the daemon's log does not keep either, so they must not
// show up as ERROR lines.
func sessionGone(r slog.Record) bool {
	gone := false
	r.Attrs(func(a slog.Attr) bool {
		err, ok := a.Value.Any().(error)
		if !ok {
			return true
		}
		gone = connGone(err)
		return false
	})
	return gone
}

// connGone reports whether err only says that the connection ended.
func connGone(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) || osConnGone(err)
}
