package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Spellings lists the ways an existing path may be written in text: as given, its canonical form and, on Windows,
// the 8.3 short form of either (C:\Users\JOHNSM~1 for C:\Users\JohnSmith). Duplicates are left out.
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
// give the same string. On Windows it also expands 8.3 short names (C:\Users\JOHNSM~1 becomes C:\Users\JohnSmith),
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

// CanonicalKey is Key of a path that is already canonical (Canonical): it does not touch the file system.
func CanonicalKey(canonical string) string { return keyOf(canonical, runtime.GOOS) }

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
