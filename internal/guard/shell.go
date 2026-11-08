package guard

import (
	"errors"
	"slices"
	"strings"
)

// Shell is a command-line dialect the guard reads.
type Shell int

// Shell dialects.
const (
	// POSIX is sh/bash/zsh (and Git Bash on Windows): backslash escapes, '...' literal, "..." with $() and `...`.
	POSIX Shell = iota
	// PowerShell: the backtick escapes, '...' literal, "..." with $(), (...) and {...} nest commands.
	PowerShell
	// Cmd is cmd.exe: ^ escapes, "..." literal, %VAR% expands.
	Cmd
)

// AllShells lists every dialect; a line from an unknown shell is checked in all of them.
var allShells = []Shell{POSIX, PowerShell, Cmd}

// maxDepth bounds the nesting of substitutions, wrappers and interpreters the guard follows.
const maxDepth = 12

// errParse means a command line could not be read in a dialect (an unclosed quote or bracket).
var errParse = errors.New("the command line could not be read")

// errDepth means the nesting is deeper than maxDepth; such a line is refused in every dialect.
var errDepth = errors.New("the command line nests too deeply")

// word is one argument of a command.
type word struct {
	// text is the argument with quotes and escapes removed; an expansion keeps its source text ($X, $(...)).
	text string
	// dynamic marks an argument that holds an expansion whose value is not known before the command runs.
	dynamic bool
	// quoted marks an argument that had quotes or escapes (so it is never a shell keyword).
	quoted bool
	// assign marks a NAME=value (or NAME+=value) word whose name and = are unquoted, so that a shell takes it as an
	// assignment: FOO="a b" is one, "FOO=a" is not.
	assign bool
}

// segment is one simple command: the words between separators (; && || | & newline).
type segment struct {
	words []word
	// piped marks a command that reads the output of the previous one (a | before it).
	piped bool
	// prev is the command before a pipe, when piped: a command of the line, or a group.
	prev *segment
	// group, set only on a prev, holds the commands of a subshell or brace group whose output is piped ((...) | sh,
	// { ...; } | sh); its own words are empty.
	group []*segment
	// stdin holds the bodies of the here-documents and here-strings (POSIX <<, <<-, <<<) the command reads: data
	// for most commands, a script for a shell that reads its script from standard input.
	stdin []string
}

// state is shared by a parser and the parsers of the substitutions it reads.
type state struct {
	out []*segment
	// writes are the targets of output redirections (> file, >> file), when they are plain words.
	writes []string
	// heredocs are the byte ranges of the line that hold here-document bodies with their closing delimiter lines
	// (POSIX only, and only those of the line itself, not of a nested script).
	heredocs [][2]int
}

// parser reads one command line (or a part of it) in one dialect.
type parser struct {
	sh    Shell
	s     string
	i     int
	depth int
	st    *state
	// root marks the parser of the whole line (its offsets are the line's), not of a nested text.
	root bool
	// heredocs wait for the next newline, which starts their bodies.
	heredocs []heredoc
}

type heredoc struct {
	delim  string
	quoted bool
	strip  bool
	// seg is the command that reads the body.
	seg *segment
}

// parse splits line into simple commands, including the commands of every substitution, subshell and block.
func parse(line string, sh Shell, depth int) ([]*segment, error) {
	st, err := parseState(line, sh, depth)
	if st == nil {
		return nil, err
	}
	return st.out, err
}

// parseState is parse with the redirection targets.
func parseState(line string, sh Shell, depth int) (*state, error) {
	if depth > maxDepth {
		return nil, errDepth
	}
	st := &state{}
	p := &parser{sh: sh, s: line, depth: depth, st: st, root: true}
	if err := p.list(0); err != nil {
		return st, err
	}
	return st, nil
}

