package ticket

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
)

// The pull request scan (old bug D5: `git add -A` used to stage everything, including .env files, keys and junk
// such as a file named nul). Seelegent never pushes the worker's history: it pushes one commit it makes itself, of
// the worktree's tree as it will be committed, on top of the commit it pushed before (or the base). Before anything
// is committed or pushed, every blob that commit adds — each entry that differs from its parents, with its path
// and its content as git stores it — is checked, and the pull request is refused when one of them is a secret, a
// key, junk or too large. Nothing is committed or pushed then; the refusal names each file and why. There is no
// override: the user removes the file (or adds it to .gitignore) and calls pr again. A scan that cannot run
// refuses the pull request too.

// MaxFileBytes is the largest file a pull request may carry.
const MaxFileBytes = 5 << 20

// ScanError is a refused pull request: the files and why.
type ScanError struct {
	Findings []api.ScanFinding
}

func (e *ScanError) Error() string {
	parts := make([]string, 0, len(e.Findings))
	for i, f := range e.Findings {
		if i == 10 {
			parts = append(parts, fmt.Sprintf("and %d more", len(e.Findings)-10))
			break
		}
		parts = append(parts, f.Path+" ("+f.Reason+")")
	}
	return "refusing to commit or push files that look like secrets or junk: " + strings.Join(parts, "; ") +
		". Remove them from the worktree (or add them to .gitignore) and call pr again"
}

// Is makes errors.Is(err, ErrConflict) true.
func (e *ScanError) Is(target error) bool { return target == ErrConflict }

// APIDetails are the error details of the HTTP API.
func (e *ScanError) APIDetails() map[string]any {
	return map[string]any{"reason": "unsafe_files", "findings": e.Findings}
}

// Name rules.
var (
	keyExts = map[string]bool{".pem": true, ".key": true, ".p12": true, ".pfx": true, ".jks": true, ".keystore": true,
		".ppk": true}
	keyNames    = map[string]bool{"id_rsa": true, "id_dsa": true, "id_ecdsa": true, "id_ed25519": true}
	credNames   = map[string]bool{".git-credentials": true, ".netrc": true, "_netrc": true, ".pypirc": true}
	envExamples = map[string]bool{".env.example": true, ".env.sample": true, ".env.template": true, ".env.dist": true}
	reserved    = regexp.MustCompile(`(?i)^(nul|con|prn|aux|com[1-9]|lpt[1-9])(\..*)?$`)
)

// Content rules: tokens and private keys.
var secretPatterns = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), "a GitHub token (github_pat_)"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}`), "a GitHub token (gh*_)"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "an AWS access key (AKIA…)"},
	{regexp.MustCompile(`-{5}BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-{5}`), "a private key"},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), "a Slack token"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`), "a Google API key"},
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), "an Anthropic API key"},
}

// nameReason says why a path may not be committed by its name alone ("" when it may).
func nameReason(rel string) string {
	base := path.Base(rel)
	lower := strings.ToLower(base)
	switch {
	case reserved.MatchString(base):
		return "reserved Windows device name (junk)"
	case lower == ".env" || strings.HasPrefix(lower, ".env.") && !envExamples[lower]:
		return "environment file (.env)"
	case keyNames[lower]:
		return "private key file"
	case keyExts[strings.ToLower(path.Ext(lower))]:
		return "key or certificate store (" + strings.ToLower(path.Ext(lower)) + ")"
	case credNames[lower]:
		return "credentials file"
	}
	return ""
}

// scanBytes says what secret data holds ("" when none). hasToken finds the stored GitHub token (may be nil).
func scanBytes(data []byte, hasToken func([]byte) bool) string {
	for _, p := range secretPatterns {
		if p.re.Match(data) {
			return "contains " + p.what
		}
	}
	if hasToken != nil && hasToken(data) {
		return "contains the stored GitHub token"
	}
	return ""
}

