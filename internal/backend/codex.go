package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// codexProviderID is the model provider Seelegent writes into config.toml for API-key accounts.
const codexProviderID = "seelegent"

// codexWindowsSandbox is the -c override that picks codex's unelevated Windows sandbox. Without a [windows] table
// codex 0.167 refuses every shell command of a read-only or workspace-write turn ("rejected: blocked by policy");
// the "elevated" sandbox needs a one-time administrator setup, so Seelegent never asks for it. The value is left
// bare rather than quoted as TOML: codex reads a value that does not parse as TOML as a literal string, and a double
// quote is one of the characters a codex.cmd shim cannot pass through cmd.exe (checkShimArgs).
const codexWindowsSandbox = "windows.sandbox=unelevated"

// Codex is the OpenAI Codex CLI.
type Codex struct {
	bin   string
	quota QuotaReader
	// goos is the operating system the CLI runs on (runtime.GOOS; tests set another).
	goos string
}

// Name implements Backend.
func (*Codex) Name() string { return config.BackendCodex }

// Bin implements Backend.
func (c *Codex) Bin() string { return c.bin }

// Capabilities implements Backend. codex has no pre-tool hook, so the git guard relies on the sandbox mode. Its
// only way to read files is the shell, which the read-only sandbox of a plan turn runs for commands that write
// nothing.
func (*Codex) Capabilities() Capabilities {
	return Capabilities{ActiveQuota: true, NativePreToolGuard: false, StdinPrompt: true, ResumeFlag: "resume",
		PlanShell: true}
}

// codexHome is CODEX_HOME of a profile.
func codexHome(p account.Profile) string { return filepath.Join(p.Home, ".codex") }

// Command implements Backend; the prompt goes on standard input ("-"):
//
//	codex exec --json --sandbox (read-only | <sandbox>) [-c windows.sandbox=unelevated] [--model M]
//	    --skip-git-repo-check [resume ID] -
//
// The exec options come before the resume subcommand: `codex exec resume` of codex-cli 0.167.0 has no --sandbox
// ("unexpected argument '--sandbox' found", exit 2), so a continued turn written as `exec resume ID --sandbox ...`
// never started. The -c override is added on Windows when the sandbox is read-only or workspace-write.
func (c *Codex) Command(t Turn) (Command, error) {
	bin, err := needBin(c.Name(), c.bin)
	if err != nil {
		return Command{}, err
	}
	argv := []string{bin, "exec"}
	sandbox := t.Sandbox
	switch {
	case t.Plan:
		sandbox = config.SandboxReadOnly
	case sandbox == "":
		sandbox = config.SandboxWorkspaceWrite
	}
	switch sandbox {
	case config.SandboxReadOnly, config.SandboxWorkspaceWrite, config.SandboxFullAccess:
	default:
		return Command{}, fmt.Errorf("codex: unknown sandbox mode %q", sandbox)
	}
	argv = append(argv, "--json", "--sandbox", sandbox)
	if c.goos == "windows" && sandbox != config.SandboxFullAccess {
		argv = append(argv, "-c", codexWindowsSandbox)
	}
	if t.Model != "" {
		argv = append(argv, "--model", t.Model)
	}
	// exec refuses to run outside a git repository without this flag; tasks may run in plain folders.
	argv = append(argv, "--skip-git-repo-check")
	if t.ConversationID != "" {
		argv = append(argv, "resume", t.ConversationID)
	}
	argv = append(argv, "-")
	if err := checkShimArgs(argv, c.goos); err != nil {
		return Command{}, err
	}
	return Command{Argv: argv, Stdin: t.Prompt, HasStdin: true}, nil
}

// Filter implements account.EnvRules: the lead's OpenAI credentials, endpoint and organization, and every CODEX_*
// variable, must not reach the worker.
func (*Codex) Filter(name, value string, _ account.Profile) (newValue string, keep bool) {
	switch strings.ToUpper(name) {
	case "OPENAI_API_KEY", "OPENAI_BASE_URL", "OPENAI_ORG_ID", "OPENAI_ORGANIZATION", account.EnvProviderKey:
		return "", false
	}
	if strings.HasPrefix(strings.ToUpper(name), "CODEX_") {
		return "", false
	}
	return value, true
}

// ProfileEnv implements account.EnvRules: CODEX_HOME is <home>/.codex, and on Windows USERPROFILE is the profile
// home.
func (c *Codex) ProfileEnv(p account.Profile) map[string]string {
	env := map[string]string{"CODEX_HOME": codexHome(p)}
	if c.goos == "windows" {
		env["USERPROFILE"] = p.Home
	}
	return env
}

