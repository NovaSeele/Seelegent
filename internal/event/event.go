// Package event defines the normalized events of a worker turn. Every backend parser turns its CLI's own output
// into these, so the task engine, the event log, the dashboard and the MCP server never see backend formats.
//
// An Event has a Kind and exactly the payload field that kind uses. Events are plain data: they marshal to JSON
// with stable camelCase field names and can be stored and replayed.
package event

import "encoding/json"

// Kind says what an event reports.
type Kind string

// Event kinds.
const (
	// KindSession: the CLI started or resumed a conversation (Session).
	KindSession Kind = "session"
	// KindMessage: a piece of the agent's answer text (Text holds the delta).
	KindMessage Kind = "message"
	// KindToolCall: the agent called a tool (Tool).
	KindToolCall Kind = "tool_call"
	// KindToolResult: a tool call finished, successfully or not (Tool).
	KindToolResult Kind = "tool_result"
	// KindFile: the agent read or edited a file (File).
	KindFile Kind = "file"
	// KindUsage: token usage reported during the turn (Usage).
	KindUsage Kind = "usage"
	// KindReport: the turn's final result (Report). Every finished turn has exactly one.
	KindReport Kind = "report"
	// KindError: the turn failed; Error says why and how to react.
	KindError Kind = "error"
	// KindLog: a line the parser did not understand or that is only informative (Text).
	KindLog Kind = "log"
	// KindBackgroundWait: the agent finished its own steps but waits for a background command (Text holds the
	// CLI's line).
	KindBackgroundWait Kind = "background_wait"
)

// Class tells the task engine how to react to a failed turn.
type Class string

// Error classes.
const (
	// ClassQuotaExhausted: the account is out of quota for this model; rest it and try another account.
	ClassQuotaExhausted Class = "quota_exhausted"
	// ClassAuthRequired: the account must sign in again (or its API key/provider is unusable).
	ClassAuthRequired Class = "auth_required"
	// ClassTransient: a network or server hiccup; retrying the same turn may work.
	ClassTransient Class = "transient"
	// ClassFatal: anything else; retrying will not help.
	ClassFatal Class = "fatal"
)

// ToolState is the state of a tool call.
type ToolState string

// Tool states.
const (
	ToolCalling ToolState = "calling"
	ToolDone    ToolState = "done"
	ToolFailed  ToolState = "failed"
)

// FileOp is what happened to a file.
type FileOp string

// File operations.
const (
	FileRead FileOp = "read"
	FileEdit FileOp = "edit"
)

// Status is the outcome a CLI reported for the turn.
type Status string

// Report statuses.
const (
	StatusSuccess Status = "success"
	StatusError   Status = "error"
)

// Event is one normalized event.
type Event struct {
	Kind Kind `json:"kind"`
	// Step orders events within a turn when the CLI numbers its steps (0 otherwise).
	Step int `json:"step,omitempty"`
	// Text is the payload of message, log and background_wait events.
	Text    string   `json:"text,omitempty"`
	Session *Session `json:"session,omitempty"`
	Tool    *Tool    `json:"tool,omitempty"`
	File    *File    `json:"file,omitempty"`
	Usage   *Usage   `json:"usage,omitempty"`
	Report  *Report  `json:"report,omitempty"`
	Error   *Error   `json:"error,omitempty"`
}

// Session identifies the conversation of the turn.
type Session struct {
	// ConversationID resumes the conversation in a later turn.
	ConversationID string `json:"conversationId"`
	// Model is the model the CLI reports (may be empty).
	Model string `json:"model,omitempty"`
}

// Tool is a tool call or its result.
type Tool struct {
	// ID links a result to its call when the CLI gives one.
	ID string `json:"id,omitempty"`
	// Name is the CLI's tool name.
	Name  string    `json:"name"`
	State ToolState `json:"state"`
	// Input is the tool's parameters as JSON (secrets masked).
	Input json.RawMessage `json:"input,omitempty"`
	// Command is the shell command line of a shell tool.
	Command string `json:"command,omitempty"`
	// Output is the tool's output or error text.
	Output string `json:"output,omitempty"`
	// DurationMs is how long the call took, when known.
	DurationMs int64 `json:"durationMs,omitempty"`
	// Blocked marks a call that Seelegent's guard refused.
	Blocked bool `json:"blocked,omitempty"`
}

// File is a file the agent read or edited.
type File struct {
	// Path is as the CLI reported it (usually absolute).
	Path string `json:"path"`
	Op   FileOp `json:"op"`
	// Tool is the tool that touched the file.
	Tool string `json:"tool,omitempty"`
}

// Usage counts tokens. Field names are camelCase everywhere (see bug B12).
type Usage struct {
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	// CachedTokens is the part of the input served from a prompt cache.
	CachedTokens int64 `json:"cachedTokens,omitempty"`
	TotalTokens  int64 `json:"totalTokens"`
	// CostUSD is the cost the CLI reports, when it does.
	CostUSD float64 `json:"costUsd,omitempty"`
}

// Add returns the sum of u and v.
func (u Usage) Add(v Usage) Usage {
	return Usage{
		InputTokens:  u.InputTokens + v.InputTokens,
		OutputTokens: u.OutputTokens + v.OutputTokens,
		CachedTokens: u.CachedTokens + v.CachedTokens,
		TotalTokens:  u.TotalTokens + v.TotalTokens,
		CostUSD:      u.CostUSD + v.CostUSD,
	}
}

// IsZero reports whether no tokens and no cost are counted.
func (u Usage) IsZero() bool { return u == Usage{} }

// Report is the final result of a turn.
type Report struct {
	Status Status `json:"status"`
	// Response is the agent's final answer.
	Response string `json:"response,omitempty"`
	// Error is the CLI's error text when Status is error.
	Error string `json:"error,omitempty"`
	// ConversationID resumes the conversation.
	ConversationID string `json:"conversationId,omitempty"`
	// Usage is the turn's total usage when the CLI reports one.
	Usage *Usage `json:"usage,omitempty"`
	// DurationMs is the turn's duration as the CLI measured it.
	DurationMs int64 `json:"durationMs,omitempty"`
	// DeniedActions lists actions the CLI's permission system refused.
	DeniedActions []string `json:"deniedActions,omitempty"`
	// Synthesized marks a report Seelegent made up because the CLI ended without one.
	Synthesized bool `json:"synthesized,omitempty"`
}

// Error is a classified failure.
type Error struct {
	Class   Class  `json:"class"`
	Message string `json:"message"`
}

// Message returns a message event.
func Message(step int, text string) Event { return Event{Kind: KindMessage, Step: step, Text: text} }

// Log returns a log event.
func Log(text string) Event { return Event{Kind: KindLog, Text: text} }

// Fail returns an error event.
func Fail(class Class, msg string) Event {
	return Event{Kind: KindError, Error: &Error{Class: class, Message: msg}}
}
