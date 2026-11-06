package quota

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
)

// fakeServer answers app-server requests from a table: method -> result (any) or rpcErr.
type fakeServer struct {
	answers map[string]func(params json.RawMessage) any
	exitOn  string // close the output instead of answering this method
	mu      sync.Mutex
	seen    []string
}

type rpcErr string

func (f *fakeServer) start(_ context.Context, argv []string, dir string, _ []string) (*Proc, error) {
	if len(argv) != 2 || argv[1] != "app-server" || dir == "" {
		return nil, errors.New("unexpected command")
	}
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() {
		defer func() { _ = outW.Close() }()
		sc := bufio.NewScanner(inR)
		enc := json.NewEncoder(outW)
		for sc.Scan() {
			var req struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			f.mu.Lock()
			f.seen = append(f.seen, req.Method)
			f.mu.Unlock()
			if req.ID == nil {
				continue
			}
			if req.Method == f.exitOn {
				return
			}
			_, _ = outW.Write([]byte("not json\n"))
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": "other", "result": 1})
			answer, ok := f.answers[req.Method]
			if !ok {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{"message": "unknown method"}})
				continue
			}
			res := answer(req.Params)
			if e, isErr := res.(rpcErr); isErr {
				_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{"message": string(e)}})
				continue
			}
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": res})
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = inW.Close()
			_ = outR.Close()
		})
	}
	return &Proc{Stdin: inW, Stdout: outR, Stop: stop}, nil
}

func fixed(v any) func(json.RawMessage) any { return func(json.RawMessage) any { return v } }

func signedInServer() *fakeServer {
	return &fakeServer{answers: map[string]func(json.RawMessage) any{
		"initialize":   fixed(map[string]any{}),
		"account/read": fixed(map[string]any{"account": map[string]any{"type": "chatgpt", "email": "x@example.com", "planType": "PLUS"}}),
		"account/rateLimits/read": fixed(map[string]any{"rateLimits": map[string]any{
			"primary":   map[string]any{"usedPercent": 40, "windowDurationMins": 300, "resetsAt": t0.Add(time.Hour).Unix()},
			"secondary": map[string]any{"usedPercent": 10, "windowDurationMins": 10080, "resetsAt": t0.Add(72 * time.Hour).UnixMilli()},
		}}),
		"model/list": func(p json.RawMessage) any {
			var params struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(p, &params)
			if params.Cursor == "" {
				return map[string]any{"data": []any{
					map[string]any{"id": "a", "model": "gpt-6-sol"},
					map[string]any{"id": "gpt-6-luna", "hidden": true},
				}, "nextCursor": "p2"}
			}
			return map[string]any{"data": []any{map[string]any{"id": "gpt-6-terra"}, map[string]any{"model": "gpt-6-sol"}}}
		},
	}}
}

func codexProfile(t *testing.T) account.Profile {
	t.Helper()
	prof := testProfile(t, account.KindIsolated)
	writeFile(t, filepath.Join(prof.Home, ".codex", "auth.json"), `{}`)
	return prof
}

func TestCodexReaderSession(t *testing.T) {
	prof := codexProfile(t)
	srv := signedInServer()
	run := func(context.Context, []string, string, []string) (Output, error) {
		t.Error("debug models must not run when model/list works")
		return Output{}, nil
	}
	r := NewCodexReader("/bin/codex", srv.start, run, func() time.Time { return t0 })
	q, err := r.ReadQuota(t.Context(), prof, nil)
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "Plus" || q.Email != "x@example.com" || !q.Entitled || !q.ExhaustedUntil.IsZero() ||
		!slices.Equal(q.Models, []string{"gpt-6-sol", "gpt-6-terra"}) {
		t.Fatalf("quota = %+v", q)
	}
	g := q.Groups[0]
	if g.Name != "Codex" || len(g.Buckets) != 2 || g.Buckets[0].Window != "weekly" || g.Buckets[1].Window != "5h" ||
		g.Buckets[1].UsedFraction != 0.4 || !g.Buckets[1].ResetAt.Equal(t0.Add(time.Hour)) ||
		!g.Buckets[0].ResetAt.Equal(t0.Add(72*time.Hour)) {
		t.Fatalf("group = %+v", g)
	}
	want := []string{"initialize", "initialized", "account/read", "account/rateLimits/read", "model/list", "model/list"}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if !slices.Equal(srv.seen, want) {
		t.Fatalf("requests = %q", srv.seen)
	}
}

