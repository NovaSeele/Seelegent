package guard

import (
	"strings"
	"testing"
)

// expect checks a table of lines against one checker and dialect list (none: unknown shell).
func expect(t *testing.T, c Checker, cases map[string]bool, shells ...Shell) {
	t.Helper()
	for line, deny := range cases {
		r := c.Command(line, shells...)
		if r.Deny != deny {
			t.Errorf("shells %v: Command(%q) deny = %v, want %v (%s)", shells, line, r.Deny, deny, r.Reason)
		}
		if r.Deny && !strings.HasPrefix(r.Reason, Marker) {
			t.Errorf("Command(%q) reason %q does not start with the marker", line, r.Reason)
		}
	}
}

// TestAutocorrectConfig (G1): help.autocorrect makes git run the command it guesses from a typo, and a later -c
// overrides the shim's own -c help.autocorrect=0.
func TestAutocorrectConfig(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		"git -c help.autocorrect=immediate stsh":       true,
		"git -c help.autoCorrect=1 status":             true,
		"git -chelp.autocorrect=prompt stsh":           true,
		"git --config-env=help.autocorrect=AC stsh":    true,
		"git --config-env help.autocorrect=AC stsh":    true,
		"git config help.autocorrect immediate":        true,
		"git config --global help.autocorrect 1":       true,
		"git config set help.autocorrect immediate":    true,
		"git config --get help.autocorrect":            false,
		"git -c help.format=web status":                false,
		"git -c core.quotepath=off -c color.ui=0 diff": false,
	})
	if r := (Checker{}).Git([]string{"-c", "help.autocorrect=0", "-c", "help.autocorrect=immediate", "stsh"}); !r.Deny {
		t.Error("shim arguments with a second -c help.autocorrect allowed")
	}
}

// TestConfigEnvironment (G2): git configuration set through environment variables on the command line is invisible
// to the alias resolver, so setting those variables is refused in every form.
func TestConfigEnvironment(t *testing.T) {
	deny := map[string]bool{
		`GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'" git x`:                                            true,
		`GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0='reset --hard' git x`:             true,
		`env GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'" git x`:                                        true,
		`env -i GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0=stash git x`:               true,
		`env 'GIT_CONFIG_COUNT=1' git x`:                                                                  true,
		`sudo GIT_CONFIG_COUNT=1 git x`:                                                                   true,
		`export GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'"; git x`:                                    true,
		`export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0=stash && git x`:            true,
		"GIT_CONFIG_PARAMETERS=\"'alias.x=stash'\"\nexport GIT_CONFIG_PARAMETERS\ngit x":                  true,
		`declare -x GIT_CONFIG_KEY_0=alias.x`:                                                             true,
		`export GIT_CONFIG_COUNT`:                                                                         true,
		`GIT_CONFIG_GLOBAL=/tmp/evil git x`:                                                               true,
		`bash -c 'GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=alias.x GIT_CONFIG_VALUE_0=stash git x'`:            true,
		"bash <<'EOF'\nexport GIT_CONFIG_PARAMETERS=\"'alias.x=stash'\"\ngit x\nEOF":                      true,
		`python3 -c "import subprocess; subprocess.run(['git','x'], env={'GIT_CONFIG_PARAMETERS': 'y'})"`: true,
		`node -e "require('child_process').execSync('git x', {env: {GIT_CONFIG_COUNT: '1'}})"`:            true,
		// Reading them, or naming them in data, is fine.
		`echo $GIT_CONFIG_PARAMETERS`:                            false,
		`env | grep GIT_CONFIG_COUNT`:                            false,
		`rg -n GIT_CONFIG_COUNT internal`:                        false,
		`git commit -m 'refuse GIT_CONFIG_COUNT=1 too'`:          false,
		`git log -S GIT_CONFIG_PARAMETERS`:                       false,
		`GIT_PAGER=cat git log -1`:                               false,
		`GIT_CONFIG_NOSYSTEM=1 git status`:                       false,
		`FOO="a b" git stash`:                                    true, // a quoted value is still an assignment
		`FOO='x' BAR="y" git reset --hard`:                       true,
		`env "FOO=a b" git stash`:                                true,
		`sudo FOO=1 git stash`:                                   true,
		`"FOO=1" git status`:                                     false,
		`python3 -c "print('GIT_CONFIG_COUNT=1')"`:               false,
		"cat <<'EOF' > notes.md\nexport GIT_CONFIG_COUNT=1\nEOF": false,
	}
	expect(t, Checker{}, deny)
	expect(t, Checker{}, deny, POSIX)
	expect(t, Checker{}, map[string]bool{
		`$env:GIT_CONFIG_PARAMETERS="'alias.x=reset --hard'"; git x`:          true,
		`$env:GIT_CONFIG_COUNT = 1; $env:GIT_CONFIG_KEY_0 = 'alias.x'; git x`: true,
		`${env:GIT_CONFIG_COUNT} = '1'`:                                       true,
		`$env:git_config_key_0 += 'x'`:                                        true,
		`Set-Item env:GIT_CONFIG_COUNT 1`:                                     true,
		`New-Item -Path Env:\GIT_CONFIG_KEY_0 -Value alias.x`:                 true,
		`[Environment]::SetEnvironmentVariable('GIT_CONFIG_COUNT', '1')`:      true,
		`if ($true) { $env:GIT_CONFIG_PARAMETERS = 'x' }; git x`:              true,
		`Write-Output $env:GIT_CONFIG_COUNT`:                                  false,
		`if ($env:GIT_CONFIG_COUNT -eq 1) { echo set }`:                       false,
		`Get-ChildItem env:GIT_CONFIG_COUNT`:                                  false,
		`$env:GIT_PAGER = 'cat'; git log -1`:                                  false,
		`while ($i -lt 3) { $i++ }`:                                           false,
		`if ($x) { echo a } else { echo b }`:                                  false,
		`if ($x) { git stash }`:                                               true,
	}, PowerShell)
	expect(t, Checker{}, map[string]bool{
		`set GIT_CONFIG_PARAMETERS='alias.x=reset --hard' && git x`: true,
		`set "GIT_CONFIG_COUNT=1" & git x`:                          true,
		`set /a GIT_CONFIG_COUNT=1`:                                 true,
		`setx GIT_CONFIG_GLOBAL C:\x`:                               true,
		`echo %GIT_CONFIG_COUNT%`:                                   false,
		`set GIT_PAGER=cat && git log -1`:                           false,
	}, Cmd)
	if r := Check(`GIT_CONFIG_COUNT=1 git x`); r.Reason != configEnvText {
		t.Errorf("reason %q", r.Reason)
	}
}

