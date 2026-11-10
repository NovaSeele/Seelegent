package mcpserver_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/mcpserver"
)

// errorLog keeps the messages of the Error records it is given.
type errorLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *errorLog) Enabled(context.Context, slog.Level) bool { return true }

func (l *errorLog) Handle(_ context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		l.mu.Lock()
		l.msgs = append(l.msgs, r.Message)
		l.mu.Unlock()
	}
	return nil
}

func (l *errorLog) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *errorLog) WithGroup(string) slog.Handler      { return l }

func (l *errorLog) errors() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// failingReader is a host connection whose first read fails with err.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
func (r failingReader) Close() error             { return nil }

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriter) Close() error                { return nil }

func newLoggedServer(t *testing.T, l *errorLog) *mcpserver.Server {
	t.Helper()
	srv, err := mcpserver.New(mcpserver.Options{Daemon: fixedDaemon{}, Version: "test", Port: 47831,
		Logger: slog.New(l), Getwd: func() (string, error) { return t.TempDir(), nil },
		Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

// TestSessionEndIsNoError: a host that closes or resets its connection ends the session without an ERROR line in
// the daemon's log; any other read error still logs one.
func TestSessionEndIsNoError(t *testing.T) {
	reset := syscall.ECONNRESET
	if runtime.GOOS == "windows" {
		reset = syscall.Errno(10054) // WSAECONNRESET: "forcibly closed by the remote host"
	}
	cases := []struct {
		name   string
		err    error
		logged bool
	}{
		{"reset", &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("wsarecv", reset)}, false},
		{"closed", &net.OpError{Op: "read", Net: "tcp", Err: net.ErrClosed}, false},
		{"unexpected EOF", io.ErrUnexpectedEOF, false},
		{"other", errors.New("disk on fire"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := &errorLog{}
			srv := newLoggedServer(t, l)
			done := make(chan error, 1)
			go func() {
				done <- srv.Run(t.Context(), &mcp.IOTransport{Reader: failingReader{err: c.err}, Writer: discardWriter{}})
			}()
			select {
			case err := <-done:
				t.Logf("Run: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("the session did not end")
			}
			if got := l.errors(); (len(got) > 0) != c.logged {
				t.Fatalf("error records = %q, want logged %v", got, c.logged)
			}
		})
	}
}

// TestStoppedSessionIsNoError: the daemon stopping a session (its context ends) logs no ERROR line.
func TestStoppedSessionIsNoError(t *testing.T) {
	l := &errorLog{}
	srv := newLoggedServer(t, l)
	st, ct := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, st) }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	cancel()
	select {
	case err := <-done:
		t.Logf("Run: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("the session did not stop")
	}
	if got := l.errors(); len(got) > 0 {
		t.Fatalf("error records = %q", got)
	}
}