// KeyEnv implements account.EnvRules: config.toml names SEELEGENT_PROVIDER_KEY as the provider's env_key.
func (*Codex) KeyEnv(_ account.Provider, key, _ string) (map[string]string, error) {
	return map[string]string{account.EnvProviderKey: key}, nil
}

// Prepare implements Backend: it writes config.toml. An API-key account gets approval_policy "never" and the
// [model_providers.seelegent] block for its provider; a sign-in account loses any such block (and gets
// approval_policy "never" in a new file). No MCP servers are ever configured.
func (*Codex) Prepare(p account.Profile, provider *account.Provider) error {
	if !p.Isolated() {
		return fmt.Errorf("codex: %w: codex accounts are always isolated", account.ErrInvalid)
	}
	dir := codexHome(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("codex: prepare profile: %w", err)
	}
	path := filepath.Join(dir, "config.toml")
	var content string
	if provider != nil {
		if strings.TrimSpace(provider.BaseURL) == "" {
			return fmt.Errorf("codex: provider %s has no baseUrl: %w", provider.ID, account.ErrProviderUnavailable)
		}
		content = codexAPIKeyConfig(*provider)
	} else {
		old, err := fsutil.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			content = codexHeader + "approval_policy = \"never\"\n"
		case err != nil:
			return fmt.Errorf("codex: prepare profile: %w", err)
		default:
			stripped, changed := stripSeelegentProvider(string(old))
			if !changed {
				return nil
			}
			content = stripped
		}
	}
	if err := fsutil.WriteFileAtomic(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("codex: prepare profile: %w", err)
	}
	return nil
}

const codexHeader = "# Seelegent-managed configuration for an isolated worker profile.\n" +
	"# Non-interactive execution: approvals are never requested interactively.\n"

// codexAPIKeyConfig is config.toml of an API-key account.
func codexAPIKeyConfig(p account.Provider) string {
	name := p.Name
	if name == "" {
		name = p.ID
	}
	wire := p.WireAPI
	if wire == "" {
		wire = account.WireChat
	}
	return codexHeader +
		"approval_policy = \"never\"\n" +
		"model_provider = \"" + codexProviderID + "\"\n\n" +
		"[model_providers." + codexProviderID + "]\n" +
		"name = " + tomlString(name) + "\n" +
		"base_url = " + tomlString(p.BaseURL) + "\n" +
		"env_key = \"" + account.EnvProviderKey + "\"\n" +
		"wire_api = " + tomlString(wire) + "\n"
}

// stripSeelegentProvider removes `model_provider = "seelegent"` and the [model_providers.seelegent] table.
func stripSeelegentProvider(s string) (out string, changed bool) {
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	inTable := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			inTable = t == "[model_providers."+codexProviderID+"]"
			if inTable {
				changed = true
				continue
			}
		}
		if inTable {
			continue
		}
		if k, v, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == "model_provider" &&
			strings.Trim(strings.TrimSpace(v), `"'`) == codexProviderID {
			changed = true
			continue
		}
		b.WriteString(l)
	}
	return b.String(), changed
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f || r == utf8.RuneError:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// SignIn implements Backend: `codex login` in the profile; exit code 0 means signed in.
func (c *Codex) SignIn(p account.Profile) (SignIn, error) {
	bin, err := needBin(c.Name(), c.bin)
	if err != nil {
		return SignIn{}, err
	}
	return SignIn{
		Title:     "Seelegent Codex sign-in " + p.AccountID,
		Banner:    []string{"Seelegent: sign in to OpenAI Codex for account " + p.AccountID},
		Argv:      []string{bin, "login"},
		Expires:   5 * time.Minute,
		Succeeded: func(code int) bool { return code == 0 },
	}, nil
}

// QuotaGroup implements Backend.
func (*Codex) QuotaGroup(string) string { return "Codex" }

// QuotaReader implements Backend.
func (c *Codex) QuotaReader() QuotaReader { return c.quota }

// NewParser implements Backend.
func (*Codex) NewParser(secrets []string) Parser {
	return &codexParser{base: newBase(config.BackendCodex, secrets, codexRules()), steps: map[string]int{}}
}

