package task

import (
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// One reading of the paths a worker names, for the edit tools' files and its commands' writes alike: the lane check
// (lanecheck.go), the unreported_file hygiene check and the scope warnings (safety_checks.go) all go through
// commandPath and repoRel, so a path is resolved the same way everywhere (field report 4, F6 review).

// gitBashDrive matches a Git Bash drive path (/c/Users/...).
var gitBashDrive = regexp.MustCompile(`^/([A-Za-z])(/.*)?$`)

// commandPath turns a path a worker names (guard.WriteTargets, or an edit tool's) into an absolute path: relative
// to cwd, a Git Bash drive path (/c/...) mapped on Windows. ok is false for the worker's home (~), a variable and
// a device.
func commandPath(cwd, p string) (string, bool) {
	p = strings.Trim(p, `"'`)
	if p == "" || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$") || strings.HasPrefix(p, "%") {
		return "", false // the worker's own home, or a variable
	}
	slash := filepath.ToSlash(p)
	if strings.HasPrefix(slash, "/dev/") || strings.HasPrefix(slash, "/proc/") || slash == "/dev/null" {
		return "", false
	}
	if runtime.GOOS == "windows" {
		if m := gitBashDrive.FindStringSubmatch(slash); m != nil {
			p = strings.ToUpper(m[1]) + ":" + filepath.FromSlash(m[2]+"/")
		}
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, filepath.FromSlash(p))
	}
	return p, true
}

// repoRel returns the path p a worker names (absolute, or relative to cwd) relative to the repository root, with '/'
// separators, when it lies inside it. Both sides are compared as canonical paths (relInside: 8.3 short names,
// junctions and letter case on Windows), so a CLI's spelling does not matter.
func repoRel(root, cwd, p string) (string, bool) {
	abs, ok := commandPath(cwd, p)
	if !ok {
		return "", false
	}
	rel, ok := relInside(root, abs)
	if !ok {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// workerWrites answers whether a worker plausibly made or changed a file of the repository: one of its edit tools'
// files, or a path one of its shell commands writes, moves or deletes (guard.WriteTargets, joined to the folder a cd
// of the same line moved to, relative to the task's folder), the file itself, a folder holding it (mkdir, cp -r, mv
// into a folder, rm -r) or a glob that matches it. A write whose place Seelegent cannot read (a variable, a
// command's output, the repository root or the task's folder as a whole) claims no file: its command is listed in
// unknown, for the lane to fail closed on.
type workerWrites struct {
	root, cwd string
	cwdKey    string   // the task's folder, relative to the root ("." for the root itself)
	keys      []string // snapshot keys of root-relative files and folders
	globs     []string
	unknown   []string // the commands that wrote where Seelegent could not read
	// runs are the windows the worker's commands ran in, and mtime a file's modification time (cmdwindows.go): a
	// file that changed during a run is the worker's (the lane check only; nil mtime: no window applies). runners
	// is true when a code runner ran: a file gone from the end snapshot, whose time is unknown, counts then.
	runs    []commandRun
	mtime   func(rel string) (time.Time, bool)
	runners bool
}

// newWorkerWrites reads a turn's edit-tool files (edited: relative to root, '/' separators) and its shell
// commands.
func newWorkerWrites(cwd, root string, edited, commands []string) *workerWrites {
	w := &workerWrites{root: root, cwd: cwd, cwdKey: "."}
	if under, ok := relInside(root, cwd); ok {
		w.cwdKey = snapshot.Key(filepath.ToSlash(under))
	}
	w.addEdited(edited)
	for _, c := range commands {
		w.addCommand(c)
	}
	return w
}

// addRel records a path relative to the repository root ('/' separators).
func (w *workerWrites) addRel(rel string) {
	if rel == "" || rel == "." {
		return
	}
	if strings.ContainsAny(rel, "*?") {
		if !slices.Contains(w.globs, rel) {
			w.globs = append(w.globs, rel)
		}
		return
	}
	if k := snapshot.Key(rel); !slices.Contains(w.keys, k) {
		w.keys = append(w.keys, k)
	}
}

// addEdited records the files of a turn's edit tools (relative to the repository root, as repoRel makes them).
func (w *workerWrites) addEdited(rels []string) {
	for _, r := range rels {
		w.addRel(filepath.ToSlash(r))
	}
}

// addCommand records the places a shell command writes.
func (w *workerWrites) addCommand(command string) {
	unknown := false
	for _, tg := range guard.WriteTargets(command) {
		if tg.Unknown {
			unknown = true
			continue
		}
		p := strings.Trim(tg.Path, `"'`)
		if strings.ContainsAny(p, "*?") {
			// A glob cannot be made canonical as a whole: resolve its folder, keep its last part.
			abs, ok := commandPath(w.cwd, p)
			if !ok {
				continue
			}
			dir, base := filepath.Split(abs)
			if rel, ok := relInside(w.root, filepath.Clean(dir)); ok {
				w.addRel(filepath.ToSlash(filepath.Join(rel, base)))
			}
			continue
		}
		rel, ok := repoRel(w.root, w.cwd, p)
		if !ok {
			continue // outside the repository, the worker's home, a device
		}
		if rel == "." || snapshot.Key(rel) == w.cwdKey {
			unknown = true // the whole repository or task folder: which of its files, it does not say
			continue
		}
		w.addRel(rel)
	}
	if unknown && !slices.Contains(w.unknown, command) {
		w.unknown = append(w.unknown, command)
	}
}

// wrote reports whether the worker plausibly made or changed rel (relative to the repository root).
func (w *workerWrites) wrote(rel string) bool {
	k := snapshot.Key(rel)
	for _, t := range w.keys {
		if k == t || strings.HasPrefix(k, t+"/") {
			return true
		}
	}
	for _, g := range w.globs {
		if proof.Match(g, rel) {
			return true
		}
	}
	if w.mtime != nil {
		if at, ok := w.mtime(rel); ok {
			return ranDuring(w.runs, at)
		}
		return w.runners
	}
	return false
}
