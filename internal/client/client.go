// Package client is the typed HTTP client of the daemon's API v1 (contracts/api-v1.md), used by the MCP server and
// the command line, and the discovery that finds the daemon of a data directory and starts it when it is not
// running (Daemon).
//
// Every call takes a context; cancelling it aborts the request (an MCP cancel ends a long wait at once). Errors
// from the daemon come back as *Error with the API's code, message and details; a daemon that cannot be reached
// gives an error that matches ErrUnreachable.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// apiPrefix is the base path of the API.
const apiPrefix = "/api/v1"

// maxResponse caps a response body read into memory (reports and event pages are far smaller).
const maxResponse = 64 << 20

// ErrUnreachable is matched by errors of calls that never got an answer: nothing listens on the port, or the
// connection broke (a daemon restart).
var ErrUnreachable = errors.New("the Seelegent daemon cannot be reached")

// Error is an error answer of the daemon (contract §1.1).
type Error struct {
	// Status is the HTTP status.
	Status int
	// Code is the API error code (invalid_argument, not_found, bad_state, ...); empty when the body was not the
	// API's error shape (another program on the port).
	Code api.ErrorCode
	// Message is the daemon's message.
	Message string
	// Details are the machine-readable extras (field, caller, status, ...).
	Details map[string]any
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return e.Message
}

// Worker reports whether the daemon refused the call because it came from inside a worker (contract §1.3).
func (e *Error) Worker() bool {
	return e.Status == http.StatusForbidden && e.Details["caller"] == "worker"
}

// IsCode reports whether err is a daemon error with the given code.
func IsCode(err error, code api.ErrorCode) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// unreachableError wraps a transport error so that it matches ErrUnreachable and keeps its cause.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return ErrUnreachable.Error() + ": " + e.err.Error() }

func (e *unreachableError) Unwrap() []error { return []error{ErrUnreachable, e.err} }

// Client calls one daemon. It is safe for concurrent use.
type Client struct {
	base string
	hc   *http.Client
}

// New returns a client of the daemon at baseURL (http://127.0.0.1:<port>). hc is the HTTP client to use
// (http.DefaultClient when nil); it should have no overall timeout, since waits last up to 50 minutes.
func New(baseURL string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{base: strings.TrimSuffix(baseURL, "/"), hc: hc}
}

// ForPort returns a client of the daemon on 127.0.0.1:port.
func ForPort(port int, hc *http.Client) *Client {
	return New("http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), hc)
}

// BaseURL is the daemon's address, such as http://127.0.0.1:47821.
func (c *Client) BaseURL() string { return c.base }

// Do calls method on path (relative to /api/v1, with its query) with body as JSON (nil sends none) and decodes the
// answer into out (nil ignores it). It returns the HTTP status of a successful answer.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+apiPrefix+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Seelegent", "1")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, &unreachableError{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if isConnBroken(err) {
			return 0, &unreachableError{err: err}
		}
		return 0, fmt.Errorf("read answer of %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		return 0, decodeError(resp.StatusCode, data)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.Unmarshal(data, out); err != nil {
			return 0, fmt.Errorf("decode answer of %s %s: %w", method, path, err)
		}
	}
	return resp.StatusCode, nil
}

// isConnBroken reports whether err is a connection that broke while the answer was read.
func isConnBroken(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, net.ErrClosed)
}

// decodeError turns an error answer into *Error. A body that is not the API's error shape (another program on the
// port, an old daemon) keeps its first line as the message.
func decodeError(status int, data []byte) error {
	var body api.ErrorBody
	if err := json.Unmarshal(data, &body); err == nil && body.Error.Code != "" {
		return &Error{Status: status, Code: body.Error.Code, Message: body.Error.Message, Details: body.Error.Details}
	}
	msg := strings.TrimSpace(string(data))
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		msg = msg[:i]
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return &Error{Status: status, Message: fmt.Sprintf("HTTP %d: %s", status, msg)}
}

// query builds "?k=v&..." from pairs, skipping empty values; "" when nothing is left.
func query(pairs ...string) string {
	v := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			v.Add(pairs[i], pairs[i+1])
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

// seg escapes one path segment (an id).
func seg(s string) string { return url.PathEscape(s) }
