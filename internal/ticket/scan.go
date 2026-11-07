package ticket

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/gitx"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// The pull request scan (old bug D5: `git add -A` used to stage everything, including .env files, keys and junk
// such as a file named nul). Before Seelegent commits or pushes a ticket branch it lists exactly the files that
// would leave the machine — the uncommitted files git does not ignore and every file the branch changed since its
// base — and refuses the pull request when one of them is a secret, a key, junk or too large. Nothing is
// committed or pushed then; the refusal names each file and why. There is no override: the user removes the file
// (or adds it to .gitignore) and calls pr again.

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
	{regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`), "a private key"},
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

// scanPaths returns the files of the ticket's pull request: the uncommitted non-ignored files (to stage) and the
// files the branch changed since base (already committed).
func (s *Service) scanPaths(ctx context.Context, wt, base string) (stage, committed []string, err error) {
	out, err := s.o.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain=v1", "-z",
		"--untracked-files=all", "--no-renames")
	if err != nil {
		_, gerr := s.git(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "status", "--porcelain")
		return nil, nil, gerr
	}
	for _, rec := range strings.Split(string(out), "\x00") {
		if len(rec) < 4 {
			continue
		}
		stage = append(stage, rec[3:])
	}
	if base != "" {
		diff, err := s.o.Git.Exec(ctx, wt, gitx.ExecOptions{Timeout: gitx.ReadTimeout}, "diff", "--name-only", "-z",
			"--no-renames", "--diff-filter=d", base, "HEAD")
		if err == nil {
			for _, p := range strings.Split(string(diff), "\x00") {
				if p != "" {
					committed = append(committed, p)
				}
			}
		}
	}
	return stage, committed, nil
}

// scan checks every file that would leave the machine and returns the findings.
func (s *Service) scan(_ context.Context, wt string, files []string) []api.ScanFinding {
	findings := []api.ScanFinding{}
	seen := map[string]bool{}
	for _, rel := range files {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if why := nameReason(rel); why != "" {
			findings = append(findings, api.ScanFinding{Path: rel, Reason: why})
			continue
		}
		p := filepath.Join(wt, filepath.FromSlash(rel))
		fi, err := os.Lstat(p)
		if err != nil {
			continue // deleted: nothing of it leaves
		}
		if platform.IsLink(fi) {
			if target, err := os.Readlink(p); err == nil && (filepath.IsAbs(target) || strings.HasPrefix(
				filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(rel), target))), "../")) {
				findings = append(findings, api.ScanFinding{Path: rel, Reason: "link to a place outside the worktree"})
			}
			continue
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		if fi.Size() > MaxFileBytes {
			findings = append(findings, api.ScanFinding{Path: rel,
				Reason: fmt.Sprintf("larger than %d MB (%d bytes)", MaxFileBytes>>20, fi.Size())})
			continue
		}
		f, err := os.Open(p) //nolint:gosec // a file of the ticket worktree
		if err != nil {
			findings = append(findings, api.ScanFinding{Path: rel, Reason: "unreadable: " + oneLine(err.Error(), 100)})
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
		_ = f.Close()
		if err != nil {
			findings = append(findings, api.ScanFinding{Path: rel, Reason: "unreadable: " + oneLine(err.Error(), 100)})
			continue
		}
		if why := scanBytes(data, s.o.GitHub.HasToken); why != "" {
			findings = append(findings, api.ScanFinding{Path: rel, Reason: why})
		}
	}
	return findings
}
