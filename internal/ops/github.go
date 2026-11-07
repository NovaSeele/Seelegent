package ops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/client"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// What the issues tool and the issues command share: naming a repo and an issue.

// ErrRepoNotConnected is returned when no connected repo has that id or folder.
var ErrRepoNotConnected = errors.New("repo not connected")

var (
	// repoIDPattern is a repo id (r + 8 hex).
	repoIDPattern = regexp.MustCompile(`^r[0-9a-fA-F]{8}$`)
	// issueNumber is a bare issue number ("12" or "#12"), which means a GitHub issue.
	issueNumber = regexp.MustCompile(`^#?\d+$`)
)

// IssueKey is an issue key as given (gh#12, local#3); a bare number means a GitHub issue.
func IssueKey(s string) string {
	s = strings.TrimSpace(s)
	if issueNumber.MatchString(s) {
		return "gh#" + strings.TrimPrefix(s, "#")
	}
	return s
}

// RepoID resolves a repo argument: a repo id, or the folder of a connected repo. abs is the argument made absolute
// by the caller (relative to its own folder).
func RepoID(ctx context.Context, c *client.Client, repo, abs string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("a repo is required (its id or path)")
	}
	if repoIDPattern.MatchString(repo) {
		return strings.ToLower(repo), nil
	}
	list, err := c.Repos(ctx)
	if err != nil {
		return "", err
	}
	want, err := fsutil.Key(abs)
	if err != nil {
		return "", err
	}
	for i := range list {
		if list[i].ID == repo {
			return list[i].ID, nil
		}
		if got, err := fsutil.Key(list[i].Path); err == nil && got == want {
			return list[i].ID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrRepoNotConnected, repo)
}