// child returns a parser over s that adds its commands to the same state, one level deeper.
func (p *parser) child(s string) (*parser, error) {
	if p.depth+1 > maxDepth {
		return nil, errDepth
	}
	return &parser{sh: p.sh, s: s, depth: p.depth + 1, st: p.st}, nil
}

// sub parses a nested command list that starts at p.i and ends with term, adding its commands to the state.
func (p *parser) sub(term byte) error {
	if p.depth+1 > maxDepth {
		return errDepth
	}
	p.depth++
	defer func() { p.depth-- }()
	return p.list(term)
}

func (p *parser) peek(off int) byte {
	if p.i+off < len(p.s) {
		return p.s[p.i+off]
	}
	return 0
}

// compoundOpeners are the POSIX words that open a compound command; compoundClosers map each closing word to
// the openers it closes.
var (
	compoundOpeners = map[string]bool{"if": true, "for": true, "while": true, "until": true, "select": true,
		"case": true}
	compoundClosers = map[string][]string{"fi": {"if"}, "done": {"for", "while", "until", "select"},
		"esac": {"case"}}
)

// compound is an open POSIX compound command: the word that opened it and where its commands start in the
// state's commands.
type compound struct {
	open  string
	start int
}

// list reads commands until term (0: the end of the text). The terminator is consumed.
func (p *parser) list(term byte) error {
	cur := &segment{}
	var prev *segment
	// group is a subshell or brace group that just closed: a | right after it pipes the output of its commands.
	var group *segment
	// braces holds where each open POSIX brace group starts in the state's commands; compounds the open compound
	// commands (if..fi, for/while/until/select..done, case..esac), which are groups as well (F16).
	var braces []int
	var compounds []compound
	// inherit gives the commands from p.st.out[start:] that read no input of their own the input of the group
	// that in opened: in echo git stash | (cd x && bash), bash reads what echo prints (F16). in is the command
	// that carried the pipe into the group; a command with its own pipe or here-document keeps it.
	inherit := func(start int, in *segment) {
		if !in.piped {
			return
		}
		for _, m := range p.st.out[start:] {
			if m != in && !m.piped && len(m.stdin) == 0 {
				m.piped, m.prev = true, in.prev
			}
		}
	}
	// groupOf is the group of the commands from p.st.out[start:], piped as in was.
	groupOf := func(start int, in *segment) *segment {
		inherit(start, in)
		return &segment{group: p.st.out[start:len(p.st.out):len(p.st.out)], piped: in.piped, prev: in.prev}
	}
	flush := func(piped bool) {
		var closed *segment
		if len(cur.words) > 0 {
			p.st.out = append(p.st.out, cur)
			prev = cur
			if p.sh == POSIX {
				closed = closeCompound(&compounds, cur, func(start int) *segment {
					return groupOf(start, p.st.out[start])
				})
				for _, w := range cur.words {
					if w.quoted {
						break
					}
					if compoundOpeners[w.text] {
						compounds = append(compounds, compound{open: w.text, start: len(p.st.out) - 1})
					}
					if !keywords[w.text] {
						break
					}
					if w.text == "{" {
						braces = append(braces, len(p.st.out)-1)
					}
				}
			}
		}
		next := &segment{piped: piped}
		if piped {
			next.prev = prev
			switch {
			case closed != nil:
				next.prev = closed // if ...; fi | sh: the output of the compound's commands
			case len(cur.words) == 0 && group != nil:
				next.prev = group
			}
		}
		cur, group = next, nil
	}
	// closeGroup records the commands from p.st.out[start:] as the group that just closed; in is the command that
	// was waiting for its first word when the group opened (it carries the pipe into the group).
	closeGroup := func(start int, in *segment) {
		if start < len(p.st.out) {
			group = groupOf(start, in)
		}
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		if term != 0 && c == term {
			p.i++
			flush(false)
			return nil
		}
		switch {
		case c == '\n':
			p.i++
			flush(false)
			if err := p.readHeredocs(); err != nil {
				return err
			}
			continue
		case c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			p.i++
			continue
		case c == '#' && p.sh != Cmd:
			if p.sh == PowerShell && p.peek(1) == '>' {
				break
			}
			p.skipComment()
			continue
		case c == '<' && p.sh == PowerShell && p.peek(1) == '#':
			if err := p.skipBlockComment(); err != nil {
				return err
			}
			continue
		case c == ';' && p.sh != Cmd:
			p.i++
			flush(false)
			continue
		case c == '|':
			p.i++
			piped := true
			switch p.peek(0) {
			case '|':
				p.i++
				piped = false
			case '&':
				p.i++
			}
			flush(piped)
			continue
		case c == '&':
			p.i++
			if p.peek(0) == '&' {
				p.i++
			} else if p.sh == POSIX && p.peek(0) == '>' {
				// &>file: both streams to a file.
				if err := p.redirect(cur); err != nil {
					return err
				}
				continue
			}
			flush(false)
			continue
		case c == ')' || c == '}':
			// A stray closer (a case pattern, a brace group): a separator.
			p.i++
			flush(false)
			if c == '}' && p.sh == POSIX && len(braces) > 0 {
				start := braces[len(braces)-1]
				braces = braces[:len(braces)-1]
				closeGroup(start, p.st.out[start])
			}
			continue
		case c == '(' && (p.sh == POSIX || p.sh == Cmd):
			// A subshell or a cmd group: its commands stand on their own.
			p.i++
			in := cur
			if len(in.words) > 0 {
				in = &segment{}
			}
			flush(false)
			start := len(p.st.out)
			if err := p.sub(')'); err != nil {
				return err
			}
			closeGroup(start, in)
			continue
		case c == '<' || c == '>':
			if err := p.redirect(cur); err != nil {
				return err
			}
			continue
		case c >= '0' && c <= '9':
			if ok, err := p.fdRedirect(cur); err != nil {
				return err
			} else if ok {
				continue
			}
		}
		w, err := p.word()
		if err != nil {
			return err
		}
		if p.sh == PowerShell && !w.quoted && w.text == "--%" {
			// Stop-parsing: the rest of the line goes to the command as it is.
			end := strings.IndexByte(p.s[p.i:], '\n')
			if end < 0 {
				end = len(p.s) - p.i
			}
			cur.words = append(cur.words, w)
			for _, f := range strings.Fields(p.s[p.i : p.i+end]) {
				cur.words = append(cur.words, word{text: f})
			}
			p.i += end
			continue
		}
		cur.words = append(cur.words, w)
	}
	if term != 0 {
		return errParse
	}
	flush(false)
	return nil
}

