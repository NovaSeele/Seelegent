package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// Limits of parsed text.
const (
	// maxOutput caps the tool output kept in one event; the full output stays in the stream file.
	maxOutput = 32 << 10
	// redacted replaces secrets.
	redacted = "[REDACTED]"
	// blockedMarker is what Seelegent's guard prints when it refuses a command (claude and codex).
	blockedMarker = guard.Marker
)

// base is the state every parser shares: secret masking, the accumulated answer, the report and the final
// classification.
type base struct {
	name    string
	secrets []string
	// redact, when set, masks by pattern after the literal secrets (MaskPatterns).
	redact func(string) string
	rules  []rule
	// bgWait matches the CLI's "waiting for background tasks" line on stderr (agy only).
	bgWait *regexp.Regexp

	text           strings.Builder
	conversationID string
	report         *event.Report
	waiting        string // the background-wait line, while the CLI waits
	lastError      string // last non-fatal error the CLI printed in its stream
	step           int
}

func newBase(name string, secrets []string, rules []rule) base {
	var keep []string
	for _, s := range secrets {
		if len(s) >= 4 {
			keep = append(keep, s)
		}
	}
	return base{name: name, secrets: keep, rules: rules}
}

// mask replaces every secret in s.
func (b *base) mask(s string) string {
	for _, sec := range b.secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, redacted)
		}
	}
	if b.redact != nil {
		s = b.redact(s)
	}
	return s
}

// MaskPatterns sets a pattern redactor run after the literal secrets (Parser.MaskPatterns).
func (b *base) MaskPatterns(redact func(string) string) {
	b.redact = redact
}

// maskJSON masks secrets inside raw JSON, keeping it valid.
func (b *base) maskJSON(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	masked := b.mask(string(raw))
	for _, sec := range b.secrets {
		// The secret as it appears inside a JSON string (quotes and backslashes escaped).
		if q, err := json.Marshal(sec); err == nil {
			if esc := string(q[1 : len(q)-1]); esc != sec {
				masked = strings.ReplaceAll(masked, esc, redacted)
			}
		}
	}
	if json.Valid([]byte(masked)) {
		return json.RawMessage(masked)
	}
	q, _ := json.Marshal(masked) // a string always marshals
	return q
}

// output turns a tool output (a JSON string, an array of text blocks or any JSON value) into masked, capped text.
func (b *base) output(raw json.RawMessage) string {
	return capText(b.mask(textOf(raw)))
}

