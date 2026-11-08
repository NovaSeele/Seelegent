package guard

import (
	"strings"
	"testing"
)

// TestExeSuffixSubcommand (H1): Git for Windows strips ".exe" from the subcommand, so git reset.exe --hard runs
// reset --hard and git st.exe runs the alias st. Both the git shim (Git) and the hook (Command) read it as the
// subcommand it names; the same names without a refused meaning stay allowed.
func TestExeSuffixSubcommand(t *testing.T) {
	c := Checker{Alias: func(name string) (string, bool) {
		if name == "st" {
			return "stash", true
		}
		return "", false
	}}
	shim := map[string]bool{
		"reset.exe --hard":    true,
		"stash.exe":           true,
		"stash.exe clear":     true,
		"clean.exe -fdx":      true,
		"checkout.exe -- .":   true,
		"push.exe --force":    true,
		"RESET.EXE --hard":    true,
		"st.exe":              true,
		"status.exe":          false,
		"log.exe --oneline":   false,
		"stash.exe list":      false,
		"clean.exe -n":        false,
		"checkout.exe -b new": false,
	}
	for args, want := range shim {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("shim: git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	hook := map[string]bool{}
	for args, want := range shim {
		hook["git "+args] = want
	}
	hook["cd x && git clean.exe -fdx"] = true
	hook["git.exe reset.exe --hard"] = true
	expect(t, c, hook, POSIX)
	expect(t, c, hook, PowerShell)
}

// TestReflogDrop (H2): git reflog drop deletes a whole reflog, refs/stash included (every stashed change); listing
// and showing stay allowed.
func TestReflogDrop(t *testing.T) {
	c := Checker{}
	for args, want := range map[string]bool{
		"reflog drop refs/stash":   true,
		"reflog drop --all":        true,
		"reflog drop":              true,
		"reflog.exe drop --all":    true,
		"reflog expire --all":      true,
		"reflog delete stash@{0}":  true,
		"reflog":                   false,
		"reflog show refs/stash":   false,
		"reflog list":              false,
		"reflog exists refs/stash": false,
	} {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	expect(t, c, map[string]bool{"git reflog drop --all": true, "git reflog show": false}, POSIX)
}

// TestBranchRewrites (H3): commands that move or delete a branch outside the always-refused ones: send-pack, git
// replay without --ref-action=print, fetch with a forced refspec into a branch or --update-head-ok, checkout -B and
// worktree add -B on an existing branch, gc with the reflogs expired now, repack -a -d. Their safe forms stay allowed.
func TestBranchRewrites(t *testing.T) {
	c := Checker{}
	for args, want := range map[string]bool{
		"send-pack . :refs/heads/feature":                               true,
		"send-pack --force . +HEAD~3:refs/heads/main":                   true,
		"replay --onto HEAD~3 --contained HEAD~1..HEAD":                 true,
		"replay --ref-action=update --onto x a..b":                      true,
		"replay --ref-action=print --onto x a..b":                       false,
		"replay --ref-action print --onto x a..b":                       false,
		"replay --ref=print --onto x a..b":                              false,
		"replay --ref-action=print --no-ref-action --onto x a..b":       true,
		"fetch --update-head-ok . +HEAD~1:refs/heads/main":              true,
		"fetch -u . main:main":                                          true,
		"fetch --update . main:main":                                    true,
		"fetch . +HEAD~3:refs/heads/feature":                            true,
		"fetch . +refs/tags/old:feature":                                true,
		"fetch . +old:heads/feature":                                    true,
		"fetch -f . refs/tags/old:refs/heads/feature":                   true,
		"fetch origin +refs/heads/*:refs/heads/*":                       true,
		"fetch":                                                         false,
		"fetch origin":                                                  false,
		"fetch --prune origin":                                          false,
		"fetch origin main":                                             false,
		"fetch origin main:feature":                                     false,
		"fetch origin +refs/heads/*:refs/remotes/origin/*":              false,
		"fetch -f origin +refs/tags/*:refs/tags/*":                      false,
		"fetch --multiple -f a b":                                       false,
		"fetch -j 4 origin":                                             false,
		"checkout -B main HEAD~3":                                       true,
		"checkout -b new HEAD~3":                                        false,
		"worktree add -f -B main ../x HEAD~3":                           true,
		"worktree add -B main ../x":                                     true,
		"worktree add -b new ../x HEAD~3":                               false,
		"worktree add ../x":                                             false,
		"worktree list":                                                 false,
		"-c gc.reflogExpire=now -c gc.reflogExpireUnreachable=now gc":   true,
		"-c gc.refs/stash.reflogExpire=now gc":                          true,
		"config gc.reflogExpire now":                                    true,
		"config gc.reflogExpire":                                        false,
		"repack -a -d":                                                  true,
		"repack -ad":                                                    true,
		"repack -A -d":                                                  false,
		"repack -a -d --cruft":                                          false,
		"repack -d":                                                     false,
		"repack":                                                        false,
	} {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	expect(t, c, map[string]bool{"git send-pack . :refs/heads/x": true, "git fetch origin": false}, POSIX)
}
