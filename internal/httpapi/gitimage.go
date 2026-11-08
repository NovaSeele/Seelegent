package httpapi

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// maxImageBytes caps an image GET /tasks/{id}/git/image serves.
const maxImageBytes = 10 << 20

// imageCSP keeps an image answer inert when it is opened as a page: an SVG's scripts never run on the daemon's
// origin, past the checks the API's other routes rely on, and no page may frame it.
const imageCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox; frame-ancestors 'none'"

// lfsPointer starts a Git LFS pointer file: the text git keeps in place of an LFS file's content.
var lfsPointer = []byte("version https://git-lfs.github.com/spec/")

// taskImage is GET /tasks/{id}/git/image?path=&version=: one image file of the task's repository, as it is in the
// working tree (version=worktree, the default) or as committed at HEAD (version=head), for the Git view's
// previews. Read only, and not on the worker allow-list. A browser request from another site (Sec-Fetch-Site
// cross-site or same-site: another port on localhost is the same site) is refused; a request without the header
// comes from a program, not a page, and is served.
func (s *Server) taskImage(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		return 0, nil, newError(api.CodeForbidden, "another site's page cannot read the repository's images").
			with("reason", "cross_site")
	}
	q := r.URL.Query()
	version := q.Get("version")
	if version == "" {
		version = "worktree"
	}
	if version != "worktree" && version != "head" {
		return 0, nil, invalid("version must be worktree or head (got %q)", version).withField("version")
	}
	rel, ctype, err := imagePath(q.Get("path"))
	if err != nil {
		return 0, nil, err
	}
	scope, err := s.o.Tasks.GitScope(r.Context(), r.PathValue("id"))
	if err != nil {
		return 0, nil, err
	}
	root, err := s.o.Git.Root(r.Context(), scope.Cwd)
	if err != nil {
		return 0, nil, err
	}
	if rel, err = gitx.CleanPath(root, rel); err != nil {
		return 0, nil, withPathField(err)
	}
	if version == "head" {
		// gitx reads a blob whole (capped at maxImageBytes); it has no streaming read.
		data, size, ok, err := s.o.Git.HeadFile(r.Context(), root, rel, maxImageBytes)
		switch {
		case err != nil:
			return 0, nil, err
		case !ok:
			return 0, nil, newError(api.CodeNotFound, "HEAD has no file %s", rel)
		case data == nil:
			return 0, nil, imageTooLarge(size)
		case bytes.HasPrefix(data, lfsPointer):
			return 0, nil, lfsRefusal(rel)
		}
		writeImageHeaders(w, ctype, int64(len(data)))
		_, _ = w.Write(data) //nolint:gosec // an image of an allowed type, sent as that type with nosniff and a sandbox CSP
		return 0, nil, nil
	}
	f, size, err := openWorktreeImage(root, rel)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = f.Close() }()
	// The first bytes say whether the file is an LFS pointer; the rest is copied as it is read, never held whole.
	peek := make([]byte, len(lfsPointer))
	n, err := io.ReadFull(f, peek)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, nil, err
	}
	if peek = peek[:n]; bytes.Equal(peek, lfsPointer) {
		return 0, nil, lfsRefusal(rel)
	}
	writeImageHeaders(w, ctype, size)
	// A client that goes away fails the next write, which ends the copy. A file that changed size since the Stat
	// sends at most Content-Length bytes; a short answer closes the connection.
	//nolint:gosec // an image of an allowed type, sent as that type with nosniff and a sandbox CSP
	if _, err := w.Write(peek); err == nil && size > int64(n) {
		_, _ = io.CopyN(w, f, size-int64(n))
	}
	return 0, nil, nil
}

// writeImageHeaders starts an image answer of size bytes with the headers that keep it inert.
func writeImageHeaders(w http.ResponseWriter, ctype string, size int64) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", imageCSP)
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// lfsRefusal is the 415 of a Git LFS pointer in place of the image.
func lfsRefusal(rel string) error {
	return newError(api.CodeUnsupportedMedia, "%s is a Git LFS pointer, not the image", rel).
		with("reason", "lfs_pointer")
}

// openWorktreeImage opens rel from the working tree under root through os.Root: a relative symbolic link is
// followed only while it stays inside the repository, and a link with an absolute target (every Windows junction)
// is refused, so nothing outside it can be read, also when a path is swapped between checks (no time-of-check
// gap). The file must be a regular file of at most maxImageBytes; its size is returned.
func openWorktreeImage(root, rel string) (*os.File, int64, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, 0, err
	}
	repo, err := os.OpenRoot(resolvedRoot)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = repo.Close() }()
	f, err := repo.OpenFile(filepath.FromSlash(rel), imageOpenFlags, 0)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0, newError(api.CodeNotFound, "no file %s in the working tree", rel)
	case err != nil:
		// Also a link or junction that leads outside the repository ("path escapes from parent").
		return nil, 0, invalid("path %q cannot be read inside the repository: %v", rel, err).withField("path")
	}
	st, err := f.Stat()
	switch {
	case err != nil:
		_ = f.Close()
		return nil, 0, err
	case !st.Mode().IsRegular():
		_ = f.Close()
		return nil, 0, invalid("path %q is not a regular file", rel).withField("path")
	case st.Size() > maxImageBytes:
		_ = f.Close()
		return nil, 0, imageTooLarge(st.Size())
	}
	return f, st.Size(), nil
}

// imageTooLarge is the 413 of an image over maxImageBytes, with its size.
func imageTooLarge(size int64) error {
	return newError(api.CodeTooLarge, "the image is %d bytes; at most %d can be shown", size, maxImageBytes).
		with("size", size).with("max", maxImageBytes)
}

// imagePath checks the path of an image request before any file or git call: relative, no "..", no ':' (a drive,
// a Windows alternate data stream), no control character and no Windows device name as any segment; its
// extension must be one api.GitImageType knows (415 otherwise), whose Content-Type is sent (never sniffed). It
// returns the path with '/' separators and the type.
func imagePath(raw string) (rel, ctype string, err error) {
	if raw == "" {
		return "", "", invalid("path is required").withField("path")
	}
	for _, c := range raw {
		if c < 0x20 || c == 0x7f || c == ':' {
			return "", "", invalid("path %q has a ':' or a control character", raw).withField("path")
		}
	}
	rel = strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(raw) {
		return "", "", invalid("path %q must be relative to the repository", raw).withField("path")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", "", invalid("path %q leaves the repository", raw).withField("path")
		}
		if deviceName(seg) {
			return "", "", invalid("path %q names a device", raw).withField("path")
		}
	}
	ctype, ok := api.GitImageType(strings.ToLower(path.Ext(rel)))
	if !ok {
		return "", "", newError(api.CodeUnsupportedMedia, "%s is not an image this view shows", raw).
			with("reason", "not_image")
	}
	return rel, ctype, nil
}

// deviceName reports a Windows device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9, CONIN$, CONOUT$), with or without
// an extension; Windows ignores trailing dots and spaces, and also takes COM and LPT with ¹ ² ³.
func deviceName(seg string) bool {
	s := strings.TrimRight(seg, ". ")
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	s = strings.ToUpper(strings.TrimRight(s, " "))
	switch s {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(s) > 3 && (strings.HasPrefix(s, "COM") || strings.HasPrefix(s, "LPT")) {
		switch s[3:] {
		case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
			return true
		}
	}
	return false
}
