// Package projectkey names projects: the key and display name of a project folder, under which roles, project
// memory, proof ledgers and usage are kept.
//
// It is apart from fsutil because the key hashes with SHA-1, and the launcher (cmd/seelegent-launch) imports fsutil
// but must not link any crypto package (see docs/development.md).
package projectkey

import (
	"crypto/sha1" //nolint:gosec // a short, stable project name, not a security hash
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// Key returns the key and display name of the project whose folder is dir: name is the base name of the canonical
// path, and key is that name made safe for file names and URLs (lower-case letters, digits, '.', '_' and '-')
// followed by '-' and the first 8 hex digits of the SHA-1 of fsutil.Key(dir). Every spelling of the same folder (8.3
// short names, junctions, letter case on Windows) gives the same key (old bug B11). These keys are not the old
// version's; the M8 migration rebuilds the old keys by the old formula to map old data onto them. The keys are
// stored in data folders, so the formula never changes (TestKeysStayTheSame pins it).
func Key(dir string) (key, name string, err error) {
	c, err := fsutil.Canonical(dir)
	if err != nil {
		return "", "", err
	}
	name = filepath.Base(c)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = c
	}
	return keyOf(fsutil.CanonicalKey(c), name), name, nil
}

// keyOf is the key of the project named name whose folder has the comparison key pathKey (fsutil.Key).
func keyOf(pathKey, name string) string {
	sum := sha1.Sum([]byte(pathKey)) //nolint:gosec // a short, stable name, not a security hash
	return safeName(name) + "-" + hex.EncodeToString(sum[:])[:8]
}

// Of checks a project folder, which must be an absolute path to an existing folder, and returns its canonical
// path, project key and name (Key).
func Of(cwd string) (canonical, key, name string, err error) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", "", "", fmt.Errorf("cwd %q is not an absolute path", cwd)
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "", "", "", fmt.Errorf("cwd %q is not an existing folder", cwd)
	}
	if canonical, err = fsutil.Canonical(cwd); err != nil {
		return "", "", "", err
	}
	key, name, err = Key(canonical)
	return canonical, key, name, err
}

// safeName lower-cases s and replaces every run of other characters than letters, digits, '.', '_' and '-' by '-'.
func safeName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		switch {
		case ok:
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "project"
	}
	return out
}
