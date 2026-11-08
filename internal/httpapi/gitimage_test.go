package httpapi_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

const lfsText = "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 12\n"

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// imageURL is the image endpoint of task id for path and version ("" for the default).
func imageURL(id, path, version string) string {
	u := "/api/v1/tasks/" + id + "/git/image?path=" + url.QueryEscape(path)
	if version != "" {
		u += "&version=" + version
	}
	return u
}

// GET /tasks/{id}/git/image serves an image of the task's repository from the working tree or HEAD, with headers
// that keep it inert, and refuses anything else before it reads a file.
func TestTaskImage(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	big := bytes.Repeat([]byte{'x'}, 10<<20+1)
	writeFile(t, filepath.Join(e.cwd, "img", "logo.png"), []byte("PNG at HEAD"))
	writeFile(t, filepath.Join(e.cwd, "pic.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x()</script></svg>`))
	writeFile(t, filepath.Join(e.cwd, "gone.png"), []byte("gone"))
	writeFile(t, filepath.Join(e.cwd, "lfs.png"), []byte(lfsText))
	writeFile(t, filepath.Join(e.cwd, "huge-head.png"), big)
	gitIn(t, e.cwd, "add", ".")
	gitIn(t, e.cwd, "commit", "-q", "-m", "images")
	writeFile(t, filepath.Join(e.cwd, "img", "logo.png"), []byte("PNG now"))
	writeFile(t, filepath.Join(e.cwd, "new.webp"), []byte("webp"))
	writeFile(t, filepath.Join(e.cwd, "big.png"), big)
	writeFile(t, filepath.Join(e.cwd, "notes.txt"), []byte("text"))
	if err := os.Remove(filepath.Join(e.cwd, "gone.png")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.png"), []byte("secret"))
	dirLink(t, outside, filepath.Join(e.cwd, "linked"))

	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Draw the logo.")
	e.wait(sum.ID)
	id := sum.ID

	serve := func(path, version, want, ctype string) {
		t.Helper()
		r := e.must(e.get(imageURL(id, path, version)), http.StatusOK)
		h := r.header
		if string(r.body) != want || h.Get("Content-Type") != ctype || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" ||
			h.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s@%s: %q, headers %v", path, version, r.body, h)
		}
	}
	serve("img/logo.png", "", "PNG now", "image/png")
	serve("img/logo.png", "worktree", "PNG now", "image/png")
	serve("img/logo.png", "head", "PNG at HEAD", "image/png")
	serve(`img\logo.png`, "head", "PNG at HEAD", "image/png")
	serve("new.webp", "", "webp", "image/webp")
	serve("gone.png", "head", "gone", "image/png")
	serve("pic.svg", "", `<svg xmlns="http://www.w3.org/2000/svg"><script>x()</script></svg>`, "image/svg+xml")

	e.wantError(e.get(imageURL(id, "gone.png", "")), http.StatusNotFound, api.CodeNotFound, "")
	e.wantError(e.get(imageURL(id, "new.webp", "head")), http.StatusNotFound, api.CodeNotFound, "")
	for _, version := range []string{"", "head"} {
		path := map[string]string{"": "big.png", "head": "huge-head.png"}[version]
		info := e.wantError(e.get(imageURL(id, path, version)), http.StatusRequestEntityTooLarge, api.CodeTooLarge, "")
		if info.Details["size"] != float64(10<<20+1) || !strings.Contains(info.Message, fmt.Sprint(10<<20+1)) {
			t.Fatalf("%s: %+v", path, info)
		}
		info = e.wantError(e.get(imageURL(id, "lfs.png", version)), http.StatusUnsupportedMediaType,
			api.CodeUnsupportedMedia, "")
		if info.Details["reason"] != "lfs_pointer" {
			t.Fatalf("lfs pointer: %+v", info)
		}
	}
	e.wantError(e.get(imageURL(id, "notes.txt", "")), http.StatusUnsupportedMediaType, api.CodeUnsupportedMedia, "")
	e.wantError(e.get(imageURL(id, "img/logo.png", "index")), http.StatusBadRequest, api.CodeInvalidArgument,
		"version")
	for _, bad := range []string{"", "../x.png", "img/../../x.png", "/etc/x.png", `\server\share\x.png`,
		"C:/x.png", "img/logo.png:stream", "NUL.png", "img/con.png", "aux", "com1.tar.png", "LPT9 .png",
		"linked/secret.png", "img/logo.png\n"} {
		e.wantError(e.get(imageURL(id, bad, "")), http.StatusBadRequest, api.CodeInvalidArgument, "path")
	}
}

// A repository without commits: the working tree serves, HEAD has nothing.
func TestTaskImageUnbornHead(t *testing.T) {
	e := newEnv(t)
	gitIn(t, e.cwd, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(e.cwd, "a.png"), []byte("png"))
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Draw.")
	e.wait(sum.ID)
	if r := e.must(e.get(imageURL(sum.ID, "a.png", "")), http.StatusOK); string(r.body) != "png" {
		t.Fatalf("worktree: %q", r.body)
	}
	e.wantError(e.get(imageURL(sum.ID, "a.png", "head")), http.StatusNotFound, api.CodeNotFound, "")
}
