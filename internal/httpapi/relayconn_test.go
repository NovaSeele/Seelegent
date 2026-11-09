package httpapi

import (
	"bufio"
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

// TestRelayConnStoppingDoesNotWaitForAStuckWrite: a write blocked on a relay that reads nothing holds the write
// lock; stopping must not wait behind it for ever. The stuck write fails, and the connection is closed.
func TestRelayConnStoppingDoesNotWaitForAStuckWrite(t *testing.T) {
	a, b := net.Pipe() // synchronous: a write blocks until the other end reads, and b never reads
	defer func() { _ = b.Close() }()
	c := &relayConn{c: a, r: bufio.NewReader(a), writeTimeout: time.Hour}
	wrote := make(chan error, 1)
	go func() {
		_, err := c.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n"))
		wrote <- err
	}()
	time.Sleep(100 * time.Millisecond) // the write blocks, holding the lock
	start := time.Now()
	stopped := make(chan struct{})
	go func() {
		c.stopping()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("stopping waited behind a write the relay never reads")
	}
	if d := time.Since(start); d > 4*relayStopWrite {
		t.Errorf("stopping took %s", d)
	}
	if err := <-wrote; err == nil {
		t.Error("the stuck write succeeded")
	}
	if _, err := c.Write([]byte("x\n")); err == nil {
		t.Error("a write after stopping succeeded")
	}
}

// TestRelayConnWriteTimeout: a relay that reads nothing for writeTimeout is cut off, so its session ends.
func TestRelayConnWriteTimeout(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	c := &relayConn{c: a, r: bufio.NewReader(a), writeTimeout: 200 * time.Millisecond}
	start := time.Now()
	if _, err := c.Write([]byte("{}\n")); err == nil {
		t.Fatal("a write nobody reads succeeded")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the write gave up after %s", d)
	}
	// The connection is closed (half a message may be on it): the session's reads end too.
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Error("the connection stayed open after a failed write")
	}
}

func TestWaitFor(t *testing.T) {
	var wg sync.WaitGroup
	if !waitFor(&wg, time.Second) {
		t.Error("an empty group timed out")
	}
	wg.Add(1)
	if waitFor(&wg, 50*time.Millisecond) {
		t.Error("a busy group reached zero")
	}
	wg.Done()
	if !waitFor(&wg, time.Second) {
		t.Error("the group did not reach zero after Done")
	}
}

// TestCloseIsBounded: Close does not wait for ever for a request that ignores the stop; it closes the relays'
// connections and returns.
func TestCloseIsBounded(t *testing.T) {
	s := &Server{relayConns: map[*relayConn]struct{}{}, closeWait: 100 * time.Millisecond, hub: &Hub{},
		log: slog.New(slog.DiscardHandler)}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	a, b := net.Pipe()
	defer func() { _ = b.Close() }()
	rc := &relayConn{c: a, r: bufio.NewReader(a), writeTimeout: time.Hour}
	s.relayTrack(rc, true)
	s.wg.Add(1) // a request that never ends (until the test does)
	defer s.wg.Done()
	done := make(chan struct{})
	go func() {
		s.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for ever")
	}
	if _, err := rc.Write([]byte("x\n")); err == nil {
		t.Error("Close left the relay's connection open")
	}
}
