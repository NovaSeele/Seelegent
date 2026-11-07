package guard

import (
	"regexp"
	"strings"
)

// codeHint ends a refusal that came from a string in inline code.
const codeHint = " The command was read from a string in inline code that can start programs; to edit a file " +
	"that mentions a git command, use your edit tool instead of a script."

// withCodeHint adds codeHint to a refusal.
func withCodeHint(r Result) Result {
	if r.Deny && !strings.HasSuffix(r.Reason, codeHint) {
		r.Reason += codeHint
	}
	return r
}

// codeLang is the language of an interpreter's inline code.
func codeLang(name string) string {
	switch name {
	case "node", "nodejs", "deno", "bun":
		return "js"
	case "python", "python3", "python2", "py", "pythonw":
		return "python"
	case "perl", "ruby", "php":
		return name
	case "osascript":
		return "applescript"
	}
	return ""
}

// processAPI finds the names through which inline code starts a program: os.system, subprocess.*, os.popen,
// os.exec*/spawn*, child_process (exec, execSync, spawn, ...), Open3, IPC::*, PHP's shell_exec, passthru and
// proc_open, Perl's qx, Deno.Command, Bun.$, AppleScript's do shell script. It matches in the strings too
// (require('child_process'), getattr(os, 'system')), and words such as "execute" or "system" in a string count as
// well: the code is then read as before, every literal as a command.
var processAPI = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:subprocess|system|popen\w*|spawn\w*|exec\w*|` +
	`child_process|startfile|shell_exec|passthru|proc_open|open[23]|ipc|getoutput|getstatusoutput|qx|` +
	`deno\.(?:command|run)|bun\.\$|do\s+shell\s+script)(?:[^a-z0-9]|$)`)

// rubyX finds Ruby's %x(...) command literal.
var rubyX = regexp.MustCompile(`%x[^\sA-Za-z0-9]`)

// runsProcess reports whether inline code in language lang ("" when unknown) can start a program. Backticks run
// commands in Perl, Ruby and PHP; in JavaScript only a tagged $`...` does (zx, Bun's shell). Perl and Ruby also
// open pipes with open("|cmd") and open(F, "-|", ...).
func runsProcess(code, lang string) bool {
	if lang == "" || processAPI.MatchString(code) {
		return true
	}
	switch lang {
	case "perl", "ruby", "php":
		if strings.Contains(code, "`") {
			return true
		}
	case "js":
		return strings.Contains(code, "$`")
	}
	if lang == "ruby" && rubyX.MatchString(code) {
		return true
	}
	if lang == "perl" || lang == "ruby" {
		for _, l := range literals(code) {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "|") || strings.HasSuffix(l, "|") || l == "-|" || l == "|-" {
				return true
			}
		}
	}
	return false
}

// commandQuotes returns the commands of Perl's qx{...} and Ruby's %x(...), which literals does not see.
func commandQuotes(code, lang string) []string {
	if lang != "" && lang != "perl" && lang != "ruby" {
		return nil
	}
	var out []string
	for i := 0; i+2 < len(code); i++ {
		qx := code[i] == 'q' && code[i+1] == 'x' && (i == 0 || !isNameChar(code[i-1]))
		if !qx && (code[i] != '%' || code[i+1] != 'x') {
			continue
		}
		j := i + 2
		for j < len(code) && (code[j] == ' ' || code[j] == '\t') && code[i] == 'q' {
			j++
		}
		if j >= len(code) || isNameChar(code[j]) || code[j] == ' ' || code[j] == '\n' {
			continue
		}
		open := code[j]
		closer := open
		switch open {
		case '(':
			closer = ')'
		case '[':
			closer = ']'
		case '{':
			closer = '}'
		case '<':
			closer = '>'
		}
		end := strings.IndexByte(code[j+1:], closer)
		if end < 0 {
			continue
		}
		out = append(out, code[j+1:j+1+end])
		i = j + end
	}
	return out
}