// textOf returns the text of a JSON value: a string as is, an array of strings or {"text": ...} blocks joined by
// newlines, null as "", anything else as its JSON.
func textOf(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			var block struct {
				Text *string `json:"text"`
			}
			switch {
			case json.Unmarshal(item, &s) == nil:
				parts = append(parts, s)
			case json.Unmarshal(item, &block) == nil && block.Text != nil:
				parts = append(parts, *block.Text)
			default:
				parts = append(parts, string(item))
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func capText(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	cut := maxOutput
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "\n…[truncated]"
}

func utf8Start(c byte) bool { return c&0xC0 != 0x80 }

// message records answer text and returns its event.
func (b *base) message(step int, delta string) event.Event {
	delta = b.mask(delta)
	b.text.WriteString(delta)
	return event.Message(step, delta)
}

// session records the conversation id and returns its event.
func (b *base) session(id, model string) event.Event {
	if id != "" {
		b.conversationID = id
	}
	return event.Event{Kind: event.KindSession, Session: &event.Session{ConversationID: b.conversationID, Model: model}}
}

// setReport records the CLI's report (masked) and returns its event.
func (b *base) setReport(r event.Report) event.Event {
	r.Response = b.mask(r.Response)
	r.Error = b.mask(r.Error)
	if r.Response == "" {
		r.Response = b.text.String()
	}
	if r.ConversationID == "" {
		r.ConversationID = b.conversationID
	} else {
		b.conversationID = r.ConversationID
	}
	b.report = &r
	return event.Event{Kind: event.KindReport, Report: &r}
}

// log returns a log event with secrets masked.
func (b *base) log(s string) event.Event { return event.Log(capText(b.mask(s))) }

// streamSeen is called for every stream line: output after a background-wait line means the CLI went on.
func (b *base) streamSeen() { b.waiting = "" }

// Stderr implements Parser.Stderr: it recognizes the background-wait line (agy).
func (b *base) Stderr(line string) []event.Event {
	if b.bgWait == nil {
		return nil
	}
	line = strings.TrimSpace(line)
	if !b.bgWait.MatchString(line) {
		return nil
	}
	b.waiting = b.mask(line)
	return []event.Event{{Kind: event.KindBackgroundWait, Text: b.waiting}}
}

// Finish implements Parser.Finish.
func (b *base) Finish(exit launch.Exit, stderrTail string) []event.Event {
	tail := lastLines(b.mask(stderrTail), tailLines)
	var out []event.Event
	cliReport := b.report != nil
	if !cliReport {
		msg := b.lastError
		if msg == "" {
			msg = tail
		}
		if msg == "" {
			msg = exitText(b.name, exit)
		}
		out = append(out, b.setReport(event.Report{Status: event.StatusError, Error: msg, Synthesized: true}))
	}
	r := b.report
	if r.Status == event.StatusSuccess || exit.Reason == launch.ReasonCanceled {
		return out
	}
	if r.Error == "" {
		r.Error = tail
		if r.Error == "" {
			r.Error = exitText(b.name, exit)
		}
	}
	if exit.Reason == launch.ReasonTimeout && b.waiting != "" {
		// The time limit hit while the CLI only waited for a command the agent had started in the background (a dev
		// server): the agent's own work was done. The same outcome as a CLI that ends by itself in that state, with
		// no error event, so the engine continues the conversation and the report (the agent's last message) is kept
		// (field report 4, F8).
		return append(out, event.Event{Kind: event.KindBackgroundWait, Text: b.waiting})
	}
	if !cliReport {
		switch exit.Reason {
		case launch.ReasonTimeout, launch.ReasonStartFailed:
			return append(out, event.Fail(event.ClassFatal, exitText(b.name, exit)))
		case launch.ReasonLost:
			return append(out, event.Fail(event.ClassTransient, exitText(b.name, exit)))
		}
	}
	// The CLI's own error text decides; the short stderr tail only when the CLI gave no error text of its own
	// (B8: the old version searched error text and 4000 characters of stderr together).
	text := b.cliErrorText(cliReport)
	class, ok := classifyText(b.rules, text)
	if !ok && strings.TrimSpace(text) == "" {
		class, ok = classifyText(b.rules, tail)
	}
	wait := b.waiting
	if wait == "" && b.bgWait != nil && b.bgWait.MatchString(r.Error) {
		wait = r.Error
	}
	if !ok && wait != "" {
		// Not an error: the agent waits for a background command; the engine decides what to do.
		return append(out, event.Event{Kind: event.KindBackgroundWait, Text: wait})
	}
	if !ok {
		class = event.ClassFatal
	}
	return append(out, event.Fail(class, r.Error))
}

// cliErrorText is the error text the CLI itself reported: the report's error, or the last error line of its
// stream. It is empty when the report was synthesized from stderr.
func (b *base) cliErrorText(cliReport bool) string {
	if cliReport {
		return b.report.Error
	}
	return b.lastError
}

// exitText describes how the process ended.
func exitText(name string, exit launch.Exit) string {
	switch exit.Reason {
	case launch.ReasonTimeout:
		return name + " was stopped at the turn's time limit"
	case launch.ReasonCanceled:
		return name + " was canceled"
	case launch.ReasonStartFailed:
		return name + " could not be started: " + exit.Error
	case launch.ReasonLost:
		return name + " launcher disappeared without an exit record"
	}
	if exit.Error != "" {
		return fmt.Sprintf("%s exited with code %d: %s", name, exit.Code, exit.Error)
	}
	return fmt.Sprintf("%s exited with code %d and no result", name, exit.Code)
}

// Kinds of an ended process (ExitFailure.Kind), as exitText words them.
const (
	ExitTimeout      = "timeout"
	ExitCanceled     = "process_canceled"
	ExitStartFailed  = "start_failed"
	ExitLauncherLost = "launcher_lost"
	ExitError        = "exit_error"
	ExitNoResult     = "no_result"
)

// ExitFailure is how a worker process ended, read back from the text exitText gave it, so that the dashboard can
// word it in the reader's language from a code instead of showing the English.
type ExitFailure struct {
	Kind    string
	Backend string
	// Code is the exit code (exit_error and no_result).
	Code int
}

var exitTexts = []struct {
	kind string
	re   *regexp.Regexp
}{
	{ExitTimeout, regexp.MustCompile(`^(\S+) was stopped at the turn's time limit$`)},
	{ExitCanceled, regexp.MustCompile(`^(\S+) was canceled$`)},
	{ExitStartFailed, regexp.MustCompile(`^(\S+) could not be started: `)},
	{ExitLauncherLost, regexp.MustCompile(`^(\S+) launcher disappeared without an exit record$`)},
	{ExitNoResult, regexp.MustCompile(`^(\S+) exited with code (-?\d+) and no result$`)},
	{ExitError, regexp.MustCompile(`^(\S+) exited with code (-?\d+): `)},
}

// ReadExitText reads a failure message that exitText wrote (a task's error); ok is false for any other message.
func ReadExitText(msg string) (f ExitFailure, ok bool) {
	for _, e := range exitTexts {
		m := e.re.FindStringSubmatch(msg)
		if m == nil {
			continue
		}
		f = ExitFailure{Kind: e.kind, Backend: m[1]}
		if len(m) > 2 {
			f.Code, _ = strconv.Atoi(m[2])
		}
		return f, true
	}
	return ExitFailure{}, false
}

// usageJSON is the union of the usage field names the CLIs use.
type usageJSON struct {
	InputTokens       *int64   `json:"input_tokens"`
	InputTokensC      *int64   `json:"inputTokens"`
	OutputTokens      *int64   `json:"output_tokens"`
	OutputTokensC     *int64   `json:"outputTokens"`
	TotalTokens       *int64   `json:"total_tokens"`
	TotalTokensC      *int64   `json:"totalTokens"`
	CacheRead         *int64   `json:"cache_read_input_tokens"`
	CachedInput       *int64   `json:"cached_input_tokens"`
	CachedTokensC     *int64   `json:"cachedTokens"`
	CachedContentToks *int64   `json:"cached_content_token_count"`
	Cost              *float64 `json:"cost_usd"`
}

// parseUsage reads a usage object; ok is false when raw holds none.
func parseUsage(raw json.RawMessage) (event.Usage, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return event.Usage{}, false
	}
	var u usageJSON
	if json.Unmarshal(raw, &u) != nil {
		return event.Usage{}, false
	}
	first := func(ps ...*int64) int64 {
		for _, p := range ps {
			if p != nil {
				return *p
			}
		}
		return 0
	}
	out := event.Usage{
		InputTokens:  first(u.InputTokensC, u.InputTokens),
		OutputTokens: first(u.OutputTokensC, u.OutputTokens),
		CachedTokens: first(u.CachedTokensC, u.CacheRead, u.CachedInput, u.CachedContentToks),
		TotalTokens:  first(u.TotalTokensC, u.TotalTokens),
	}
	if out.TotalTokens == 0 {
		out.TotalTokens = out.InputTokens + out.OutputTokens
	}
	if u.Cost != nil {
		out.CostUSD = *u.Cost
	}
	return out, true
}

// seconds converts a duration in (fractional) seconds to milliseconds.
func seconds(s float64) int64 { return int64(s * float64(time.Second/time.Millisecond)) }

// stringField returns the first non-empty string among the named keys of a JSON object.
func stringField(params json.RawMessage, keys ...string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(params, &m) != nil {
		return ""
	}
	for _, k := range keys {
		var s string
		if json.Unmarshal(m[k], &s) == nil {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

// stringList decodes a JSON value that is a string or a list of strings.
func stringList(raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) == nil {
		list := make([]string, 0, len(items))
		for _, item := range items {
			list = append(list, textOf(item))
		}
		return list
	}
	return []string{string(raw)}
}