func TestCodexReaderFallbacksAndFailures(t *testing.T) {
	now := func() time.Time { return t0 }
	debug := func(context.Context, []string, string, []string) (Output, error) {
		return Output{Stdout: []byte(`log line
{"models":[{"slug":"b","visibility":"list","priority":2},{"slug":"a","visibility":"list","priority":1},{"slug":"h","visibility":"hide"}]}`)}, nil
	}
	t.Run("model list fails, debug models fallback", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["model/list"] = fixed(rpcErr("nope"))
		q, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err != nil || !slices.Equal(q.Models, []string{"a", "b"}) {
			t.Fatalf("quota = %+v, %v", q, err)
		}
	})
	t.Run("rate limit reached without full window", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/rateLimits/read"] = fixed(map[string]any{"rateLimits": map[string]any{
			"primary":              map[string]any{"usedPercent": 50, "windowDurationMins": 300, "resetsAt": t0.Add(time.Hour).Unix()},
			"rateLimitReachedType": "rate_limit",
		}})
		q, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err != nil || !q.ExhaustedUntil.Equal(t0.Add(time.Hour)) || q.ExhaustedReason != "rate limit reached" {
			t.Fatalf("quota = %+v, %v", q, err)
		}
	})
	t.Run("full window", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/rateLimits/read"] = fixed(map[string]any{"rateLimits": map[string]any{
			"primary": map[string]any{"usedPercent": 100, "windowDurationMins": 10080},
		}})
		q, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err != nil || !q.ExhaustedUntil.Equal(t0.Add(time.Hour)) || q.ExhaustedReason != "quota used up: Weekly" {
			t.Fatalf("quota = %+v, %v", q, err)
		}
	})
	t.Run("not entitled", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/rateLimits/read"] = fixed(map[string]any{"rateLimits": map[string]any{}, "ordinaryUsageAllowed": false})
		q, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err != nil || q.Entitled || q.EntitlementError != "the ChatGPT Plus plan cannot use Codex" {
			t.Fatalf("quota = %+v, %v", q, err)
		}
	})
	t.Run("signed out", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/read"] = fixed(map[string]any{"account": nil})
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if !errors.Is(err, ErrNeedsLogin) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no auth file", func(t *testing.T) {
		_, err := NewCodexReader("/bin/codex", signedInServer().start, debug, now).
			ReadQuota(t.Context(), testProfile(t, account.KindIsolated), nil)
		if !errors.Is(err, ErrNeedsLogin) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("account answer changed shape", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/read"] = fixed(map[string]any{"account": "chatgpt"})
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if !errors.Is(err, ErrFormat) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rate limits changed shape", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/rateLimits/read"] = fixed(map[string]any{"rateLimits": []int{1}})
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if !errors.Is(err, ErrFormat) {
			t.Fatalf("err = %v", err)
		}
		srv.answers["account/rateLimits/read"] = fixed(map[string]any{"limits": 1})
		_, err = NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if !errors.Is(err, ErrFormat) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("rate limits refused", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["account/rateLimits/read"] = fixed(rpcErr("denied"))
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err == nil || KindOf(err) != KindFailed {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("initialize refused", func(t *testing.T) {
		srv := signedInServer()
		srv.answers["initialize"] = fixed(rpcErr("go away"))
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if err == nil || KindOf(err) != KindFailed {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("server exits early", func(t *testing.T) {
		srv := signedInServer()
		srv.exitOn = "account/read"
		_, err := NewCodexReader("/bin/codex", srv.start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if KindOf(err) != KindUnavailable {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("server hangs", func(t *testing.T) {
		srv := signedInServer()
		block := make(chan struct{})
		defer close(block)
		srv.answers["account/read"] = func(json.RawMessage) any { <-block; return nil }
		r := NewCodexReader("/bin/codex", srv.start, debug, now)
		r.timeout = 100 * time.Millisecond
		_, err := r.ReadQuota(t.Context(), codexProfile(t), nil)
		if KindOf(err) != KindUnavailable {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("start fails", func(t *testing.T) {
		start := func(context.Context, []string, string, []string) (*Proc, error) { return nil, errors.New("no exec") }
		_, err := NewCodexReader("/bin/codex", start, debug, now).ReadQuota(t.Context(), codexProfile(t), nil)
		if KindOf(err) != KindUnavailable {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not installed and passive", func(t *testing.T) {
		if _, err := NewCodexReader("", nil, nil, nil).ReadQuota(t.Context(), codexProfile(t), nil); !errors.Is(err, backend.ErrNotInstalled) {
			t.Fatalf("err = %v", err)
		}
		if _, err := NewCodexReader("/x", nil, nil, nil).ReadQuota(t.Context(), testProfile(t, account.KindAPIKey), nil); !errors.Is(err, ErrPassive) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestCodexHelpers(t *testing.T) {
	if planLabel("") != "" || planLabel("education") != "Edu" || planLabel("ultra") != "Ultra" {
		t.Fatal("planLabel")
	}
	if planText("", "x") != "this ChatGPT account x" {
		t.Fatal("planText")
	}
	for raw, want := range map[string]bool{"": false, "null": false, "false": false, `""`: false, "0": false, `"x"`: true, "true": true} {
		if rawTruthy(json.RawMessage(raw)) != want {
			t.Errorf("rawTruthy(%s)", raw)
		}
	}
	if modelsFromDebugModels([]byte("no json")) != nil || modelsFromDebugModels([]byte(`{"other":1}`)) != nil {
		t.Fatal("modelsFromDebugModels")
	}
	if _, ok := codexBucket(&codexRateWindow{}); ok {
		t.Fatal("a window without usedPercent is not a bucket")
	}
	// Codex accounts whose model list is known and empty are not entitled.
	q, err := parseCodexStatus(codexResults{account: json.RawMessage(`{"account":{"type":"apiKey"}}`), models: []string{}}, t0)
	if err != nil || q.Entitled || q.EntitlementError != "this ChatGPT account has no Codex model it may use" {
		t.Fatalf("quota = %+v, %v", q, err)
	}
	q, err = parseCodexStatus(codexResults{account: json.RawMessage(`{"account":{"type":"chatgpt"}}`),
		rateLimits: json.RawMessage(`{"rateLimits":{"planType":"pro","spendControlReached":true}}`)}, t0)
	if err != nil || q.Plan != "Pro" || q.ExhaustedReason != "spend limit reached" || !q.ExhaustedUntil.Equal(t0.Add(time.Hour)) {
		t.Fatalf("quota = %+v, %v", q, err)
	}
}

func TestCodexReaderRealProcess(t *testing.T) {
	prof := codexProfile(t)
	r := NewCodexReader(selfBin(t), nil, nil, func() time.Time { return t0 })
	q, err := r.ReadQuota(t.Context(), prof, fakeEnv("app-server"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Plan != "Plus" || len(q.Groups) != 1 || !slices.Equal(q.Models, []string{"gpt-6-sol"}) {
		t.Fatalf("quota = %+v", q)
	}
	// debug models through the real runner: the fake prints a catalog.
	got := (&CodexReader{bin: selfBin(t), run: runCommand}).debugModels(t.Context(), prof.Home,
		fakeEnv("print", `QUOTA_FAKE_STDOUT={"models":[{"slug":"m1","visibility":"list"}]}`))
	if !slices.Equal(got, []string{"m1"}) {
		t.Fatalf("debug models = %q", got)
	}
	if _, err := startProc(t.Context(), nil, "", nil); err == nil {
		t.Fatal("empty command started")
	}
	if _, err := startProc(t.Context(), []string{filepath.Join(t.TempDir(), "missing")}, "", nil); err == nil {
		t.Fatal("missing binary started")
	}
}
