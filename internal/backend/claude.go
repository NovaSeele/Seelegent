package backend

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// claudeEmptyMCP is the MCP config every claude turn gets: no servers, so a worker never reaches the lead's MCP
// servers (and never delegates recursively).
const claudeEmptyMCP = "mcp-empty.json"

// Claude is the Claude Code CLI.
type Claude struct {
	bin string
}

// Name implements Backend.
func (*Claude) Name() string { return config.BackendClaude }

// Bin implements Backend.
func (c *Claude) Bin() string { return c.bin }

// Capabilities implements Backend. Quota can be read for sign-in accounts only; API-key accounts are passive.
func (*Claude) Capabilities() Capabilities {
	return Capabilities{ActiveQuota: true, NativePreToolGuard: true, StdinPrompt: true, ResumeFlag: "--resume"}
}

// Command implements Backend; the prompt goes on standard input:
//
//	claude -p --output-format stream-json --verbose --input-format text --strict-mcp-config
//	       --mcp-config <home>/mcp-empty.json --setting-sources user
//	       --permission-mode (plan|bypassPermissions) [--model M] [--resume ID]
func (c *Claude) Command(t Turn) (Command, error) {
	bin, err := needBin(c.Name(), c.bin)
	if err != nil {
		return Command{}, err
	}
	if t.Profile.Home == "" {
		return Command{}, fmt.Errorf("claude: no profile home")
	}
	mode := "bypassPermissions"
	if t.Plan {
		mode = "plan"
	}
	argv := []string{
		bin, "-p", "--output-format", "stream-json", "--verbose", "--input-format", "text",
		"--strict-mcp-config", "--mcp-config", filepath.Join(t.Profile.Home, claudeEmptyMCP),
		"--setting-sources", "user", "--permission-mode", mode,
	}
	if t.Model != "" {
		argv = append(argv, "--model", t.Model)
	}
	if t.ConversationID != "" {
		argv = append(argv, "--resume", t.ConversationID)
	}
	if err := checkShimArgs(argv); err != nil {
		return Command{}, err
	}
	return Command{Argv: argv, Stdin: t.Prompt, HasStdin: true}, nil
}

// claudeStripped lists inherited variables that would make the worker use the lead's own credentials, endpoint,
// model or session.
func claudeStripped(name string) bool {
	switch strings.ToUpper(name) {
	case "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "CLAUDE_CONFIG_DIR", account.EnvProviderKey:
		return true
	}
	return strings.HasPrefix(strings.ToUpper(name), "CLAUDE_CODE_")
}

// Filter implements account.EnvRules: the lead's Anthropic variables and every CLAUDE_CODE_* go; a variable that
// points into the real ~/.claude goes too, except PATH, which only loses those entries.
func (*Claude) Filter(name, value string, p account.Profile) (newValue string, keep bool) {
	if claudeStripped(name) {
		return "", false
	}
	if p.RealHome == "" {
		return value, true
	}
	dir := filepath.Join(p.RealHome, ".claude")
	if strings.EqualFold(name, "PATH") {
		parts := filepath.SplitList(value)
		kept := parts[:0]
		for _, d := range parts {
			if !pointsInto(d, dir) {
				kept = append(kept, d)
			}
		}
		return strings.Join(kept, string(os.PathListSeparator)), true
	}
	if pointsInto(value, dir) {
		return "", false
	}
	return value, true
}

// pointsInto reports whether value mentions dir as a path (dir itself or something inside it).
func pointsInto(value, dir string) bool {
	v, d := value, dir
	if runtime.GOOS == "windows" {
		v, d = strings.ToLower(v), strings.ToLower(d)
	}
	for i := 0; ; {
		j := strings.Index(v[i:], d)
		if j < 0 {
			return false
		}
		end := i + j + len(d)
		if end == len(v) || strings.ContainsRune(`/\;:"'`, rune(v[end])) {
			return true
		}
		i = end
	}
}

// ProfileEnv implements account.EnvRules: CLAUDE_CONFIG_DIR is the profile home.
func (*Claude) ProfileEnv(p account.Profile) map[string]string {
	return map[string]string{"CLAUDE_CONFIG_DIR": p.Home}
}

