package guard

import (
	"errors"
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
		"reset.exe --hard":  true,
		"stash.exe":         true,
		"stash.exe clear":   true,
		"clean.exe -fdx":    true,
		"checkout.exe -- .": true,
		"push.exe --force":  true,
		"RESET.EXE --hard":  true,
		"st.exe":            true,
		// Git for Windows before 2.29 ships git-<builtin>.exe in its exec-path: git strips one ".exe" from
		// reset.exe.exe and runs the dashed git-reset.exe, which resets (review G1).
		"reset.exe.exe --hard":  true,
		"stash.EXE.exe.exe":     true,
		"status.exe":            false,
		"log.exe.exe --oneline": false,
		"log.exe --oneline":     false,
		"stash.exe list":        false,
		"clean.exe -n":          false,
		"checkout.exe -b new":   false,
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
		"send-pack . :refs/heads/feature":                         true,
		"send-pack --force . +HEAD~3:refs/heads/main":             true,
		"replay --onto HEAD~3 --contained HEAD~1..HEAD":           true,
		"replay --ref-action=update --onto x a..b":                true,
		"replay --ref-action=print --onto x a..b":                 false,
		"replay --ref-action print --onto x a..b":                 false,
		"replay --ref=print --onto x a..b":                        false,
		"replay --ref-action=print --no-ref-action --onto x a..b": true,
		"fetch --update-head-ok . +HEAD~1:refs/heads/main":        true,
		"fetch -u . main:main":                                    true,
		"fetch --update . main:main":                              true,
		"fetch . +HEAD~3:refs/heads/feature":                      true,
		"fetch . +refs/tags/old:feature":                          true,
		"fetch . +old:heads/feature":                              true,
		"fetch -f . refs/tags/old:refs/heads/feature":             true,
		"fetch origin +refs/heads/*:refs/heads/*":                 true,
		"fetch":                     false,
		"fetch origin":              false,
		"fetch --prune origin":      false,
		"fetch origin main":         false,
		"fetch origin main:feature": false,
		"fetch origin +refs/heads/*:refs/remotes/origin/*": false,
		"fetch -f origin +refs/tags/*:refs/tags/*":         false,
		"fetch --multiple -f a b":                          false,
		"fetch -j 4 origin":                                false,
		// Review N1: every refspec that applies, in lower case, with wildcards and pruning.
		"fetch ../old +refs/*:refs/*":                              true,
		"fetch ../old --refmap=+refs/heads/*:refs/heads/* main":    true,
		"fetch ../old --refmap +refs/heads/*:refs/heads/* main":    true,
		"fetch ../old --refm=+refs/heads/*:refs/heads/* main":      true,
		"remote add --mirror=fetch old ../old":                     true,
		"remote add --mirror old ../old":                           true,
		"remote add --mirr=fetch old ../old":                       true,
		"config remote.old.fetch +refs/heads/*:refs/heads/*":       true,
		"config --add remote.old.fetch +refs/heads/*:refs/heads/*": true,
		"config set remote.old.fetch refs/x/*:refs/*":              true,
		"config remote.old.mirror true":                            true,
		"config fetch.prune true":                                  true,
		"config remote.origin.prune true":                          true,
		"-c remote.old.fetch=+refs/heads/*:refs/heads/* fetch old": true,
		"-c remote.old.fetch=refs/heads/*:refs/heads/* fetch old":  true,
		"-c fetch.prune=true fetch . refs/x:refs/heads/x":          true,
		"--config-env=remote.old.fetch=X fetch old":                true,
		"--config-env remote.old.fetch=X fetch old":                true,
		"fetch . +c1c1c1c:refs/HEADS/main":                         true,
		"fetch . c1c1c1c:refs/HEADS/main":                          true,
		"fetch . +c1c1c1c:refs/x/heads/main":                       true,
		"fetch . c1c1c1c:refs/x/heads/main":                        true,
		"fetch . +refs/x/*:refs/*":                                 true,
		"fetch . refs/x/*:refs/*":                                  true,
		"fetch --prune . refs/nothing/*:refs/heads/*":              true,
		"fetch --prune . refs/nothing/x:refs/heads/x":              true,
		"fetch -p . refs/nothing/x:x":                              true,
		"fetch --prune-tags . refs/nothing/x:refs/heads/x":         true,
		"fetch --stdin origin":                                     true,
		"fetch --tags":                                             false,
		"fetch --tags origin":                                      false,
		"fetch origin main:main":                                   false,
		"fetch origin refs/heads/main:refs/heads/main":             false,
		"fetch origin tag v1":                                      false,
		"fetch origin ^refs/heads/x":                               false,
		"fetch --prune origin +refs/heads/*:refs/remotes/origin/*": false,
		"fetch origin +refs/heads/main:FETCH_HEAD":                 false,
		"fetch --refmap= origin main":                              false,
		"remote add origin https://example.com/r.git":              false,
		"remote -v":             false,
		"remote prune origin":   false,
		"remote update --prune": false,
		"config remote.origin.fetch +refs/heads/*:refs/remotes/origin/*":   false,
		"config remote.origin.fetch ^refs/heads/wip":                       false,
		"config --unset remote.old.fetch":                                  false,
		"config remote.old.mirror false":                                   false,
		"config fetch.prune false":                                         false,
		"-c remote.origin.fetch=+refs/heads/*:refs/remotes/origin/* fetch": false,
		"-c fetch.prune=false fetch":                                       false,
		"checkout -B main HEAD~3":                                          true,
		"checkout -b new HEAD~3":                                           false,
		"worktree add -f -B main ../x HEAD~3":                              true,
		"worktree add -B main ../x":                                        true,
		"worktree add -b new ../x HEAD~3":                                  false,
		"worktree add ../x":                                                false,
		"worktree list":                                                    false,
		"-c gc.reflogExpire=now -c gc.reflogExpireUnreachable=now gc":      true,
		"-c gc.refs/stash.reflogExpire=now gc":                             true,
		"config gc.reflogExpire now":                                       true,
		"config gc.reflogExpire":                                           false,
		"repack -a -d":                                                     true,
		"repack -ad":                                                       true,
		"repack -A -d":                                                     false,
		"repack -a -d --cruft":                                             false,
		"repack -d":                                                        false,
		"repack":                                                           false,
	} {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	expect(t, c, map[string]bool{
		"git send-pack . :refs/heads/x":                                              true,
		"git fetch origin":                                                           false,
		`git fetch ../old "+refs/*:refs/*"`:                                          true,
		`git fetch ../old --refmap="+refs/heads/*:refs/heads/*" main`:                true,
		`git remote add --mirror=fetch old ../old && git fetch old`:                  true,
		`git config remote.old.fetch "+refs/heads/*:refs/heads/*"`:                   true,
		`git -c remote.old.fetch="+refs/heads/*:refs/heads/*" fetch old`:             true,
		`git fetch . "+c1c1c1c:refs/HEADS/main"`:                                     true,
		`git fetch . "+c1c1c1c:refs/x/heads/main" && git fetch . "+refs/x/*:refs/*"`: true,
		`git fetch --prune . "refs/nothing/*:refs/heads/*"`:                          true,
		`git fetch --prune origin "+refs/heads/*:refs/remotes/origin/*"`:             false,
	}, POSIX)
}