// TestHeredocDialects (G3): a line from an unknown shell reads a POSIX here-document's lines as POSIX does (data for
// cat, tee, sed, a script file; a script for a shell), unless HeredocAsCommands (agy on Windows, PowerShell).
func TestHeredocDialects(t *testing.T) {
	data := []string{
		"cat <<'EOF' | tee notes.md\ngit stash\nEOF",
		"cat <<'EOF' > /tmp/x.sh\ngit stash\nEOF",
		"sed -n '1,5p' <<'EOF'\ngit reset --hard\nEOF",
		"bash script.sh <<'EOF'\ngit stash\nEOF",
		"cat <<'EOF'\ngit reset --hard\nEOF",
		"cat <<-EOF\n\tgit stash\n\tEOF",
		"git commit -m \"$(cat <<'EOF'\nfix: x\n\ngit stash and git reset --hard are refused now\nEOF\n)\"",
		"cat > a.md <<'EOF'\n# Notes\n\nNever `git stash` here (it's shared).\nEOF\ngo test ./...",
	}
	scripts := []string{
		"bash <<'EOF'\ngit reset --hard\nEOF",
		"cat <<'EOF' | sh\ngit stash\nEOF",
		"cat <<'EOF'\nx\nEOF\ngit stash",
		"cat <<EOF\n$(git stash)\nEOF",
		"pwsh -Command - <<'EOF'\ngit stash\nEOF",
		"cmd <<'EOF'\ngit stash\nEOF",
		"cat <<'EOF' | tee a\ngit stash\nEOF\ngit reset --hard",
	}
	unknown := map[string]bool{}
	strict := map[string]bool{}
	for i, l := range data {
		unknown[l] = false
		// agy on Windows: run_command runs PowerShell, where << starts no here-document and the next lines run
		// (the last one has no git command at the start of a line).
		strict[l] = i < len(data)-1
	}
	for _, l := range scripts {
		unknown[l], strict[l] = true, true
	}
	expect(t, Checker{}, unknown)
	expect(t, Checker{}, unknown, POSIX)
	expect(t, Checker{HeredocAsCommands: true}, strict)
	// An explicit dialect is read as it is.
	expect(t, Checker{}, map[string]bool{"cat <<'EOF'\ngit reset --hard\nEOF": true}, PowerShell)
}

