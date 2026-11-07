package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// webFiles is the embedded dashboard (contract §8).
type webFiles struct {
	fsys fs.FS
	// build is a hash of every file, computed once (Health.build, hello.build).
	build    string
	hasIndex bool
}

// newWebFiles hashes the bundle.
func newWebFiles(fsys fs.FS) (*webFiles, error) {
	h := sha256.New()
	wf := &webFiles{fsys: fsys}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		if p == "index.html" {
			wf.hasIndex = true
		}
		h.Write([]byte(p))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	wf.build = hex.EncodeToString(h.Sum(nil))[:12]
	return wf, nil
}

// build returns the id of the embedded web bundle ("none" without one).
func (s *Server) build() string {
	if s.web == nil {
		return "none"
	}
	return s.web.build
}

// serveWeb serves the dashboard: an existing file as itself (MIME type of the real file), a missing path with an
// extension as 404, anything else as index.html (history routing). Only GET and HEAD.
func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.writeError(w, r, newError(api.CodeNotFound, "the dashboard answers only GET and HEAD"))
		return
	}
	if s.web == nil || !s.web.hasIndex {
		http.Error(w, "the dashboard is not part of this build", http.StatusNotFound)
		return
	}
	// path.Clean on a rooted path removes every "..", so the name stays inside the bundle (old D-23: no prefix
	// check on file system paths at all, the embedded FS has no outside).
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := s.readWeb(name)
	switch {
	case err == nil:
	case path.Ext(name) != "":
		http.Error(w, "not found", http.StatusNotFound)
		return
	default:
		name = "index.html"
		if data, err = s.readWeb(name); err != nil {
			s.writeError(w, r, err)
			return
		}
	}
	h := w.Header()
	ctype := mime.TypeByExtension(path.Ext(name))
	if ctype == "" {
		ctype = http.DetectContentType(data)
	}
	h.Set("Content-Type", ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

// readWeb reads a regular file of the bundle; a folder or a missing file is fs.ErrNotExist.
func (s *Server) readWeb(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrNotExist
	}
	st, err := fs.Stat(s.web.fsys, name)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fs.ErrNotExist
	}
	data, err := fs.ReadFile(s.web.fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return data, err
}
