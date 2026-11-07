package guard

import "testing"

// TestConfigEnvAfterKeywords (N1): the configuration variables are refused on every path to a command, after
// keywords, cmd's if and do, and wrappers, not only at the start of a segment.
func TestConfigEnvAfterKeywords(t *testing.T) {
	posix := map[string]bool{
		`if [ -z "$X" ]; then export GIT_CONFIG_GLOBAL=/tmp/g.cfg; fi`:     true,
		`{ export GIT_CONFIG_PARAMETERS="'alias.x=stash'"; }`:              true,
		`if true; then :; else export GIT_CONFIG_COUNT=1; fi`:              true,
		`while false; do export GIT_CONFIG_KEY_0=alias.x; done`:            true,
		`! export GIT_CONFIG_COUNT=1`:                                      true,
		`time export GIT_CONFIG_COUNT=1`:                                   true,
		`f() { export GIT_CONFIG_PARAMETERS="'alias.x=stash'"; }; f`:       true,
		`then GIT_CONFIG_COUNT=1 git x`:                                    true,
		`if true; then GIT_CONFIG_PARAMETERS="'alias.x=stash'" git x; fi`:  true,
		`time GIT_CONFIG_COUNT=1 git x`:                                    true,
		`nohup env GIT_CONFIG_COUNT=1 git x`:                               true,
		`sudo -u bob GIT_CONFIG_COUNT=1 git x`:                             true,
		`if [ -z "$X" ]; then export GIT_PAGER=cat; fi`:                    false,
		`{ export FOO=1; git status; }`:                                    false,
		`while read -r f; do echo "$f"; done < GIT_CONFIG_COUNT.txt`:       false,
		`if [ -n "$GIT_CONFIG_COUNT" ]; then echo "$GIT_CONFIG_COUNT"; fi`: false,
	}
	expect(t, Checker{}, posix, POSIX)
	expect(t, Checker{}, posix)
	cmd := map[string]bool{
		`if defined X set GIT_CONFIG_COUNT=1`:                true,
		`if not defined X set "GIT_CONFIG_PARAMETERS='x'"`:   true,
		`for %i in (1) do set GIT_CONFIG_COUNT=1`:            true,
		`for %i in (1) do (set GIT_CONFIG_KEY_0=alias.x)`:    true,
		`if exist a (set GIT_CONFIG_COUNT=1) else (echo no)`: true,
		`if defined X set GIT_PAGER=cat`:                     false,
		`for %i in (1) do echo %GIT_CONFIG_COUNT%`:           false,
	}
	expect(t, Checker{}, cmd, Cmd)
	expect(t, Checker{}, cmd)
	expect(t, Checker{}, map[string]bool{
		`if ($x) { $env:GIT_CONFIG_COUNT = 1 }`:       true,
		`while ($true) { $env:GIT_CONFIG_KEY_0='a' }`: true,
		`$a = $env:GIT_CONFIG_COUNT`:                  false,
	}, PowerShell)
}

// TestPipedGroups (G-a): a subshell or brace group piped into a shell has its commands' output read as the script.
func TestPipedGroups(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		`{ echo git stash; } | bash`:                       true,
		`(echo git stash) | bash`:                          true,
		`{ printf 'git stash\n'; } | bash`:                 true,
		`(printf 'cd x\n'; echo git reset --hard) | sh`:    true,
		`{ echo cd x; echo git stash; } | tr -d '\r' | sh`: true,
		`echo x | (cat; echo git stash) | bash`:            true,
		"{ cat <<'EOF'\ngit stash\nEOF\n} | bash":          true,
		`(cd sub && echo git stash) 2>&1 | bash`:           true,
		`( (echo git stash) ) | bash`:                      true,
		`{ echo git status; echo go test; } | bash`:        false,
		`(echo git stash) | cat`:                           false,
		`{ echo git stash; } > notes.txt`:                  false,
		`(echo git stash); echo ok | bash`:                 false,
		`{ git log -1; } | grep fix`:                       false,
	}, POSIX)
	expect(t, Checker{}, map[string]bool{
		`(echo git stash) | cmd`:  true,
		`(echo git status) | cmd`: false,
	}, Cmd)
	// The interpreters read a group's output as their code too.
	expect(t, Checker{}, map[string]bool{
		`(echo "import os"; echo "os.system('git stash')") | python3`: true,
		`{ echo "print('git stash')"; } | python3`:                    false,
	}, POSIX)
}