// closeCompound closes the open compound command that seg's first word ends (fi, done, esac) and returns its
// group (made by group from where it starts), or nil when seg closes none.
func closeCompound(open *[]compound, seg *segment, group func(start int) *segment) *segment {
	if len(seg.words) == 0 || seg.words[0].quoted {
		return nil
	}
	openers, ok := compoundClosers[seg.words[0].text]
	if !ok {
		return nil
	}
	for j := len(*open) - 1; j >= 0; j-- {
		if slices.Contains(openers, (*open)[j].open) {
			start := (*open)[j].start
			*open = (*open)[:j]
			return group(start)
		}
	}
	return nil
}

// fdRedirect handles a redirection with a file descriptor (2>&1, 2>file) at p.i and reports whether there was one.
func (p *parser) fdRedirect(seg *segment) (bool, error) {
	j := p.i
	for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
		j++
	}
	if j >= len(p.s) || (p.s[j] != '>' && p.s[j] != '<') {
		return false, nil
	}
	p.i = j
	return true, p.redirect(seg)
}

// redirect reads a redirection operator at p.i of command seg; the target of an output redirection goes to the
// state's writes, the body of a here-document or here-string to seg's stdin.
func (p *parser) redirect(seg *segment) error {
	c := p.s[p.i]
	p.i++
	if p.sh == POSIX && c == '<' {
		switch p.peek(0) {
		case '<':
			p.i++
			if p.peek(0) == '<' { // here-string: the next word is the command's input
				p.i++
				p.skipBlanks()
				w, err := p.word()
				if err == nil {
					seg.stdin = append(seg.stdin, w.text)
				}
				return err
			}
			strip := false
			if p.peek(0) == '-' {
				p.i++
				strip = true
			}
			p.skipBlanks()
			start := p.i
			w, err := p.word()
			if err != nil {
				return err
			}
			quoted := strings.ContainsAny(p.s[start:p.i], `'"\`)
			p.heredocs = append(p.heredocs, heredoc{delim: w.text, quoted: quoted, strip: strip, seg: seg})
			return nil
		case '(':
			p.i++
			return p.sub(')')
		}
	}
	if p.sh == POSIX && c == '>' && p.peek(0) == '(' {
		p.i++
		return p.sub(')')
	}
	for p.i < len(p.s) && (p.s[p.i] == '>' || p.s[p.i] == '<' || p.s[p.i] == '|') {
		p.i++
	}
	if p.peek(0) == '&' {
		p.i++
		for p.i < len(p.s) && (p.s[p.i] >= '0' && p.s[p.i] <= '9' || p.s[p.i] == '-') {
			p.i++
		}
		return nil
	}
	p.skipBlanks()
	if p.i >= len(p.s) {
		return nil
	}
	w, err := p.word()
	if err == nil && c == '>' && !w.dynamic && w.text != "" {
		p.st.writes = append(p.st.writes, w.text)
	}
	return err
}

func (p *parser) skipBlanks() {
	for p.i < len(p.s) && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

func (p *parser) skipComment() {
	for p.i < len(p.s) && p.s[p.i] != '\n' {
		p.i++
	}
}

func (p *parser) skipBlockComment() error {
	end := strings.Index(p.s[p.i+2:], "#>")
	if end < 0 {
		return errParse
	}
	p.i += 2 + end + 2
	return nil
}

// readHeredocs reads the bodies of the here-documents opened on the line that just ended and gives each to the
// command that reads it. An unquoted body expands $(...) and `...`, so those are read as commands.
func (p *parser) readHeredocs() error {
	docs := p.heredocs
	p.heredocs = nil
	for _, h := range docs {
		start := p.i
		bodyEnd := len(p.s)
		next := len(p.s)
		for j := p.i; j <= len(p.s); {
			k := strings.IndexByte(p.s[j:], '\n')
			lineEnd := len(p.s)
			if k >= 0 {
				lineEnd = j + k
			}
			line := strings.TrimSuffix(p.s[j:lineEnd], "\r")
			if h.strip {
				line = strings.TrimLeft(line, "\t")
			}
			if line == h.delim {
				bodyEnd = j
				next = lineEnd
				if next < len(p.s) {
					next++
				}
				break
			}
			if k < 0 {
				break
			}
			j = lineEnd + 1
		}
		p.i = next
		if p.root {
			p.st.heredocs = append(p.st.heredocs, [2]int{start, next})
		}
		if h.seg != nil {
			h.seg.stdin = append(h.seg.stdin, p.s[start:bodyEnd])
		}
		if !h.quoted {
			if err := p.expansions(p.s[start:bodyEnd]); err != nil {
				return err
			}
		}
	}
	return nil
}

// expansions reads the command substitutions of text that is otherwise data (an unquoted here-document body).
func (p *parser) expansions(text string) error {
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\':
			i++
		case text[i] == '$' && i+1 < len(text) && text[i+1] == '(':
			c, err := p.child(text)
			if err != nil {
				return err
			}
			c.i = i + 2
			if err := c.list(')'); err != nil {
				return err
			}
			i = c.i - 1
		case text[i] == '`':
			end := strings.IndexByte(text[i+1:], '`')
			if end < 0 {
				return errParse
			}
			if err := p.nested(text[i+1 : i+1+end]); err != nil {
				return err
			}
			i += end + 1
		}
	}
	return nil
}

// nested parses text as a separate command line in the same dialect.
func (p *parser) nested(text string) error {
	c, err := p.child(text)
	if err != nil {
		return err
	}
	return c.list(0)
}

// stops reports whether c ends a word in this dialect.
func (p *parser) stops(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v', '|', '&', '<', '>':
		return true
	case ';':
		return p.sh != Cmd
	case ')':
		return true
	case '(':
		return p.sh != PowerShell
	case '}':
		return p.sh == PowerShell
	}
	return false
}

