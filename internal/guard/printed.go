package guard

import (
	"strconv"
	"strings"
)

// printed returns what a printf or echo command writes, with its backslash escapes (\n, \r\n, \t, octal and hex)
// decoded and printf's format filled in, so that a script piped into a shell is read line by line
// (printf 'git stash\n' | bash). Every echo counts as echo -e: sh's echo (dash) decodes escapes without it.
func printed(ws []word) (string, bool) {
	if len(ws) == 0 {
		return "", false
	}
	args := ws[1:]
	switch cmdName(ws[0]) {
	case "printf":
		if len(args) > 0 && args[0].text == "--" {
			args = args[1:]
		}
		if len(args) == 0 || args[0].text == "-v" {
			return "", false // printf -v var assigns, it writes nothing
		}
		return printfText(args[0].text, args[1:]), true
	case "echo":
		for len(args) > 0 && isEchoFlags(args[0].text) {
			args = args[1:]
		}
		return unescape(joinWords(args), true), true
	}
	return "", false
}

// isEchoFlags reports an echo option word (-n, -e, -E, -ne, ...).
func isEchoFlags(t string) bool {
	return len(t) > 1 && t[0] == '-' && strings.Trim(t[1:], "neE") == ""
}

// printfText fills in a printf format with args (each conversion takes the next argument; the format is used again
// while arguments remain) and decodes its escapes.
func printfText(format string, args []word) string {
	var b strings.Builder
	for round := 0; round <= len(args); round++ {
		used := 0
		for i := 0; i < len(format); i++ {
			ch := format[i]
			if ch == '\\' {
				j := escapeEnd(format, i)
				b.WriteString(unescape(format[i:j], false))
				i = j - 1
				continue
			}
			if ch != '%' || i+1 >= len(format) {
				b.WriteByte(ch)
				continue
			}
			if format[i+1] == '%' {
				b.WriteByte('%')
				i++
				continue
			}
			j := i + 1
			for j < len(format) && strings.IndexByte("-+ #0123456789.*", format[j]) >= 0 {
				j++
			}
			if j >= len(format) {
				b.WriteString(format[i:])
				break
			}
			arg := ""
			if used < len(args) {
				arg = args[used].text
			}
			used++
			if format[j] == 'b' {
				arg = unescape(arg, true)
			}
			b.WriteString(arg)
			i = j
		}
		args = args[min(used, len(args)):]
		if used == 0 || len(args) == 0 {
			break
		}
	}
	return b.String()
}

// escapeEnd returns the end of the backslash escape that starts at s[i].
func escapeEnd(s string, i int) int {
	j := i + 2
	if j > len(s) {
		return len(s)
	}
	switch c := s[i+1]; {
	case c >= '0' && c <= '7':
		limit := i + 4 // \nnn
		if c == '0' {
			limit = i + 5 // \0nnn
		}
		for j < len(s) && j < limit && s[j] >= '0' && s[j] <= '7' {
			j++
		}
	case c == 'x':
		for j < len(s) && j < i+4 && strings.IndexByte("0123456789abcdefABCDEF", s[j]) >= 0 {
			j++
		}
	}
	return j
}

// unescape decodes the backslash escapes of printf and echo -e. With stopAtC, \c ends the output (echo, %b).
func unescape(s string, stopAtC bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		j := escapeEnd(s, i)
		switch c := s[i+1]; c {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v', 'f', 'a', 'b', 'e', 'E':
			b.WriteByte(' ')
		case '\\':
			b.WriteByte('\\')
		case 'c':
			if stopAtC {
				return b.String()
			}
			b.WriteString(`\c`)
		case 'x', '0', '1', '2', '3', '4', '5', '6', '7':
			digits := s[i+2 : j]
			base := 8
			if c == 'x' {
				base = 16
			} else if c != '0' {
				digits = s[i+1 : j]
			}
			n, err := strconv.ParseUint(digits, base, 8)
			if err != nil {
				n = 0
			}
			b.WriteByte(byte(n))
		default:
			b.WriteString(s[i:j])
		}
		i = j - 1
	}
	return b.String()
}
