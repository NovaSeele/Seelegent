package fsutil

import (
	"crypto/sha1" //nolint:gosec // a short, stable project name, not a security hash
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Spellings lists the ways an existing path may be written in text: as given, its canonical form and, on Windows,
// the 8.3 short form of either (C:\Users\NOVASE~1 for C:\Users\NovaSeele). Duplicates are left out.
func Spellings(p string) []string {
	out := []string{}
	add := func(s string) {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	add(p)
	if c, err := Canonical(p); err == nil {
		add(c)
		add(shortName(c))
	}
	add(shortName(p))
	return out
}

// Canonical returns the absolute, cleaned form of p with links resolved, so that two spellings of the same location
// give the same string. On Windows it also expands 8.3 short names (C:\Users\NOVASE~1 becomes C:\Users\NovaSeele),
// resolves junctions and restores the on-disk letter case. Trailing components that do not exist yet are kept as
// given, appended to the canonical form of the longest existing prefix.
func Canonical(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("canonical path: empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("canonical path %s: %w", p, err)
	}
	existing := abs
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs, nil // not even the volume root is reachable; nothing to resolve
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved := resolveExisting(existing)
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return resolved, nil
}

// Key returns a string that identifies the location of p for map keys, hashes and equality checks: the canonical
// path with forward slashes, lower-cased on platforms whose file systems are case-insensitive by default (Windows
// and macOS). Use it wherever the old version compared raw path strings (project memory keys, lane and ledger
// lookups).
func Key(p string) (string, error) {
	c, err := Canonical(p)
	if err != nil {
		return "", err
	}
	return keyOf(c, runtime.GOOS), nil
}

// keyOf builds the comparison key of an already canonical path for the given GOOS.
func keyOf(canonical, goos string) string {
	k := filepath.ToSlash(canonical)
	if goos == "windows" {
		k = strings.ReplaceAll(k, `\`, "/")
	}
	if caseInsensitive(goos) {
		k = strings.ToLower(k)
	}
	return k
}

// caseInsensitive reports whether file systems on goos ignore letter case by default.
func caseInsensitive(goos string) bool {
	return goos == "windows" || goos == "darwin" || goos == "ios"
}

// ProjectKey returns the key and display name of the project whose folder is dir: name is the base name of the
// canonical path, and key is that name made safe for file names and URLs (lower-case letters, digits, '.', '_' and
// '-') followed by '-' and the first 8 hex digits of the SHA-1 of Key(dir). Every spelling of the same folder (8.3
// short names, junctions, letter case on Windows) gives the same key (old bug B11). These keys are not the old
// version's; the M8 migration rebuilds the old keys by the old formula to map old data onto them.
func ProjectKey(dir string) (key, name string, err error) {
	c, err := Canonical(dir)
	if err != nil {
		return "", "", err
	}
	k := keyOf(c, runtime.GOOS)
	name = filepath.Base(c)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = c
	}
	sum := sha1.Sum([]byte(k)) //nolint:gosec // a short, stable name, not a security hash
	return safeName(name) + "-" + hex.EncodeToString(sum[:])[:8], name, nil
}

// ProjectOf checks a project folder, which must be an absolute path to an existing folder, and returns its canonical
// path, project key and name (ProjectKey).
func ProjectOf(cwd string) (canonical, key, name string, err error) {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return "", "", "", fmt.Errorf("cwd %q is not an absolute path", cwd)
	}
	if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
		return "", "", "", fmt.Errorf("cwd %q is not an existing folder", cwd)
	}
	if canonical, err = Canonical(cwd); err != nil {
		return "", "", "", err
	}
	key, name, err = ProjectKey(canonical)
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

// SamePath reports whether a and b name the same location (see Key).
func SamePath(a, b string) (bool, error) {
	ka, err := Key(a)
	if err != nil {
		return false, err
	}
	kb, err := Key(b)
	if err != nil {
		return false, err
	}
	return ka == kb, nil
}

// Within reports whether p is root itself or lies inside root, comparing canonical keys. Unlike a plain prefix test
// it does not treat a sibling such as /data/ui-old as inside /data/ui.
func Within(root, p string) (bool, error) {
	kr, err := Key(root)
	if err != nil {
		return false, err
	}
	kp, err := Key(p)
	if err != nil {
		return false, err
	}
	return withinKey(kr, kp), nil
}

// withinKey is Within on precomputed keys.
func withinKey(root, p string) bool {
	if p == root {
		return true
	}
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}
	return strings.HasPrefix(p, root)
}
