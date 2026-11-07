// Package mcpserver is `seelegent mcp`: the stdio MCP server a lead host (Claude Code, Codex, agy, OpenCode) starts.
// It is thin: every tool maps onto the daemon's HTTP API v1 through internal/client, and the daemon is found or
// started on the first call (client.Daemon).
//
// Tool names, input schemas and descriptions are the old server's (tools.json), so leads and the lead skill keep
// working. A tool result carries the JSON the old server printed as its first text block, the same value as
// structuredContent (a list is wrapped as {"items": [...]}, since structured content is an object), and a short
// text summary as a second text block. Errors are tool errors (isError) whose text starts with "Seelegent error:",
// with the daemon's code, message and details as structuredContent.
//
// Tools whose daemon side is a later milestone (proof, members, meeting, consult, notes, contest, issues,
// automations) keep their schemas and answer a "not available in this version yet" tool error; any daemon route
// that answers 501 is reported the same way.
//
// The worker guard has two layers (contract §1.3). The daemon is the authority: a call from inside a worker's
// process tree gets 403 with details.caller "worker", which the server turns into the old refusal text. The server
// also keeps the old local guard: with SEELEGENT_WORKER=1 in its environment it refuses mutating tools (old
// isMutating) with the same text before calling the daemon.
//
// A host's notifications/cancelled cancels the call's context, which aborts its HTTP request (a long wait ends at
// once). It never cancels the worker tasks (old C-B4).
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/config"
)

// serverName is the MCP server name hosts register.
const serverName = "seelegent"

// workerMessage is the refusal a worker gets (the old worker guard's text).
const workerMessage = "Seelegent is not available inside a Seelegent worker."

// Unavailable lists the tools whose daemon side comes with a later milestone.
func Unavailable() map[string]bool {
	return map[string]bool{"proof": true, "members": true, "meeting": true, "consult": true, "notes": true,
		"contest": true, "issues": true, "automations": true}
}

// Daemon finds (and starts) the daemon (client.Daemon).
type Daemon interface {
	Ensure(ctx context.Context) (*client.Client, error)
}

// Options configure a Server. Daemon is required.
type Options struct {
	Daemon Daemon
	// Version is the server version reported to the host.
	Version string
	// Port is the daemon's port, for the instructions and the dashboard link.
	Port int
	// Tiers are the agy tier models named in the model descriptions.
	Tiers config.TierModels
	// ConfigErr, when set, is why config.json could not be read: every tool call fails with it (the server still
	// starts, so the lead sees the reason instead of a host that cannot start Seelegent).
	ConfigErr error
	// Getenv reads the host session variables (os.Getenv when nil).
	Getenv func(string) string
	// Getwd is the folder relative paths are resolved against (os.Getwd when nil).
	Getwd func() (string, error)
	// RetryPause is the pause before a wait reconnects after the daemon went away (1 s when 0).
	RetryPause time.Duration
	Logger     *slog.Logger
}

// Server is the MCP server.
type Server struct {
	o           Options
	mcp         *mcp.Server
	unavailable map[string]bool
}

// New builds the server with every tool registered.
func New(o Options) (*Server, error) {
	if o.Daemon == nil {
		return nil, errors.New("mcpserver: no daemon")
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Getwd == nil {
		o.Getwd = os.Getwd
	}
	if o.RetryPause <= 0 {
		o.RetryPause = time.Second
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{o: o, unavailable: Unavailable()}
	defs, err := loadTools(o.Tiers, s.unavailable)
	if err != nil {
		return nil, err
	}
	s.mcp = mcp.NewServer(&mcp.Implementation{Name: serverName, Version: o.Version}, &mcp.ServerOptions{
		Instructions: "Seelegent dispatches coding work to worker accounts (Antigravity/Gemini by default; other " +
			"enabled backends and department members on request). You are the lead: design, write precise briefs, " +
			"review and test; let workers write the code. Live view: " + s.dashboard(),
		Logger:       o.Logger,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})
	handlers := s.handlers()
	for _, d := range defs {
		h, ok := handlers[d.Name]
		if s.unavailable[d.Name] {
			h, ok = s.notAvailable(d.Name), true
		}
		if !ok {
			return nil, fmt.Errorf("mcpserver: no handler for tool %s", d.Name)
		}
		s.mcp.AddTool(&mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}, s.wrap(d.Name, h))
	}
	return s, nil
}

// MCP returns the underlying MCP server (tests connect it to an in-memory transport).
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Run serves one session on t (the stdio transport for `seelegent mcp`) until the host closes it or ctx ends. It
// looks for the daemon at once in the background, as the old server did on initialize, so the first call rarely
// waits for a start.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	if s.o.ConfigErr == nil {
		go func() {
			wctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			if _, err := s.o.Daemon.Ensure(wctx); err != nil && wctx.Err() == nil {
				s.o.Logger.Warn("mcp: daemon not ready", "err", err)
			}
		}()
	}
	err := s.mcp.Run(ctx, t)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// dashboard is the dashboard's address.
func (s *Server) dashboard() string {
	return "http://127.0.0.1:" + strconv.Itoa(s.o.Port) + "/"
}