// word reads one argument at p.i.
func (p *parser) word() (word, error) {
	var b strings.Builder
	w := word{}
	start := p.i
	if p.sh == Cmd && p.peek(0) == '@' {
		p.i++ // @command: echo off for one command
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		if p.stops(c) {
			break
		}
		var err error
		switch p.sh {
		case POSIX:
			err = p.posixChar(&b, &w)
		case PowerShell:
			err = p.pwshChar(&b, &w)
		default:
			p.cmdChar(&b, &w)
		}
		if err != nil {
			return w, err
		}
	}
	if p.i == start && p.i < len(p.s) {
		// A character that stops a word but is not handled by list: take it alone so the parser moves on.
		b.WriteByte(p.s[p.i])
		p.i++
	}
	w.text = b.String()
	w.assign = assignment.MatchString(p.s[start:p.i])
	return w, nil
}

// posixChar reads one unit of a POSIX word.
func (p *parser) posixChar(b *strings.Builder, w *word) error {
	c := p.s[p.i]
	switch c {
	case '\\':
		w.quoted = true
		if p.i+1 < len(p.s) {
			if p.s[p.i+1] != '\n' {
				b.WriteByte(p.s[p.i+1])
			}
			p.i += 2
		} else {
			p.i++
		}
	case '\'':
		w.quoted = true
		end := strings.IndexByte(p.s[p.i+1:], '\'')
		if end < 0 {
			return errParse
		}
		b.WriteString(p.s[p.i+1 : p.i+1+end])
		p.i += end + 2
	case '"':
		w.quoted = true
		p.i++
		for {
			if p.i >= len(p.s) {
				return errParse
			}
			d := p.s[p.i]
			if d == '"' {
				p.i++
				break
			}
			switch {
			case d == '\\' && p.i+1 < len(p.s) && strings.IndexByte("$`\"\\\n", p.s[p.i+1]) >= 0:
				if p.s[p.i+1] != '\n' {
					b.WriteByte(p.s[p.i+1])
				}
				p.i += 2
			case d == '$' || d == '`':
				if err := p.posixExpansion(b, w); err != nil {
					return err
				}
			default:
				b.WriteByte(d)
				p.i++
			}
		}
	case '$':
		if p.peek(1) == '\'' {
			// $'...': ANSI-C quoting.
			w.quoted = true
			p.i += 2
			for {
				if p.i >= len(p.s) {
					return errParse
				}
				d := p.s[p.i]
				if d == '\'' {
					p.i++
					break
				}
				if d == '\\' && p.i+1 < len(p.s) {
					b.WriteByte(ansiEscape(p.s[p.i+1]))
					p.i += 2
					continue
				}
				b.WriteByte(d)
				p.i++
			}
			return nil
		}
		return p.posixExpansion(b, w)
	case '`':
		return p.posixExpansion(b, w)
	default:
		b.WriteByte(c)
		p.i++
	}
	return nil
}

