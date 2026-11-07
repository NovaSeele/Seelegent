package guard

import "strings"

// xargsCommand returns the command xargs runs, given xargs's own options (opts) and the command words after them
// (cmd): with a replacement string (-I R, -i, --replace) each word holding it gets the input in its place, otherwise
// the input is appended; either way the input is a value only known when it runs. fromInput reports a command that
// is a shell whose script holds the input (xargs -I{} sh -c '{}', xargs sh -c, xargs cmd /c), in dialect script:
// the caller then reads what is piped into xargs as that script.
func xargsCommand(opts, cmd []word) (out []word, script Shell, fromInput bool) {
	repl := xargsReplace(opts)
	const input = "$STDIN"
	if repl == "" {
		out = append(append(out, cmd...), word{text: input, dynamic: true})
	} else {
		for _, w := range cmd {
			if strings.Contains(w.text, repl) {
				w = word{text: strings.ReplaceAll(w.text, repl, input), dynamic: true}
			}
			out = append(out, w)
		}
	}
	if len(cmd) == 0 {
		return out, POSIX, false
	}
	script, ok := shellDialect(cmdName(cmd[0]))
	if !ok {
		return out, POSIX, false
	}
	args := cmd[1:]
	if repl != "" {
		// The input is the script when the replacement stands in it: the word after -c (POSIX), any word after
		// /c or -Command (cmd and PowerShell join them). sh -c 'wc -l "$1"' _ {} passes it as an argument.
		for i, a := range args {
			if !isScriptFlag(a.text, script) {
				continue
			}
			after := args[i+1:]
			if script == POSIX && len(after) > 1 {
				after = after[:1]
			}
			return out, script, strings.Contains(joinWords(after), repl)
		}
		return out, script, false
	}
	// Without a replacement string the input words follow the last argument: after -c they are the script.
	return out, script, len(args) > 0 && isScriptFlag(args[len(args)-1].text, script)
}

// xargsReplace returns xargs's replacement string ("" without one).
func xargsReplace(opts []word) string {
	repl := ""
	for i := 0; i < len(opts); i++ {
		t := opts[i].text
		switch {
		case t == "-I" && i+1 < len(opts):
			repl = opts[i+1].text
			i++
		case strings.HasPrefix(t, "-I") && len(t) > 2:
			repl = t[2:]
		case t == "-i" || t == "--replace":
			repl = "{}"
		case strings.HasPrefix(t, "-i") && len(t) > 2:
			repl = t[2:]
		case strings.HasPrefix(t, "--replace="):
			repl = strings.TrimPrefix(t, "--replace=")
		}
	}
	return repl
}

// shellDialect returns the dialect of a shell program's script.
func shellDialect(name string) (Shell, bool) {
	switch name {
	case "bash", "sh", "zsh", "dash", "ksh", "mksh", "ash", "fish", "yash", "tcsh", "csh":
		return POSIX, true
	case "cmd":
		return Cmd, true
	case "powershell", "pwsh", "powershell_ise":
		return PowerShell, true
	}
	return POSIX, false
}

// isScriptFlag reports the option after which a shell of dialect sh takes its script (-c, -lc, /c, -Command).
func isScriptFlag(t string, sh Shell) bool {
	l := strings.ToLower(t)
	switch sh {
	case Cmd:
		return l == "/c" || l == "/k"
	case PowerShell:
		return l == "-c" || len(l) >= 3 && isAbbrev(l, "-command")
	}
	return strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "c")
}
