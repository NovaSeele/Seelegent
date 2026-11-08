package httpapi_test

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
)

// writeSpec makes a spec folder with a tasks.md naming marker.
func writeSpec(t *testing.T, dir, marker string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tasks.md"), []byte("- [ ] T001 "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// dirLink makes link point at the folder target: a junction on Windows, a symbolic link elsewhere.
func dirLink(t *testing.T, target, link string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		return
	}
	if out, err := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", link, target).
		CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v\n%s", err, out)
	}
}

// A worker reads only its own project's spec: with its own cwd, a dir outside it (absolute, or through a link or
// junction inside it) is refused like a foreign cwd; its own task's spec folder is allowed (M18 hunt H7).
func TestWorkerSpecDirStaysInOwnFolder(t *testing.T) {
	e := newEnv(t)
	e.addAccount("first")
	ownSpec := filepath.Join(t.TempDir(), "own-spec", "specs", "001-own")
	writeSpec(t, ownSpec, "own plan")
	e.queue.Push(t, ok("c1", "done"))
	var own api.TaskSummary
	e.must(e.post("/api/v1/tasks", map[string]any{"prompt": "Do it.", "cwd": e.cwd,
		"spec": map[string]any{"dir": ownSpec}}), http.StatusCreated).json(t, &own)
	e.wait(own.ID)
	other := filepath.Join(t.TempDir(), "secret-project", "specs", "001-x")
	writeSpec(t, other, "SECRET plan of another project")
	writeSpec(t, filepath.Join(e.cwd, "specs", "002-in"), "inside plan")
	dirLink(t, filepath.Dir(other), filepath.Join(e.cwd, "linked"))

	e.client.Transport = &http.Transport{DisableKeepAlives: true}
	e.who.set(&httpapi.Caller{Worker: true, TaskID: own.ID})
	spec := func(dir string) string {
		return "/api/v1/spec?cwd=" + url.QueryEscape(e.cwd) + "&dir=" + url.QueryEscape(dir)
	}
	for _, dir := range []string{other, filepath.Join("linked", "001-x"), filepath.Join("..", "x")} {
		got := e.wantError(e.get(spec(dir)), http.StatusForbidden, api.CodeForbidden, "")
		if got.Details["caller"] != "worker" {
			t.Errorf("%s: details = %v", dir, got.Details)
		}
	}
	for _, dir := range []string{filepath.Join("specs", "002-in"), filepath.Join(e.cwd, "specs", "002-in"), ownSpec} {
		var v api.SpecView
		e.must(e.get(spec(dir)), http.StatusOK).json(t, &v)
		if len(v.Tasks) != 1 {
			t.Errorf("%s: %+v", dir, v)
		}
	}

	// The lead may read any spec folder.
	e.who.set(nil)
	e.must(e.get(spec(other)), http.StatusOK)
}
