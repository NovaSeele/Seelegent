package guard

import (
	"strings"
)

// On a disk that ignores case (NTFS, APFS by default) refs/heads/Main and refs/heads/main are one loose ref file.
// git looks a branch up case-sensitively in packed-refs and in its own list, so creating Main beside main does not
// fail as "already exists": it writes the loose file refs/heads/Main, which the disk stores as main, and main now
// points where the new branch does (git branch Main <old>, checkout -b Main <old>, fetch . old:Main). The guard
// refuses a new branch whose name matches an existing one in all but case (review of fix/m18-open-items, item 1).

// branchListing are the git branch options that list, describe or set the upstream of existing branches instead of
// creating the one named.
var branchListing = []string{"-l", "--list", "-a", "--all", "-r", "--remotes", "--contains", "--no-contains",
	"--merged", "--no-merged", "--points-at", "--show-current", "--edit-description", "--unset-upstream", "-u",
	"--set-upstream-to", "--format", "--sort", "--column", "--no-column", "-i", "--ignore-case"}

// headRef is the full name of the branch name.
func headRef(name word) word {
	name.text = "refs/heads/" + name.text
	return name
}

// mapWords applies f to every word.
func mapWords(ws []word, f func(word) word) []word {
	out := make([]word, len(ws))
	for i, w := range ws {
		out[i] = f(w)
	}
	return out
}

// fetchBranchRef is the branch a fetch refspec destination writes ("" for none): a full name under refs/heads/, or
// a short one as git expands it (heads/x and a plain x are refs/heads/..., remotes/ and tags/ are not branches).
func fetchBranchRef(dst string) string {
	switch {
	case dst == "" || strings.Contains(dst, "*") || strings.EqualFold(dst, "FETCH_HEAD"):
		return ""
	case strings.HasPrefix(dst, "refs/"):
		if strings.HasPrefix(dst, "refs/heads/") {
			return dst
		}
		return ""
	case strings.HasPrefix(dst, "heads/"):
		return "refs/" + dst
	case strings.HasPrefix(dst, "remotes/") || strings.HasPrefix(dst, "tags/"):
		return ""
	}
	return "refs/heads/" + dst
}

// worktreeBranch is the branch git worktree add (arguments after "add") may create without -b: with no commit-ish,
// one named after the path's last element; with a commit-ish that names no local branch but a remote one, a branch
// of that name. Not with --detach.
func worktreeBranch(p parsed) []word {
	if p.has("-d", "--detach") || len(p.values("-b")) > 0 {
		return nil
	}
	switch args := p.pos[1:]; len(args) {
	case 1:
		path := strings.TrimRight(args[0].text, `/\`)
		if i := strings.LastIndexAny(path, `/\`); i >= 0 {
			path = path[i+1:]
		}
		if path == "" {
			return nil
		}
		return []word{{text: path, dynamic: args[0].dynamic}}
	case 2:
		return args[1:]
	}
	return nil
}

// caseClash reports whether creating the branches refs (full names) could write over an existing branch whose name
// differs only in case. Branches lists the existing ones when the repository's disk ignores case; a list that cannot
// be read, or a name the guard cannot see, counts as a clash.
func (c Checker) caseClash(globals []string, refs []word) bool {
	if c.Branches == nil || len(refs) == 0 {
		return false
	}
	existing, err := c.Branches(globals)
	if err != nil {
		return true
	}
	for _, r := range refs {
		for _, e := range existing {
			if r.dynamic || r.text != e && strings.EqualFold(r.text, e) {
				return true
			}
		}
	}
	return false
}
