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
