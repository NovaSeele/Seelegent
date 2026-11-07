package gitx

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
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
// is used when the shim names it by path; then Git for Windows' default places (%ProgramFiles%\Git, then the
// per-user %LOCALAPPDATA%\Programs\Git); otherwise Path fails with a clear error. A git found any other way than
// as a plain PATH hit is logged once per resolution.
type Binary struct {
	env func() []string
	log *slog.Logger

	mu   sync.Mutex
	path string
}

// NewBinary returns a Binary that searches the PATH of env (os.Environ when nil) and logs to log (a logx logger;
// discarded when nil).
func NewBinary(env func() []string, log *slog.Logger) *Binary {
	if env == nil {
		env = os.Environ
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Binary{env: env, log: log}
}

// Path returns the git executable. A found path is kept until a run reports it gone (see Run); a failure is not
// kept, so a git installed later is found by the next call.
func (b *Binary) Path() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path != "" {
		return b.path, nil
	}
	p, via, err := findGit(b.env())
	if err != nil {
		return "", err
	}
	if via != "" {
		b.log.Info("gitx: using git", "path", p, "found", via)
	}
	b.path = p
	return p, nil
}

// Run calls fn with the git executable. When fn fails because that executable no longer exists (removed or moved
// since it was found), the kept path is dropped, git is found again and fn runs once more with the new path.
func (b *Binary) Run(fn func(git string) error) error {
	git, err := b.Path()
	if err != nil {
		return err
	}
	err = fn(git)
	if !b.forget(git, err) {
		return err
	}
	if git, err = b.Path(); err != nil {
		return err
	}
	return fn(git)
}

// forget drops the kept path git when err, from running it, says the executable is gone, and reports whether it
// did. A git that is still a file is kept: the error is about something else, such as a missing working folder.
// Only the path that failed is dropped, so a newer one found by a concurrent call stays.
func (b *Binary) forget(git string, err error) bool {
	missing := errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound)
	if !missing || isFile(git) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.path == git {
		b.path = ""
		b.log.Warn("gitx: git is gone, looking for it again", "path", git)
	}
	return true
}

// findGit resolves git in the PATH of env. Empty and relative PATH entries are skipped (no git from the current
// folder), so the result is always an absolute path. via says how git was found when it was not a plain PATH hit
// (for the log), and is "" for one.
func findGit(env []string) (path, via string, err error) {
	pathVar, _ := envx.Lookup(env, "PATH")
	var dirs []string
	for _, dir := range filepath.SplitList(pathVar) {
		if dir = strings.Trim(strings.TrimSpace(dir), `"`); dir != "" && filepath.IsAbs(dir) {
			dirs = append(dirs, dir)
		}
	}
	if runtime.GOOS != "windows" {
		if p := envx.ResolveCommand("git", []string{"PATH=" + strings.Join(dirs, string(filepath.ListSeparator))}); p != "" {
			return p, "", nil
		}
		return "", "", fmt.Errorf("%w: git was not found on PATH", ErrNoGit)
	}
	// Windows: only git.exe is accepted, in PATH order (exec.LookPath would try PATHEXT per folder and return a
	// git.cmd that comes before a git.exe).
	shim := ""
	for _, dir := range dirs {
		if p := filepath.Join(dir, "git.exe"); isFile(p) {
			return p, "", nil
		}
		for _, name := range []string{"git.cmd", "git.bat"} {
			if p := filepath.Join(dir, name); shim == "" && isFile(p) {
				shim = p
			}
		}
	}
	if shim != "" {
		if p := shimTarget(shim); p != "" {
			return p, "as the target of the batch shim " + shim, nil
		}
	}
	// Git for Windows' default places, machine-wide then per-user, for a PATH that only has a shim or no git at all.
	for _, place := range []struct{ variable, sub string }{
		{"ProgramFiles", filepath.Join("Git", "cmd", "git.exe")},
		{"LOCALAPPDATA", filepath.Join("Programs", "Git", "cmd", "git.exe")},
	} {
		if base, ok := envx.Lookup(env, place.variable); ok && filepath.IsAbs(base) {
			if p := filepath.Join(base, place.sub); isFile(p) {
				return p, "in Git for Windows' default place under %" + place.variable + "%", nil
			}
		}
	}
	if shim != "" {
		return "", "", fmt.Errorf("%w: git was found only as a batch shim at %s; put git.exe on PATH", ErrNoGit, shim)
	}
	return "", "", fmt.Errorf("%w: git was not found on PATH", ErrNoGit)
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
