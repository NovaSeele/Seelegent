package backend

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// e2eRun is one turn run through the real launcher with the fake CLI.
type e2eRun struct {
	handle  *launch.Handle
	parser  Parser
	record  string
	events  []event.Event
	outcome Outcome
	exit    launch.Exit
}

// startE2E starts a turn of backend name on prof that replays script.
func startE2E(t *testing.T, name, script string, prof account.Profile, turn Turn, env []string) *e2eRun {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(name, exe)
	if err != nil {
		t.Fatal(err)
	}
	return startE2EOn(t, b, script, prof, turn, env)
}

// startE2EOn is startE2E with backend b, whose binary runs the test binary (itself or through a shim).
func startE2EOn(t *testing.T, b Backend, script string, prof account.Profile, turn Turn, env []string) *e2eRun {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(prof, nil); err != nil {
		t.Fatal(err)
	}
	attempt := t.TempDir()
	turn.Profile, turn.AttemptDir = prof, attempt
	cmd, err := b.Command(turn)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	spec, err := LaunchSpec(cmd, work, attempt, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(script)
	if err != nil {
		t.Fatal(err)
	}
	r := &e2eRun{record: filepath.Join(attempt, "record.json")}
	if env == nil {
		env = os.Environ()
	}
	// Under `go test -cover` every child process writes coverage data when it exits; parallel children sharing one
	// GOCOVERDIR collide on Windows and print the failure on the fake CLI's stderr. Each run gets its own.
	env = append(withoutVar(env, "GOCOVERDIR"), "GOCOVERDIR="+t.TempDir(),
		"FAKECLI_SCRIPT="+abs, "FAKECLI_RECORD="+r.record)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r.handle, err = launch.Start(ctx, launch.Options{
		Launcher: []string{exe, "__launch"}, Dir: filepath.Join(attempt, "launch"), Spec: spec, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.parser = b.NewParser(secretsOf(env))
	return r
}

// secretsOf picks the API key variables out of env, as the task engine gets them from account.RunEnv.
func secretsOf(env []string) []string {
	var out []string
	for _, n := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", account.EnvProviderKey} {
		if v, ok := envx.Lookup(env, n); ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// finish follows the launch to its end the way the task engine does: stream lines, then stderr lines, then Finish.
func (r *e2eRun) finish(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := r.handle.Tail(ctx, 0, func(line []byte, _ int64) error {
		r.events = append(r.events, r.parser.Line(line)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = r.handle.TailStderr(ctx, 0, func(line []byte, _ int64) error {
		r.events = append(r.events, r.parser.Stderr(string(line))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.exit, err = r.handle.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	tail, err := r.handle.StderrTail(4000)
	if err != nil {
		t.Fatal(err)
	}
	r.events = append(r.events, r.parser.Finish(r.exit, tail)...)
	var c Collector
	for _, e := range r.events {
		c.Add(e)
	}
	r.outcome = c.Outcome(r.exit)
}

func (r *e2eRun) fakeSaw(t *testing.T) fakeRecord {
	t.Helper()
	data, err := os.ReadFile(r.record)
	if err != nil {
		t.Fatal(err)
	}
	var rec fakeRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestEndToEndStreams runs every recorded stream through the launcher and the fake CLI and checks that the outcome
// equals the one the parser gives when fed the same stream directly.
func TestEndToEndStreams(t *testing.T) {
	cases := map[string][]string{
		"agy":    {"success.jsonl", "quota.jsonl", "auth.jsonl", "background.jsonl", "fatal.jsonl"},
		"claude": {"success.jsonl", "quota.jsonl", "auth.jsonl", "overloaded.jsonl"},
		"codex":  {"success.jsonl", "quota.jsonl", "transient.jsonl", "policy.jsonl"},
	}
	for name, files := range cases {
		for _, file := range files {
			t.Run(name+"/"+file, func(t *testing.T) {
				t.Parallel()
				prof := testProfile(t)
				r := startE2E(t, name, filepath.Join("testdata", "streams", name, file), prof,
					Turn{Prompt: "fix the handler\nsecond line", Model: "model-x", Timeout: time.Minute}, nil)
				r.finish(t)
				_, want := replay(t, mustBackend(t, name), file)
				if !reflect.DeepEqual(r.outcome, want) {
					t.Errorf("through the launcher:\n%+v\ndirect:\n%+v", r.outcome, want)
				}
				rec := r.fakeSaw(t)
				switch name {
				case "agy":
					if len(rec.Args) < 2 || rec.Args[0] != "-p" || rec.Args[1] != "fix the handler\nsecond line" || rec.Stdin != "" {
						t.Errorf("agy saw %+v", rec)
					}
				default:
					if rec.Stdin != "fix the handler\nsecond line" {
						t.Errorf("%s stdin = %q", name, rec.Stdin)
					}
				}
			})
		}
	}
}

// TestEndToEndClaudeLimit runs Claude Code's 5-hour limit message through the launcher (field report 3, B1): the
// turn is quota_exhausted with the reset time of the message, the next 3:10am in Ho Chi Minh City after the exit,
// and keeps the conversation and the tokens of the work done before the limit.
func TestEndToEndClaudeLimit(t *testing.T) {
	r := startE2E(t, "claude", filepath.Join("testdata", "streams", "claude", "limit.jsonl"), testProfile(t),
		Turn{Prompt: "add the limiter", Model: "claude-opus-5", Timeout: time.Minute}, nil)
	r.finish(t)
	o := r.outcome
	if o.Status != OutcomeFailed || o.Class != event.ClassQuotaExhausted ||
		o.ConversationID != "7e5d1c3b-2a4f-4b6e-8c9d-0f1e2d3c4b5a" || o.Usage.TotalTokens == 0 {
		t.Fatalf("outcome = %+v", o)
	}
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	reset := o.ResetAt.In(hcm)
	if r.exit.EndedAt.IsZero() || reset.Hour() != 3 || reset.Minute() != 10 ||
		reset.Before(r.exit.EndedAt.Add(-resetGrace)) || reset.After(r.exit.EndedAt.Add(24*time.Hour)) {
		t.Errorf("reset = %v for an exit at %v", o.ResetAt, r.exit.EndedAt)
	}
}

// TestEndToEndCancelKillsTree is the guard for bug B1: canceling a turn must stop the CLI and everything it started,
// for every backend.
func TestEndToEndCancelKillsTree(t *testing.T) {
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			pidFile := filepath.Join(dir, "grandchild.pid")
			script := filepath.Join(dir, "hang.jsonl")
			lines := map[string]string{
				"agy":    `{"event":"init","conversation_id":"cancel-me","init":{"model":"m"}}`,
				"claude": `{"type":"system","subtype":"init","session_id":"cancel-me","model":"m"}`,
				"codex":  `{"type":"thread.started","thread_id":"cancel-me"}`,
			}
			if err := os.WriteFile(script, []byte(lines[name]+"\n#!spawn\n#!hang\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			r := startE2E(t, name, script, testProfile(t), Turn{Prompt: "p", Model: "m"},
				append(os.Environ(), "FAKECLI_PIDFILE="+pidFile))
			pid := waitPid(t, pidFile)
			if !platform.ProcessAlive(pid) {
				t.Fatal("grandchild not running")
			}
			if err := r.handle.Cancel(); err != nil {
				t.Fatal(err)
			}
			r.finish(t)
			if r.exit.Reason != launch.ReasonCanceled || r.outcome.Status != OutcomeCanceled ||
				r.outcome.ConversationID != "cancel-me" {
				t.Errorf("exit %+v outcome %+v", r.exit, r.outcome)
			}
			deadline := time.Now().Add(10 * time.Second)
			for platform.ProcessAlive(pid) {
				if time.Now().After(deadline) {
					t.Fatalf("grandchild %d still alive after cancel", pid)
				}
				time.Sleep(50 * time.Millisecond)
			}
		})
	}
}

func waitPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("grandchild never wrote its pid")
	return 0
}

// TestEndToEndAPIKeyAccount builds the environment of a claude API-key account through account.Manager with the
// claude rules, runs a turn and checks what the CLI saw and that the key never comes back unmasked.
func TestEndToEndAPIKeyAccount(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	realHome := filepath.Join(dir, "real")
	m := account.NewManager(db, account.NewProfiles(filepath.Join(dir, "profiles"), realHome), nil)
	if _, err := m.CreateProvider(ctx, account.Provider{ID: "zai", Backend: "claude", BaseURL: "https://api.z.ai/api/anthropic",
		Enabled: true, Tiers: config.TierModels{Standard: "glm-4.6"}}); err != nil {
		t.Fatal(err)
	}
	a, _, err := m.Add(ctx, account.New{Backend: "claude", Kind: account.KindAPIKey, Provider: "zai", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	claude := mustBackend(t, "claude")
	if _, err := m.Env(ctx, a, claude, account.EnvOptions{Base: []string{}}); !errors.Is(err, account.ErrNoKey) {
		t.Fatalf("env before the key is stored: %v", err)
	}
	const key = "sk-zai-test-1234567890"
	if _, err := m.SetKey(ctx, a.ID, key); err != nil {
		t.Fatal(err)
	}
	base := append(os.Environ(),
		"ANTHROPIC_API_KEY=lead-own-key", "CLAUDE_CODE_SESSION_ID=lead-session",
		"FAKECLI_ENV=ANTHROPIC_AUTH_TOKEN,ANTHROPIC_BASE_URL,ANTHROPIC_API_KEY,CLAUDE_CONFIG_DIR,CLAUDE_CODE_SESSION_ID,"+
			"ANTHROPIC_DEFAULT_SONNET_MODEL,SEELEGENT_WORKER,NO_COLOR")
	env, err := m.Env(ctx, a, claude, account.EnvOptions{Base: base, Model: "glm-4.6"})
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "key.jsonl")
	body := `{"type":"system","subtype":"init","session_id":"k1","model":"glm-4.6"}` + "\n#!echo-env ANTHROPIC_AUTH_TOKEN\n" +
		`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"k1"}` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r := startE2E(t, "claude", script, env.Profile, Turn{Prompt: "p", Model: "glm-4.6"}, env.Vars)
	r.parser = claude.NewParser(env.Secrets)
	r.finish(t)
	if r.outcome.Status != OutcomeOK {
		t.Fatalf("outcome = %+v", r.outcome)
	}
	saw := r.fakeSaw(t).Env
	want := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": key, "ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic",
		"CLAUDE_CONFIG_DIR": env.Profile.Home, "ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-4.6", "SEELEGENT_WORKER": "1",
		"NO_COLOR": "1",
	}
	if !reflect.DeepEqual(saw, want) {
		t.Errorf("the CLI saw %v\nwant %v", saw, want)
	}
	for _, e := range r.events {
		data, _ := json.Marshal(e)
		if strings.Contains(string(data), key) {
			t.Errorf("event leaks the key: %s", data)
		}
	}
}