// uncommitted returns the uncommitted files of the worktree that git does not ignore: what the commit step stages.
func (s *Service) uncommitted(ctx context.Context, wt string) ([]string, error) {
	out, err := s.o.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain=v1", "-z",
		"--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, s.scanErr(err)
	}
	var stage []string
	for _, rec := range strings.Split(string(out), "\x00") {
		if len(rec) < 4 {
			continue
		}
		stage = append(stage, rec[3:])
	}
	return stage, nil
}

// ErrScan is matched by a scan that could not run: the pull request is refused, never sent unscanned.
var ErrScan = errors.New("could not scan the changes")

// PushError refuses a push before anything leaves the machine. Reason is scan_failed, base_not_pushed,
// branch_diverged or history_unsafe; Details go to the HTTP API next to it.
type PushError struct {
	Reason  string
	Message string
	Details map[string]any
	// Findings are the unsafe files of history_unsafe.
	Findings []api.ScanFinding
}

func (e *PushError) Error() string { return e.Message }

// Is makes errors.Is(err, ErrConflict) true, and errors.Is(err, ErrScan) for a scan that could not run.
func (e *PushError) Is(target error) bool {
	return target == ErrConflict || target == ErrScan && e.Reason == "scan_failed"
}

// APIDetails are the error details of the HTTP API.
func (e *PushError) APIDetails() map[string]any {
	d := map[string]any{"reason": e.Reason}
	for k, v := range e.Details {
		d[k] = v
	}
	if e.Findings != nil {
		d["findings"] = e.Findings
	}
	return d
}

// scanErr is a scan that could not run (a git failure, redacted).
func (s *Service) scanErr(err error) error {
	return &PushError{Reason: "scan_failed", Message: "could not scan the changes, so nothing was committed or " +
		"pushed: " + s.redactErr(err, 400)}
}

// object is a blob the push would send: its path in the pushed tree and its mode ("" when unknown: a history scan).
type object struct {
	path, mode, sha string
}

// Tree entry modes.
const (
	modeLink    = "120000"
	modeGitlink = "160000"
)

// changedObjects lists the blobs of tree that differ from parent (a tree or a commit): what a commit of tree on
// top of parent adds. Deleted entries carry nothing out; a submodule entry is only a commit name.
func (s *Service) changedObjects(ctx context.Context, dir, parent, tree string) (map[string]object, error) {
	out, err := s.o.Git.Exec(ctx, dir, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff-tree", "-r", "-z",
		"--no-renames", parent, tree)
	if err != nil {
		return nil, s.scanErr(err)
	}
	objs := map[string]object{}
	recs := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(recs); i += 2 {
		meta := strings.Fields(strings.TrimPrefix(recs[i], ":"))
		if len(meta) != 5 {
			return nil, s.scanErr(fmt.Errorf("unexpected diff-tree line %q", recs[i]))
		}
		mode, sha, status := meta[1], meta[3], meta[4]
		if status == "D" || mode == modeGitlink || strings.Trim(sha, "0") == "" {
			continue
		}
		objs[recs[i+1]] = object{path: recs[i+1], mode: mode, sha: sha}
	}
	return objs, nil
}

// historyObjects lists every blob reachable from rev, with the first path it was seen at (the first publish of a
// branch sends its whole history).
func (s *Service) historyObjects(ctx context.Context, dir, rev string) ([]object, error) {
	out, err := s.o.Git.Exec(ctx, dir, gitx.ExecOptions{Timeout: 5 * time.Minute}, "rev-list", "--objects", rev)
	if err != nil {
		return nil, s.scanErr(err)
	}
	var objs []object
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		sha, p, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || p == "" || seen[sha] {
			continue // a commit, or a blob seen at another path
		}
		seen[sha] = true
		objs = append(objs, object{path: p, sha: sha})
	}
	return objs, nil
}

// batchMax bounds the bytes one `cat-file --batch` call returns.
const batchMax = 64 << 20

