package guard

import (
	"slices"
	"strings"
)

// writeDepth bounds the shell wrappers (bash -c, cmd /c, powershell -c) Writes looks into.
const writeDepth = 3

// Every argument of these commands may be a path they write, move or delete.
var writeVerbs = set("rm", "rmdir", "unlink", "shred", "truncate", "mv", "touch", "mkdir", "chmod", "chown", "ln",
	"tee", "del", "erase", "rd", "move", "ren", "rename", "md",
	"remove-item", "ri", "move-item", "mi", "rename-item", "rni", "set-content", "sc", "add-content", "ac",
	"out-file", "new-item", "ni", "clear-content", "clc", "tee-object")

// Only the destination of these commands is written.
var copyVerbs = set("cp", "copy", "copy-item", "cpi", "xcopy", "robocopy", "install", "rsync")

// prefixVerbs run the command that follows their options.
var prefixVerbs = set("sudo", "doas", "nohup", "time", "command", "exec", "env", "nice")

// cdVerbs change the folder the rest of the line runs in; pushVerbs also remember the one they leave, popVerbs go
// back to it.
var (
	cdVerbs   = set("cd", "chdir", "sl", "set-location", "pushd", "push-location")
	pushVerbs = set("pushd", "push-location")
	popVerbs  = set("popd", "pop-location")
)

// nullTargets are the redirection targets that write nowhere.
var nullTargets = set("$null", "nul", "/dev/null", "nul:")

// tempVars are the variables that name the temporary folder, and tempMakers the commands that make a temporary file
// or name the folder: a write under one is not in a repository.
var (
	tempVars   = []string{"$env:temp", "$env:tmp", "%temp%", "%tmp%", "$tmpdir", "${tmpdir}", "$temp", "$tmp"}
	tempMakers = []string{"mktemp", "new-temporaryfile", "gettempfilename", "gettemppath"}
)

// tempTarget reports whether a target with an expansion (its text in lower case) names a temporary file or folder:
// a temp folder variable, the output of mktemp, New-TemporaryFile or [IO.Path]::GetTempFileName(), or a variable
// this line assigned from one of those (temps).
func tempTarget(lower string, temps map[string]bool) bool {
	for _, v := range tempVars {
		if strings.HasPrefix(lower, v) {
			return true
		}
	}
	if strings.HasPrefix(lower, "$(") || strings.HasPrefix(lower, "`") || strings.HasPrefix(lower, "(") ||
		strings.HasPrefix(lower, "[") {
		for _, m := range tempMakers {
			if strings.Contains(lower, m) {
				return true
			}
		}
	}
	name, ok := leadingVar(lower)
	return ok && temps[name]
}

// leadingVar returns the variable an expansion starts with ($name, ${name}, $env:name), in lower case.
func leadingVar(t string) (string, bool) {
	if !strings.HasPrefix(t, "$") {
		return "", false
	}
	t = t[1:]
	if strings.HasPrefix(t, "{") {
		end := strings.IndexByte(t, '}')
		if end < 0 {
			return "", false
		}
		return strings.ToLower(t[1:end]), true
	}
	n := 0
	for n < len(t) && (isNameChar(t[n]) || t[n] == ':') {
		n++
	}
	return strings.ToLower(t[:n]), n > 0
}

// tempValue reports whether an assigned value (in lower case) comes from a temporary source.
func tempValue(lower string, temps map[string]bool) bool {
	for _, v := range append(slices.Clone(tempVars), tempMakers...) {
		if strings.Contains(lower, v) {
			return true
		}
	}
	for name := range temps {
		if strings.Contains(lower, "$"+name) || strings.Contains(lower, "${"+name+"}") {
			return true
		}
	}
	return false
}

// Target is one place a command line may write to.
type Target struct {
	// Path is the path as written (relative, absolute or with ~), joined to the folder an earlier cd, Set-Location
	// or pushd of the same line moved to: in `cd app && rm x`, app/x. A path with an expansion whose folder is
	// written out (src/$name.ts) is that folder. Empty when Unknown.
	Path string
	// Unknown marks a write whose place the line does not spell out: an expansion names it ($X, $env:X, %X%, a
	// command's output) or the line moved to such a folder before it. A write under the temporary folder's
	// variable ($env:TEMP\x, %TEMP%\x, $TMPDIR/x) is neither a path nor unknown: it is left out.
	Unknown bool
}

