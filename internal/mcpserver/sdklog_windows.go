package mcpserver

import (
	"errors"
	"syscall"
)

// osConnGone reports the Winsock codes of a peer that reset or aborted the connection ("An existing connection
// was forcibly closed by the remote host"); syscall.ECONNRESET is not one of them on Windows.
func osConnGone(err error) bool {
	return errors.Is(err, syscall.WSAECONNRESET) || errors.Is(err, syscall.WSAECONNABORTED)
}