// KeyEnv implements account.EnvRules. The official API takes the key as ANTHROPIC_API_KEY; other providers take
// it as a bearer token with their base URL. The turn's model is pinned for every model alias the CLI uses.
func (*Claude) KeyEnv(p account.Provider, key, model string) (map[string]string, error) {
	env := map[string]string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}
	if p.Official {
		env["ANTHROPIC_API_KEY"] = key
	} else {
		if strings.TrimSpace(p.BaseURL) == "" {
			return nil, fmt.Errorf("claude: provider %s has no baseUrl: %w", p.ID, account.ErrProviderUnavailable)
		}
		env["ANTHROPIC_AUTH_TOKEN"] = key
		env["ANTHROPIC_BASE_URL"] = p.BaseURL
	}
	if model != "" {
		for _, alias := range []string{"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
			"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
			env[alias] = model
		}
	}
	return env, nil
}

// Prepare implements Backend: the profile home and its empty MCP config.
func (*Claude) Prepare(p account.Profile, _ *account.Provider) error {
	if !p.Isolated() {
		return fmt.Errorf("claude: %w: claude accounts are always isolated", account.ErrInvalid)
	}
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return fmt.Errorf("claude: prepare profile: %w", err)
	}
	if err := fsutil.WriteFileAtomic(filepath.Join(p.Home, claudeEmptyMCP), []byte(`{"mcpServers":{}}`+"\n"), 0o600); err != nil {
		return fmt.Errorf("claude: prepare profile: %w", err)
	}
	return nil
}

// SignIn implements Backend: an interactive claude in the profile; the user runs /login and closes the window.
func (c *Claude) SignIn(p account.Profile) (SignIn, error) {
	bin, err := needBin(c.Name(), c.bin)
	if err != nil {
		return SignIn{}, err
	}
	return SignIn{
		Title: "Seelegent Claude sign-in " + p.AccountID,
		Banner: []string{
			"Seelegent: sign in to Claude Code for account " + p.AccountID,
			"Run /login or finish signing in in the browser.",
			"Close this window when you are done.",
		},
		Argv:      []string{bin},
		Expires:   5 * time.Minute,
		Succeeded: func(code int) bool { return code == 0 },
	}, nil
}

// QuotaGroup implements Backend: one group per backend.
func (*Claude) QuotaGroup(string) string { return "Claude" }

// QuotaReader implements Backend (not yet).
func (*Claude) QuotaReader() QuotaReader { return nil }

// NewParser implements Backend.
func (*Claude) NewParser(secrets []string) Parser {
	return &claudeParser{base: newBase(config.BackendClaude, secrets, claudeRules()), calls: map[string]claudeCall{}}
}

// claudeParser parses `claude -p --output-format stream-json --verbose`.
type claudeParser struct {
	base
	calls map[string]claudeCall // tool calls by tool_use id
}

type claudeCall struct {
	name  string
	input json.RawMessage
	step  int
	cmd   string
}

type claudeLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Message   *struct {
		Content []claudeBlock `json:"content"`
	} `json:"message"`
	// result line
	IsError       bool            `json:"is_error"`
	Result        json.RawMessage `json:"result"`
	Error         json.RawMessage `json:"error"`
	Usage         json.RawMessage `json:"usage"`
	TotalCostUSD  *float64        `json:"total_cost_usd"`
	DurationMs    *float64        `json:"duration_ms"`
	Denials       json.RawMessage `json:"permission_denials"`
	DeniedActions json.RawMessage `json:"denied_actions"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Line implements Parser.
func (p *claudeParser) Line(raw []byte) []event.Event {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	p.streamSeen()
	var ev claudeLine
	if json.Unmarshal([]byte(line), &ev) != nil {
		return []event.Event{p.log(line)}
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			return []event.Event{p.session(ev.SessionID, ev.Model)}
		}
	case "assistant":
		if ev.Message != nil {
			return p.assistant(ev.Message.Content)
		}
		return nil
	case "user":
		if ev.Message != nil {
			return p.toolResults(ev.Message.Content)
		}
		return nil
	case "result":
		return []event.Event{p.result(&ev)}
	}
	return []event.Event{p.log(line)}
}

func (p *claudeParser) assistant(blocks []claudeBlock) []event.Event {
	var out []event.Event
	for i := range blocks {
		b := &blocks[i]
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, p.message(p.step, b.Text))
				p.step++
			}
		case "tool_use":
			call := claudeCall{name: b.Name, input: p.maskJSON(b.Input), step: p.step}
			if strings.EqualFold(b.Name, "Bash") {
				call.cmd = p.mask(stringField(b.Input, "command"))
			}
			p.calls[b.ID] = call
			out = append(out, event.Event{Kind: event.KindToolCall, Step: call.step, Tool: &event.Tool{
				ID: b.ID, Name: b.Name, State: event.ToolCalling, Input: call.input, Command: call.cmd,
			}})
			// An edit counts from the moment it is attempted: a canceled turn may already have written the file.
			if f := claudeFileOf(b.Name, b.Input); f != nil && f.Op == event.FileEdit {
				out = append(out, event.Event{Kind: event.KindFile, Step: call.step, File: f})
			}
			p.step++
		}
	}
	return out
}

func (p *claudeParser) toolResults(blocks []claudeBlock) []event.Event {
	var out []event.Event
	for i := range blocks {
		b := &blocks[i]
		if b.Type != "tool_result" {
			continue
		}
		call := p.calls[b.ToolUseID]
		output := p.output(b.Content)
		t := &event.Tool{
			ID: b.ToolUseID, Name: call.name, State: event.ToolDone, Input: call.input, Command: call.cmd,
			Output: output, Blocked: strings.Contains(output, blockedMarker),
		}
		if b.IsError {
			t.State = event.ToolFailed
		}
		out = append(out, event.Event{Kind: event.KindToolResult, Step: call.step, Tool: t})
		if f := claudeFileOf(call.name, call.input); f != nil && f.Op == event.FileRead && !b.IsError {
			out = append(out, event.Event{Kind: event.KindFile, Step: call.step, File: f})
		}
	}
	return out
}

// claudeFileOf returns the file a claude tool reads or edits (old editedFileOf, plus Read).
func claudeFileOf(tool string, input json.RawMessage) *event.File {
	name := strings.ToLower(tool)
	var op event.FileOp
	switch {
	case name == "read":
		op = event.FileRead
	case strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "sed"):
		op = event.FileEdit
	default:
		return nil
	}
	path := stringField(input, "file_path", "notebook_path", "Path", "TargetFile", "AbsolutePath", "File", "path",
		"file")
	if path == "" {
		return nil
	}
	return &event.File{Path: path, Op: op, Tool: tool}
}

func (p *claudeParser) result(ev *claudeLine) event.Event {
	isError := ev.IsError || strings.HasPrefix(strings.ToLower(ev.Subtype), "error")
	rep := event.Report{Status: event.StatusSuccess, ConversationID: ev.SessionID}
	text := textOf(ev.Result)
	if isError {
		rep.Status = event.StatusError
		rep.Error = text
		if rep.Error == "" {
			rep.Error = textOf(ev.Error)
		}
		if rep.Error == "" {
			rep.Error = "claude reported an error (" + ev.Subtype + ")"
		}
	} else {
		rep.Response = text
	}
	if u, ok := parseUsage(ev.Usage); ok {
		if ev.TotalCostUSD != nil {
			u.CostUSD = *ev.TotalCostUSD
		}
		rep.Usage = &u
	}
	if ev.DurationMs != nil {
		rep.DurationMs = int64(*ev.DurationMs)
	}
	var denials []struct {
		ToolName string `json:"tool_name"`
	}
	if json.Unmarshal(ev.Denials, &denials) == nil {
		for _, d := range denials {
			if d.ToolName != "" {
				rep.DeniedActions = append(rep.DeniedActions, d.ToolName)
			}
		}
	}
	for _, d := range stringList(ev.DeniedActions) {
		rep.DeniedActions = append(rep.DeniedActions, p.mask(d))
	}
	return p.setReport(rep)
}