// codexParser parses `codex exec --json`.
type codexParser struct {
	base
	lastAgentMessage string
	steps            map[string]int // step number of each item id
	// held is the turn's report event, kept from turn.completed or turn.failed until Finish: commands codex's own
	// policy refused are known only from standard error, which is read alongside the stream, so the report gets
	// them (DeniedActions) once both have been read. A turn still has exactly one report event.
	held *event.Event
	// policyDenied are the commands codex refused with "rejected: blocked by policy", in first-seen order.
	policyDenied []string
	// ranCommand is set by any command_execution item: codex ran at least one shell command.
	ranCommand bool
	// baseline is the thread's usage total before this run (SetUsageBaseline) and thread the total turn.completed
	// reported: see codexRunUsage.
	baseline event.Usage
	thread   *event.Usage
}

// The usage of turn.completed is the thread's running total, not this run's. In openai/codex at rust-v0.167.0,
// codex exec --json fills it from usage.total of ThreadTokenUsage (codex-rs/exec/src/
// event_processor_with_jsonl_output.rs:118-128, usage_from_last_total; set at :511-513, sent at :535-537), and a
// resumed thread starts from the total of its recorded rollout (codex-rs/core/src/session/mod.rs:1746-1751, "Seed
// usage info from the recorded rollout"). ThreadTokenUsage's `last` is one model request, not the run. So `codex
// exec resume` reports every earlier run of the thread again (openai/codex issue #49574; exec reported
// total_token_usage in 0.44.0 and 0.100.0 too). The parser takes the total before the run from the task engine
// (SetUsageBaseline) and reports the difference; ThreadUsage hands the new total back for the next run.

// SetUsageBaseline sets the thread's usage total before this run: the total codex reported at the end of the
// thread's previous run, zero for a new thread.
func (p *codexParser) SetUsageBaseline(u event.Usage) { p.baseline = u }

// ThreadUsage returns the thread's usage total turn.completed reported; ok is false when it reported none (or only
// zeros: codex sends zeros when no token count arrived in the run).
func (p *codexParser) ThreadUsage() (event.Usage, bool) {
	if p.thread == nil {
		return event.Usage{}, false
	}
	return *p.thread, true
}

// codexRunUsage is one run's part of the thread total: total minus base, field by field. A total below base in any
// field means codex's counter started again (another thread, or the count replaced after the context window filled),
// and total is the run's usage as is.
func codexRunUsage(total, base event.Usage) event.Usage {
	if total.InputTokens < base.InputTokens || total.OutputTokens < base.OutputTokens ||
		total.CachedTokens < base.CachedTokens || total.TotalTokens < base.TotalTokens || total.CostUSD < base.CostUSD {
		return total
	}
	return event.Usage{
		InputTokens:  total.InputTokens - base.InputTokens,
		OutputTokens: total.OutputTokens - base.OutputTokens,
		CachedTokens: total.CachedTokens - base.CachedTokens,
		TotalTokens:  total.TotalTokens - base.TotalTokens,
		CostUSD:      total.CostUSD - base.CostUSD,
	}
}

// codexPolicyRejected is what codex prints on standard error after a command its policy refused, for example
//
//	ERROR codex_core::tools::router: error=exec_command failed: CreateProcess { message: "Rejected(\"`<command>`
//	rejected: blocked by policy\")" }
//
// The stream shows no command item for such a call, and codex still ends the turn with turn.completed and the
// agent's own account of it; Finish puts the refused commands in the report's denied actions and fails the turn when
// no command ran at all.
const codexPolicyRejected = "rejected: blocked by policy"

// Stderr implements Parser: a command refused by codex's policy is remembered for the report and logged.
func (p *codexParser) Stderr(line string) []event.Event {
	i := strings.Index(line, codexPolicyRejected)
	if i < 0 {
		return nil
	}
	cmd := p.mask(policyCommand(line))
	if cmd == "" {
		cmd = p.mask(strings.TrimSpace(line))
	}
	p.policyDenied = appendUnique(p.policyDenied, cmd)
	return []event.Event{p.log("codex refused a command (" + codexPolicyRejected + "): " + cmd)}
}

