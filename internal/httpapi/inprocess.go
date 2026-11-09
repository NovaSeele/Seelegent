package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// inProcessKey carries the caller of an in-process request (inProcessClient). Only a context built in this process
// can hold it: no request from the network reaches it, so callerOf trusts it.
type inProcessKey struct{}

// errInProcessStopping answers an in-process request once Close began.
var errInProcessStopping = errors.New("the Seelegent daemon is stopping")

// inProcessClient returns an HTTP client whose requests this server handles in process, with c as their caller:
// the tools of a relayed MCP session call the API through it, so every call passes the same checks (Host,
// X-Seelegent, the worker guard with the relay's caller) as from `seelegent mcp`, and nothing goes over the
// network. Its requests end when Close begins.
func (s *Server) inProcessClient(c Caller) *http.Client {
	return &http.Client{Transport: inProcessTransport{s: s, caller: c}}
}

type inProcessTransport struct {
	s      *Server
	caller Caller
}

// RoundTrip serves req with the server's handler in a goroutine and returns as soon as the handler wrote its
// header; the body streams through a pipe.
func (t inProcessTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s := t.s
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		closeBody(req)
		return nil, errInProcessStopping
	}
	s.wg.Add(1)
	s.mu.Unlock()
	ctx, cancel := context.WithCancel(context.WithValue(req.Context(), inProcessKey{}, t.caller))
	stop := context.AfterFunc(s.ctx, cancel)
	r := req.Clone(ctx)
	if r.Body == nil {
		r.Body = http.NoBody
	}
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	r.RequestURI = r.URL.RequestURI()
	r.RemoteAddr = "in-process"
	pr, pw := io.Pipe()
	rw := &pipeResponse{header: http.Header{}, pw: pw, ready: make(chan struct{})}
	go func() {
		defer s.wg.Done()
		defer stop()
		defer cancel()
		defer func() {
			if v := recover(); v != nil {
				rw.fail(errors.New("the handler aborted"))
				return
			}
			rw.finish()
		}()
		s.Handler().ServeHTTP(rw, r)
		_ = r.Body.Close() // a RoundTripper closes the request body, as the server does after the handler
	}()
	select {
	case <-rw.ready:
	case <-ctx.Done():
		_ = pr.CloseWithError(ctx.Err())
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		return nil, errInProcessStopping
	}
	return &http.Response{Status: http.StatusText(rw.status), StatusCode: rw.status, Proto: "HTTP/1.1",
		ProtoMajor: 1, ProtoMinor: 1, Header: rw.sent, Body: pr, ContentLength: -1, Request: req}, nil
}

// closeBody closes a request body the transport does not send.
func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// pipeResponse is the http.ResponseWriter of an in-process request: the header is handed over once written, the
// body goes through the pipe (a Write waits for the client to read it).
type pipeResponse struct {
	header http.Header
	pw     *io.PipeWriter

	once   sync.Once
	ready  chan struct{}
	status int
	sent   http.Header
}

func (p *pipeResponse) Header() http.Header { return p.header }

func (p *pipeResponse) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		return // informational answers are not passed on
	}
	p.once.Do(func() {
		p.status, p.sent = status, p.header.Clone()
		close(p.ready)
	})
}

func (p *pipeResponse) Write(b []byte) (int, error) {
	p.WriteHeader(http.StatusOK)
	return p.pw.Write(b)
}

// Flush implements http.Flusher; every Write already reaches the reader.
func (p *pipeResponse) Flush() {}

// finish ends the body once the handler returned.
func (p *pipeResponse) finish() {
	p.WriteHeader(http.StatusOK)
	_ = p.pw.Close()
}

func (p *pipeResponse) fail(err error) {
	p.WriteHeader(http.StatusInternalServerError)
	_ = p.pw.CloseWithError(err)
}
