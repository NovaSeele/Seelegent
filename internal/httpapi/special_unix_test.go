//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package httpapi_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// TestSpecialFilesDoNotBlock: a worker can reach /spec and can put a FIFO where tasks.md or a log is expected.
// The daemon answers at once instead of waiting for a writer forever.
func TestSpecialFilesDoNotBlock(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.cwd, "specs", "001-fifo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "tasks.md"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The hook runner opens hooks.log when the daemon starts; put the FIFO in its place.
	hooksLog := filepath.Join(e.paths.Logs, "hooks.log")
	if err := os.Remove(hooksLog); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(hooksLog, 0o600); err != nil {
		t.Fatal(err)
	}
	type answer struct{ r response }
	ask := func(path string) response {
		done := make(chan answer, 1)
		go func() { done <- answer{e.get(path)} }()
		select {
		case a := <-done:
			return a.r
		case <-time.After(time.Second):
			t.Fatalf("GET %s blocked on a FIFO", path)
			return response{}
		}
	}
	e.wantError(ask("/api/v1/spec?cwd="+url.QueryEscape(e.cwd)+"&dir=specs/001-fifo"), http.StatusBadRequest,
		api.CodeInvalidArgument, "dir")
	e.wantError(ask("/api/v1/logs?name=hooks"), http.StatusServiceUnavailable, api.CodeUnavailable, "")
}