// scanObjects checks the blobs a push would send (D5 rules: names, links leaving the tree, size, secret content)
// and returns the findings. A git failure is an ErrScan error: nothing may be sent unscanned.
func (s *Service) scanObjects(ctx context.Context, dir string, objs []object) ([]api.ScanFinding, error) {
	findings := []api.ScanFinding{}
	var check []object
	for _, o := range objs {
		if why := nameReason(o.path); why != "" {
			findings = append(findings, api.ScanFinding{Path: o.path, Reason: why})
			continue
		}
		check = append(check, o)
	}
	if len(check) == 0 {
		return findings, nil
	}
	// Sizes and types first: large blobs are findings without being read.
	var in bytes.Buffer
	for _, o := range check {
		in.WriteString(o.sha + "\n")
	}
	out, err := s.o.Git.Exec(ctx, dir, gitx.ExecOptions{Timeout: 5 * time.Minute, Stdin: in.Bytes()}, "cat-file",
		"--batch-check")
	if err != nil {
		return nil, s.scanErr(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != len(check) {
		return nil, s.scanErr(fmt.Errorf("cat-file answered %d of %d objects", len(lines), len(check)))
	}
	var read []object
	size := map[string]int64{}
	for i, o := range check {
		f := strings.Fields(lines[i])
		if len(f) != 3 || f[0] != o.sha {
			return nil, s.scanErr(fmt.Errorf("object %s: %q", o.sha, lines[i]))
		}
		if f[1] != "blob" {
			continue // a tree or a commit of a history listing
		}
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			return nil, s.scanErr(err)
		}
		if n > MaxFileBytes && o.mode != modeLink {
			findings = append(findings, api.ScanFinding{Path: o.path,
				Reason: fmt.Sprintf("larger than %d MB (%d bytes)", MaxFileBytes>>20, n)})
			continue
		}
		size[o.sha] = n
		read = append(read, o)
	}
	for len(read) > 0 {
		var chunk []object
		var total int64
		for len(read) > 0 && (len(chunk) == 0 || total+size[read[0].sha] <= batchMax) {
			total += size[read[0].sha]
			chunk, read = append(chunk, read[0]), read[1:]
		}
		more, err := s.scanBatch(ctx, dir, chunk)
		if err != nil {
			return nil, err
		}
		findings = append(findings, more...)
	}
	return findings, nil
}

// scanBatch reads the blobs of objs with one `cat-file --batch` and checks their content.
func (s *Service) scanBatch(ctx context.Context, dir string, objs []object) ([]api.ScanFinding, error) {
	var in bytes.Buffer
	for _, o := range objs {
		in.WriteString(o.sha + "\n")
	}
	out, err := s.o.Git.Exec(ctx, dir, gitx.ExecOptions{Timeout: 5 * time.Minute, Stdin: in.Bytes()}, "cat-file",
		"--batch")
	if err != nil {
		return nil, s.scanErr(err)
	}
	var findings []api.ScanFinding
	for _, o := range objs {
		head, rest, ok := bytes.Cut(out, []byte("\n"))
		f := strings.Fields(string(head))
		if !ok || len(f) != 3 || f[0] != o.sha {
			return nil, s.scanErr(fmt.Errorf("object %s: unexpected cat-file answer", o.sha))
		}
		n, err := strconv.Atoi(f[2])
		if err != nil || n+1 > len(rest) {
			return nil, s.scanErr(fmt.Errorf("object %s: short cat-file answer", o.sha))
		}
		data := rest[:n]
		out = rest[n+1:]
		if o.mode == modeLink {
			target := string(data)
			if path.IsAbs(target) || filepath.IsAbs(target) || strings.HasPrefix(
				path.Clean(path.Join(path.Dir(o.path), filepath.ToSlash(target))), "../") ||
				path.Clean(path.Join(path.Dir(o.path), filepath.ToSlash(target))) == ".." {
				findings = append(findings, api.ScanFinding{Path: o.path, Reason: "link to a place outside the worktree"})
			}
			continue
		}
		if why := scanBytes(data, s.o.GitHub.HasToken); why != "" {
			findings = append(findings, api.ScanFinding{Path: o.path, Reason: why})
		}
	}
	return findings, nil
}