// TestPipedCompoundsAndGroupInput (F16): a compound command (if..fi, for/while/until..done, case..esac) piped into
// a shell is a group whose commands' output is the script, and a shell inside a group on the read end of a pipe
// reads that pipe; read-only lines stay allowed.
func TestPipedCompoundsAndGroupInput(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		// (a) compound commands on the write end.
		`if true; then echo git stash; fi | bash`:                          true,
		`for i in 1; do echo git stash; done | bash`:                       true,
		`while true; do echo git reset --hard; break; done | sh`:           true,
		`until false; do printf 'git stash\n'; done | bash`:                true,
		`case x in x) echo git stash;; esac | bash`:                        true,
		`if true; then for i in 1; do echo git stash; done; fi | bash`:     true,
		`if true; then echo cd x; fi; echo git stash | bash`:               true,
		"if true\nthen\n  echo git stash\nfi | bash":                       true,
		`for f in a b; do echo git status "$f"; done | bash`:               false,
		`if true; then echo git stash; fi | cat`:                           false,
		`while read -r l; do echo "$l"; done < notes.txt | sort`:           false,
		`for i in 1 2; do echo "fi done esac"; done | bash`:                false,
		`if [ -f x ]; then echo git stash > notes.txt; fi; echo ok | bash`: false,
		// (b) a shell inside a group on the read end.
		`echo git stash | (cd x && bash)`:                         true,
		`echo git stash | { cd x && bash; }`:                      true,
		`echo git stash | (cd x; sh -s)`:                          true,
		`printf 'git reset --hard\n' | ( (bash) )`:                true,
		`echo git stash | if true; then bash; fi`:                 true,
		`echo git stash | (cd x && python3)`:                      false,
		`echo git status | (cd x && bash)`:                        false,
		`echo git stash | (cd x && cat)`:                          false,
		`echo git stash | (cd x && bash ./build.sh)`:              false,
		"echo git stash | (bash <<'EOF'\ngo test ./...\nEOF\n)":   false,
		`git log --oneline | while read -r l; do echo "$l"; done`: false,
	}, POSIX)
}

// TestConfigReads (G-b): git config <key> and git config get <key> read; the writing forms stay refused.
func TestConfigReads(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		"git config help.autocorrect":                   false,
		"git config core.hooksPath":                     false,
		"git config alias.st":                           false,
		"git config --global alias.st":                  false,
		"git config --type=bool core.fsmonitor":         false,
		"git config -f .gitmodules submodule.x.update":  false,
		"git config get alias.st":                       false,
		"git config get --all alias.st":                 false,
		"git config --get-regexp '^alias\\.'":           false,
		"git config user.name 'A B'":                    false,
		"git config --unset user.name":                  false,
		"git config remove-section user":                false,
		"git config help.autocorrect immediate":         true,
		"git config alias.st status":                    true,
		"git config --add alias.st status":              true,
		"git config --unset alias.st":                   true,
		"git config --uns alias.st":                     true,
		"git config --unset-all core.hooksPath":         true,
		"git config --replace-all alias.st status":      true,
		"git config --replace alias.st status":          true,
		"git config set alias.st status":                true,
		"git config unset alias.st":                     true,
		"git config -e":                                 true,
		"git config --edit --global":                    true,
		"git config edit":                               true,
		"git config --rename-section user alias":        true,
		"git config rename-section foo filter.x":        true,
		"git config --remove-section alias":             true,
		"git config remove-section alias":               true,
		"git config $KEY":                               true,
		"git config --global core.hooksPath /tmp/hooks": true,
	}, POSIX)
}

// TestXargsShell (G-c): a shell xargs runs with the input as its script reads what is piped into xargs.
func TestXargsShell(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		`echo git stash | xargs -I{} sh -c '{}'`:            true,
		`echo git stash | xargs -I % bash -c 'cd x && %'`:   true,
		`echo git stash | xargs -i sh -c '{}'`:              true,
		`echo git stash | xargs --replace sh -c '{}'`:       true,
		`echo git stash | xargs --replace=@ sh -c '@'`:      true,
		`echo 'git stash' | xargs sh -c`:                    true,
		`echo git stash | xargs -0 bash -lc`:                true,
		`printf 'git stash\n' | xargs -I{} sh -c '{}'`:      true,
		`echo git stash | xargs -I{} cmd /c {}`:             true,
		`echo stash | xargs -I{} git {}`:                    true,
		`echo stash | xargs git`:                            true,
		`git ls-files | xargs -I{} sh -c 'wc -l "$1"' _ {}`: false,
		`git ls-files | xargs -I{} sh -c 'wc -l {}'`:        false,
		`git ls-files -z | xargs -0 wc -l`:                  false,
		`echo a.go | xargs -I{} git add {}`:                 false,
		`echo git status | xargs -I{} sh -c '{}'`:           false,
		`echo git stash | xargs -I{} echo {}`:               false,
	}, POSIX)
}
