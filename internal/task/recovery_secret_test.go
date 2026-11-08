package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/testutil/fakecli"
)

func claudeLine(v map[string]any) string { return jsonLine(v) }

func claudeText(sess, text string) string {
	return claudeLine(map[string]any{"type": "assistant", "session_id": sess, "message": map[string]any{
		"id": "m-" + text[:4], "type": "message", "role": "assistant", "model": "claude-sonnet-5",
		"content": []any{map[string]any{"type": "text", "text": text}}}})
}

// TestRestartMasksKeyOfRemovedKey: a worker of an API-key account reattached after a daemon restart is still masked
// for its key when the key was removed (or can no longer be read) while no daemon ran (bugs.md B15.11): the mask
// list is kept with the attempt, never rebuilt from an environment that may fail. damaged: the mask list cannot be
// read either; the attempt's output is masked by key patterns and the reattach event says so (fail closed).
func TestRestartMasksKeyOfRemovedKey(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		t.Run(fmt.Sprint("damaged=", damaged), func(t *testing.T) { restartMasksKey(t, damaged) })
	}
}

func restartMasksKey(t *testing.T, damaged bool) {
	h := newHarness(t)
	h.backends = []string{config.BackendClaude}
	cl, err := backend.New(config.BackendClaude, fakecli.Exe(t))
	if err != nil {
		t.Fatal(err)
	}
	h.s.o.Backends[config.BackendClaude] = cl
	set := h.config()
	set.Backends.Claude.Enabled = true
	h.settings.Store(&set)

	ctx := t.Context()
	if _, err := h.accounts.CreateProvider(ctx, account.Provider{ID: "anthropic", Backend: config.BackendClaude,
		Official: true, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc, _, err := h.accounts.Add(ctx, account.New{Backend: config.BackendClaude, Kind: account.KindAPIKey,
		Label: "key", Provider: "anthropic", Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	key := "qz7Kp2mWx9Rt4Lb8Nc3Vd6Fh1Jg5Ty0Ue"
	if damaged {
		key = "sk-ant-api03-" + key // a shape the key patterns know
	}
	if _, err := h.accounts.SetKey(ctx, acc.ID, key); err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(time.Second)
	h.pool.Notify()

	release := filepath.Join(h.dir, "release")
	sess := "sess-key"
	h.queue.Push(t, script(
		claudeLine(map[string]any{"type": "system", "subtype": "init", "session_id": sess, "model": "claude-sonnet-5"}),
		claudeText(sess, "Before the restart, key "+key+"."),
		"#!wait-file "+release,
		claudeText(sess, "After the restart, key "+key+"."),
		claudeLine(map[string]any{"type": "result", "subtype": "success", "is_error": false,
			"result": "Done with " + key, "session_id": sess,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5}})))
	sum := h.delegate(api.DelegateRequest{Backend: config.BackendClaude, Provider: "anthropic", Model: "claude-sonnet-5"})
	h.waitFor("the worker's session", func() bool {
		return strings.Contains(eventText(t, h, sum.ID), sess)
	})
	h.stop()
	mask := filepath.Join(h.s.attemptDir(sum.ID, 1, 1), maskFile)
	if got, err := loadMask(filepath.Dir(mask)); err != nil || len(got) != 1 || got[0] != key {
		t.Fatalf("mask list kept with the attempt = %d values, %v", len(got), err)
	}
	if damaged {
		if err := os.WriteFile(mask, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The key goes while no daemon runs.
	if _, err := h.accounts.ClearKey(ctx, acc.ID); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.waitEvent(sum.ID, api.EventReattach)
	var rec api.RecoveryData
	for _, e := range eventsOf(h.events(sum.ID), api.EventReattach) {
		decode(t, e, &rec)
	}
	if wantCode := map[bool]api.RecoveryNoticeCode{true: api.RecoveryNoticeMaskUnreadable}[damaged]; rec.NoticeCode != wantCode ||
		(rec.Notice != "") != damaged {
		t.Errorf("reattach notice = %q (%q)", rec.NoticeCode, rec.Notice)
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	text := eventText(t, h, sum.ID)
	if !strings.Contains(text, "After the restart") {
		t.Fatalf("the reattached worker's message is missing: %s", text)
	}
	if strings.Contains(text, key) {
		t.Errorf("the API key leaked into the task's events after the restart")
	}
	if strings.Contains(tk.LastTurn().Response, key) {
		t.Errorf("the API key leaked into the response: %q", tk.LastTurn().Response)
	}
	if _, err := os.Stat(mask); !os.IsNotExist(err) {
		t.Errorf("the mask list outlived its attempt: %v", err)
	}
}

func TestMaskFile(t *testing.T) {
	dir := t.TempDir()
	if err := saveMask(dir, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := loadMask(dir); err != nil || got != nil {
		t.Fatalf("no list: %v %v", got, err)
	}
	if err := saveMask(dir, []string{"k1", "k2"}); err != nil {
		t.Fatal(err)
	}
	if got, err := loadMask(dir); err != nil || len(got) != 2 {
		t.Fatalf("list = %v %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, maskFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMask(dir); err == nil {
		t.Error("a damaged list read as fine")
	}
	if err := removeMask(dir); err != nil {
		t.Fatal(err)
	}
	if err := removeMask(dir); err != nil {
		t.Errorf("removing twice: %v", err)
	}
	if got := mergeSecrets([]string{"a"}, []string{"a", "", "b"}); len(got) != 2 {
		t.Errorf("merge = %q", got)
	}
}

// eventText is every event of a task as stored, joined.
func eventText(t *testing.T, h *harness, id string) string {
	t.Helper()
	var b strings.Builder
	for _, e := range h.events(id) {
		data, err := json.Marshal(e.Data)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
