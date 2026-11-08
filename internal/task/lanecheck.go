package task

import (
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/guard"
	"github.com/NovaSeele/Seelegent/internal/proof"
	"github.com/NovaSeele/Seelegent/internal/snapshot"
)

// The lane check (docs/gates.md, field report 4): the lane judges the net change since the lead's instruction (F5),
// and only a file a worker plausibly touched, with its edit tools or a command whose writes Seelegent can read,
// counts against it (F6). A file that changed without either (the lead or the user editing the same tree, a
// command's side effects) is a warning.

// laneTouch holds the files and folders (snapshot keys, relative to the repository root) a worker plausibly touched,
// and the globs its commands named (rm src/*.vue).
type laneTouch struct {
	keys  []string
	globs []string
}

// addRel records a path relative to the repository root ('/' or '\' separators).
func (l *laneTouch) addRel(rel string) {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		return
	}
	if strings.ContainsAny(rel, "*?") {
		if !slices.Contains(l.globs, rel) {
			l.globs = append(l.globs, rel)
		}
		return
	}
	if k := snapshot.Key(rel); !slices.Contains(l.keys, k) {
		l.keys = append(l.keys, k)
	}
}

// addEdited records the files of a turn's edit tools (Turn.Edited: relative to the repository root, already
// resolved through fsutil.Canonical, so 8.3 names, junctions and letter case do not matter).
func (l *laneTouch) addEdited(rels []string) {
	for _, r := range rels {
		l.addRel(r)
	}
}

// gitBashPath matches a Git Bash drive path (/c/Users/...).
var gitBashPath = regexp.MustCompile(`^/([A-Za-z])(/.*)?$`)

// addCommand records the paths a shell command writes, moves or deletes (guard.Writes), resolved against cwd and
// made relative to root the same way the edit tools' files are (relInside: canonical paths on both sides). A folder
// a command removes counts for every file under it (has).
func (l *laneTouch) addCommand(root, cwd, command string) {
	for _, p := range guard.Writes(command) {
		p = strings.Trim(p, `"'`)
		if p == "" || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$") || strings.HasPrefix(p, "%") ||
			strings.HasPrefix(p, "/dev/") {
			continue
		}
		if runtime.GOOS == "windows" {
			if m := gitBashPath.FindStringSubmatch(filepath.ToSlash(p)); m != nil {
				p = strings.ToUpper(m[1]) + ":" + filepath.FromSlash(m[2]+"/")
			}
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, filepath.FromSlash(p))
		}
		if strings.ContainsAny(p, "*?") {
			// A glob cannot be made canonical as a whole: resolve its folder, keep its last part.
			dir, base := filepath.Split(p)
			if rel, ok := relInside(root, filepath.Clean(dir)); ok {
				l.addRel(filepath.ToSlash(filepath.Join(rel, base)))
			}
			continue
		}
		if rel, ok := relInside(root, p); ok {
			l.addRel(rel)
		}
	}
}

// has reports whether a worker touched rel: the file itself, a folder above it, or a glob that matches it.
func (l *laneTouch) has(rel string) bool {
	k := snapshot.Key(rel)
	for _, t := range l.keys {
		if k == t || strings.HasPrefix(k, t+"/") {
			return true
		}
	}
	for _, g := range l.globs {
		if proof.Match(g, rel) {
			return true
		}
	}
	return false
}

// laneTouched collects what the workers of t's turns since the instruction, up to turn n, touched.
func laneTouched(t *Task, n int) *laneTouch {
	l := &laneTouch{}
	root := claimRoot(t)
	for _, tu := range t.Turns[instructionStart(t, n):] {
		if tu.N > n {
			break
		}
		l.addEdited(turnFiles(t, tu, liveState{}))
		for _, c := range tu.Commands {
			l.addCommand(root, t.Cwd, c)
		}
	}
	return l
}

