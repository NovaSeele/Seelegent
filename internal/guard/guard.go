// Package guard is Seelegent's git guard: it reads a worker's shell command (or a git argument list) and refuses
// the git operations that can discard, move or sweep up uncommitted work that belongs to other people in the same
// folder (stash, reset, restore, checkout of paths, clean, commit -a/--amend, push, ...).
//
// The guard is pure: it never runs anything. The pre-tool hooks of the worker CLIs (`seelegent __guard`) and the
// git shim (`seelegent __git`) call it; the only outside lookup, a git alias, goes through Checker.Alias. It reads
// POSIX shells, PowerShell and cmd.exe, follows substitutions, subshells, blocks, wrappers (env, xargs, sudo,
// nohup, time, timeout, find -exec, ...) and interpreters (bash -c, cmd /c, powershell -c and -EncodedCommand,
// node -e, python -c, eval, Invoke-Expression), and fails closed: a line it cannot read that mentions git is
// refused. What it cannot see (a command stored in a variable, a script file, a build tool) is listed in
// docs/safety.md.
package guard

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"
)

// Marker starts every refusal; the backend parsers look for it to record blocked commands.
const Marker = "Blocked by Seelegent"

// reasonText is the refusal of a risky git command (the old guard's wording, which workers already know).
const reasonText = Marker + ": this git command can discard or move uncommitted work that belongs to other people " +
	"in this folder. Files with changes you did not make are expected: leave them. Undo your own edit with your " +
	"edit tool, and do not rewrite a file from git either (git show HEAD:path > path throws away the same " +
	"lines). If you truly need this, say so under ## Blocked."

// unreadableText is the refusal of a line the guard could not read.
const unreadableText = Marker + ": this command mentions git but could not be read safely (an unclosed quote, " +
	"a variable used as a git subcommand or option, or nesting that is too deep). Write the git command plainly, " +
	"without variables or substitutions in the git part. If you truly need it, say so under ## Blocked."

// Result is the guard's decision.
type Result struct {
	// Deny is true when the command must not run.
	Deny bool
	// Reason is the text shown to the worker (it starts with Marker).
	Reason string
	// Git is the refused git command, as words (empty for an unreadable line).
	Git string
	// bad marks an allowed result in which a part could not be read in the dialect tried: the line is refused
	// only when no dialect could read it.
	bad bool
}

// merge keeps the "could not read" mark of r in an allowed result.
func (r Result) merge(o Result) Result {
	if o.bad {
		r.bad = true
	}
	return r
}

// allow is the decision to let a command run.
var allow = Result{}

func denyGit(args []word) Result {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "git")
	for _, a := range args {
		parts = append(parts, a.text)
	}
	cmd := strings.Join(parts, " ")
	if len(cmd) > 120 {
		cmd = cmd[:117] + "..."
	}
	return Result{Deny: true, Reason: reasonText + " (refused: " + cmd + ")", Git: cmd}
}

func denyUnreadable() Result { return Result{Deny: true, Reason: unreadableText} }

// Checker checks commands. The zero value works; Alias adds git alias resolution.
type Checker struct {
	// Alias returns the value of git alias name (alias.<name>), and whether it exists. nil: aliases are not
	// resolved, and an unknown git subcommand is allowed.
	Alias func(name string) (value string, ok bool)
	// HeredocAsCommands keeps the lines of a POSIX here-document in the PowerShell and cmd readings of a line read in
	// every dialect, where they are commands. Set for agy on Windows: its run_command runs PowerShell, where
	// cat <<'EOF' starts no here-document and the next lines run. Unset, those lines are read as POSIX only.
	HeredocAsCommands bool
}

// Check reads a command line in every dialect and refuses it when one of them runs a risky git command.
func Check(line string) Result { return Checker{}.Command(line) }

// Command reads line in the given shells. With none given the shell is unknown: the line is read in every dialect,
// and a POSIX here-document's lines only as what POSIX makes of them (the script of bash <<'EOF', the data of
// cat <<'EOF') unless HeredocAsCommands is set.
func (c Checker) Command(line string, shells ...Shell) Result {
	unknown := len(shells) == 0
	if unknown {
		shells = allShells
	}
	other := line // the line as the PowerShell and cmd readings see it
	if unknown && !c.HeredocAsCommands {
		other = withoutHeredocs(line)
	}
	read := false
	for _, sh := range shells {
		text := line
		if sh != POSIX {
			text = other
		}
		r := c.line(text, sh, 0)
		if r.Deny {
			return r
		}
		read = read || !r.bad
	}
	if !read && mentionsGit.MatchString(line) {
		return denyUnreadable()
	}
	return allow
}

