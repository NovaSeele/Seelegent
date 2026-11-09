package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/app"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/signin"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

func TestMain(m *testing.M) { fakecli.Main(m) }

const limit = 60 * time.Second

// switchClassifier lets a test decide who calls; nil caller means the real classification (lead here).
type switchClassifier struct {
	mu     sync.Mutex
	caller *httpapi.Caller
}

func (c *switchClassifier) set(v *httpapi.Caller) {
	c.mu.Lock()
	c.caller = v
	c.mu.Unlock()
}

func (c *switchClassifier) Classify(netip.AddrPort, netip.AddrPort, time.Time) httpapi.Caller {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.caller == nil {
		return httpapi.Caller{}
	}
	return *c.caller
}

// env is a daemon with the HTTP API on a random port, the fake CLI as agy and a temp data directory.
type env struct {
	t      *testing.T
	a      *app.App
	paths  config.Paths
	queue  *fakecli.Queue
	cwd    string
	port   int
	base   string
	who    *switchClassifier
	client *http.Client
}

var webFiles = fstest.MapFS{
	"index.html":         {Data: []byte("<!doctype html><title>Seelegent</title>")},
	"assets/app-1a2b.js": {Data: []byte("console.log(1)")},
	"favicon.svg":        {Data: []byte("<svg/>")},
}

// envOptions adjust newEnvWith.
type envOptions struct {
	queue  *fakecli.Queue
	opener signin.Opener
	// imageWriteTimeout is app.Options.ImageWriteTimeout.
	imageWriteTimeout time.Duration
}

func newEnv(t *testing.T) *env { return newEnvWith(t, nil) }

func newEnvWith(t *testing.T, adjust func(*envOptions)) *env {
	t.Helper()
	opts := &envOptions{queue: fakecli.NewQueue(t)}
	if adjust != nil {
		adjust(opts)
	}
	home := t.TempDir()
	paths, err := config.NewPaths(filepath.Join(home, "data"))
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "project")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, paths: paths, queue: opts.queue, cwd: cwd, who: &switchClassifier{},
		client: &http.Client{Timeout: limit}}
	set := config.Defaults()
	set.Port = 47831
	// The engine tests must not depend on this machine's free memory.
	set.MinFreeMemoryMB, set.MinFreeCommitMB, set.CriticalFreeMemoryMB = config.Int(0), config.Int(0), config.Int(0)
	// Nor on its CPUs: the gate slots and the test hint the tests were written for.
	set.GateConcurrency, set.TestParallelism = config.Int(2), config.Int(4)
	a, err := app.New(t.Context(), app.Options{
		Paths: paths, Settings: &set, Env: e.queue.Env(os.Environ()), RealHome: filepath.Join(home, "home"),
		Launcher: fakecli.Launcher(t), NoQuotaReaders: true, NoGh: true,
		Bins: map[string]string{config.BackendAgy: fakecli.Exe(t), config.BackendClaude: "", config.BackendCodex: ""},
		Web:  webFiles, Classifier: e.who, SignInOpener: opts.opener, ImageWriteTimeout: opts.imageWriteTimeout,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	e.a = a
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), limit)
		defer cancel()
		if err := a.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	addr, err := a.ListenHTTP(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	e.base = "http://" + addr
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	e.port = int(ap.Port())
	return e
}

// response is one HTTP answer.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (r response) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

// errBody returns the error of an error answer.
func (r response) err(t *testing.T) api.ErrorInfo {
	t.Helper()
	var b api.ErrorBody
	r.json(t, &b)
	return b.Error
}