func ansiEscape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	}
	return c
}

// posixExpansion reads $name, ${...}, $(...), $((...)) or `...` at p.i. Its source text stays in the word, which
// becomes dynamic; a command substitution is parsed as commands.
func (p *parser) posixExpansion(b *strings.Builder, w *word) error {
	start := p.i
	c := p.s[p.i]
	switch {
	case c == '`':
		var inner strings.Builder
		p.i++
		for {
			if p.i >= len(p.s) {
				return errParse
			}
			d := p.s[p.i]
			if d == '`' {
				p.i++
				break
			}
			if d == '\\' && p.i+1 < len(p.s) && strings.IndexByte("$`\\", p.s[p.i+1]) >= 0 {
				inner.WriteByte(p.s[p.i+1])
				p.i += 2
				continue
			}
			inner.WriteByte(d)
			p.i++
		}
		if err := p.nested(inner.String()); err != nil {
			return err
		}
	case p.peek(1) == '(':
		p.i += 2
		if err := p.sub(')'); err != nil {
			return err
		}
	case p.peek(1) == '{':
		end := strings.IndexByte(p.s[p.i+2:], '}')
		if end < 0 {
			return errParse
		}
		inner := p.s[p.i+2 : p.i+2+end]
		p.i += end + 3
		// ${x:-$(cmd)} and the like can run commands.
		if err := p.expansions(inner); err != nil {
			return err
		}
	case isNameStart(p.peek(1)) || strings.IndexByte("0123456789@*#?$!-", p.peek(1)) >= 0 && p.peek(1) != 0:
		p.i++
		if isNameStart(p.s[p.i]) {
			for p.i < len(p.s) && isNameChar(p.s[p.i]) {
				p.i++
			}
		} else {
			p.i++
		}
	default:
		b.WriteByte('$')
		p.i++
		return nil
	}
	w.dynamic = true
	b.WriteString(p.s[start:p.i])
	return nil
}