// TestFetchConfig (review N1): a fetch is checked with the refspecs and prune settings of the configuration of the
// remotes it reads, unless --refmap replaces them; a configuration that cannot be read refuses it.
func TestFetchConfig(t *testing.T) {
	normal := []ConfigEntry{
		{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
		{"remote.origin.prune", "true"},
		{"fetch.prune", "true"},
	}
	mirror := append([]ConfigEntry{{"remote.old.fetch", "+refs/heads/*:refs/heads/*"}}, normal...)
	stale := []ConfigEntry{
		{"remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"},
		{"remote.Old.fetch", "refs/nothing/x:refs/heads/x"},
		{"remote.Old.prune", "true"},
	}
	var gotGlobals []string
	with := func(cfg []ConfigEntry, err error) Checker {
		return Checker{Config: func(globals []string) ([]ConfigEntry, error) {
			gotGlobals = globals
			return cfg, err
		}}
	}
	cases := []struct {
		name string
		c    Checker
		args string
		deny bool
	}{
		{"plain fetch, normal config", with(normal, nil), "fetch", false},
		{"fetch origin, normal config", with(normal, nil), "fetch origin", false},
		{"fetch --tags, normal config", with(normal, nil), "fetch --tags", false},
		{"fetch --prune origin, normal config", with(normal, nil), "fetch --prune origin", false},
		{"fetch --all, normal config", with(normal, nil), "fetch --all", false},
		{"fetch origin main, normal config", with(normal, nil), "fetch origin main", false},
		{"remote update, normal config", with(normal, nil), "remote update", false},
		{"remote prune origin, normal config", with(normal, nil), "remote prune origin", false},
		{"fetch old, mirror refspec", with(mirror, nil), "fetch old", true},
		{"fetch old main, mirror refspec as refmap", with(mirror, nil), "fetch old main", true},
		{"fetch old, --refmap replaces it", with(mirror, nil), "fetch --refmap= old main", false},
		{"fetch origin, other remote mirrors", with(mirror, nil), "fetch origin", false},
		{"plain fetch, some remote mirrors", with(mirror, nil), "fetch", true},
		{"fetch --all, some remote mirrors", with(mirror, nil), "fetch --all", true},
		{"fetch group, unknown name", with(mirror, nil), "fetch grp", true},
		{"fetch URL, mirror elsewhere", with(mirror, nil), "fetch ../old main", false},
		{"remote update, mirror", with(mirror, nil), "remote update", true},
		{"fetch old, configured prune of a branch", with(stale, nil), "fetch old", true},
		{"fetch old --no-prune", with(stale, nil), "fetch --no-prune old", false},
		{"remote prune old", with(stale, nil), "remote prune old", true},
		{"fetch origin, stale elsewhere", with(stale, nil), "fetch origin", false},
		{"config cannot be read", with(nil, errors.New("exit status 128")), "fetch origin", true},
		{"no reader", Checker{}, "fetch old", false},
	}
	for _, tc := range cases {
		if got := tc.c.Git(strings.Fields(tc.args)); got.Deny != tc.deny {
			t.Errorf("%s: git %s deny = %v, want %v", tc.name, tc.args, got.Deny, tc.deny)
		}
	}
	with(normal, nil).Git([]string{"-C", "../x", "--git-dir=../x/.git", "fetch"})
	if strings.Join(gotGlobals, " ") != "-C ../x --git-dir=../x/.git" {
		t.Errorf("config read with globals %q, want -C ../x --git-dir=../x/.git", gotGlobals)
	}
	expect(t, with(mirror, nil), map[string]bool{"git fetch": true, "cd x && git fetch origin": false}, POSIX)
}

// TestProgramOptions (H4): git options, settings, URLs and variables that make git run a command of the caller's
// choosing are refused on both layers; plain grep, difftool and remote reads stay allowed.
func TestProgramOptions(t *testing.T) {
	c := Checker{}
	for args, want := range map[string]bool{
		"grep -Ogit-stash foo":                              true,
		"grep --open-files-in-pager=vim foo":                true,
		"grep --open foo":                                   true,
		"grep -nO foo":                                      true,
		"grep -n foo":                                       false,
		"grep -e -O foo":                                    false,
		"grep -- -O":                                        false,
		"difftool -y -x true HEAD":                          true,
		"difftool --extcmd=true HEAD":                       true,
		"difftool --ext=true HEAD":                          true,
		"difftool -y HEAD":                                  false,
		"difftool --tool=vimdiff HEAD":                      false,
		"ls-remote --upload-pack=x .":                       true,
		"ls-remote --upload-pack x .":                       true,
		"ls-remote --exec=x .":                              true,
		"fetch --upload-pack=x origin":                      true,
		"fetch --upl=x origin":                              true,
		"archive --remote=. --exec=x HEAD":                  true,
		"clone -u x src dst":                                true,
		"clone -qu x src dst":                               true,
		"clone --upload-pack=x src dst":                     true,
		"clone -c core.fsmonitor=x src dst":                 true,
		"clone --config=core.hooksPath=/tmp src dst":        true,
		"clone -c user.name=x src dst":                      false,
		"clone --depth 1 https://example.com/r.git":         false,
		"ls-remote origin":                                  false,
		"fetch origin --prune":                              false,
		"-c protocol.ext.allow=always fetch ext::sh":        true,
		"-c protocol.allow=always fetch origin":             true,
		"fetch ext::sh%20-c%20git%20stash":                  true,
		"remote add x ext::sh":                              true,
		"submodule add ext::sh x":                           true,
		"-c core.alternateRefsCommand=x fetch":              true,
		"-c interactive.diffFilter=x add -p":                true,
		"config interactive.diffFilter x":                   true,
		"config core.alternateRefsCommand x":                true,
		"config url.ext::sh.insteadOf origin":               true,
		"-c url.ext::sh.insteadOf=origin fetch origin":      true,
		"config interactive.diffFilter":                     false,
		"--exec-path=/tmp/x status":                         true,
		"--exec-path":                                       false,
		"send-email --to-cmd=x a.patch":                     true,
		"send-email --smtp-server=/usr/bin/x a.patch":       true,
		"send-email --smtp-server=smtp.example.com a.patch": false,
	} {
		if got := c.Git(strings.Fields(args)); got.Deny != want {
			t.Errorf("git %s deny = %v, want %v", args, got.Deny, want)
		}
	}
	expect(t, c, map[string]bool{
		`git -c protocol.ext.allow=always fetch "ext::sh -c git% stash"`: true,
		`git grep -O'git stash' foo`:                                     true,
		`git difftool -y -x 'git stash; true' HEAD`:                      true,
		`git ls-remote --upload-pack='git stash; git-upload-pack' .`:     true,
		`git config interactive.diffFilter 'git stash'`:                  true,
		`GIT_ALLOW_PROTOCOL=ext git fetch origin`:                        true,
		`export GIT_ALLOW_PROTOCOL=ext`:                                  true,
		`GIT_SSH_COMMAND='git stash' git fetch origin`:                   true,
		`env GIT_EXTERNAL_DIFF=x git diff`:                               true,
		`git grep -n "TODO" -- '*.go'`:                                   false,
		`git grep "$pattern"`:                                            false,
		`git grep "-O$x" a`:                                              true,
		`git difftool --dir-diff HEAD~1`:                                 false,
		`echo "$GIT_SSH_COMMAND"`:                                        false,
		// G2: editors, pagers and password prompts git runs, set for the git command or the shell.
		`GIT_EDITOR='sh -c "git stash"' git commit`:             true,
		`GIT_SEQUENCE_EDITOR='git stash;' git rebase -i HEAD~1`: true,
		`GIT_PAGER='git stash; less' git log`:                   true,
		`GIT_ASKPASS=./x.sh git fetch origin`:                   true,
		`EDITOR="git stash" git commit`:                         true,
		`VISUAL=x git commit`:                                   true,
		`PAGER='sh -c x' git log`:                               true,
		`SSH_ASKPASS=/tmp/p git fetch`:                          true,
		`export GIT_EDITOR=vim`:                                 true,
		`env GIT_PAGER=x git log`:                               true,
		`GIT_EDITOR=$E git commit`:                              true,
		`PAGER+=x git log`:                                      true,
		`GIT_PAGER=cat git log`:                                 false,
		`GIT_PAGER= git log`:                                    false,
		`PAGER=less git log`:                                    false,
		`GIT_PAGER='less -R' git log`:                           false,
		`GIT_EDITOR=true git commit`:                            false,
		`export PAGER=cat`:                                      false,
		`echo $EDITOR`:                                          false,
	}, POSIX)
	expect(t, c, map[string]bool{
		`$env:GIT_EDITOR = 'git stash'`:    true,
		`$env:GIT_PAGER = 'cat'`:           false,
		`$env:GIT_PAGER='cat'; git log`:    false,
		`setx EDITOR "git stash"`:          true,
		`setx PAGER cat`:                   false,
		`$env:GIT_ALLOW_PROTOCOL = 'ext'`:  true,
		`git grep -n TODO`:                 false,
		`git difftool -x 'git stash' HEAD`: true,
	}, PowerShell)
}