// req sends a request; mutating requests get X-Seelegent unless a header map says otherwise.
func (e *env) req(method, path string, body any, hdr map[string]string) response {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	r, err := http.NewRequestWithContext(e.t.Context(), method, e.base+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if method != http.MethodGet {
		r.Header.Set("X-Seelegent", "1")
	}
	for k, v := range hdr {
		if k == "Host" {
			r.Host = v
			continue
		}
		if v == "" {
			r.Header.Del(k)
			continue
		}
		r.Header.Set(k, v)
	}
	res, err := e.client.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return response{status: res.StatusCode, header: res.Header, body: data}
}

func (e *env) get(path string) response { return e.req(http.MethodGet, path, nil, nil) }

func (e *env) post(path string, body any) response { return e.req(http.MethodPost, path, body, nil) }

// must checks the status of an answer.
func (e *env) must(r response, status int) response {
	e.t.Helper()
	if r.status != status {
		e.t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
	return r
}

// wantError checks an error answer's status and code (and the field, when given).
func (e *env) wantError(r response, status int, code api.ErrorCode, field string) api.ErrorInfo {
	e.t.Helper()
	e.must(r, status)
	got := r.err(e.t)
	if got.Code != code {
		e.t.Fatalf("code %s, want %s: %s", got.Code, code, r.body)
	}
	if field != "" && got.Details["field"] != field {
		e.t.Fatalf("details.field = %v, want %s: %s", got.Details["field"], field, r.body)
	}
	if r.header.Get("Cache-Control") != "no-store" {
		e.t.Fatalf("error without Cache-Control no-store: %v", r.header)
	}
	return got
}

func line(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(data)
}

// ok is a fake agy run that answers response.
func ok(conv, response string, extra ...string) string {
	init := line(map[string]any{"event": "init", "conversation_id": conv, "init": map[string]any{"model": "m"}})
	result := line(map[string]any{"event": "result", "result": map[string]any{"status": "SUCCESS",
		"response": response, "conversation_id": conv,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}}})
	return init + "\n" + strings.Join(extra, "\n") + "\n" + result + "\n"
}

// hang is a fake agy run that starts and never ends.
func hang(conv string) string {
	return line(map[string]any{"event": "init", "conversation_id": conv, "init": map[string]any{"model": "m"}}) +
		"\n#!hang\n"
}

// addAccount adds an enabled agy account through the API.
func (e *env) addAccount(label string) api.Account {
	e.t.Helper()
	var a api.Account
	e.must(e.post("/api/v1/accounts", map[string]any{"backend": "agy", "kind": "isolated", "label": label}),
		http.StatusCreated).json(e.t, &a)
	return a
}

// delegate starts a task through the API.
func (e *env) delegate(prompt string) api.TaskSummary {
	e.t.Helper()
	var sum api.TaskSummary
	e.must(e.post("/api/v1/tasks", map[string]any{"prompt": prompt, "cwd": e.cwd}), http.StatusCreated).
		json(e.t, &sum)
	return sum
}

// wait waits for tasks through POST /wait.
func (e *env) wait(ids ...string) api.WaitResult {
	e.t.Helper()
	var res api.WaitResult
	e.must(e.post("/api/v1/wait", map[string]any{"ids": ids, "timeoutSeconds": 60, "wakeOnBlocked": false}),
		http.StatusOK).json(e.t, &res)
	if !res.Done {
		e.t.Fatalf("wait = %+v", res)
	}
	return res
}

// sse is a reader of GET /events.
type sse struct {
	t      *testing.T
	header http.Header
	body   io.ReadCloser
	rd     *bufio.Reader
}

type frame struct {
	id, event, data string
	retry           string
}

func (e *env) stream(lastID string) *sse {
	e.t.Helper()
	return e.streamPath("/api/v1/events", lastID)
}

func (e *env) streamPath(path, lastID string) *sse {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(e.t.Context(), limit)
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, e.base+path, http.NoBody)
	if err != nil {
		e.t.Fatal(err)
	}
	if lastID != "" {
		r.Header.Set("Last-Event-ID", lastID)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		e.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" ||
		res.Header.Get("X-Accel-Buffering") != "no" || res.Header.Get("Cache-Control") != "no-store" {
		e.t.Fatalf("events: %d %v", res.StatusCode, res.Header)
	}
	s := &sse{t: e.t, header: res.Header, body: res.Body, rd: bufio.NewReader(res.Body)}
	e.t.Cleanup(func() {
		cancel()
		_ = res.Body.Close()
	})
	return s
}

// next reads the next frame (comments are returned as event ":").
func (s *sse) next() frame {
	s.t.Helper()
	var f frame
	for {
		l, err := s.rd.ReadString('\n')
		if err != nil {
			s.t.Fatalf("sse read: %v (partial %+v)", err, f)
		}
		l = strings.TrimSuffix(l, "\n")
		switch {
		case l == "":
			if f.event != "" || f.id != "" || f.data != "" {
				return f
			}
		case strings.HasPrefix(l, ":"):
			return frame{event: ":"}
		case strings.HasPrefix(l, "id: "):
			f.id = strings.TrimPrefix(l, "id: ")
		case strings.HasPrefix(l, "event: "):
			f.event = strings.TrimPrefix(l, "event: ")
		case strings.HasPrefix(l, "data: "):
			f.data = strings.TrimPrefix(l, "data: ")
		case strings.HasPrefix(l, "retry: "):
			f.retry = strings.TrimPrefix(l, "retry: ")
		}
	}
}

// until reads frames until one of event matches ok (the stream's context ends the read after limit).
func (s *sse) until(event string, ok func(frame) bool) frame {
	s.t.Helper()
	for {
		f := s.next()
		if f.event == event && (ok == nil || ok(f)) {
			return f
		}
	}
}

func seqOf(t *testing.T, id string) uint64 {
	t.Helper()
	_, s, _ := strings.Cut(id, ".")
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatalf("id %q: %v", id, err)
	}
	return n
}