// policyCommand is the command codex quoted in backticks in a "rejected: blocked by policy" line: from the backtick
// that opens it (right after `Rejected(` and its quote) to the backtick before " rejected: blocked by policy", so
// a backtick inside the command (PowerShell's escape character) does not cut it. The escape levels are read once
// from the opening and undone that many times, so a UNC path's \\ survives. Without `Rejected(` the command runs
// from the first backtick, unescaped. "" when the line quotes no command.
func policyCommand(line string) string {
	end := strings.Index(line, "` "+codexPolicyRejected)
	if end < 0 {
		return ""
	}
	head := line[:end]
	levels, start := 0, -1
	if i := strings.Index(head, "Rejected("); i >= 0 {
		rest := head[i+len("Rejected("):]
		// How the quoted command opens, by levels of Rust debug escaping: the reason as is, debug-formatted once
		// (Rejected(\"`...) or twice (Rejected(\\\"`...).
		for n, open := range []string{"\"`", `\"` + "`", `\\\"` + "`"} {
			if strings.HasPrefix(rest, open) {
				levels, start = n, i+len("Rejected(")+len(open)
				break
			}
		}
	}
	if start < 0 {
		start = strings.IndexByte(head, '`') + 1
		if start == 0 {
			return ""
		}
	}
	cmd := head[start:]
	for range levels {
		cmd = unescapeDebug(cmd)
	}
	return strings.TrimSpace(cmd)
}

// unescapeDebug undoes one level of \\ and \" escapes.
func unescapeDebug(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '"') {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Finish implements Parser: it emits the held report, with the commands codex's policy refused among its denied
// actions, before the closing events of the base parser. A turn that codex reported as a success although its
// policy refused every shell command the agent tried (and none ran) fails instead (fatal: another account runs
// with the same sandbox), so that it reaches the lead as a failed task rather than as done work.
func (p *codexParser) Finish(exit launch.Exit, stderrTail string) []event.Event {
	out := p.base.Finish(exit, stderrTail)
	if r := p.report; r != nil {
		for _, c := range p.policyDenied {
			r.DeniedActions = appendUnique(r.DeniedActions, c)
		}
		if r.Status == event.StatusSuccess && len(p.policyDenied) > 0 && !p.ranCommand &&
			exit.Reason != launch.ReasonCanceled {
			r.Status, r.Error = event.StatusError, policyRefusedText(p.policyDenied)
			out = append(out, event.Fail(event.ClassFatal, r.Error))
		}
	}
	if p.held != nil {
		out = append([]event.Event{*p.held}, out...)
		p.held = nil
	}
	return out
}

// policyRefusedPrefix starts the error of a codex turn whose every shell command codex's policy refused.
const policyRefusedPrefix = "codex refused every shell command the worker tried (" + codexPolicyRejected + ")"

// policyRefusedText is the error of a turn whose every shell command was refused.
func policyRefusedText(cmds []string) string {
	return policyRefusedPrefix + ": " + strings.Join(cmds, "; ") +
		". The worker could not run commands; on Windows check the codex sandbox (windows.sandbox)."
}

type codexLine struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     *codexItem      `json:"item"`
	Usage    json.RawMessage `json:"usage"`
	Error    json.RawMessage `json:"error"`
	Message  string          `json:"message"`
}

type codexItem struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             *string         `json:"text"`
	Command          string          `json:"command"`
	AggregatedOutput json.RawMessage `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Changes          []struct {
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"changes"`
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
	Error     json.RawMessage `json:"error"`
	Query     string          `json:"query"`
	Items     json.RawMessage `json:"items"`
}

// Line implements Parser.
func (p *codexParser) Line(raw []byte) []event.Event {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	p.streamSeen()
	var ev codexLine
	if json.Unmarshal([]byte(line), &ev) != nil {
		return []event.Event{p.log(line)}
	}
	switch ev.Type {
	case "thread.started":
		return []event.Event{p.session(ev.ThreadID, "")}
	case "turn.started":
		return nil
	case "item.started", "item.updated", "item.completed":
		if ev.Item == nil {
			return nil
		}
		return p.item(ev.Type, ev.Item)
	case "turn.completed":
		total, ok := parseUsage(ev.Usage)
		var out []event.Event
		rep := event.Report{Status: event.StatusSuccess, Response: p.lastAgentMessage}
		if ok {
			if !total.IsZero() {
				p.thread = &total
			}
			u := codexRunUsage(total, p.baseline)
			out = append(out, event.Event{Kind: event.KindUsage, Usage: &u})
			rep.Usage = &u
		}
		p.hold(p.setReport(rep))
		return out
	case "turn.failed":
		msg := errorText(ev.Error)
		if msg == "" {
			msg = "codex turn failed"
		}
		p.hold(p.setReport(event.Report{Status: event.StatusError, Response: p.lastAgentMessage, Error: msg}))
		return nil
	case "error":
		// Not fatal in codex exec --json (reconnect and retry notices); remembered in case no result follows.
		msg := ev.Message
		if msg == "" {
			msg = errorText(ev.Error)
		}
		p.lastError = p.mask(msg)
		return []event.Event{p.log(msg)}
	}
	return []event.Event{p.log(line)}
}

