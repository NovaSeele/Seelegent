package backend

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// agy constants (old backends/agy.js).
const (
	// agyMaxArgvPrompt: longer prompts go through a brief file; Windows caps a command line at 32767 characters.
	agyMaxArgvPrompt = 24000
	// agyBriefName is the brief file inside the attempt directory (one per attempt, so two turns never share one).
	agyBriefName = "brief.md"
	// agyLoginResult is where the sign-in command writes agy's /usage answer.
	agyLoginResult = "seelegent-login.json"
	// agyDenied is agy's error text for a tool call refused by a pre-tool hook.
	agyDenied = "tool call denied by pre-tool hook"
)

// agyBackgroundWait matches the stderr line agy prints when the agent has finished its own steps but a command it
// started in the background still runs; agy then waits up to 45 minutes before ending the turn.
const agyBackgroundWait = `(?i)root agent idle; waiting up to [0-9hms.]+ for \d+ background task`

// Agy is the Antigravity CLI (Gemini models).
type Agy struct {
	bin string
}

// Name implements Backend.
func (*Agy) Name() string { return config.BackendAgy }

// Bin implements Backend.
func (a *Agy) Bin() string { return a.bin }

// Capabilities implements Backend.
func (*Agy) Capabilities() Capabilities {
	return Capabilities{ActiveQuota: true, NativePreToolGuard: true, StdinPrompt: false, ResumeFlag: "--conversation"}
}

// Command implements Backend:
//
//	agy -p <prompt | pointer to brief> --output-format stream-json --model M [--effort E] [--conversation ID]
//	    (--mode plan | --dangerously-skip-permissions) [--print-timeout Nm]
func (a *Agy) Command(t Turn) (Command, error) {
	bin, err := needBin(a.Name(), a.bin)
	if err != nil {
		return Command{}, err
	}
	if t.Model == "" {
		return Command{}, fmt.Errorf("agy: %w: no model", ErrModel)
	}
	prompt := t.Prompt
	if len(prompt) > agyMaxArgvPrompt {
		if t.AttemptDir == "" {
			return Command{}, fmt.Errorf("agy: a %d-character prompt needs an attempt directory for its brief file",
				len(prompt))
		}
		brief := filepath.Join(t.AttemptDir, agyBriefName)
		if err := os.MkdirAll(t.AttemptDir, 0o700); err != nil {
			return Command{}, fmt.Errorf("agy: write brief: %w", err)
		}
		if err := fsutil.WriteFileAtomic(brief, []byte(prompt), 0o600); err != nil {
			return Command{}, fmt.Errorf("agy: write brief: %w", err)
		}
		prompt = "Your full task brief is in this file (outside the workspace; read it completely before doing " +
			"anything): " + brief
	}
	argv := []string{bin, "-p", prompt, "--output-format", "stream-json", "--model", t.Model}
	if t.Effort != "" {
		argv = append(argv, "--effort", t.Effort)
	}
	if t.ConversationID != "" {
		argv = append(argv, "--conversation", t.ConversationID)
	}
	if t.Plan {
		argv = append(argv, "--mode", "plan")
	} else {
		argv = append(argv, "--dangerously-skip-permissions")
	}
	if t.Timeout > 0 {
		argv = append(argv, "--print-timeout", fmt.Sprintf("%dm", int(math.Ceil(t.Timeout.Minutes()))))
	}
	return Command{Argv: argv}, nil
}

// Filter implements account.EnvRules: agy keeps the inherited environment.
func (*Agy) Filter(_, value string, _ account.Profile) (newValue string, keep bool) {
	return value, true
}

// ProfileEnv implements account.EnvRules. An isolated profile redirects USERPROFILE (where agy keeps its
// configuration and, with the keyring bypassed, its token file) while HOME stays real; the fake SSH variables make
// agy's keyring detector skip the OS credential store, which all profiles would share. Outside Windows HOME is the
// profile home (USERPROFILE means nothing there). Self-update is off: its check opens a visible console window.
func (*Agy) ProfileEnv(p account.Profile) map[string]string {
	env := map[string]string{
		"AGY_CLI_HIDE_LOGO":           "1",
		"AGY_CLI_DISABLE_AUTO_UPDATE": "true",
		"CODEGRAPH_TELEMETRY":         "0",
		"CODEGRAPH_NO_UPDATE_CHECK":   "1",
	}
	if p.Isolated() {
		env["SSH_CONNECTION"] = "127.0.0.1 50000 127.0.0.1 22"
		env["SSH_CLIENT"] = "127.0.0.1 50000 22"
		if runtime.GOOS == "windows" {
			env["USERPROFILE"] = p.Home
			if p.RealHome != "" {
				env["HOME"] = p.RealHome
			}
		} else {
			env["HOME"] = p.Home
		}
	}
	return env
}