// Git checks the arguments of a git command (without "git" itself), as the git shim receives them.
func (c Checker) Git(args []string) Result {
	ws := make([]word, len(args))
	for i, a := range args {
		ws[i] = word{text: a}
	}
	return c.git(ws, 0)
}

// withoutHeredocs blanks the here-document bodies (and their closing delimiters) that the POSIX reading of line
// finds; the line is returned unchanged when POSIX cannot read it.
func withoutHeredocs(line string) string {
	if !strings.Contains(line, "<<") {
		return line
	}
	st, err := parseState(line, POSIX, 0)
	if err != nil || len(st.heredocs) == 0 {
		return line
	}
	b := []byte(line)
	for _, span := range st.heredocs {
		for i := span[0]; i < span[1] && i < len(b); i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// mentionsGit is the fail-closed test for a line that could not be read.
var mentionsGit = regexp.MustCompile(`(?i)git`)

func (c Checker) line(line string, sh Shell, depth int) Result {
	if depth > maxDepth {
		return denyUnreadable()
	}
	if strings.TrimSpace(line) == "" {
		return allow
	}
	segs, err := parse(line, sh, depth)
	if errors.Is(err, errDepth) {
		return denyUnreadable()
	}
	res := Result{bad: err != nil}
	for _, s := range segs {
		r := c.segment(s, sh, depth)
		if r.Deny {
			return r
		}
		res = res.merge(r)
	}
	return res
}

// isAbbrev reports whether t abbreviates the PowerShell option full (a prefix of it, as pwsh accepts).
func isAbbrev(t, full string) bool { return strings.HasPrefix(full, t) }

// cmdName is a command word reduced to its name: the last path element, lower case, without .exe/.cmd/.bat/.com.
func cmdName(w word) string {
	t := strings.Trim(w.text, `"'`)
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
	}
	t = strings.ToLower(t)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".com"} {
		t = strings.TrimSuffix(t, ext)
	}
	return t
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\+?=`)

// dotnetStart matches a .NET call that starts a process from PowerShell ([Diagnostics.Process]::Start(...),
// [Diagnostics.ProcessStartInfo]::new(...)).
var dotnetStart = regexp.MustCompile(`(?i)process(startinfo)?\]::(start|new)\b`)

// keywords are shell words that come before a command without changing it.
var keywords = map[string]bool{
	"!": true, "{": true, "}": true, "if": true, "then": true, "else": true, "elif": true, "do": true,
	"while": true, "until": true, "time": true, "&": true, ".": false,
}

// wrapper describes a command that runs another command given in its arguments.
type wrapper struct {
	// valued lists options that take the next word as their value.
	valued map[string]bool
	// positional is the number of arguments before the command (timeout's duration, flock's file).
	positional int
	// assigns: NAME=value words may come before the command (env).
	assigns bool
	// stdinArgs: the command gets more arguments from standard input (xargs).
	stdinArgs bool
}

func set(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

var wrappers = map[string]wrapper{
	"env":        {valued: set("-u", "--unset", "-C", "--chdir"), assigns: true},
	"sudo":       {valued: set("-u", "-g", "-h", "-p", "-C", "-D", "-r", "-t", "-U", "-T", "--user", "--group", "--host", "--prompt", "--chdir")},
	"doas":       {valued: set("-u", "-C")},
	"nohup":      {},
	"nice":       {valued: set("-n", "--adjustment")},
	"ionice":     {valued: set("-c", "-n", "-p", "--class", "--classdata")},
	"timeout":    {valued: set("-s", "-k", "--signal", "--kill-after"), positional: 1},
	"stdbuf":     {valued: set("-i", "-o", "-e")},
	"setsid":     {},
	"command":    {},
	"builtin":    {},
	"exec":       {valued: set("-a")},
	"time":       {valued: set("-f", "-o", "--format", "--output")},
	"xargs":      {valued: set("-I", "-L", "-n", "-P", "-s", "-d", "-E", "-a", "--arg-file", "--delimiter", "--max-args", "--max-procs", "--max-lines", "--max-chars", "--replace", "--eof"), stdinArgs: true},
	"watch":      {valued: set("-n", "--interval", "-d", "-x")},
	"strace":     {valued: set("-o", "-e", "-p", "-s", "-u")},
	"ltrace":     {valued: set("-o", "-e", "-p", "-s", "-u")},
	"flock":      {valued: set("-w", "--timeout", "-E", "--conflict-exit-code"), positional: 1},
	"chronic":    {},
	"unbuffer":   {},
	"caffeinate": {valued: set("-t", "-w")},
	"call":       {},
	"wsl":        {valued: set("-d", "--distribution", "-u", "--user", "--cd")},
	"runas":      {},
	"busybox":    {},
	"winpty":     {},
	"chrt":       {valued: set("-T", "-P", "-D", "--sched-runtime", "--sched-period", "--sched-deadline"), positional: 1},
	"taskset":    {positional: 1},
	"unshare": {valued: set("-S", "-G", "-R", "-w", "--setuid", "--setgid", "--root", "--wd", "--propagation",
		"--map-user", "--map-group", "--map-users", "--map-groups")},
}

// segment checks one simple command.
func (c Checker) segment(s *segment, sh Shell, depth int) Result {
	ws := s.words
	// PowerShell's $x = ...: what follows the = is the command. POSIX NAME=value words are left to command, which
	// checks them after any keyword (then FOO=1 git ...).
	for sh == PowerShell && len(ws) > 1 && strings.HasPrefix(ws[0].text, "$") &&
		(ws[1].text == "=" || ws[1].text == "+=") && !ws[1].quoted {
		if setsConfigEnv(ws, sh) { // $env:GIT_CONFIG_COUNT = 1
			return denyConfigEnv()
		}
		ws = ws[2:]
	}
	return c.command(ws, s, sh, depth)
}

// command checks a command given as words (after assignments).
func (c Checker) command(ws []word, s *segment, sh Shell, depth int) Result {
	if depth > maxDepth {
		return denyUnreadable()
	}
	for sh == Cmd && len(ws) > 0 && !ws[0].quoted && strings.EqualFold(ws[0].text, "if") {
		ws = cmdIf(ws[1:])
	}
	for len(ws) > 0 && !ws[0].quoted && keywords[strings.ToLower(ws[0].text)] {
		kw := strings.ToLower(ws[0].text)
		ws = ws[1:]
		if len(ws) > 0 && ws[0].text == "-p" {
			ws = ws[1:]
		}
		if sh == PowerShell && setHas(kw, "if", "while", "until") && len(ws) > 0 && strings.HasPrefix(ws[0].text, "(") {
			// if (...) { ... }: the condition's commands were parsed already, and it is not a program to run.
			ws = ws[1:]
		}
	}
	// Every path to a command passes here (after a keyword, cmd's if and do, a wrapper): a command that sets the git
	// configuration variables is refused, then NAME=value words before the command (a segment's own, and those a
	// wrapper passes on, sudo NAME=x git) are skipped.
	if setsConfigEnv(ws, sh) {
		return denyConfigEnv()
	}
	for len(ws) > 0 && ws[0].assign {
		ws = ws[1:]
	}
	if len(ws) == 0 {
		return allow
	}
	head := ws[0]
	args := ws[1:]
	if sh == PowerShell && dotnetStart.MatchString(head.text) {
		// [Diagnostics.Process]::Start('git', 'reset --hard'): the second argument is a command line of its own.
		text := joinWords(ws)
		lits := literals(text)
		for i, l := range lits {
			if cmdName(word{text: l}) == "git" && i+1 < len(lits) {
				if r := c.line("git "+lits[i+1], POSIX, depth+1); r.Deny {
					return r
				}
			}
		}
		return c.code(text, "", depth)
	}
	if head.dynamic {
		// An unknown program: if its arguments read as a risky git command, the program may well be git.
		if r := c.git(args, depth+1); r.Deny {
			return r
		}
		return allow
	}
	name := cmdName(head)
	if name == "git" {
		return c.git(args, depth+1)
	}
	if strings.HasPrefix(name, "git-") && builtins[name[4:]] {
		return c.git(append([]word{{text: name[4:]}}, args...), depth+1)
	}
	if wr, ok := wrappers[name]; ok {
		if name == "command" && len(args) > 0 && (args[0].text == "-v" || args[0].text == "-V") {
			return allow
		}
		rest, ok := unwrap(wr, args)
		for _, a := range args[:len(args)-len(rest)] {
			if configAssign(a.text) { // env GIT_CONFIG_PARAMETERS=... git x
				return denyConfigEnv()
			}
		}
		if !ok {
			return allow
		}
		if wr.stdinArgs {
			rest = append(rest, word{text: "$STDIN", dynamic: true})
		}
		if name == "env" {
			if r := c.envSplit(args, sh, depth); r.Deny {
				return r
			}
		}
		return c.command(rest, s, sh, depth+1)
	}
	switch name {
	case "bash", "sh", "zsh", "dash", "ksh", "mksh", "ash", "fish", "yash", "tcsh", "csh":
		return c.posixShell(args, s, depth)
	case "source", ".":
		if len(args) > 0 && isStdinFile(args[0].text) {
			return c.stdinScript(s, POSIX, depth)
		}
	case "cmd":
		return c.cmdShell(args, s, depth)
	case "powershell", "pwsh", "powershell_ise":
		return c.pwshShell(args, s, depth)
	case "node", "nodejs", "deno", "bun", "python", "python3", "python2", "py", "pythonw", "perl", "ruby", "php",
		"osascript":
		return c.interpreter(name, args, s, depth)
	case "eval":
		return c.line(joinWords(args), POSIX, depth+1)
	case "iex", "invoke-expression":
		return c.line(joinWords(stripNamed(args, "-command")), PowerShell, depth+1)
	case "invoke-command", "icm", "start-job", "sajb":
		// Their -ScriptBlock {...} was parsed as nested commands already.
		return allow
	case "find":
		return c.findExec(args, s, sh, depth)
	case "start-process", "saps", "start":
		return c.startProcess(args, sh, depth)
	case "parallel":
		return c.parallel(args, depth)
	case "script":
		return c.scriptCommand(args, sh, depth)
	case "su", "runuser":
		return c.su(name, args, s, sh, depth)
	}
	return allow
}

// parallelOpts are GNU parallel's options that take the next word as their value.
var parallelOpts = wrapper{valued: set("-j", "--jobs", "-P", "--max-procs", "-S", "--sshlogin", "-a", "--arg-file",
	"-d", "--delimiter", "-I", "--colsep", "-C", "-N", "-n", "--max-args", "-L", "--max-lines", "--joblog",
	"--results", "--res", "--tmpdir", "--workdir", "--wd", "-E", "--eof", "--timeout", "--delay", "--tag-string",
	"--basefile", "--bf", "--transferfile", "--tf", "--return", "--env", "--memfree", "--load", "--nice")}

// placeholder matches GNU parallel's replacement strings ({}, {.}, {/}, {1}, ...).
var placeholder = regexp.MustCompile(`\{[^{}\s]*\}`)

// parallel checks GNU parallel: its command, with every replacement string (or the appended argument) read as a
// value only known when it runs, and, without a command, each input after ::: as a command line.
func (c Checker) parallel(args []word, depth int) Result {
	rest, ok := unwrap(parallelOpts, args)
	if !ok {
		return allow
	}
	cmd, inputs := rest, []word(nil)
	for i, w := range rest {
		if strings.HasPrefix(w.text, ":::") {
			cmd, inputs = rest[:i], rest[i+1:]
			break
		}
	}
	if len(cmd) == 0 {
		for _, w := range inputs {
			if strings.HasPrefix(w.text, ":::") {
				continue
			}
			if r := c.line(w.text, POSIX, depth+1); r.Deny {
				return r
			}
		}
		return allow
	}
	parts := make([]string, 0, len(cmd)+1)
	replaced := false
	for _, w := range cmd {
		t := w.text
		if placeholder.MatchString(t) {
			t = placeholder.ReplaceAllLiteralString(t, "$PARALLEL_ARG")
			replaced = true
		}
		parts = append(parts, t)
	}
	if !replaced {
		parts = append(parts, "$PARALLEL_ARG")
	}
	return c.line(strings.Join(parts, " "), POSIX, depth+1)
}

// scriptValued are the options of script (util-linux) that take the next word as their value.
var scriptValued = set("-T", "-B", "-I", "-O", "-E", "--log-in", "--log-out", "--log-io", "--log-timing",
	"--logging-format", "--echo", "--output-limit")

// scriptCommand checks script -c 'command' [file] (util-linux) and script [file [command ...]] (BSD, macOS).
func (c Checker) scriptCommand(args []word, sh Shell, depth int) Result {
	var pos []word
	for i := 0; i < len(args); i++ {
		t := args[i].text
		switch {
		case t == "-c" || t == "--command":
			if i+1 >= len(args) {
				return allow
			}
			return c.line(args[i+1].text, POSIX, depth+1)
		case strings.HasPrefix(t, "--command="):
			return c.line(strings.TrimPrefix(t, "--command="), POSIX, depth+1)
		case scriptValued[t]:
			i++
		case strings.HasPrefix(t, "--"):
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if j := strings.IndexByte(t, 'c'); j > 0 { // -qc 'command'
				value := t[j+1:]
				if value == "" && i+1 < len(args) {
					value = args[i+1].text
				}
				return c.line(value, POSIX, depth+1)
			}
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) > 1 {
		return c.command(pos[1:], nil, sh, depth+1)
	}
	return allow
}

// suValued are the options of su and runuser that take the next word as their value.
var suValued = set("-s", "--shell", "-g", "--group", "-G", "--supp-group", "-w", "--whitelist-environment")

// su checks su/runuser -c 'command', runuser -u user command, and the script a shell started by su reads from
// standard input.
func (c Checker) su(name string, args []word, s *segment, sh Shell, depth int) Result {
	user := false
	for i := 0; i < len(args); i++ {
		t := args[i].text
		switch {
		case t == "-c" || t == "--command" || t == "--session-command":
			if i+1 >= len(args) {
				return allow
			}
			return c.line(args[i+1].text, POSIX, depth+1)
		case strings.HasPrefix(t, "--command=") || strings.HasPrefix(t, "--session-command="):
			return c.line(t[strings.IndexByte(t, '=')+1:], POSIX, depth+1)
		case name == "runuser" && (t == "-u" || t == "--user"):
			user = true
			i++
		case suValued[t]:
			i++
		case user && t == "--":
			return c.command(args[i+1:], s, sh, depth+1)
		case strings.HasPrefix(t, "--") || t == "-":
		case strings.HasPrefix(t, "-") && len(t) > 1:
			if j := strings.IndexByte(t, 'c'); j > 0 { // -lc 'command'
				value := t[j+1:]
				if value == "" && i+1 < len(args) {
					value = args[i+1].text
				}
				return c.line(value, POSIX, depth+1)
			}
		case user:
			return c.command(args[i:], s, sh, depth+1)
		}
	}
	return c.stdinScript(s, POSIX, depth)
}

// cmdCompare are the comparison operators of cmd's if.
var cmdCompare = set("equ", "neq", "lss", "leq", "gtr", "geq")

// cmdIf returns the command of cmd's "if [/i] [not] <condition> command" (the words after "if"): the condition is
// exist <file>, errorlevel <n>, defined <var>, cmdextversion <n>, a==b or a <op> b.
func cmdIf(ws []word) []word {
	for len(ws) > 0 && !ws[0].quoted && setHas(strings.ToLower(ws[0].text), "/i", "not") {
		ws = ws[1:]
	}
	switch {
	case len(ws) == 0:
		return ws
	case !ws[0].quoted && setHas(strings.ToLower(ws[0].text), "exist", "errorlevel", "defined", "cmdextversion"):
		if len(ws) < 2 {
			return nil
		}
		return ws[2:]
	case strings.Contains(ws[0].text, "=="):
		if strings.HasSuffix(ws[0].text, "==") && len(ws) > 1 { // a== b
			return ws[2:]
		}
		return ws[1:]
	case len(ws) > 1 && (ws[1].text == "==" || cmdCompare[strings.ToLower(ws[1].text)]):
		if len(ws) < 3 {
			return nil
		}
		return ws[3:]
	}
	return ws
}

// unwrap skips a wrapper's own options and arguments and returns the command it runs (ok false: none).
func unwrap(wr wrapper, args []word) ([]word, bool) {
	i := 0
	for i < len(args) {
		a := args[i]
		t := a.text
		switch {
		case t == "--":
			i++
			goto positional
		case wr.assigns && assignment.MatchString(t): // env takes "NAME=value" quoted as well
			i++
			continue
		case strings.HasPrefix(t, "-") && len(t) > 1 && !a.dynamic:
			if eq := strings.IndexByte(t, '='); eq > 0 {
				i++
				continue
			}
			if wr.valued[t] {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
positional:
	i += wr.positional
	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

// envSplit checks env -S/--split-string, whose value is a command line.
func (c Checker) envSplit(args []word, sh Shell, depth int) Result {
	for i, a := range args {
		switch {
		case (a.text == "-S" || a.text == "--split-string") && i+1 < len(args):
			return c.line(args[i+1].text+" "+joinWords(args[i+2:]), sh, depth+1)
		case strings.HasPrefix(a.text, "--split-string="):
			return c.line(strings.TrimPrefix(a.text, "--split-string=")+" "+joinWords(args[i+1:]), sh, depth+1)
		case strings.HasPrefix(a.text, "-S") && len(a.text) > 2:
			return c.line(a.text[2:]+" "+joinWords(args[i+1:]), sh, depth+1)
		}
	}
	return allow
}

func joinWords(ws []word) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.text
	}
	return strings.Join(parts, " ")
}

// stripNamed removes a named PowerShell parameter (its name only) from args.
func stripNamed(args []word, name string) []word {
	out := make([]word, 0, len(args))
	for _, a := range args {
		if !a.quoted && strings.EqualFold(a.text, name) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// posixShell checks bash -c 'script' and a script the shell reads from standard input (no script file, -s, or the
// file /dev/stdin): its here-documents, here-strings and what is piped into it.
func (c Checker) posixShell(args []word, s *segment, depth int) Result {
	fromStdin, options := false, true
	for i := 0; i < len(args); i++ {
		t := args[i].text
		short := options && strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--")
		switch {
		case options && (t == "-" || t == "--"):
			options = false
		case options && (t == "--rcfile" || t == "--init-file" || t == "-o" || t == "+o" || t == "-O" || t == "+O"):
			i++
		case short && strings.Contains(t, "c"):
			if i+1 >= len(args) {
				return allow
			}
			return c.line(args[i+1].text, POSIX, depth+1)
		case short && strings.Contains(t, "s"):
			fromStdin = true // -s: the script comes from standard input, the words after it are its arguments
		case options && (strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+")):
		case fromStdin || isStdinFile(t):
			return c.stdinScript(s, POSIX, depth)
		default:
			return allow // a script file
		}
	}
	return c.stdinScript(s, POSIX, depth)
}

// isStdinFile reports a file name that is standard input.
func isStdinFile(t string) bool {
	return t == "/dev/stdin" || t == "/proc/self/fd/0" || t == "/dev/fd/0"
}

// stdinScript checks the script a shell reads from standard input, read in its dialect: the here-documents and
// here-strings of its command, and what is piped into it.
func (c Checker) stdinScript(s *segment, script Shell, depth int) Result {
	if s == nil {
		return allow
	}
	res := allow
	for _, body := range s.stdin {
		r := c.line(body, script, depth+1)
		if r.Deny {
			return r
		}
		res = res.merge(r)
	}
	r := c.piped(s, script, depth)
	if r.Deny {
		return r
	}
	return res.merge(r)
}

// feeder is a command whose output goes down a pipeline: its words (without leading keywords) and the bodies of
// its here-documents and here-strings.
type feeder struct {
	words []word
	stdin []string
}

// upstream returns the commands whose output a pipeline stage reads from u: u itself, or the commands of a
// subshell or brace group, each with its leading keywords removed ({ echo x; }: echo x).
func upstream(u *segment) []feeder {
	members := []*segment{u}
	if u.group != nil {
		members = u.group
	}
	out := make([]feeder, 0, len(members))
	for _, m := range members {
		ws := m.words
		for len(ws) > 0 && !ws[0].quoted && keywords[strings.ToLower(ws[0].text)] {
			ws = ws[1:]
		}
		out = append(out, feeder{words: ws, stdin: m.stdin})
	}
	return out
}

// piped checks what is piped into an interpreter that reads its script from standard input: the arguments and the
// here-documents of every command up the pipeline, inside subshells and brace groups too (cat <<'EOF' | sh,
// echo 'git stash' | tr -d '\r' | sh, { printf 'git stash\n'; } | sh).
func (c Checker) piped(s *segment, sh Shell, depth int) Result {
	if s == nil || !s.piped {
		return allow
	}
	res := allow
	for u := s.prev; u != nil; u = u.prev {
		for _, m := range upstream(u) {
			ws := m.words
			if len(ws) > 0 {
				ws = ws[1:]
			}
			for _, w := range ws {
				if r := c.Command(w.text); r.Deny {
					return r
				}
			}
			texts := append([]string{joinWords(ws)}, m.stdin...)
			if out, ok := printed(m.words); ok {
				texts = append(texts, out) // printf 'git stash\n' | bash: the \n is a line break by then
			}
			for _, text := range texts {
				r := c.line(text, sh, depth+1)
				if r.Deny {
					return r
				}
				res = res.merge(r)
			}
		}
		if !u.piped {
			break
		}
	}
	return res
}

// cmdShell checks cmd /c ... and cmd /k ....
func (c Checker) cmdShell(args []word, s *segment, depth int) Result {
	for i, a := range args {
		t := strings.ToLower(a.text)
		if strings.HasPrefix(t, "/c") || strings.HasPrefix(t, "/k") || t == "-c" || t == "-k" {
			first := a.text[2:]
			if t == "-c" || t == "-k" {
				first = ""
			}
			script := strings.TrimSpace(first + " " + joinWords(args[i+1:]))
			return c.line(script, Cmd, depth+1)
		}
		if !strings.HasPrefix(t, "/") {
			return allow
		}
	}
	return c.stdinScript(s, Cmd, depth)
}

// pwshValued are powershell.exe/pwsh options that take a value.
var pwshValued = set("-executionpolicy", "-ep", "-ex", "-exec", "-windowstyle", "-w", "-workingdirectory", "-wd",
	"-inputformat", "-if", "-inp", "-outputformat", "-of", "-o", "-configurationname", "-config", "-version", "-v",
	"-settingsfile", "-psconsolefile", "-custompipename", "-configurationfile")

// pwshShell checks powershell -Command ..., -EncodedCommand ... and a bare command.
func (c Checker) pwshShell(args []word, s *segment, depth int) Result {
	for i := 0; i < len(args); i++ {
		t := strings.ToLower(args[i].text)
		if strings.HasPrefix(t, "/") {
			t = "-" + t[1:]
		}
		stdinNext := i+1 < len(args) && args[i+1].text == "-"
		switch {
		case stdinNext && (t == "-c" || t == "-f" || len(t) >= 3 && (isAbbrev(t, "-command") || isAbbrev(t, "-file"))):
			// -Command - and -File - read the script from standard input.
			return c.stdinScript(s, PowerShell, depth)
		case t == "-c" || len(t) >= 3 && isAbbrev(t, "-command") && t != "-co":
			return c.line(joinWords(args[i+1:]), PowerShell, depth+1)
		case t == "-co":
			// -co is ambiguous for pwsh (-Command, -ConfigurationName); refuse when what follows is git.
			return c.line(joinWords(args[i+1:]), PowerShell, depth+1)
		case t == "-e" || t == "-ec" || len(t) >= 3 && isAbbrev(t, "-encodedcommand"):
			if i+1 >= len(args) {
				return allow
			}
			script, ok := decodeUTF16Base64(args[i+1].text)
			if !ok {
				return denyUnreadable()
			}
			return c.line(script, PowerShell, depth+1)
		case t == "-f" || t == "-file":
			return allow
		case pwshValued[t]:
			i++
		case strings.HasPrefix(t, "-"):
		default:
			// powershell.exe runs the remaining words as a command (pwsh would treat the first as a file).
			return c.line(joinWords(args[i:]), PowerShell, depth+1)
		}
	}
	return c.stdinScript(s, PowerShell, depth)
}

// decodeUTF16Base64 decodes powershell -EncodedCommand (base64 of UTF-16LE text).
func decodeUTF16Base64(s string) (string, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(raw)%2 != 0 {
		return "", false
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return string(utf16.Decode(u)), true
}

// interpreter checks the inline code of a scripting language (node -e, python -c, ...) as code does. Without
// inline code or a script file (no file, or "-"), the code comes from standard input: its here-documents and
// here-strings, and the words piped into it, are read the same way.
func (c Checker) interpreter(name string, args []word, s *segment, depth int) Result {
	flags := map[string]string{
		"node": "e", "nodejs": "e", "bun": "e", "deno": "", "python": "c", "python3": "c", "python2": "c", "py": "c",
		"pythonw": "c", "perl": "eE", "ruby": "e", "php": "r", "osascript": "e",
	}[name]
	file, stdin := false, false
	for i := 0; i < len(args); i++ {
		t := args[i].text
		code := ""
		switch {
		case name == "deno" && t == "eval" && i+1 < len(args):
			code = args[i+1].text
		case t == "--eval" || t == "--print" || (name == "node" || name == "bun") && t == "-p":
			if i+1 < len(args) {
				code = args[i+1].text
			}
		case strings.HasPrefix(t, "--eval=") || strings.HasPrefix(t, "--print="):
			code = t[strings.IndexByte(t, '=')+1:]
		case strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && len(t) > 1 && flags != "" &&
			strings.ContainsAny(t[len(t)-1:], flags):
			if i+1 < len(args) {
				code = args[i+1].text
			}
		default:
			if !file && (t == "-" || isStdinFile(t)) {
				stdin = true
			}
			if !strings.HasPrefix(t, "-") || t == "-" {
				file = true // the script (or "-"); later words are its arguments
			}
			continue
		}
		return c.code(code, codeLang(name), depth)
	}
	if name != "deno" && (stdin || !file) {
		return c.stdinCode(s, codeLang(name), depth)
	}
	return allow
}

// stdinCode checks the code an interpreter of language lang reads from standard input.
func (c Checker) stdinCode(s *segment, lang string, depth int) Result {
	if s == nil {
		return allow
	}
	texts := slices.Clone(s.stdin)
	if s.piped {
		for u := s.prev; u != nil; u = u.prev {
			for _, m := range upstream(u) {
				if len(m.words) > 1 {
					texts = append(texts, joinWords(m.words[1:]))
				}
				texts = append(texts, m.stdin...)
			}
			if !u.piped {
				break
			}
		}
	}
	for _, text := range texts {
		if r := c.code(text, lang, depth); r.Deny {
			return r
		}
	}
	return allow
}

// code checks inline code in language lang ("" when unknown). Code that can start a program (runsProcess) has
// every string literal read as a command line, and a literal "git" followed by more literals as a git argument
// list; code that cannot start one is data (a script that edits a file mentioning git stash).
func (c Checker) code(code, lang string, depth int) Result {
	if !runsProcess(code, lang) {
		return allow
	}
	if configEnvMention.MatchString(code) {
		return denyConfigEnv() // env={'GIT_CONFIG_PARAMETERS': ...}, os.environ[...], process.env...
	}
	lits := append(literals(code), commandQuotes(code, lang)...)
	for i, l := range lits {
		if r := c.Command(l); r.Deny {
			return withCodeHint(r)
		}
		if cmdName(word{text: l}) == "git" {
			ws := make([]word, 0, len(lits)-i-1)
			for _, a := range lits[i+1:] {
				ws = append(ws, word{text: a})
			}
			if r := c.git(ws, depth+1); r.Deny {
				return withCodeHint(r)
			}
		}
	}
	return allow
}

// literals returns the quoted string literals of code ('...', "...", `...`, with backslash escapes).
func literals(code string) []string {
	var out []string
	for i := 0; i < len(code); i++ {
		q := code[i]
		if q != '\'' && q != '"' && q != '`' {
			continue
		}
		var b strings.Builder
		j := i + 1
		for ; j < len(code) && code[j] != q; j++ {
			if code[j] == '\\' && j+1 < len(code) {
				j++
			}
			b.WriteByte(code[j])
		}
		out = append(out, b.String())
		i = j
	}
	return out
}