// pwshChar reads one unit of a PowerShell word.
func (p *parser) pwshChar(b *strings.Builder, w *word) error {
	c := p.s[p.i]
	switch c {
	case '`':
		w.quoted = true
		if p.i+1 < len(p.s) {
			if p.s[p.i+1] != '\n' && p.s[p.i+1] != '\r' {
				b.WriteByte(p.s[p.i+1])
			}
			p.i += 2
		} else {
			p.i++
		}
	case '\'':
		w.quoted = true
		p.i++
		for {
			if p.i >= len(p.s) {
				return errParse
			}
			if p.s[p.i] == '\'' {
				if p.peek(1) == '\'' {
					b.WriteByte('\'')
					p.i += 2
					continue
				}
				p.i++
				break
			}
			b.WriteByte(p.s[p.i])
			p.i++
		}
	case '"':
		w.quoted = true
		p.i++
		for {
			if p.i >= len(p.s) {
				return errParse
			}
			d := p.s[p.i]
			if d == '"' {
				if p.peek(1) == '"' {
					b.WriteByte('"')
					p.i += 2
					continue
				}
				p.i++
				break
			}
			switch d {
			case '`':
				if p.i+1 < len(p.s) {
					b.WriteByte(p.s[p.i+1])
				}
				p.i += 2
			case '$':
				if err := p.pwshExpansion(b, w); err != nil {
					return err
				}
			default:
				b.WriteByte(d)
				p.i++
			}
		}
	case '@':
		switch p.peek(1) {
		case '\'', '"':
			return p.hereString(b, w)
		case '(', '{':
			start := p.i
			term := byte(')')
			if p.peek(1) == '{' {
				term = '}'
			}
			p.i += 2
			if err := p.sub(term); err != nil {
				return err
			}
			w.dynamic = true
			b.WriteString(p.s[start:p.i])
		default:
			b.WriteByte(c)
			p.i++
		}
	case '$':
		return p.pwshExpansion(b, w)
	case '(', '{':
		start := p.i
		term := byte(')')
		if c == '{' {
			term = '}'
		}
		p.i++
		if err := p.sub(term); err != nil {
			return err
		}
		w.dynamic = true
		b.WriteString(p.s[start:p.i])
	default:
		b.WriteByte(c)
		p.i++
	}
	return nil
}

