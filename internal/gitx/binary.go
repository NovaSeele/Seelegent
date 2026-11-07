package gitx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// ErrNoGit means no usable git executable was found.
var ErrNoGit = errors.New("git is not available")

// Binary finds the git executable through the daemon's PATH, once per daemon: every package that runs git (gitx,
// the task engine's baseline) shares one Binary from the composition root.
//
// On Windows a batch shim (git.cmd, git.bat) is never run: Go starts it through cmd.exe, which rewrites the
// arguments (it eats ^, so HEAD^{commit} became HEAD{commit}) and can run a file name holding & or | as a command.
// A real git.exe anywhere on PATH wins over a shim earlier on PATH; with only a shim, the git.exe the shim starts
// is used when the shim names it by path; otherwise Path fails with a clear error.
type Binary struct {
	env func() []string

	mu   sync.Mutex
	path string
}

// NewBinary returns a Binary that searches the PATH of env (os.Environ when nil).
func NewBinary(env func() []string) *Binary {
	if env == nil {
		env = os.Environ
	}
	return &Binary{env: env}
}

// Path returns the git executable. A found path is kept for the life of the Binary; a failure is not, so a git
// installed later is found by the next call.
func (b *Binary) Path() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path != "" {
		return b.path, nil
	}
	p, err := findGit(b.env())
	if err != nil {
		return "", err
	}
	b.path = p
	return p, nil
}

// findGit resolves git in the PATH of env. Empty and relative PATH entries are skipped (no git from the current
// folder), so the result is always an absolute path.
func findGit(env []string) (string, error) {
	pathVar, _ := envx.Lookup(env, "PATH")
	var dirs []string
	for _, dir := range filepath.SplitList(pathVar) {
		if dir = strings.Trim(strings.TrimSpace(dir), `"`); dir != "" && filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	if runtime.GOOS != "windows" {
		if p := envx.ResolveCommand("git", []string{"PATH=" + strings.Join(dirs, string(filepath.ListSeparator))}); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("%w: git was not found on PATH", ErrNoGit)
	}
	// Windows: only git.exe is accepted, in PATH order (exec.LookPath would try PATHEXT per folder and return a
	// git.cmd that comes before a git.exe).
	shim := ""
	for _, dir := range dirs {
		if p := filepath.Join(dir, "git.exe"); isFile(p) {
			return p, nil
		}
		for _, name := range []string{"git.cmd", "git.bat"} {
			if p := filepath.Join(dir, name); shim == "" && isFile(p) {
				shim = p
			}
		}
	}
	if shim != "" {
		if p := shimTarget(shim); p != "" {
			return p, nil
		}
	}
	// Git for Windows' default place, for a PATH that only has a shim or no git at all.
	if pf, ok := envx.Lookup(env, "ProgramFiles"); ok && filepath.IsAbs(pf) {
		if p := filepath.Join(pf, "Git", "cmd", "git.exe"); isFile(p) {
			return p, nil
		}
	}
	if shim != "" {
		return "", fmt.Errorf("%w: git was found only as a batch shim at %s; put git.exe on PATH", ErrNoGit, shim)
	}
	return "", fmt.Errorf("%w: git was not found on PATH", ErrNoGit)
}

// shimExe matches a git.exe a batch file starts: quoted, or a run of characters without spaces.
var shimExe = regexp.MustCompile(`(?i)"([^"\r\n]*git\.exe)"|([^\s"]*git\.exe)`)

// shimTarget returns the real git.exe a batch shim starts, when the shim names it by an absolute path or relative
// to its own folder (%~dp0); "" otherwise (a bare git.exe would be another PATH lookup, which found nothing).
func shimTarget(shim string) string {
	data, _, err := fsutil.ReadRegular(shim, 64<<10)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(shim) + string(filepath.Separator)
	for _, m := range shimExe.FindAllStringSubmatch(string(data), -1) {
		p := m[1]
		if p == "" {
			p = m[2]
		}
		p = strings.NewReplacer("%~dp0", dir, "%~DP0", dir).Replace(p)
		if !filepath.IsAbs(p) {
			continue
		}
		if p = filepath.Clean(p); isFile(p) {
			return p
		}
	}
	return ""
}

// isFile reports a regular file at p.
func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}