// findExec checks the commands of find -exec/-execdir/-ok/-okdir.
func (c Checker) findExec(args []word, s *segment, sh Shell, depth int) Result {
	for i := 0; i < len(args); i++ {
		switch args[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			j := i + 1
			for j < len(args) && args[j].text != ";" && args[j].text != "+" {
				j++
			}
			if r := c.command(args[i+1:j], s, sh, depth+1); r.Deny || r.bad {
				return r
			}
			i = j
		}
	}
	return allow
}

// argumentList splits a PowerShell argument list ('reset','--hard' or "reset --hard"; Start-Process joins the
// elements with spaces) into arguments.
func argumentList(w word) []word {
	var out []word
	for _, part := range strings.FieldsFunc(w.text, func(r rune) bool { return r == ',' || r == ' ' }) {
		out = append(out, word{text: strings.Trim(part, `'"`), dynamic: w.dynamic && strings.ContainsAny(part, "$(")})
	}
	return out
}

// startProcess checks Start-Process git -ArgumentList ... (PowerShell) and start git ... (cmd).
func (c Checker) startProcess(args []word, sh Shell, depth int) Result {
	var file string
	var rest []word
	for i := 0; i < len(args); i++ {
		t := strings.ToLower(args[i].text)
		switch {
		case t == "-filepath" || t == "-file" || t == "-path":
			if i+1 < len(args) {
				file = args[i+1].text
				i++
			}
		case t == "-argumentlist" || t == "-args" || t == "-arguments":
			if i+1 < len(args) {
				rest = append(rest, argumentList(args[i+1])...)
				i++
			}
		case strings.HasPrefix(t, "-") || strings.HasPrefix(t, "/"):
			if t == "-wait" || t == "-nonewwindow" || t == "-passthru" || strings.HasPrefix(t, "/") {
				continue
			}
			i++ // other parameters take a value
		case file == "":
			file = args[i].text
		case sh == PowerShell:
			// The second positional parameter of Start-Process is -ArgumentList: git 'reset','--hard'.
			rest = append(rest, argumentList(args[i])...)
		default:
			rest = append(rest, args[i])
		}
	}
	if file == "" {
		return allow
	}
	if cmdName(word{text: file}) == "git" {
		return c.git(rest, depth+1)
	}
	if r := c.command(rest, nil, sh, depth+1); r.Deny {
		return r // start "title" git ...: cmd's first quoted word can be a window title
	}
	return c.command(append([]word{{text: file}}, rest...), nil, sh, depth+1)
}
