package guard

import "strings"

// writeDepth bounds the shell wrappers (bash -c, cmd /c, powershell -c) Writes looks into.
const writeDepth = 3

// Every argument of these commands may be a path they write, move or delete.
var writeVerbs = set("rm", "rmdir", "unlink", "shred", "truncate", "mv", "touch", "mkdir", "chmod", "chown", "ln",
	"tee", "sed", "perl", "del", "erase", "rd", "move", "ren", "rename", "md",
	"remove-item", "ri", "move-item", "mi", "rename-item", "rni", "set-content", "sc", "add-content", "ac",
	"out-file", "new-item", "ni", "clear-content", "clc", "tee-object")

// Only the destination of these commands is written.
var copyVerbs = set("cp", "copy", "copy-item", "cpi", "xcopy", "robocopy", "install", "rsync")

// prefixVerbs run the command that follows their options.
var prefixVerbs = set("sudo", "doas", "nohup", "time", "command", "exec", "env", "nice")

// Writes returns the paths a command line may write to, as written (relative, absolute or with ~): the targets of
// output redirections and the arguments of commands that write, move or delete files (destinations only for
// copies). The line is read in every dialect that can read it; an argument that expands in a dialect ($X in POSIX
// and PowerShell, %X% in cmd) is left out of that dialect's reading.
// It is a cheap hint for the scope warnings, not a guard: nothing is refused because of it.
func Writes(line string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, sh := range allShells {
		lineWrites(line, sh, 0, add)
	}
	return out
}

func lineWrites(line string, sh Shell, depth int, add func(string)) {
	if depth > writeDepth {
		return
	}
	st, err := parseState(line, sh, 0)
	if err != nil {
		return // a dialect that cannot read the line would only add noise
	}
	for _, w := range st.writes {
		add(w)
	}
	for _, seg := range st.out {
		segmentWrites(seg.words, sh, depth, add)
	}
}

func segmentWrites(ws []word, sh Shell, depth int, add func(string)) {
	for len(ws) > 0 && !ws[0].quoted && (assignment.MatchString(ws[0].text) || keywords[ws[0].text]) {
		ws = ws[1:]
	}
	if len(ws) == 0 {
		return
	}
	name, args := cmdName(ws[0]), ws[1:]
	switch {
	case prefixVerbs[name]:
		for len(args) > 0 && !args[0].quoted && (strings.HasPrefix(args[0].text, "-") ||
			assignment.MatchString(args[0].text)) {
			args = args[1:]
		}
		segmentWrites(args, sh, depth, add)
	case name == "bash" || name == "sh" || name == "zsh" || name == "dash":
		if code, ok := flagValue(args, "-c"); ok {
			lineWrites(code, POSIX, depth+1, add)
		}
	case name == "cmd":
		for i, a := range args {
			if t := strings.ToLower(a.text); t == "/c" || t == "/k" {
				lineWrites(joinWords(args[i+1:]), Cmd, depth+1, add)
				break
			}
		}
	case name == "powershell" || name == "pwsh":
		for _, f := range []string{"-c", "-command"} {
			if code, ok := flagValue(args, f); ok {
				lineWrites(code, PowerShell, depth+1, add)
				break
			}
		}
	case writeVerbs[name]:
		for _, a := range positional(args, sh) {
			add(a)
		}
	case copyVerbs[name]:
		if d, ok := flagValue(args, "-destination"); ok {
			add(d)
			return
		}
		pos := positional(args, sh)
		switch {
		case name == "robocopy" && len(pos) >= 2:
			add(pos[1])
		case len(pos) >= 2:
			add(pos[len(pos)-1])
		}
	}
}

// flagValue returns the word after flag (compared without case).
func flagValue(args []word, flag string) (string, bool) {
	for i, a := range args {
		if strings.EqualFold(a.text, flag) && i+1 < len(args) {
			return args[i+1].text, true
		}
	}
	return "", false
}

// positional returns the arguments that are not options and hold no expansion. A cmd switch (/q, /s) is an
// option; a PowerShell named parameter (-Path) is skipped, its value kept.
func positional(args []word, sh Shell) []string {
	var out []string
	for _, a := range args {
		t := a.text
		switch {
		case a.dynamic || t == "":
		case !a.quoted && strings.HasPrefix(t, "-"):
		case sh == Cmd && len(t) <= 3 && strings.HasPrefix(t, "/"):
		default:
			out = append(out, t)
		}
	}
	return out
}