// Writes returns the paths a command line may write to (WriteTargets without the unknown ones). It is a cheap hint
// for the scope warnings and the lane, not a guard: nothing is refused because of it.
func Writes(line string) []string {
	var out []string
	for _, t := range WriteTargets(line) {
		if !t.Unknown {
			out = append(out, t.Path)
		}
	}
	return out
}

// WriteTargets returns the places a command line may write to: the targets of output redirections and the
// arguments of commands that write, move or delete files (destinations only for copies), each joined to the folder
// the line moved to before it. The line is read in every dialect that can read it.
func WriteTargets(line string) []Target {
	var out []Target
	seen := map[Target]bool{}
	add := func(t Target) {
		if (t.Path != "" || t.Unknown) && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	posixQuotes := singleQuoted(line)
	for _, sh := range allShells {
		if sh == Cmd && posixQuotes {
			continue
		}
		lineWrites(line, sh, 0, &dirState{}, add)
	}
	return out
}

// singleQuoted reports whether line uses single quotes the way POSIX shells and PowerShell do: it holds one and
// reads as a POSIX line, so its single quotes pair up. Such a line was not written for cmd, which has no single
// quotes: cmd's reading splits the quoted text into words and takes a '<' or '>' inside it for a redirection, so the
// sed script in `sed -i '/<a \/>/d' f.vue` would give the paths "\/" and "/d" (field report 3, B2).
func singleQuoted(line string) bool {
	if !strings.Contains(line, "'") {
		return false
	}
	_, err := parseState(line, POSIX, 0)
	return err == nil
}

// dirState is the folder a line runs in, relative to where it started ("" there); unknown once a cd went to a
// folder the line does not spell out. stack holds the folders pushd left.
type dirState struct {
	dir     string
	unknown bool
	// temp: the line moved into a temporary folder (cd $(mktemp -d), Set-Location $env:TEMP); its relative writes
	// are not in a repository.
	temp  bool
	stack []dirState
	// temps are the variables (lower case) the line assigned from a temporary source.
	temps map[string]bool
}

// learnTemps records the variables a command assigns from a temporary source: t=$(mktemp) in POSIX shells,
// $f = Join-Path $env:TEMP x or $f = New-TemporaryFile in PowerShell.
func (d *dirState) learnTemps(ws []word) {
	if d.temps == nil {
		d.temps = map[string]bool{}
	}
	for _, w := range ws {
		if !w.assign {
			break
		}
		name, value, _ := strings.Cut(w.text, "=")
		if tempValue(strings.ToLower(value), d.temps) {
			d.temps[strings.ToLower(strings.TrimSuffix(name, "+"))] = true
		}
	}
	if len(ws) == 0 || !strings.HasPrefix(ws[0].text, "$") {
		return
	}
	name, value, inline := strings.Cut(ws[0].text, "=")
	rest := ws[1:]
	if !inline {
		if len(rest) == 0 || rest[0].text != "=" {
			return
		}
		rest = rest[1:]
	}
	var b strings.Builder
	b.WriteString(value)
	for _, w := range rest {
		b.WriteString(" " + w.text)
	}
	if v, ok := leadingVar(name); ok && v == strings.ToLower(name[1:]) && tempValue(strings.ToLower(b.String()), d.temps) {
		d.temps[v] = true
	}
}

// cd moves to the folder w names.
func (d *dirState) cd(w word) {
	t := w.text
	switch {
	case (w.dynamic || strings.HasPrefix(t, "(")) && tempTarget(strings.ToLower(t), d.temps):
		d.dir, d.unknown, d.temp = "", false, true
	case w.dynamic || strings.HasPrefix(t, "(") || t == "-":
		d.dir, d.unknown, d.temp = "", true, false
	case isAbsPath(t):
		d.dir, d.unknown, d.temp = t, false, false
	default:
		d.dir = joinPath(d.dir, t)
	}
}

// resolve joins p (a target as written) to the folder the line is in.
func (d *dirState) resolve(w word) Target {
	t := w.text
	if w.dynamic || strings.HasPrefix(t, "(") {
		if tempTarget(strings.ToLower(t), d.temps) {
			return Target{}
		}
		dir := literalFolder(t)
		if dir == "" {
			return Target{Unknown: true}
		}
		t = dir
	}
	if isAbsPath(t) {
		return Target{Path: t}
	}
	if d.temp {
		return Target{}
	}
	if d.unknown {
		return Target{Unknown: true}
	}
	return Target{Path: joinPath(d.dir, t)}
}

// literalFolder is the folder an argument with an expansion names before its first expansion (src/ of
// src/$name.ts), or "" when it names none.
func literalFolder(t string) string {
	i := strings.IndexAny(t, "$%!`(")
	if i <= 0 {
		return ""
	}
	j := strings.LastIndexAny(t[:i], `/\`)
	if j <= 0 {
		return ""
	}
	return t[:j]
}

// isAbsPath reports whether p is absolute in some dialect: /x, \x, C:\x or C:/x, ~ (the home folder).
func isAbsPath(p string) bool {
	if p == "" {
		return false
	}
	if p[0] == '/' || p[0] == '\\' || p[0] == '~' {
		return true
	}
	return len(p) >= 2 && p[1] == ':' && (p[0] >= 'A' && p[0] <= 'Z' || p[0] >= 'a' && p[0] <= 'z')
}

// joinPath joins a relative path to dir ("" for the starting folder), keeping p's spelling.
func joinPath(dir, p string) string {
	switch {
	case dir == "" || dir == ".":
		return p
	case p == "" || p == ".":
		return dir
	}
	return strings.TrimRight(dir, `/\`) + "/" + p
}

func lineWrites(line string, sh Shell, depth int, d *dirState, add func(Target)) {
	if depth > writeDepth {
		return
	}
	st, err := parseState(line, sh, 0)
	if err != nil {
		return // a dialect that cannot read the line would only add noise
	}
	// moved is the command list whose cd changed the folder (-1: none). A cd inside a subshell, a substitution or a
	// PowerShell block may or may not reach past it (a POSIX subshell's does not, cmd's ( ) and PowerShell's
	// Set-Location do): the rest of the line is in a folder Writes cannot tell (field report 4, review G1).
	moved := -1
	for _, seg := range st.out {
		if moved >= 0 && seg.list != moved {
			d.dir, d.unknown, d.temp, d.stack, moved = "", true, false, nil, -1
		}
		d.learnTemps(seg.words)
		for _, w := range seg.redirs {
			if !nullTargets[strings.ToLower(w.text)] {
				add(d.resolve(w))
			}
		}
		if segmentWrites(seg.words, sh, depth, d, add) && seg.list != 0 {
			moved = seg.list
		}
	}
}

// segmentWrites adds the writes of one command and reports whether it changed the folder (cd, popd).
func segmentWrites(ws []word, sh Shell, depth int, d *dirState, add func(Target)) bool {
	for len(ws) > 0 && (ws[0].assign || !ws[0].quoted && keywords[ws[0].text]) {
		ws = ws[1:]
	}
	if len(ws) > 1 && sh == PowerShell && !ws[0].quoted && (ws[0].text == "&" || ws[0].text == ".") {
		ws = ws[1:] // the call operator
	}
	if len(ws) == 0 {
		return false
	}
	name, args := cmdName(ws[0]), ws[1:]
	switch {
	case prefixVerbs[name]:
		for len(args) > 0 && !args[0].quoted && (strings.HasPrefix(args[0].text, "-") ||
			assignment.MatchString(args[0].text)) {
			args = args[1:]
		}
		return segmentWrites(args, sh, depth, d, add)
	case cdVerbs[name]:
		pos := positionalWords(args, Cmd) // cd /d D:\x: the /d switch is no folder
		if v, ok := flagWord(args, "-path"); ok {
			pos = []word{v}
		} else if v, ok := flagWord(args, "-literalpath"); ok {
			pos = []word{v}
		}
		if pushVerbs[name] {
			d.stack = append(d.stack, dirState{dir: d.dir, unknown: d.unknown, temp: d.temp})
		}
		switch {
		case len(pos) > 0:
			d.cd(pos[0])
		case sh == POSIX && name == "cd":
			d.dir, d.unknown, d.temp = "~", false, false // the home folder
		}
		return true
	case popVerbs[name]:
		if n := len(d.stack); n > 0 {
			d.dir, d.unknown, d.temp = d.stack[n-1].dir, d.stack[n-1].unknown, d.stack[n-1].temp
			d.stack = d.stack[:n-1]
		} else {
			d.dir, d.unknown, d.temp = "", true, false
		}
		return true
	case name == "bash" || name == "sh" || name == "zsh" || name == "dash":
		if code, ok := flagValue(args, "-c"); ok {
			lineWrites(code, POSIX, depth+1, d.child(), add)
		}
	case name == "cmd":
		for i, a := range args {
			if t := strings.ToLower(a.text); t == "/c" || t == "/k" {
				lineWrites(joinWords(args[i+1:]), Cmd, depth+1, d.child(), add)
				break
			}
		}
	case name == "powershell" || name == "pwsh":
		for _, f := range []string{"-c", "-command"} {
			if code, ok := flagValue(args, f); ok {
				lineWrites(code, PowerShell, depth+1, d.child(), add)
				break
			}
		}
	case name == "sed" || name == "perl":
		for _, a := range inPlaceFiles(name, args) {
			add(d.resolve(a))
		}
	case writeVerbs[name]:
		for _, a := range positionalWords(args, sh) {
			add(d.resolve(a))
		}
	case copyVerbs[name]:
		if w, ok := flagWord(args, "-destination"); ok {
			add(d.resolve(w))
			return false
		}
		pos := positionalWords(args, sh)
		switch {
		case name == "robocopy" && len(pos) >= 2:
			add(d.resolve(pos[1]))
		case len(pos) >= 2:
			add(d.resolve(pos[len(pos)-1]))
		}
	}
	return false
}

// child is the folder a wrapped shell (bash -c, cmd /c, powershell -c) starts in: this one; its own cd stays its
// own.
func (d *dirState) child() *dirState {
	return &dirState{dir: d.dir, unknown: d.unknown, temp: d.temp, temps: d.temps}
}

// inPlaceFiles returns the files sed or perl edits in place (sed -i, -i.bak, BSD's -i "", --in-place[=suffix];
// perl -pi): its file arguments, without the script. Without in-place editing they only read their files: nothing.
func inPlaceFiles(name string, args []word) []word {
	inPlace, script := false, false
	var pos []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		switch {
		case a.quoted || a.dynamic || !strings.HasPrefix(t, "-") || t == "-":
			pos = append(pos, a)
		case t == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(t, "--"):
			long, _, valued := strings.Cut(t, "=")
			switch {
			case name == "sed" && long == "--in-place":
				inPlace = true
			case name == "sed" && long == "--line-length":
				if !valued {
					i++
				}
			case name == "sed" && (long == "--expression" || long == "--file"):
				script = true
				if !valued {
					i++
				}
			}
		default:
			// A cluster of switches; some take the rest of the word, or the next word, as their value.
			for j := 1; j < len(t); j++ {
				c := t[j]
				if c == 'i' {
					inPlace = true // the rest of the word is the backup suffix
					if name == "sed" && j == len(t)-1 && i+1 < len(args) && args[i+1].quoted && args[i+1].text == "" {
						i++ // BSD sed: -i '' is an empty suffix, not the script
					}
					break
				}
				if name == "sed" && (c == 'e' || c == 'f' || c == 'l') || name == "perl" && (c == 'e' || c == 'E') {
					script = script || c != 'l'
					if j == len(t)-1 {
						i++
					}
					break
				}
				if name == "perl" && strings.IndexByte("MmIDdFxC", c) >= 0 {
					break
				}
			}
		}
	}
	if !inPlace {
		return nil
	}
	if !script && len(pos) > 0 {
		pos = pos[1:] // the script (sed) or the program file (perl)
	}
	var out []word
	for _, a := range pos {
		if a.text != "" {
			out = append(out, a)
		}
	}
	return out
}

// flagValue returns the word after flag (compared without case).
func flagValue(args []word, flag string) (string, bool) {
	w, ok := flagWord(args, flag)
	return w.text, ok
}

// flagWord returns the word after flag (compared without case).
func flagWord(args []word, flag string) (word, bool) {
	for i, a := range args {
		if strings.EqualFold(a.text, flag) && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return word{}, false
}

// valueParams are the PowerShell parameters of the file cmdlets whose value is no path (Set-Content -Value x).
var valueParams = set("-value", "-encoding", "-itemtype", "-type", "-filter", "-include", "-exclude", "-inputobject",
	"-stream", "-delimiter", "-width", "-credential")

// positionalWords returns the arguments that are not options. A cmd switch (/q, /s) is an option; a PowerShell
// named parameter (-Path) is skipped, its value kept, except the values of valueParams.
func positionalWords(args []word, sh Shell) []word {
	var out []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		t := a.text
		switch {
		case t == "":
		case !a.quoted && valueParams[strings.ToLower(t)]:
			i++
		case !a.quoted && strings.HasPrefix(t, "-"):
		case sh == Cmd && len(t) <= 3 && strings.HasPrefix(t, "/"):
		default:
			out = append(out, a)
		}
	}
	return out
}
