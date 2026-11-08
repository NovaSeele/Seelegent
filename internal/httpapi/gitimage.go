package httpapi

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// maxImageBytes caps an image GET /tasks/{id}/git/image serves.
const maxImageBytes = 10 << 20

// imageTypes are the extensions the image endpoint serves, with the Content-Type it sends (never sniffed).
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".bmp": "image/bmp", ".ico": "image/x-icon", ".svg": "image/svg+xml",
}

// imageCSP keeps an image answer inert when it is opened as a page: an SVG's scripts never run on the daemon's
// origin, past the checks the API's other routes rely on.
const imageCSP = "default-src 'none'; style-src 'unsafe-inline'; sandbox"

// lfsPointer starts a Git LFS pointer file: the text git keeps in place of an LFS file's content.
var lfsPointer = []byte("version https://git-lfs.github.com/spec/")

// taskImage is GET /tasks/{id}/git/image?path=&version=: one image file of the task's repository, as it is in the
// working tree (version=worktree, the default) or as committed at HEAD (version=head), for the Git view's
// previews. Read only, and not on the worker allow-list.
func (s *Server) taskImage(w http.ResponseWriter, r *http.Request) (code int, body any, err error) {
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
	var data []byte
	if version == "head" {
		var size int64
		var ok bool
		data, size, ok, err = s.o.Git.HeadFile(r.Context(), root, rel, maxImageBytes)
		switch {
		case err != nil:
			return 0, nil, err
		case !ok:
			return 0, nil, newError(api.CodeNotFound, "HEAD has no file %s", rel)
		case data == nil:
			return 0, nil, imageTooLarge(size)
		}
	} else if data, err = readWorktreeImage(root, rel); err != nil {
		return 0, nil, err
	}
	if bytes.HasPrefix(data, lfsPointer) {
		return 0, nil, newError(api.CodeUnsupportedMedia, "%s is a Git LFS pointer, not the image", rel).
			with("reason", "lfs_pointer")
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", imageCSP)
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) //nolint:gosec // an image of an allowed type, sent as that type with nosniff and a sandbox CSP
	return 0, nil, nil
}

// readWorktreeImage reads rel from the working tree under root: links and junctions are resolved, and the file
// must still lie inside the repository (compared through fsutil.Key), be a regular file and fit maxImageBytes.
func readWorktreeImage(root, rel string) ([]byte, error) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, newError(api.CodeNotFound, "no file %s in the working tree", rel)
	}
	if err != nil {
		return nil, invalid("path %q cannot be read: %v", rel, err).withField("path")
	}
	kr, err := fsutil.Key(resolvedRoot)
	if err != nil {
		return nil, err
	}
	kp, err := fsutil.Key(resolved)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(kp, strings.TrimSuffix(kr, "/")+"/") {
		return nil, invalid("path %q leads outside the repository", rel).withField("path")
	}
	data, truncated, err := fsutil.ReadRegular(resolved, maxImageBytes)
	switch {
	case errors.Is(err, fsutil.ErrNotRegular):
		return nil, invalid("path %q is not a regular file", rel).withField("path")
	case errors.Is(err, fs.ErrNotExist):
		return nil, newError(api.CodeNotFound, "no file %s in the working tree", rel)
	case err != nil:
		return nil, err
	case truncated:
		size := int64(maxImageBytes) + 1
		if st, err := os.Stat(resolved); err == nil { //nolint:gosec // checked above to lie inside the repository
			size = st.Size()
		}
		return nil, imageTooLarge(size)
	}
	return data, nil
}

// imageTooLarge is the 413 of an image over maxImageBytes, with its size.
func imageTooLarge(size int64) error {
	return newError(api.CodeTooLarge, "the image is %d bytes; at most %d can be shown", size, maxImageBytes).
		with("size", size).with("max", maxImageBytes)
}

// imagePath checks the path of an image request before any file or git call: relative, no "..", no ':' (a drive,
// a Windows alternate data stream), no control character and no Windows device name as any segment; its
// extension must be one of imageTypes (415 otherwise). It returns the path with '/' separators and the type.
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
	ctype, ok := imageTypes[strings.ToLower(path.Ext(rel))]
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
