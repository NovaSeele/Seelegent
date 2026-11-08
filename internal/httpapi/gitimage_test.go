package httpapi_test

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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
			h.Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox; frame-ancestors 'none'" ||
			h.Get("Cross-Origin-Resource-Policy") != "same-origin" ||
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

// The image endpoint is for the dashboard's own page and for programs: a page of another site (another port on
// localhost is the same site) is refused, the dashboard's same-origin request and a request without
// Sec-Fetch-Site (the CLI, MCP) are served. Every answer carries Cross-Origin-Resource-Policy: same-origin.
func TestTaskImageFetchSite(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	writeFile(t, filepath.Join(e.cwd, "a.png"), []byte("png"))
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Draw.")
	e.wait(sum.ID)
	u := imageURL(sum.ID, "a.png", "")
	for _, site := range []string{"", "same-origin", "none"} {
		r := e.must(e.req(http.MethodGet, u, nil, map[string]string{"Sec-Fetch-Site": site}), http.StatusOK)
		if string(r.body) != "png" || r.header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatalf("Sec-Fetch-Site %q: %q, headers %v", site, r.body, r.header)
		}
	}
	for _, site := range []string{"cross-site", "same-site"} {
		r := e.req(http.MethodGet, u, nil, map[string]string{"Sec-Fetch-Site": site})
		info := e.wantError(r, http.StatusForbidden, api.CodeForbidden, "")
		if info.Details["reason"] != "cross_site" || r.header.Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatalf("Sec-Fetch-Site %q: %+v, headers %v", site, info, r.header)
		}
	}
}

// The working tree is read through os.Root. A link or junction leading out of the repository is refused, also
// below a folder that is itself inside (dirLink: a junction on Windows, an absolute symbolic link elsewhere). An
// absolute target counts as leaving even when it points back inside, so every junction is refused; a relative
// symbolic link that stays inside is followed (Unix: Windows needs a privilege to make one).
func TestTaskImageLinks(t *testing.T) {
	e := newEnv(t)
	initRepo(t, e.cwd)
	writeFile(t, filepath.Join(e.cwd, "art", "in.png"), []byte("inside"))
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.png"), []byte("secret"))
	dirLink(t, filepath.Join(e.cwd, "art"), filepath.Join(e.cwd, "inner"))
	dirLink(t, outside, filepath.Join(e.cwd, "outer"))
	dirLink(t, outside, filepath.Join(e.cwd, "art", "deep"))
	if runtime.GOOS != "windows" {
		if err := os.Symlink("art", filepath.Join(e.cwd, "rel")); err != nil {
			t.Fatal(err)
		}
	}
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Draw.")
	e.wait(sum.ID)
	if runtime.GOOS != "windows" {
		if r := e.must(e.get(imageURL(sum.ID, "rel/in.png", "")), http.StatusOK); string(r.body) != "inside" {
			t.Fatalf("relative link inside the repository: %q", r.body)
		}
	}
	for _, p := range []string{"outer/secret.png", "art/deep/secret.png", "inner/deep/secret.png", "inner/in.png"} {
		e.wantError(e.get(imageURL(sum.ID, p, "")), http.StatusBadRequest, api.CodeInvalidArgument, "path")
	}
}

// A client that stops reading never keeps a working-tree image open for long: an image up to 1 MiB is read whole
// and closed before the answer, a larger one streams under the route's write deadline, after which its file is
// closed. fileHeld asks the OS whether any handle on the file is open (Windows, Linux; elsewhere the test skips).
func TestTaskImageStalledClient(t *testing.T) {
	const deadline = time.Second
	e := newEnvWith(t, func(o *envOptions) { o.imageWriteTimeout = deadline })
	initRepo(t, e.cwd)
	small, large := filepath.Join(e.cwd, "small.png"), filepath.Join(e.cwd, "large.png")
	writeFile(t, small, bytes.Repeat([]byte{'s'}, 1<<20))
	writeFile(t, large, bytes.Repeat([]byte{'l'}, 10<<20))
	if _, known := fileHeld(t, small); !known {
		t.Skip("cannot tell whether a file is open on " + runtime.GOOS)
	}
	e.addAccount("first")
	e.queue.Push(t, ok("c1", "done"))
	sum := e.delegate("Draw.")
	e.wait(sum.ID)

	// stall sends a GET for path, reads the status line and headers, then reads nothing more.
	stall := func(path string) {
		t.Helper()
		c, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(e.port))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetReadBuffer(4 << 10)
		}
		req := "GET " + imageURL(sum.ID, path, "") + " HTTP/1.1\r\nHost: 127.0.0.1:" + strconv.Itoa(e.port) + "\r\n\r\n"
		if _, err := io.WriteString(c, req); err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 0, 1024)
		one := make([]byte, 1)
		for !bytes.HasSuffix(head, []byte("\r\n\r\n")) {
			if _, err := c.Read(one); err != nil {
				t.Fatalf("%s: %v after %q", path, err, head)
			}
			head = append(head, one[0])
		}
		if !bytes.HasPrefix(head, []byte("HTTP/1.1 200 ")) {
			t.Fatalf("%s: %q", path, head)
		}
	}
	// released waits up to limit for the file's last handle to close and says how long that took.
	released := func(path string, limit time.Duration) (time.Duration, bool) {
		start := time.Now()
		for {
			if held, _ := fileHeld(t, path); !held {
				return time.Since(start), true
			}
			if time.Since(start) > limit {
				return time.Since(start), false
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	stall("small.png")
	if took, ok := released(small, time.Second); !ok {
		t.Fatalf("small image still open after %v", took)
	}

	stall("large.png")
	// The stream is under way and the file is open, or this test proves nothing.
	if held, _ := fileHeld(t, large); !held {
		t.Fatal("large image already closed while the client reads nothing: the socket buffers took it all")
	}
	if took, ok := released(large, deadline+5*time.Second); !ok {
		t.Fatalf("large image still open after %v", took)
	}
}