// KeyEnv implements account.EnvRules: agy has no API-key accounts.
func (*Agy) KeyEnv(account.Provider, string, string) (map[string]string, error) {
	return nil, fmt.Errorf("agy: %w: agy has no API-key accounts", account.ErrInvalid)
}

// Prepare implements Backend: the profile directories are enough (the git guard arrives with the task engine).
func (*Agy) Prepare(p account.Profile, _ *account.Provider) error {
	if !p.Isolated() {
		return nil
	}
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return fmt.Errorf("agy: prepare profile: %w", err)
	}
	return nil
}

// SignIn implements Backend: agy's first command in a fresh profile starts its Google sign-in, so the window runs
// `agy -p /usage --output-format json` and the result file tells whether it worked.
func (a *Agy) SignIn(p account.Profile) (SignIn, error) {
	bin, err := needBin(a.Name(), a.bin)
	if err != nil {
		return SignIn{}, err
	}
	result := filepath.Join(p.Home, agyLoginResult)
	if err := os.Remove(result); err != nil && !os.IsNotExist(err) {
		return SignIn{}, fmt.Errorf("agy: sign-in: %w", err)
	}
	return SignIn{
		Title: "Seelegent sign-in " + p.AccountID,
		Banner: []string{
			"Seelegent: sign in to Google for account " + p.AccountID,
			"1. Ctrl+Click the link below and pick the right Google account.",
			"2. Paste the code shown into this window and press Enter. You have 60 seconds.",
		},
		Argv:       []string{bin, "-p", "/usage", "--output-format", "json"},
		StdoutPath: result,
		Expires:    62 * time.Second,
		Succeeded: func(int) bool {
			data, err := fsutil.ReadFile(result)
			return err == nil && agyLoginOK(data)
		},
	}, nil
}

// agyLoginOK reports whether agy's /usage answer says SUCCESS.
func agyLoginOK(data []byte) bool {
	return regexp.MustCompile(`"status"\s*:\s*"SUCCESS"`).Match(data)
}

// QuotaGroup implements Backend (old config.js quotaGroupFor).
func (*Agy) QuotaGroup(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "argon"):
		return "Gemini 4 Argon"
	case strings.HasPrefix(m, "gemini"):
		return "Gemini Models"
	}
	return "Claude and GPT models"
}

// QuotaReader implements Backend (not yet).
func (*Agy) QuotaReader() QuotaReader { return nil }

// NewParser implements Backend.
func (*Agy) NewParser(secrets []string) Parser {
	p := &agyParser{base: newBase(config.BackendAgy, secrets, agyRules()), seen: map[string]bool{}}
	p.bgWait = regexp.MustCompile(agyBackgroundWait)
	return p
}

// agyParser parses `agy --output-format stream-json`.
type agyParser struct {
	base
	seen map[string]bool // tool steps already reported, by "step:state"
}

type agyLine struct {
	Event          string                  `json:"event"`
	ConversationID string                  `json:"conversation_id"`
	Init           *struct{ Model string } `json:"init"`
	StepUpdate     *agyStep                `json:"step_update"`
	Result         *agyResult              `json:"result"`
	DeniedActions  json.RawMessage         `json:"denied_actions"`
}

type agyStep struct {
	StepIndex int             `json:"step_index"`
	StepType  string          `json:"step_type"`
	State     string          `json:"state"`
	ToolName  string          `json:"tool_name"`
	Duration  *float64        `json:"duration_seconds"`
	TextDelta string          `json:"text_delta"`
	Usage     json.RawMessage `json:"usage"`
	ToolInfo  *struct {
		Parameters json.RawMessage `json:"parameters"`
		Output     json.RawMessage `json:"output"`
		Error      json.RawMessage `json:"error"`
	} `json:"tool_info"`
}

type agyResult struct {
	Status         string          `json:"status"`
	Response       string          `json:"response"`
	Error          json.RawMessage `json:"error"`
	Usage          json.RawMessage `json:"usage"`
	Duration       *float64        `json:"duration_seconds"`
	ConversationID string          `json:"conversation_id"`
	DeniedActions  json.RawMessage `json:"denied_actions"`
}

