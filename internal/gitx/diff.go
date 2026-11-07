package gitx

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// baseRe is a commit a diff may be based on: HEAD or 7 to 40 hex digits.
var baseRe = regexp.MustCompile(`^(HEAD|[0-9a-fA-F]{7,40})$`)

// hunkRe reads a hunk header: @@ -oldStart[,oldLines] +newStart[,newLines] @@.
var hunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// CleanPath checks a path from a request that must be relative to the repository root: not empty, not absolute,
// no "..", no NUL, and inside root once links are resolved (fsutil). It returns the path with '/' separators.
func CleanPath(root, p string) (string, error) {
	if p == "" || strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: empty path", ErrInvalid)
	}
	s := strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(s, "/") || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return "", fmt.Errorf("%w: path %q must be relative to the repository root", ErrInvalid, p)
	}
	for _, part := range strings.Split(s, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: path %q must not contain a .. element", ErrInvalid, p)
		}
	}
	s = path.Clean(s)
	if s == "." {
		return "", fmt.Errorf("%w: path %q names the repository itself", ErrInvalid, p)
	}
	ok, err := fsutil.Within(root, filepath.Join(root, filepath.FromSlash(s)))
	if err != nil {
		return "", fmt.Errorf("%w: path %q: %w", ErrInvalid, p, err)
	}
	if !ok {
		return "", fmt.Errorf("%w: path %q leaves the repository", ErrInvalid, p)
	}
	return s, nil
}

// Diff returns the diff of one file of the repository at root against base (HEAD when empty), working tree
// included. rel must come from CleanPath. An untracked file (or untracked set) is shown as added in full.
func (r *Repos) Diff(ctx context.Context, root, rel, base string, untracked bool) (api.FileDiff, error) {
	if base == "" {
		base = "HEAD"
	}
	if !baseRe.MatchString(base) {
		return api.FileDiff{}, fmt.Errorf("%w: base %q must be HEAD or 7 to 40 hex digits", ErrInvalid, base)
	}
	if untracked {
		return untrackedDiff(root, rel)
	}
	if base == "HEAD" {
		if _, _, err := r.run(ctx, root, ReadTimeout, 0, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
			base = emptyTree
		}
	}
	out, over, err := r.run(ctx, root, ReadTimeout, maxDiffBytes, "diff", "--no-color", "--no-ext-diff",
		"--no-textconv", "--no-renames", "-U3", base, "--", rel)
	if err != nil {
		return api.FileDiff{}, err
	}
	if len(out) == 0 {
		// No tracked change: an untracked file shows in full, anything else has an empty diff.
		ls, _, err := r.run(ctx, root, ReadTimeout, 0, "ls-files", "-z", "--", rel)
		if err != nil {
			return api.FileDiff{}, err
		}
		if len(ls) == 0 {
			if d, err := untrackedDiff(root, rel); err == nil {
				return d, nil
			}
		}
		return api.FileDiff{Path: rel, Hunks: []api.DiffHunk{}}, nil
	}
	d := ParseDiff(string(out), over)
	d.Path = rel
	return d, nil
}

// untrackedDiff shows a new file as one hunk of added lines (a symbolic link: its target, as git shows it).
func untrackedDiff(root, rel string) (api.FileDiff, error) {
	d := api.FileDiff{Path: rel, Hunks: []api.DiffHunk{}}
	data, err := readHead(filepath.Join(root, filepath.FromSlash(rel)), maxDiffBytes+1)
	if errors.Is(err, fsutil.ErrNotRegular) {
		return d, nil // a FIFO, socket or device has no content git would show
	}
	if err != nil {
		return api.FileDiff{}, fmt.Errorf("%w: %s: %w", ErrInvalid, rel, err)
	}
	if isBinary(data) {
		d.Binary = true
		return d, nil
	}
	if len(data) > maxDiffBytes {
		data, d.Truncated = data[:maxDiffBytes], true
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" && len(data) == 0 {
		return d, nil
	}
	lines := strings.Split(text, "\n")
	if d.Truncated && len(lines) > 1 {
		lines = lines[:len(lines)-1] // a partial last line
	}
	h := api.DiffHunk{Header: fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)), NewStart: 1, NewLines: len(lines),
		Lines: make([]api.DiffLine, 0, len(lines))}
	for i, l := range lines {
		h.Lines = append(h.Lines, api.DiffLine{Kind: api.DiffAdd, Text: strings.TrimSuffix(l, "\r"), NewNo: i + 1})
	}
	d.Hunks = append(d.Hunks, h)
	return d, nil
}

// ParseDiff reads the unified diff of one file. truncated marks a diff cut at the size limit: the last, partial
// line is dropped.
func ParseDiff(text string, truncated bool) api.FileDiff {
	d := api.FileDiff{Hunks: []api.DiffHunk{}, Truncated: truncated}
	lines := strings.Split(text, "\n")
	if truncated && len(lines) > 1 {
		lines = lines[:len(lines)-1]
	}
	var h *api.DiffHunk
	oldNo, newNo := 0, 0
	inHunk := false
	for _, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if m := hunkRe.FindStringSubmatch(l); m != nil {
			d.Hunks = append(d.Hunks, api.DiffHunk{Header: l, OldStart: atoi(m[1]), OldLines: count(m[2]),
				NewStart: atoi(m[3]), NewLines: count(m[4]), Lines: []api.DiffLine{}})
			h = &d.Hunks[len(d.Hunks)-1]
			oldNo, newNo = h.OldStart, h.NewStart
			inHunk = true
			continue
		}
		if !inHunk {
			switch {
			case strings.HasPrefix(l, "Binary files ") || l == "GIT binary patch":
				d.Binary = true
			case strings.HasPrefix(l, "rename from "):
				d.OldPath = strings.TrimPrefix(l, "rename from ")
			}
			continue
		}
		if l == "" {
			continue
		}
		switch l[0] {
		case ' ':
			h.Lines = append(h.Lines, api.DiffLine{Kind: api.DiffContext, Text: l[1:], OldNo: oldNo, NewNo: newNo})
			oldNo++
			newNo++
		case '-':
			h.Lines = append(h.Lines, api.DiffLine{Kind: api.DiffDel, Text: l[1:], OldNo: oldNo})
			oldNo++
		case '+':
			h.Lines = append(h.Lines, api.DiffLine{Kind: api.DiffAdd, Text: l[1:], NewNo: newNo})
			newNo++
		case '\\': // "\ No newline at end of file"
		default:
			inHunk = false // the header of a next file
		}
	}
	return d
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// count reads the optional line count of a hunk header; absent means 1.
func count(s string) int {
	if s == "" {
		return 1
	}
	return atoi(s)
}
