package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/ops"
	"github.com/NovaSeele/Seelegent/internal/update"
)

func runUpdate(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), append([]string{"update"}, args...), &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUpdateCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.0.2","assets":[]}`))
	}))
	defer srv.Close()
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(update.EnvUpdateURL, srv.URL+"/releases/latest")
	t.Setenv(account.EnvWorker, "")

	// A test build is a development build: check says so, apply refuses without --version.
	code, out, errOut := runUpdate(t, "--json")
	var chk update.CheckResult
	if code != ExitOK || json.Unmarshal([]byte(out), &chk) != nil || !chk.DevBuild || chk.LatestVersion != "v0.0.2" {
		t.Fatalf("update --json: exit %d\n%s\n%s", code, out, errOut)
	}
	code, out, _ = runUpdate(t, "--apply", "--json")
	var e errorOut
	if code != ExitError || json.Unmarshal([]byte(out), &e) != nil || e.Code != codeDevBuild {
		t.Errorf("update --apply in a dev build: exit %d %s", code, out)
	}
	// The test binary is not an installed seelegent: rollback refuses before touching anything.
	code, out, _ = runUpdate(t, "--rollback", "--json")
	if code != ExitError || !strings.Contains(out, "installed") {
		t.Errorf("update --rollback: exit %d %s", code, out)
	}

	for _, args := range [][]string{{"--check", "--apply"}, {"--apply", "--rollback"}, {"--rollback", "--version", "v1.0.0"},
		{"--version", "latest"}, {"extra"}} {
		if code, _, errOut := runUpdate(t, args...); code != ExitUsage {
			t.Errorf("update %v: exit %d (%s)", args, code, errOut)
		}
	}

	// Inside a worker every mode is refused before anything runs (old C-B12).
	t.Setenv(account.EnvWorker, "1")
	for _, args := range [][]string{{"--check", "--json"}, {"--apply", "--json"}, {"--rollback", "--json"}} {
		code, out, _ := runUpdate(t, args...)
		var e errorOut
		if code != ExitError || json.Unmarshal([]byte(out), &e) != nil || e.Code != "forbidden" ||
			e.Error != ops.WorkerMessage {
			t.Errorf("update %v in a worker: exit %d %s", args, code, out)
		}
	}
}

func TestUpdateErrorCodes(t *testing.T) {
	for err, want := range map[error]string{
		update.ErrChecksum: codeChecksum, update.ErrDevBuild: codeDevBuild, update.ErrNoPrevious: codeNoPrevious,
	} {
		if _, out := classify(updateError(err)); out.Code != want {
			t.Errorf("%v: code %q, want %q", err, out.Code, want)
		}
	}
}