// netChanged reports whether rel differs between the snapshot taken when the instruction's turn started and the
// end snapshot of the latest turn, by their manifest entries (hash, else size and time: a large file that was not
// stored still has its hash). A file missing from a snapshot was clean then (not truncated, same HEAD: netLane).
func netChanged(start, end snapshot.Manifest, rel string) bool {
	before, inStart := start.Find(rel)
	after, inEnd := end.Find(rel)
	switch {
	case inStart && inEnd:
		changed, _ := snapshot.Changed(before, after)
		return changed
	case !inStart && !inEnd:
		return false
	case !inStart:
		return after.Status != "" // clean when the instruction began, dirty now
	default:
		return before.Status != "" // dirty when the instruction began, clean now
	}
}

// netLane keeps the files of union (changed by some turn since the instruction, up to turn n) that differ between
// the start snapshot of the instruction's turn and the end snapshot of turn n (end, or loaded when nil): a file
// created and deleted again, or changed and restored, is no change. When the snapshots cannot tell (one is missing
// or truncated, HEAD moved), union is returned as it is: the check fails closed.
func (s *Service) netLane(t *Task, n int, union []string, end *snapshot.Manifest) []string {
	if s.o.Snapshots == nil || len(union) == 0 {
		return union
	}
	first := t.Turns[instructionStart(t, n)]
	if first.Snap == nil {
		return union
	}
	start, err := s.o.Snapshots.Load(t.ID, first.N, snapshot.KindStart)
	if err != nil {
		return union
	}
	if end == nil {
		e, err := s.o.Snapshots.Load(t.ID, n, snapshot.KindEnd)
		if err != nil {
			return union
		}
		end = &e
	}
	if start.Truncated || end.Truncated || start.Head != end.Head {
		return union
	}
	out := []string{}
	for _, f := range union {
		if netChanged(start, *end, f) {
			out = append(out, f)
		}
	}
	return out
}

// laneFindings words a lane step as the warn-only scope findings of a task whose gate is off (field report 4, F12):
// the lane is still checked after each turn, but only as information.
func laneFindings(st api.GateStep) []api.Finding {
	var out []api.Finding
	for _, f := range st.Files {
		out = append(out, api.Finding{Kind: api.FindingOutsideLane, Severity: api.SeverityWarn, Path: f,
			Detail: "outside the lane"})
	}
	for _, f := range st.Unseen {
		out = append(out, api.Finding{Kind: api.FindingOutsideLane, Severity: api.SeverityWarn, Path: f,
			Detail: "outside the lane, " + gate.UnseenNote})
	}
	return out
}

// laneOffGate reports whether t has a lane but its gate is off: no gate phase, the lane is checked after each turn
// as scope findings.
func laneOffGate(t *Task, laneMode string) bool {
	p := t.Proof
	return gated(t) && p != nil && p.GateOff && p.Lane != nil && laneMode != api.LaneOff && t.RepoRoot != ""
}

// laneOffFindings checks the lane of a gate-off task at the end of turn tu, whose own results (not stored yet) are
// changed, edited and commands, with end its end snapshot.
func (s *Service) laneOffFindings(t *Task, tu *Turn, changed, edited, commands []string,
	end *snapshot.Manifest) []api.Finding {
	laneMode := s.settings().Lanes
	if !laneOffGate(t, laneMode) {
		return nil
	}
	var union []string
	seen := map[string]bool{}
	add := func(fs []string) {
		for _, f := range fs {
			if k := snapshot.Key(f); !seen[k] {
				seen[k] = true
				union = append(union, f)
			}
		}
	}
	touch := &laneTouch{}
	if tu.N > 1 {
		prev, _, _ := gateInput(t, tu.N-1)
		if instructionStart(t, tu.N-1) == instructionStart(t, tu.N) {
			add(prev)
			touch = laneTouched(t, tu.N-1)
		}
	}
	add(changed)
	touch.addEdited(edited)
	for _, c := range commands {
		touch.addCommand(claimRoot(t), t.Cwd, c)
	}
	files := s.netLane(t, tu.N, union, end)
	return laneFindings(gate.LaneStep(t.Proof.Lane.Paths, files, laneMode, touch.has))
}