// hereString reads a PowerShell here-string @'...'@ or @"..."@ at p.i.
func (p *parser) hereString(b *strings.Builder, w *word) error {
	w.quoted = true
	q := p.s[p.i+1]
	closer := "\n" + string(q) + "@"
	end := strings.Index(p.s[p.i+2:], closer)
	if end < 0 {
		return errParse
	}
	body := p.s[p.i+2 : p.i+2+end]
	p.i += 2 + end + len(closer)
	body = strings.TrimPrefix(strings.TrimPrefix(body, "\r"), "\n")
	if q == '"' && strings.Contains(body, "$(") {
		w.dynamic = true
		c, err := p.child(body)
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(body); i++ {
			if body[i] == '$' && body[i+1] == '(' {
				c.i = i + 2
				if err := c.list(')'); err != nil {
					return err
				}
				i = c.i - 1
			}
		}
	}
	b.WriteString(body)
	return nil
}

// pwshExpansion reads $name, ${...}, $env:X or $(...) at p.i.
func (p *parser) pwshExpansion(b *strings.Builder, w *word) error {
	start := p.i
	switch {
	case p.peek(1) == '(':
		p.i += 2
		if err := p.sub(')'); err != nil {
			return err
		}
	case p.peek(1) == '{':
		end := strings.IndexByte(p.s[p.i+2:], '}')
		if end < 0 {
			return errParse
		}
		p.i += end + 3
	case isNameStart(p.peek(1)) || strings.IndexByte("$?^_", p.peek(1)) >= 0 && p.peek(1) != 0:
		p.i++
		for p.i < len(p.s) && (isNameChar(p.s[p.i]) || p.s[p.i] == ':') {
			p.i++
		}
		if p.i == start+1 {
			p.i++
		}
	default:
		b.WriteByte('$')
		p.i++
		return nil
	}
	w.dynamic = true
	b.WriteString(p.s[start:p.i])
	return nil
}

// cmdChar reads one unit of a cmd.exe word.
func (p *parser) cmdChar(b *strings.Builder, w *word) {
	c := p.s[p.i]
	switch c {
	case '^':
		w.quoted = true
		if p.i+1 < len(p.s) {
			if p.s[p.i+1] != '\n' && p.s[p.i+1] != '\r' {
				b.WriteByte(p.s[p.i+1])
			}
			p.i += 2
		} else {
			p.i++
		}
	case '"':
		w.quoted = true
		end := strings.IndexByte(p.s[p.i+1:], '"')
		if end < 0 {
			// cmd runs a line with an unclosed quote: the rest is quoted.
			p.cmdQuoted(b, w, p.s[p.i+1:])
			p.i = len(p.s)
			return
		}
		p.cmdQuoted(b, w, p.s[p.i+1:p.i+1+end])
		p.i += end + 2
	case '%', '!':
		end := strings.IndexByte(p.s[p.i+1:], c)
		if end > 0 && !strings.ContainsAny(p.s[p.i+1:p.i+1+end], " \t\"") {
			w.dynamic = true
			b.WriteString(p.s[p.i : p.i+end+2])
			p.i += end + 2
			return
		}
		if c == '%' && p.peek(1) == '%' || c == '%' && isNameChar(p.peek(1)) {
			// %%i or %1: a loop or batch variable.
			w.dynamic = true
		}
		b.WriteByte(c)
		p.i++
	default:
		b.WriteByte(c)
		p.i++
	}
}

// cmdQuoted adds the inside of a cmd.exe quoted string; %VAR% still expands there.
func (p *parser) cmdQuoted(b *strings.Builder, w *word, s string) {
	if i := strings.IndexByte(s, '%'); i >= 0 && strings.IndexByte(s[i+1:], '%') > 0 {
		w.dynamic = true
	}
	b.WriteString(s)
}

func isNameStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isNameChar(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }
