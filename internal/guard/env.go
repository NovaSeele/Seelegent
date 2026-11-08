package guard

import (
	"regexp"
	"strings"
)

// configEnvText is the refusal of a command that sets git configuration through environment variables.
const configEnvText = Marker + ": this command sets GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT, GIT_CONFIG_KEY_<n>, " +
	"GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM or a variable that makes git run a program (GIT_ALLOW_PROTOCOL, " +
	"GIT_SSH_COMMAND, GIT_SSH, GIT_PROXY_COMMAND, GIT_EXTERNAL_DIFF, GIT_EXEC_PATH), or sets an editor, pager or " +
	"password prompt git runs (GIT_EDITOR, GIT_SEQUENCE_EDITOR, GIT_PAGER, GIT_ASKPASS, EDITOR, VISUAL, PAGER, " +
	"SSH_ASKPASS) to anything but cat, less, more or true. They can define git aliases, " +
	"settings and commands that the guard cannot see, for this git command and every later one of the same shell. " +
	"Give a setting with git -c instead. If you truly need it, say so under ## Blocked."

func denyConfigEnv() Result { return Result{Deny: true, Reason: configEnvText} }

// configEnvName matches the environment variables that give git configuration: the -c settings git passes to its
// children (GIT_CONFIG_PARAMETERS), the GIT_CONFIG_COUNT/KEY_<n>/VALUE_<n> list, and the files that replace the
// global and system configuration. The guard resolves aliases with the hook's own environment, so an alias defined
// in the command's environment would pass it.
// The variables that make git run a program of their own are refused the same way (H4): GIT_ALLOW_PROTOCOL (ext::
// URLs), GIT_SSH_COMMAND, GIT_SSH, GIT_PROXY_COMMAND, GIT_EXTERNAL_DIFF and GIT_EXEC_PATH.
var configEnvName = regexp.MustCompile(`(?i)^(GIT_CONFIG_(PARAMETERS|COUNT|GLOBAL|SYSTEM|KEY_\w*)|` + programEnv + `)$`)

// configEnvMention finds such a variable name in text (inline code, a .NET call), and the git-specific names of
// editorEnv (the bare EDITOR, VISUAL and PAGER are ordinary words in code and text).
var configEnvMention = regexp.MustCompile(`(?i)\b(GIT_CONFIG_(PARAMETERS|COUNT|GLOBAL|SYSTEM|KEY_\w*)|` +
	programEnv + `|GIT_EDITOR|GIT_SEQUENCE_EDITOR|GIT_PAGER|GIT_ASKPASS|SSH_ASKPASS)\b`)

// programEnv are the names of the variables whose value git runs (or runs programs from).
const programEnv = `GIT_ALLOW_PROTOCOL|GIT_SSH_COMMAND|GIT_SSH|GIT_PROXY_COMMAND|GIT_EXTERNAL_DIFF|GIT_EXEC_PATH`

// editorEnvName matches the variables whose value git runs as its editor, pager or password prompt, the same as the
// core.editor, sequence.editor, core.pager and core.askpass keys the guard refuses (G2): GIT_EDITOR,
// GIT_SEQUENCE_EDITOR, GIT_PAGER, GIT_ASKPASS, and the fallbacks EDITOR, VISUAL, PAGER and SSH_ASKPASS. Unlike
// the variables of configEnvName they are refused only with a value that is not a plain pager (safeProgramValue):
// GIT_PAGER=cat git log is common and harmless.
var editorEnvName = regexp.MustCompile(
	`(?i)^(GIT_EDITOR|GIT_SEQUENCE_EDITOR|GIT_PAGER|GIT_ASKPASS|EDITOR|VISUAL|PAGER|SSH_ASKPASS)$`)

// safePrograms are the values of an editorEnvName variable that run nothing else: no program, cat, less, more
// and true (as a pager, or an editor that keeps the message).
var safePrograms = set("", "cat", "less", "more", "true")

// safeOption matches an option of a safe pager (less -R, less -FRX).
var safeOption = regexp.MustCompile(`^-[A-Za-z0-9=-]*$`)

// safeProgramValue reports whether value, given to an editorEnvName variable, runs only a plain pager: empty, or
// cat, less, more or true with options only.
func safeProgramValue(value string) bool {
	fields := strings.Fields(strings.Trim(strings.TrimSpace(value), `'"`))
	if len(fields) == 0 {
		return true
	}
	if !safePrograms[fields[0]] {
		return false
	}
	for _, f := range fields[1:] {
		if !safeOption.MatchString(f) {
			return false
		}
	}
	return true
}

// configAssign reports a NAME=value (or NAME+=value) word that sets one of the configuration variables, or an
// editorEnvName variable to a program other than a plain pager.
func configAssign(t string) bool {
	eq := strings.IndexByte(t, '=')
	if eq <= 0 {
		return false
	}
	name := t[:eq]
	if configEnvName.MatchString(strings.TrimSuffix(name, "+")) {
		return true
	}
	if editorEnvName.MatchString(strings.TrimSuffix(name, "+")) {
		return strings.HasSuffix(name, "+") || !safeProgramValue(t[eq+1:])
	}
	return false
}

// pwshEditorAssign reports whether a PowerShell $env:NAME (rest: the text after NAME in ws[0]) is assigned or
// appended a value that is not a plain pager (safeProgramValue), or one the guard cannot read.
func pwshEditorAssign(ws []word, rest string) bool {
	rest = strings.TrimSpace(rest)
	next := 1
	if rest == "" && len(ws) > 1 {
		rest, next = ws[1].text, 2
	}
	if strings.HasPrefix(rest, "+=") {
		return true
	}
	if !strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "==") {
		return false
	}
	value := strings.TrimSpace(rest[1:])
	if value == "" && next < len(ws) {
		value = joinWords(ws[next:])
		for _, w := range ws[next:] {
			if w.dynamic {
				return true
			}
		}
	} else if next == 2 && ws[1].dynamic {
		return true
	}
	// A value inside ws[0] ($env:GIT_PAGER='cat') keeps its $ and ( in the text, which safeProgramValue refuses;
	// ws[0] itself always reads as dynamic because of $env.
	return !safeProgramValue(value)
}

// pwshEnvTarget splits a PowerShell environment reference ($env:NAME, ${env:NAME}, env:NAME, Env:\NAME) into the
// variable name and the text after it.
func pwshEnvTarget(t string) (name, rest string, ok bool) {
	t = strings.TrimPrefix(t, "$")
	braced := strings.HasPrefix(t, "{")
	t = strings.TrimPrefix(t, "{")
	if len(t) < 4 || !strings.EqualFold(t[:4], "env:") {
		return "", "", false
	}
	t = strings.TrimLeft(t[4:], `\/`)
	end := 0
	for end < len(t) && isNameChar(t[end]) {
		end++
	}
	name, rest = t[:end], t[end:]
	if braced {
		rest = strings.TrimPrefix(rest, "}")
	}
	return name, rest, true
}

// envSetters are the commands whose arguments set environment variables: POSIX export and its relatives (and fish's
// set -x), cmd's set and setx.
var envSetters = set("export", "declare", "typeset", "readonly", "local", "set", "setx")

// pwshItemSetters are the PowerShell cmdlets that write an item, such as env:NAME.
var pwshItemSetters = set("set-item", "si", "new-item", "ni", "set-content", "sc", "add-content", "ac", "copy-item",
	"cpi", "copy", "cp", "move-item", "mi", "move", "mv", "rename-item", "rni", "ren")

// setsConfigEnv reports whether the simple command ws (with its leading assignments) sets one of the git
// configuration variables: NAME=value before a command or alone, export/declare/typeset/readonly/local NAME[=value],
// cmd's set NAME=value and setx NAME, PowerShell's $env:NAME = value, Set-Item/New-Item env:NAME and
// [Environment]::SetEnvironmentVariable('NAME', ...).
func setsConfigEnv(ws []word, sh Shell) bool {
	if len(ws) == 0 {
		return false
	}
	for _, w := range ws {
		if !w.assign {
			break
		}
		if configAssign(w.text) {
			return true
		}
	}
	if sh == PowerShell {
		if name, rest, ok := pwshEnvTarget(ws[0].text); ok && configEnvName.MatchString(name) {
			rest = strings.TrimSpace(rest)
			if rest == "" && len(ws) > 1 {
				rest = ws[1].text
			}
			if strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==") || strings.HasPrefix(rest, "+=") {
				return true
			}
		}
		if name, rest, ok := pwshEnvTarget(ws[0].text); ok && editorEnvName.MatchString(name) &&
			pwshEditorAssign(ws, rest) {
			return true // $env:GIT_EDITOR = 'git stash; notepad'
		}
		text := joinWords(ws)
		if strings.Contains(strings.ToLower(text), "setenvironmentvariable") && configEnvMention.MatchString(text) {
			return true
		}
	}
	name := cmdName(ws[0])
	switch {
	case envSetters[name]:
		for i, a := range ws[1:] {
			if configAssign(a.text) || configEnvName.MatchString(a.text) {
				return true
			}
			// setx NAME value: the value is the next argument.
			if name == "setx" && editorEnvName.MatchString(a.text) &&
				(i+2 >= len(ws) || ws[i+2].dynamic || !safeProgramValue(ws[i+2].text)) {
				return true
			}
		}
	case pwshItemSetters[name]:
		text := joinWords(ws[1:])
		if configEnvMention.MatchString(text) && strings.Contains(strings.ToLower(text), "env:") {
			return true
		}
	}
	return false
}
