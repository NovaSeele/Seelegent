package ticket

import (
	"context"
	"errors"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/github"
)

// The English error text of a failed step stays for the lead's tools; the dashboard explains the step in the
// reader's language from its cause (api.Cause*) and keeps the text behind a "Seelegent said" disclosure.

// Phrases of git's own messages, lower case. Auth comes first: an SSH key git cannot use ends with "could not read
// from remote repository" too.
var (
	gitAuthPhrases = []string{"authentication failed", "could not read username", "could not read password",
		"terminal prompts disabled", "permission denied (publickey", "invalid username or password",
		"http basic: access denied", "the requested url returned error: 401",
		"the requested url returned error: 403", "403 forbidden", "permission to ", "denied to ",
		"support for password authentication was removed", "host key verification failed"}
	gitRejectedPhrases = []string{"[rejected]", "[remote rejected]", "pre-receive hook declined",
		"protected branch", "failed to push some refs"}
	networkPhrases = []string{"could not resolve host", "failed to connect", "connection timed out",
		"connection refused", "connection reset", "network is unreachable", "unable to access",
		"could not read from remote repository", "operation timed out", "tls handshake", "ssl_connect",
		"early eof", "the remote end hung up"}
)

// errorCause is the cause of a failed step for the dashboard (api.Cause*).
func errorCause(err error) string {
	if err == nil {
		return ""
	}
	var ge *github.Error
	if errors.As(err, &ge) {
		switch ge.Reason {
		case github.ReasonAuth:
			return api.CauseGitHubAuth
		case github.ReasonRateLimited:
			return api.CauseGitHubRateLimited
		case github.ReasonForbidden:
			return api.CauseGitHubForbidden
		case github.ReasonNotFound:
			return api.CauseGitHubNotFound
		case github.ReasonNetwork:
			return api.CauseNetwork
		}
		return api.CauseGitHub
	}
	if errors.Is(err, github.ErrManual) {
		return api.CauseGitHubManual
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return api.CauseNetwork
	}
	var gerr *GitError
	if errors.As(err, &gerr) {
		return gitCause(gerr.Message)
	}
	if errors.Is(err, ErrGit) {
		return gitCause(err.Error())
	}
	return api.CauseOther
}

// gitCause is the cause of a git command that failed with msg.
func gitCause(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case containsAny(lower, gitAuthPhrases):
		return api.CauseGitAuth
	case containsAny(lower, gitRejectedPhrases):
		return api.CauseGitRejected
	case containsAny(lower, networkPhrases):
		return api.CauseNetwork
	}
	return api.CauseGit
}

func containsAny(s string, phrases []string) bool {
	for _, p := range phrases {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