// stepOf numbers items in the order they first appear.
func (p *codexParser) stepOf(id string) int {
	if id == "" {
		p.step++
		return p.step
	}
	if s, ok := p.steps[id]; ok {
		return s
	}
	p.step++
	p.steps[id] = p.step
	return p.step
}

func (p *codexParser) item(kind string, it *codexItem) []event.Event {
	step := p.stepOf(it.ID)
	started, completed := kind == "item.started", kind == "item.completed"
	failed := it.Status == "failed" || (it.ExitCode != nil && *it.ExitCode != 0)
	switch it.Type {
	case "agent_message":
		if it.Text == nil {
			return nil
		}
		delta := *it.Text
		delta = strings.TrimPrefix(delta, p.lastAgentMessage)
		p.lastAgentMessage = *it.Text
		if delta == "" {
			return nil
		}
		return []event.Event{p.message(step, delta)}
	case "reasoning":
		if it.Text == nil || *it.Text == "" {
			return nil
		}
		return []event.Event{p.log(*it.Text)}
	case "command_execution":
		p.ranCommand = true
		t := &event.Tool{ID: it.ID, Name: "shell", Command: p.mask(it.Command)}
		input, _ := json.Marshal(map[string]string{"command": it.Command})
		t.Input = p.maskJSON(input)
		return p.toolEvent(step, t, started, completed, failed, it.AggregatedOutput)
	case "file_change":
		if !completed {
			return nil
		}
		paths := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			if c.Path != "" {
				paths = append(paths, c.Path)
			}
		}
		input, _ := json.Marshal(map[string][]string{"paths": paths})
		t := &event.Tool{ID: it.ID, Name: "apply_patch", State: event.ToolDone, Input: p.maskJSON(input)}
		if failed {
			t.State = event.ToolFailed
		}
		out := []event.Event{{Kind: event.KindToolResult, Step: step, Tool: t}}
		if !failed {
			// Every path of a multi-file patch (the old version kept only the first: B15).
			for _, path := range paths {
				out = append(out, event.Event{Kind: event.KindFile, Step: step,
					File: &event.File{Path: path, Op: event.FileEdit, Tool: "apply_patch"}})
			}
		}
		return out
	case "mcp_tool_call":
		name := it.Tool
		if it.Server != "" {
			name = it.Server + "." + it.Tool
		}
		if name == "" {
			name = "mcp_tool_call"
		}
		t := &event.Tool{ID: it.ID, Name: name, Input: p.maskJSON(it.Arguments)}
		out := it.Result
		if failed && len(it.Error) > 0 {
			out, _ = json.Marshal(errorText(it.Error))
		}
		return p.toolEvent(step, t, started, completed, failed, out)
	case "web_search":
		input, _ := json.Marshal(map[string]string{"query": it.Query})
		t := &event.Tool{ID: it.ID, Name: "web_search", Input: p.maskJSON(input)}
		return p.toolEvent(step, t, started, completed, failed, nil)
	case "todo_list":
		t := &event.Tool{ID: it.ID, Name: "todo_list", Input: p.maskJSON(it.Items)}
		return p.toolEvent(step, t, started, completed, failed, nil)
	case "error":
		msg := ""
		if it.Text != nil {
			msg = *it.Text
		}
		p.lastError = p.mask(msg)
		return []event.Event{p.log(msg)}
	}
	return []event.Event{p.log(fmt.Sprintf("codex item %s (%s)", it.Type, strconv.Quote(it.ID)))}
}

// toolEvent emits the call on item.started and the result on item.completed (item.updated is ignored).
func (p *codexParser) toolEvent(step int, t *event.Tool, started, completed, failed bool, output json.RawMessage) []event.Event {
	switch {
	case started:
		t.State = event.ToolCalling
		return []event.Event{{Kind: event.KindToolCall, Step: step, Tool: t}}
	case completed:
		t.State = event.ToolDone
		if failed {
			t.State = event.ToolFailed
		}
		t.Output = p.output(output)
		t.Blocked = strings.Contains(t.Output, blockedMarker)
		return []event.Event{{Kind: event.KindToolResult, Step: step, Tool: t}}
	}
	return nil
}

// hold keeps the turn's report event for Finish.
func (p *codexParser) hold(ev event.Event) { p.held = &ev }