func TestWithoutHeredocs(t *testing.T) {
	for in, want := range map[string]string{
		"cat <<'EOF' | tee a\ngit stash\nEOF\necho done": "cat <<'EOF' | tee a\n         \n   \necho done",
		"cat <<'EOF'\nunclosed":                          "cat <<'EOF'\n        ",
		"echo 'unclosed <<EOF\ngit stash":                "echo 'unclosed <<EOF\ngit stash",
		"git status":                                     "git status",
	} {
		if got := withoutHeredocs(in); got != want {
			t.Errorf("withoutHeredocs(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestInterpreterData (G4): inline code that cannot start a program is data, however many git commands its strings
// name; code that can has every string read as before.
func TestInterpreterData(t *testing.T) {
	cases := map[string]bool{
		"python3 - <<'EOF'\nfrom pathlib import Path\np = Path('docs/safety.md')\n" +
			"p.write_text(p.read_text().replace('git stash', 'git stash (refused)'))\nEOF": false,
		`python -c "print('git stash')"`:                                                                false,
		`node -e "fs.writeFileSync('a.md', s.replace('git reset --hard', 'x'))"`:                        false,
		`ruby -e 'puts "git stash"'`:                                                                    false,
		`perl -pi -e 's/git stash/git stash (refused)/' a.md`:                                           false,
		`node -e 'console.log(` + "`git stash`" + `)'`:                                                  false,
		`python3 -c "s = '%x' % 10; print('git stash', s)"`:                                             false,
		"python3 - <<'EOF'\nimport subprocess\ncmd = 'git stash'\nsubprocess.run(cmd, shell=True)\nEOF": true,
		`python -c "import os; c='git reset --hard'; os.system(c)"`:                                     true,
		`python3 -c "import asyncio; asyncio.create_subprocess_exec('git', 'stash')"`:                   true,
		`python3 -c "getattr(__import__('os'), 'system')('git stash')"`:                                 true,
		"ruby -e '`git stash`'":                                                                         true,
		`ruby -e '%x(git stash)'`:                                                                       true,
		`ruby -e 'IO.popen("git stash")'`:                                                               true,
		`perl -e 'qx{git reset --hard}'`:                                                                true,
		`perl -e 'open(my $f, "|-", "git stash")'`:                                                      true,
		`perl -e 'open(F, "| git stash")'`:                                                              true,
		`php -r 'echo shell_exec("git stash");'`:                                                        true,
		`node -e "const {execSync} = require('child_process'); execSync('git stash')"`:                  true,
		`osascript -e 'do shell script "git stash"'`:                                                    true,
		`deno eval "new Deno.Command('git', {args: ['stash']}).outputSync()"`:                           true,
		"bun -e 'await $`git stash`'":                                                                   true,
	}
	expect(t, Checker{}, cases)
	expect(t, Checker{}, cases, POSIX)
	r := (Checker{}).Command(`python -c "import os; os.system('git stash')"`, POSIX)
	if !strings.HasSuffix(r.Reason, codeHint) || !strings.HasPrefix(r.Reason, Marker) {
		t.Errorf("code refusal without the hint: %q", r.Reason)
	}
}

// TestPrintedScripts (G6): printf and echo decode \n before a shell reads what they print.
func TestPrintedScripts(t *testing.T) {
	expect(t, Checker{}, map[string]bool{
		`printf 'git stash\n' | tr -d '\r' | bash`:        true,
		`printf 'git stash\r\n' | bash`:                   true,
		`printf 'cd x\ngit stash\n' | sh`:                 true,
		`echo -e 'echo hi\ngit reset --hard' | sh`:        true,
		`echo 'echo a\ngit stash' | sh`:                   true,
		`printf '%s\n' 'cd x' 'git stash' | bash`:         true,
		`printf 'git %s\n' stash | bash`:                  true,
		`printf 'git\tstash\n' | bash`:                    true,
		`printf '%b' 'git stash\n' | bash`:                true,
		`printf 'git \163tash\n' | bash`:                  true,
		`printf 'git \x73tash\n' | bash`:                  true,
		`printf -- 'git stash\n' | bash`:                  true,
		`printf 'git status\n' | bash`:                    false,
		`printf 'git stash\n' | cat`:                      false,
		`printf 'git stash\n' > notes.txt`:                false,
		`echo -e 'a\nb' | sort`:                           false,
		`printf '%s\n' a b | bash`:                        false,
		`echo -n 'git stash\c and more' | grep stash`:     false,
		`printf -v line 'git stash\n'; echo "$line" | wc`: false,
	}, POSIX)
}

func TestPrinted(t *testing.T) {
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{`printf 'a\nb'`, "a\nb", true},
		{`printf '%s-%s\n' a b c`, "a-b\nc-\n", true},
		{`printf '100%%\n'`, "100%\n", true},
		{`printf '\0101\102'`, "AB", true},
		{`echo -ne 'x\ty\c rest'`, "x\ty", true},
		{`echo a b`, "a b", true},
		{`printf -v x 'a'`, "", false},
		{`cat a`, "", false},
	} {
		segs, err := parse(tc.line, POSIX, 0)
		if err != nil || len(segs) != 1 {
			t.Fatalf("parse(%q): %v", tc.line, err)
		}
		if got, ok := printed(segs[0].words); got != tc.want || ok != tc.ok {
			t.Errorf("printed(%q) = %q %v, want %q %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}