// result is a tool's answer: the value (the JSON of the old server) and a one-line summary.
type result struct {
	value   any
	summary string
}

// call is one tool call: its arguments and, once needed, the daemon client.
type call struct {
	s    *Server
	name string
	args json.RawMessage
	c    *client.Client
}

// client returns the daemon client, finding or starting the daemon on first use.
func (k *call) client(ctx context.Context) (*client.Client, error) {
	if k.c == nil {
		c, err := k.s.o.Daemon.Ensure(ctx)
		if err != nil {
			return nil, err
		}
		k.c = c
	}
	return k.c, nil
}

// decode decodes the call's arguments into dst.
func (k *call) decode(dst any) error { return decodeArgs(k.args, dst) }

// toolFunc runs one tool.
type toolFunc func(ctx context.Context, k *call) (result, error)

// wrap turns a toolFunc into an MCP handler with the result envelope.
func (s *Server) wrap(name string, fn toolFunc) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// The worker guard runs first, as in the old server: a worker learns nothing else.
		if s.isWorker() && mutating(name, req.Params.Arguments) {
			s.o.Logger.Info("mcp: refused inside a worker", "tool", name)
			return s.fail(name, workerError{}), nil
		}
		if s.o.ConfigErr != nil && !s.unavailable[name] {
			return s.fail(name, fmt.Errorf("the settings cannot be read: %w", s.o.ConfigErr)), nil
		}
		k := &call{s: s, name: name, args: req.Params.Arguments}
		res, err := fn(ctx, k)
		if err != nil {
			if ctx.Err() != nil {
				s.o.Logger.Debug("mcp: call cancelled", "tool", name)
				return nil, ctx.Err()
			}
			s.o.Logger.Info("mcp: tool error", "tool", name, "err", err)
			return s.fail(name, err), nil
		}
		return ok(res), nil
	}
}

// notAvailable is the handler of a tool a later milestone builds.
func (s *Server) notAvailable(name string) toolFunc {
	return func(context.Context, *call) (result, error) {
		return result{}, &unavailableError{what: "the " + name + " tool"}
	}
}

// unavailableError is a feature this version does not have yet.
type unavailableError struct{ what, detail string }

func (e *unavailableError) Error() string {
	msg := e.what + " is not available in this version of Seelegent yet; nothing was done"
	if e.detail != "" {
		msg += " (" + e.detail + ")"
	}
	return msg
}

// ok builds a successful result.
func ok(r result) *mcp.CallToolResult {
	data := marshal(r.value)
	content := []mcp.Content{&mcp.TextContent{Text: string(data)}}
	if r.summary != "" {
		content = append(content, &mcp.TextContent{Text: r.summary})
	}
	var structured any = json.RawMessage(data)
	if t := bytes.TrimSpace(data); len(t) == 0 || t[0] != '{' {
		structured = map[string]json.RawMessage{"items": data}
	}
	return &mcp.CallToolResult{Content: content, StructuredContent: structured}
}

// marshal is JSON.stringify(v, null, 1) of the old server, without HTML escaping.
func marshal(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		return []byte(strconv.Quote(err.Error()))
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// errorInfo is the structured content of a tool error.
type errorInfo struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Status  int            `json:"status,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

// fail builds a tool error. A worker refusal keeps the old text; a 501 says the feature is not there yet.
func (s *Server) fail(name string, err error) *mcp.CallToolResult {
	info := errorInfo{Code: "error", Message: err.Error()}
	text := "Seelegent error: " + err.Error()
	var apiErr *client.Error
	var foreign *client.ForeignError
	var notYetErr *unavailableError
	switch {
	case errors.Is(err, workerError{}):
		text = workerMessage
		info = errorInfo{Code: string(api.CodeForbidden), Message: workerMessage,
			Details: map[string]any{"caller": "worker"}}
	case errors.As(err, &apiErr) && apiErr.Worker():
		text = workerMessage
		info = errorInfo{Code: string(apiErr.Code), Message: workerMessage, Status: apiErr.Status, Details: apiErr.Details}
	case errors.As(err, &apiErr) && (apiErr.Code == api.CodeNotImplemented || apiErr.Status == 501):
		u := &unavailableError{what: "this part of the " + name + " tool", detail: apiErr.Message}
		text = "Seelegent error: " + u.Error()
		info = errorInfo{Code: "not_available", Message: u.Error(), Status: apiErr.Status}
	case errors.As(err, &apiErr):
		info = errorInfo{Code: string(apiErr.Code), Message: apiErr.Message, Status: apiErr.Status,
			Details: apiErr.Details}
	case errors.As(err, &notYetErr):
		info.Code = "not_available"
	case errors.As(err, &foreign), errors.Is(err, client.ErrNotRunning), errors.Is(err, client.ErrUnreachable):
		info.Code = "daemon_unavailable"
	}
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: map[string]errorInfo{"error": info}}
}

// absPath resolves p against the server's folder (old C-B13: a relative cwd landed in the daemon's folder). An
// empty p stays empty.
func (s *Server) absPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	wd, err := s.o.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", p, err)
	}
	return filepath.Join(wd, p), nil
}
