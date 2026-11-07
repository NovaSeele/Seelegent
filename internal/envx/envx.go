// Package envx reads environment lists ("NAME=value" entries, as os.Environ returns them) the way the operating
// system would: variable lookup with case-insensitive names on Windows, and command lookup through PATH (and
// PATHEXT on Windows). It depends only on the standard library, so every package that runs a program (account,
// backend, gitx, the task engine) can use it without importing another domain package.
package envx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Lookup returns the value of name in env ("NAME=value" entries), the last one winning, with case-insensitive names
// on Windows.
func Lookup(env []string, name string) (string, bool) {
	fold := runtime.GOOS == "windows"
	var (
		val   string
		found bool
	)
	for _, kv := range env {
		n, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if n == name || (fold && strings.EqualFold(n, name)) {
			val, found = v, true
		}
	}
	return val, found
}

// ResolveCommand finds name the way the shell would with the PATH (and on Windows PATHEXT) of env: PATH folders in
// order, and on Windows each PATHEXT extension in order. It returns "" when nothing matches.
//
// Empty and relative PATH entries (".", "bin") are skipped, so a command is never taken from the daemon's working
// folder (the policy of Go's exec.ErrDot); the result is always an absolute path.
func ResolveCommand(name string, env []string) string {
	pathVar, _ := Lookup(env, "PATH")
	exts := []string{""}
	if runtime.GOOS == "windows" {
		pathext, ok := Lookup(env, "PATHEXT")
		if !ok || pathext == "" {
			pathext = ".COM;.EXE;.BAT;.CMD"
		}
		exts = exts[:0]
		for _, e := range strings.Split(pathext, ";") {
			if e = strings.TrimSpace(e); e != "" {
				exts = append(exts, strings.ToLower(e))
			}
		}
	}
	for _, dir := range filepath.SplitList(pathVar) {
		dir = strings.Trim(strings.TrimSpace(dir), `"`)
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			candidate := filepath.Join(dir, name+ext)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() && executable(st) {
				return candidate
			}
		}
	}
	return ""
}

// executable reports whether the file mode allows running it (always true on Windows, where PATHEXT decides).
func executable(st os.FileInfo) bool {
	return runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0
}
