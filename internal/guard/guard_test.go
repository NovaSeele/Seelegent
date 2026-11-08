package guard

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

func encodePS(s string) string {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[2*i:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestCheckAllShells(t *testing.T) {
	deny := []string{
		// The old list.
		"git stash", "git stash push -m x", "git stash pop", "git stash drop", "git reset --hard",
		"git reset HEAD~1", "git restore .", "git restore --staged a.go", "git clean -fd", "git clean -f",
		"git checkout main", "git checkout -- a.go", "git checkout .", "git switch main", "git revert HEAD",
		"git rebase main", "git merge feature", "git pull", "git push", "git push --force origin main",
		"git cherry-pick abc", "git am x.patch", "git rm a.go", "git rm --cached a.go", "git filter-branch",
		"git update-ref -d refs/heads/x", "git prune", "git branch -D feature", "git branch -d x",
		"git branch -m a b", "git branch --delete x", "git worktree remove ../wt", "git worktree prune",
		// Added by the rewrite (bugs.md D2).
		"git commit -a -m x", "git commit -am x", "git commit --all -m x", "git commit --amend",
		"git commit --amend --no-edit", "git branch -f main HEAD~1", "git tag -d v1", "git tag --delete v1",
		"git checkout -f -b x", "git checkout -fb x", "git apply x.patch", "git mv a b", "git reflog expire --all",
		"git update-index --assume-unchanged a", "git read-tree --reset -u HEAD", "git checkout-index -f -a",
		"git -c alias.x='!git reset --hard' x", "git -c core.hooksPath=/tmp/h commit -m x",
		"git -c core.fsmonitor=evil status", "git --config-env=alias.x=ENV x",
		"git config alias.co checkout", "git config --global alias.x '!rm -rf .'", "git config core.hooksPath h",
		"git config set alias.x reset", "git submodule deinit -f x", "git submodule update --force",
		"git submodule foreach 'git reset --hard'", "git sparse-checkout set src", "git bisect start",
		"git symbolic-ref HEAD refs/heads/x", "git worktree move a b",
		// Names and paths of git.
		"git.exe reset --hard", `"C:\Program Files\Git\cmd\git.exe" stash`, "/usr/bin/git stash",
		"GIT stash", "git RESET --hard", "git-reset --hard",
		// Global options before the subcommand.
		"git -C /repo stash", "git --git-dir=.git --work-tree=. reset --hard", "git --no-pager stash",
		"git -c color.ui=never push",
		// Chains and pipes.
		"echo hi && git stash", "git status; git stash", "true || git reset --hard", "ls | git stash",
		"git status & git stash", "echo a\ngit stash",
		// Wrappers (bugs.md D2).
		"env git stash", "env -i FOO=1 git stash", "FOO=1 git stash", "xargs git stash", "echo x | xargs git rm",
		"time git stash", "nohup git stash &", "command git stash", "exec git stash", "sudo git reset --hard",
		"sudo -u bob git stash", "timeout 10 git stash", "nice -n 5 git stash", "find . -name x -exec git rm {} ;",
		"find . -exec git checkout -- {} +", "watch -n 2 git stash", "stdbuf -o0 git stash",
		"env -S 'git stash'",
		// Interpreters.
		"bash -c 'git stash'", "sh -c \"git reset --hard\"", "bash -lc 'cd x && git stash'",
		"cmd /c git stash", "cmd.exe /C \"git reset --hard\"", "powershell -Command git stash",
		"pwsh -c 'git reset --hard'", "powershell -NoProfile -EncodedCommand " + encodePS("git stash"),
		"powershell git stash", "node -e \"require('child_process').execSync('git stash')\"",
		"node -e \"require('child_process').spawnSync('git', ['stash'])\"",
		"python -c \"import os; os.system('git reset --hard')\"",
		"python3 -c \"import subprocess; subprocess.run(['git','checkout','--','.'])\"",
		"perl -e 'system(\"git stash\")'", "eval git stash", "eval 'git reset --hard'",
		"echo 'git stash' | sh", "echo git stash | bash", "wsl git stash", "wsl -d Ubuntu -- git reset --hard",
		// Whole-word variables where git or a git option should be.
		"git $CMD", "git ${SUB} --hard", "x=stash; git $x", "git commit $FLAGS", "git branch \"$opt\" main",
	}
	for _, line := range deny {
		r := Check(line)
		if !r.Deny {
			t.Errorf("Check(%q) allowed, want deny", line)
			continue
		}
		if !strings.HasPrefix(r.Reason, Marker) {
			t.Errorf("Check(%q) reason %q does not start with the marker", line, r.Reason)
		}
	}
	allowLines := []string{
		"", "git status", "git log --oneline -5", "git diff", "git diff --cached", "git show HEAD",
		"git add a.go", "git add -A", "git commit -m 'fix: x'", "git commit -m \"fix: x\" a.go",
		"git commit -F msg.txt", "git stash list", "git stash show -p", "git clean -n", "git clean -nd",
		"git clean --dry-run", "git checkout -b feature", "git checkout -b feature origin/main",
		"git checkout -q -b x", "git branch", "git branch feature", "git branch -a", "git branch --list",
		"git branch -c a b", "git tag v1", "git tag -a v1 -m 'release'", "git fetch", "git fetch origin",
		"git worktree add ../wt -b x", "git worktree list", "git reflog", "git reflog show",
		"git config user.name", "git config --get alias.co", "git config --list", "git config user.email a@b",
		"git -c core.quotepath=off status", "git --version", "git --help", "git reset -h", "git push --help",
		"git apply --check x.patch", "git apply --stat x.patch", "git rev-parse HEAD", "git blame a.go",
		"git grep stash", "git ls-files", "git init", "git clone https://x/y", "git remote -v",
		"git submodule status", "git sparse-checkout list", "git bisect log", "git symbolic-ref HEAD",
		"git describe --tags", "git log -- reset.go",
		// Not git, even when git words appear as data.
		"echo git stash", "grep -r reset .", "rg stash src", "go test ./...", "npm run build", "ls -la",
		"cat .git/config", "echo 'hello world'", "echo \"done\" > out.txt 2>&1", "make test",
		"node -e \"console.log('stash')\"", "python -c 'print(1)'", "command -v git",
		// Commit messages built with substitutions, as Claude Code writes them.
		"git commit -m \"$(cat <<'EOF'\nfix: don't reset (really)\n\nCo-Authored-By: x\nEOF\n)\"",
		"git commit -m \"$(printf 'a\\nb')\"",
		"cd /repo && git status && git diff --stat",
		"git log --format='%H %s' | head -5",
		"for f in *.go; do gofmt -l \"$f\"; done",
		// Variables in harmless places.
		"git add \"$FILE\"", "git log $RANGE", "git checkout -b \"$BRANCH\"", "git commit -m \"$MSG\"",
		"git diff \"${BASE}\"..HEAD",
	}
	for _, line := range allowLines {
		if r := Check(line); r.Deny {
			t.Errorf("Check(%q) denied (%s), want allow", line, r.Reason)
		}
	}
}

func TestCheckSubstitutions(t *testing.T) {
	// bugs.md D2: commands hidden in substitutions, subshells and groups.
	deny := map[Shell][]string{
		POSIX: {
			"echo $(git reset --hard)", "(git stash)", "{ git stash; }", "echo \"$(git stash)\"",
			"echo `git stash`", "echo \"`git reset --hard`\"", "x=$(git stash pop)", "diff <(git stash) x",
			"cat <<EOF\n$(git stash)\nEOF", "echo ${X:-$(git stash)}", "if true; then git stash; fi",
			"! git stash", "a && (cd x; git reset --hard)",
		},
		PowerShell: {
			"echo $(git reset --hard)", "(git stash)", "& git stash", "& 'git' stash", "& \"git.exe\" reset --hard",
			"Write-Output \"$(git stash)\"", "git `stash", "g`it stash", "Invoke-Expression 'git stash'",
			"iex \"git reset --hard\"", "Start-Process git -ArgumentList 'stash'", "$r = git stash",
			"Invoke-Command -ScriptBlock { git stash }", "if ($true) { git reset --hard }",
			"1..3 | ForEach-Object { git stash }", "git commit -m @'\nx\n'@ --amend",
		},
		Cmd: {
			"g^it stash", "(git stash)", "git stash & echo x", "start /b git stash", "start \"t\" git stash",
			"call git stash", "@git stash", "cmd /c \"git reset --hard\"", "git %X%",
		},
	}
	for sh, lines := range deny {
		for _, line := range lines {
			if r := (Checker{}).Command(line, sh); !r.Deny {
				t.Errorf("shell %d: Command(%q) allowed, want deny", sh, line)
			}
		}
	}
	allowed := map[Shell][]string{
		POSIX: {
			"git commit -m 'it`s (fine)'", "echo 'git stash'", "echo \"git stash\"", "printf '%s' stash",
			"cat <<'EOF'\ngit reset --hard\nEOF", "git commit -m 'git stash is bad'",
		},
		PowerShell: {
			"git commit -m 'don''t stash'", "Write-Output 'git stash'", "git log --oneline | Select-Object -First 5",
			"git status 2>&1 | Out-String", "git commit -m @'\nfix: x\n\nbody (with parens)\n'@",
			"Get-ChildItem | Where-Object { $_.Name -like '*.go' }",
		},
		Cmd: {
			"git status", "echo git stash", "git commit -m \"fix it\"", "git log > out.txt 2>&1",
		},
	}
	for sh, lines := range allowed {
		for _, line := range lines {
			if r := (Checker{}).Command(line, sh); r.Deny {
				t.Errorf("shell %d: Command(%q) denied (%s), want allow", sh, line, r.Reason)
			}
		}
	}
}

func TestUnreadableFailsClosed(t *testing.T) {
	for _, sh := range []Shell{POSIX, PowerShell} {
		if r := (Checker{}).Command("git commit -m 'unclosed", sh); !r.Deny {
			t.Errorf("shell %d: an unclosed quote in a git line was allowed", sh)
		}
	}
	// A line no dialect can read is refused when it mentions git, allowed otherwise.
	if r := Check("echo $(git status"); !r.Deny {
		t.Error("unreadable git line allowed")
	}
	if r := (Checker{}).Command("echo 'unclosed", POSIX); r.Deny {
		t.Error("unreadable line without git denied")
	}
	if r := (Checker{}).Command("bash -c 'git status \"", POSIX); !r.Deny {
		t.Error("unreadable nested script allowed")
	}
}

func TestBacktickIsPowerShellEscape(t *testing.T) {
	// bugs.md D16: in PowerShell the backtick escapes; in POSIX it substitutes.
	if r := (Checker{}).Command("git `stash", PowerShell); !r.Deny {
		t.Error("PowerShell git `stash allowed")
	}
	if r := (Checker{}).Command("echo `git stash`", POSIX); !r.Deny {
		t.Error("POSIX `git stash` substitution allowed")
	}
	if r := (Checker{}).Command("git commit -m \"a `\"quoted`\" b\"", PowerShell); r.Deny {
		t.Errorf("PowerShell escaped quotes denied: %s", r.Reason)
	}
}

func TestGitArgs(t *testing.T) {
	c := Checker{}
	cases := []struct {
		args []string
		deny bool
	}{
		{[]string{"status"}, false},
		{[]string{"stash"}, true},
		{[]string{"stash", "list"}, false},
		{[]string{"-C", "x", "reset", "--hard"}, true},
		{[]string{"commit", "-m", "-a"}, false}, // -a is the message here
		{[]string{"commit", "-ma"}, false},      // the message is "a"
		{[]string{"commit", "-am", "x"}, true},
		{[]string{"checkout", "-b", "x"}, false},
		{[]string{"checkout", "-b", "x", "y", "z"}, true},
		{[]string{}, false},
		{[]string{"--version"}, false},
	}
	for _, tc := range cases {
		if r := c.Git(tc.args); r.Deny != tc.deny {
			t.Errorf("Git(%q) deny = %v, want %v", tc.args, r.Deny, tc.deny)
		}
	}
}

// TestStdinScripts: a here-document, here-string or pipe that feeds a shell its script is read as that script; fed
// to any other command it is data.
func TestStdinScripts(t *testing.T) {
	deny := []string{
		"bash <<'EOF'\ngit reset --hard\nEOF", "bash <<EOF\ngit stash\nEOF", "cat <<'EOF' | sh\ngit stash\nEOF",
		"bash -s <<EOF\ngit stash\nEOF", "bash -s -- a b <<'EOF'\ngit stash\nEOF", "sh -s <<< 'git stash'",
		"bash <<< 'git stash'", "bash -x <<-EOF\n\tgit stash\n\tEOF",
		"cat <<'EOF' | tr -d '\\r' | bash\ngit stash\nEOF", "sudo bash <<'EOF'\ngit stash\nEOF",
		"env FOO=1 bash -e <<'EOF'\necho ok\ngit checkout -- .\nEOF", "bash /dev/stdin <<< 'git stash'",
		"source /dev/stdin <<< 'git stash'", ". /dev/stdin <<'EOF'\ngit stash\nEOF",
		"(bash <<'EOF'\ngit stash\nEOF\n)", "echo start && bash <<'EOF' && echo done\ncd x\ngit stash\nEOF",
		"bash <<'A' <<'B'\necho a\nA\ngit stash\nB", "bash - <<'EOF'\ngit stash\nEOF",
		"cmd <<'EOF'\ngit stash\nEOF", "pwsh -Command - <<'EOF'\ngit stash\nEOF", "pwsh -NoProfile <<< 'git stash'",
		"python3 <<'EOF'\nimport os\nos.system('git stash')\nEOF",
		"python3 - <<'EOF'\nimport subprocess\nsubprocess.run(['git', 'reset', '--hard'])\nEOF",
		"node <<< \"require('child_process').execSync('git stash')\"",
	}
	for _, line := range deny {
		if r := (Checker{}).Command(line, POSIX); !r.Deny {
			t.Errorf("Command(%q) allowed, want deny", line)
		}
	}
	allowLines := []string{
		// Claude Code's commit messages: the heredoc is fed to cat, and its text is data.
		"git commit -m \"$(cat <<'EOF'\nfix: x\n\ngit stash and git reset --hard are refused now\nEOF\n)\"",
		"cat <<'EOF'\ngit reset --hard\nEOF", "cat <<'EOF' > notes.md\ngit stash\nEOF",
		"tee out.txt <<'EOF'\ngit reset --hard\nEOF", "grep -c stash <<< 'git stash'",
		"bash script.sh <<'EOF'\ngit stash\nEOF", "bash -c 'read x; echo $x' <<< 'git stash'",
		"bash <<'EOF'\ngo test ./...\ngit status\nEOF", "sh -s <<< 'git log --oneline -3'",
		"python3 - <<'EOF'\nprint('ok')\nEOF", "python3 script.py <<'EOF'\ngit stash\nEOF",
		"cat <<'EOF' | sh\necho hi\nEOF", "git log --oneline | head -5",
	}
	for _, line := range allowLines {
		if r := (Checker{}).Command(line, POSIX); r.Deny {
			t.Errorf("Command(%q) denied (%s), want allow", line, r.Reason)
		}
	}
}

// TestMoreWrappers: wrappers and dialect forms the reviewer found passing.
func TestMoreWrappers(t *testing.T) {
	deny := map[Shell][]string{
		POSIX: {
			"chrt 10 git stash", "chrt -f 10 git reset --hard", "chrt -T 100 -D 200 -d 0 git stash",
			"taskset 0x1 git stash", "taskset -c 0,1 git stash", "unshare git stash", "unshare -r -w /x git stash",
			"parallel git {} ::: stash", "parallel 'git {}' ::: stash", "parallel -j 2 git ::: stash",
			"parallel ::: 'git stash' 'echo hi'", "echo stash | parallel git", "script -c 'git stash' /dev/null",
			"script -qc 'git reset --hard' /dev/null", "script --command='git stash'", "script -q /dev/null git stash",
			"su -c 'git stash' bob", "su bob -c 'git stash'", "su - bob -c 'git reset --hard'", "su -lc 'git stash'",
			"sudo su -c 'git stash'", "runuser -u bob -- git stash", "runuser -u bob git stash",
			"su bob <<'EOF'\ngit stash\nEOF",
		},
		PowerShell: {
			"Start-Process git 'reset','--hard'", "Start-Process git reset,--hard", "saps git 'stash'",
			"Start-Process -FilePath git 'reset --hard'", "[Diagnostics.Process]::Start('git','stash')",
			"[System.Diagnostics.Process]::Start(\"git\", \"reset --hard\")",
			"$p = [Diagnostics.Process]::Start('git', 'stash')",
			"([Diagnostics.Process]::Start('git','stash')).WaitForExit()",
		},
		Cmd: {
			"if exist a git stash", "if not exist a git reset --hard", "if /i \"%x%\"==\"y\" git stash",
			"if errorlevel 1 git stash", "if defined X git stash", "if a == b git stash", "if 1 EQU 1 git stash",
			"if exist a if exist b git stash",
		},
	}
	for sh, lines := range deny {
		for _, line := range lines {
			if r := (Checker{}).Command(line, sh); !r.Deny {
				t.Errorf("shell %d: Command(%q) allowed, want deny", sh, line)
			}
		}
	}
	allowed := map[Shell][]string{
		POSIX: {
			"chrt 10 make", "taskset -c 0 go test ./...", "unshare -r id", "parallel gzip ::: a.log b.log",
			"parallel git log -1 {} ::: a b", "script -c 'git status' /dev/null", "script out.txt",
			"su -c 'id' bob", "runuser -u bob -- git status",
		},
		PowerShell: {
			"Start-Process git 'status'", "Start-Process notepad a.txt", "[Diagnostics.Process]::Start('git','status')",
			"Start-Process git -ArgumentList \"commit -m $msg\"",
		},
		Cmd: {"if exist a git status", "if not exist out mkdir out", "if \"%x%\"==\"y\" echo git stash"},
	}
	for sh, lines := range allowed {
		for _, line := range lines {
			if r := (Checker{}).Command(line, sh); r.Deny {
				t.Errorf("shell %d: Command(%q) denied (%s), want allow", sh, line, r.Reason)
			}
		}
	}
}

// TestAbbreviatedOptions: git accepts any unambiguous prefix of a long option, so a prefix of a refused option is
// refused too.
func TestAbbreviatedOptions(t *testing.T) {
	for line, deny := range map[string]bool{
		// Probed with git 2.53/2.55: each of these really acts.
		"git commit --amen --no-edit": true, "git commit --am --no-edit": true, "git branch --del merged": true,
		"git branch --d merged": true, "git checkout -b x --forc": true, "git checkout -b x --f": true,
		"git branch --forc main HEAD~1": true, "git tag --del v1": true, "git tag --d v1": true,
		"git checkout -b x --merg": true, "git checkout -b x --ove": true, "git checkout -b x --orph=y": true,
		"git commit --al -m x": true, "git commit --amend=x": true, "git branch --mov a b": true,
		"git branch --cop a b": true, "git apply --appl x.patch": true, "git apply --check --app x.patch": true,
		"git symbolic-ref --del HEAD": true, "git commit -m x --ame": true, "git commit --amen=$X": true,
		"git gc --prune=now": true, "git gc --prun=now": true, "git gc --pr=all": true,
		"git gc --prune=\"$WHEN\"": true, "git -c gc.pruneExpire=now gc": true,
		"git config gc.pruneExpire now": true,
		// Prefixes of harmless options still pass.
		"git log --onel": false, "git log --oneline": false, "git commit -m x --no-ver": false,
		"git commit -m x --allow-empt": false, "git commit -m x --autho='A <a@b>'": false,
		"git commit -m x --sig": false, "git branch --lis": false, "git branch --no-forc z": false,
		"git branch --contains HEAD": false, "git branch --sor=-committerdate": false,
		"git tag -l --sor=v:refname": false, "git tag v2 --mess=x": false, "git checkout -b x --tr origin/x": false,
		"git checkout -b x --qui": false, "git apply --check --verb x.patch": false, "git gc": false,
		"git gc --prune": false, "git gc --auto --quiet": false, "git gc --no-prune": false,
		"git commit --dry-run": false,
	} {
		if r := (Checker{}).Command(line, POSIX); r.Deny != deny {
			t.Errorf("Command(%q) deny = %v, want %v (%s)", line, r.Deny, deny, r.Reason)
		}
	}
}

func TestAliases(t *testing.T) {
	aliases := map[string]string{
		"co":      "checkout",
		"st":      "status",
		"nuke":    "!git reset --hard && git clean -fd",
		"unstage": "reset HEAD --",
		"loop":    "loop",
		"lg":      "log --oneline --graph",
	}
	c := Checker{Alias: func(name string) (string, bool) {
		v, ok := aliases[name]
		return v, ok
	}}
	for line, deny := range map[string]bool{
		"git co main": true, "git co -b x": false, "git st": false, "git nuke": true, "git unstage a.go": true,
		"git loop": true, "git lg": false, "git unknown-tool": false,
	} {
		if r := c.Command(line); r.Deny != deny {
			t.Errorf("Command(%q) deny = %v, want %v", line, r.Deny, deny)
		}
	}
	// Without a resolver an unknown name is allowed (git fails on it anyway).
	if r := Check("git co main"); r.Deny {
		t.Error("git co main denied without an alias resolver")
	}
}

func TestDepthLimit(t *testing.T) {
	line := strings.Repeat("$(", 40) + "git stash" + strings.Repeat(")", 40)
	if r := (Checker{}).Command(line, POSIX); !r.Deny {
		t.Error("deep nesting allowed")
	}
	line = strings.Repeat("bash -c '", 1) + "git status'"
	if r := Check(line); r.Deny {
		t.Errorf("bash -c 'git status' denied: %s", r.Reason)
	}
}

func FuzzCheck(f *testing.F) {
	for _, s := range []string{"git stash", "echo $(git reset --hard)", "a | b && c", "@'\nx\n'@", "`", "\"", "$(",
		"printf 'git %s\\0101\\x7\\n%' a | bash", "echo -e 'a\\c' | sh", "cat <<'EOF' | tee x\ngit stash\nEOF",
		"perl -e 'qx{git'", "ruby -e '%x(", "$env:GIT_CONFIG_COUNT=1", "${env:", "env 'A=1' git x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		r := Check(line)
		if r.Deny && !strings.HasPrefix(r.Reason, Marker) {
			t.Fatalf("reason without marker: %q", r.Reason)
		}
	})
}