// Line implements Parser.
func (p *agyParser) Line(raw []byte) []event.Event {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	p.streamSeen()
	var ev agyLine
	if json.Unmarshal([]byte(line), &ev) != nil {
		return []event.Event{p.log(line)}
	}
	switch ev.Event {
	case "init":
		model := ""
		if ev.Init != nil {
			model = ev.Init.Model
		}
		return []event.Event{p.session(ev.ConversationID, model)}
	case "step_update":
		if ev.StepUpdate == nil {
			return nil
		}
		return p.step(ev.StepUpdate)
	case "result":
		if ev.Result == nil {
			ev.Result = &agyResult{}
		}
		return []event.Event{p.result(ev.Result, ev.DeniedActions)}
	}
	return []event.Event{p.log(line)}
}

func (p *agyParser) step(s *agyStep) []event.Event {
	switch s.StepType {
	case "agent_response":
		var out []event.Event
		if s.TextDelta != "" {
			out = append(out, p.message(s.StepIndex, s.TextDelta))
		}
		if s.State == "DONE" {
			if u, ok := parseUsage(s.Usage); ok {
				out = append(out, event.Event{Kind: event.KindUsage, Step: s.StepIndex, Usage: &u})
			}
		}
		return out
	case "tool":
		return p.tool(s)
	}
	return nil
}

func (p *agyParser) tool(s *agyStep) []event.Event {
	key := fmt.Sprintf("%d:%s", s.StepIndex, s.State)
	if p.seen[key] {
		return nil
	}
	p.seen[key] = true
	t := &event.Tool{Name: s.ToolName}
	var params, errRaw json.RawMessage
	if s.ToolInfo != nil {
		params, errRaw = s.ToolInfo.Parameters, s.ToolInfo.Error
		t.Output = p.output(s.ToolInfo.Output)
	}
	t.Input = p.maskJSON(params)
	t.Command = p.mask(stringField(params, "CommandLine", "commandLine", "command"))
	if s.Duration != nil {
		t.DurationMs = seconds(*s.Duration)
	}
	ev := event.Event{Kind: event.KindToolResult, Step: s.StepIndex, Tool: t}
	var out []event.Event
	switch s.State {
	case "CALLING":
		t.State = event.ToolCalling
		ev.Kind = event.KindToolCall
		out = append(out, ev)
	case "DONE":
		t.State = event.ToolDone
		out = append(out, ev)
		if f := agyFileOf(s.ToolName, params); f != nil {
			out = append(out, event.Event{Kind: event.KindFile, Step: s.StepIndex, File: f})
		}
	case "ERROR":
		t.State = event.ToolFailed
		msg := p.mask(errorText(errRaw))
		if t.Output == "" {
			t.Output = capText(msg)
		}
		t.Blocked = strings.Contains(msg, agyDenied)
		out = append(out, ev)
	default:
		return nil
	}
	return out
}

// errorText reads agy's tool error: a string or {"message": ...}.
func errorText(raw json.RawMessage) string {
	var withMsg struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &withMsg) == nil && withMsg.Message != "" {
		return withMsg.Message
	}
	return textOf(raw)
}

// agyFileOf returns the file an agy tool read or edited (old editedFileOf, plus reads).
func agyFileOf(tool string, params json.RawMessage) *event.File {
	name := strings.ToLower(tool)
	var op event.FileOp
	switch {
	case strings.Contains(name, "write") || strings.Contains(name, "replace") || strings.Contains(name, "edit") ||
		strings.Contains(name, "sed"):
		op = event.FileEdit
	case strings.Contains(name, "view") || strings.Contains(name, "read"):
		op = event.FileRead
	default:
		return nil
	}
	path := stringField(params, "Path", "TargetFile", "AbsolutePath", "File", "path", "file")
	if path == "" {
		return nil
	}
	return &event.File{Path: path, Op: op, Tool: tool}
}

func (p *agyParser) result(r *agyResult, topDenied json.RawMessage) event.Event {
	rep := event.Report{
		Status:         event.StatusError,
		Response:       r.Response,
		Error:          errorText(r.Error),
		ConversationID: r.ConversationID,
	}
	if r.Status == "SUCCESS" {
		rep.Status = event.StatusSuccess
	}
	if u, ok := parseUsage(r.Usage); ok {
		rep.Usage = &u
	}
	if r.Duration != nil {
		rep.DurationMs = seconds(*r.Duration)
	}
	denied := r.DeniedActions
	if len(denied) == 0 {
		denied = topDenied
	}
	for _, d := range stringList(denied) {
		rep.DeniedActions = append(rep.DeniedActions, p.mask(d))
	}
	return p.setReport(rep)
}
