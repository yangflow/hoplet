package rules

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// This file reads a shell command well enough to refuse what it cannot read. It is not a shell:
// it splits a command into simple commands, notes every construct that can change what runs
// (control), and looks into substitutions and the wrappers deny and ask rules see through.

const (
	maxNesting   = 6 // command substitutions inside command substitutions
	maxShellHops = 3 // sh -c inside sh -c
	maxWrappers  = 8 // env sudo env ...
)

type word struct {
	text   string
	expand bool // $ expansion, substitution, backticks or ANSI-C quoting
	glob   bool // unquoted glob or brace expansion
	tilde  bool // unquoted leading ~
	quoted bool // any quote or escape
}

type redirect struct {
	op      string
	target  string
	write   bool
	heredoc bool
}

type simple struct {
	argv     []word
	assigns  int
	redirs   []redirect
	pipeline int
	stage    int
}

type script struct {
	cmds       []simple
	control    []string // why an allow must not match
	unreadable bool     // an unbalanced quote, a missing parenthesis, nesting too deep
}

type pstate struct{ pipelines int }

var assignRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\+?=`)

var leadingReserved = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "do": true, "while": true, "until": true, "!": true, "{": true,
}

type parser struct {
	s     string
	i     int
	depth int
	st    *pstate
	out   script

	cmd      simple
	buf      strings.Builder
	w        word
	inWord   bool
	quoteAt  int // length of buf when the first quote or escape began, -1 if none
	pend     *redirect
	pipeline int
	stage    int
	lastPipe bool

	unquotedBrace bool
}

func parseScript(s string, depth int, st *pstate) script {
	p := &parser{s: s, depth: depth, st: st, quoteAt: -1}
	st.pipelines++
	p.pipeline = st.pipelines
	p.run()
	return p.out
}

func (p *parser) ctl(reason string) {
	for _, r := range p.out.control {
		if r == reason {
			return
		}
	}
	p.out.control = append(p.out.control, reason)
}

func (p *parser) startWord() {
	p.inWord = true
}

func (p *parser) quote() {
	p.startWord()
	if p.quoteAt < 0 {
		p.quoteAt = p.buf.Len()
	}
	p.w.quoted = true
}

func (p *parser) add(s string) {
	p.startWord()
	p.buf.WriteString(s)
}

func (p *parser) endWord() {
	if !p.inWord {
		return
	}
	w := p.w
	w.text = p.buf.String()
	if strings.Contains(w.text, "{") && (strings.Contains(w.text, ",") || strings.Contains(w.text, "..")) && p.unquotedBrace {
		w.glob = true
	}
	eq := strings.Index(w.text, "=")
	assignment := len(p.cmd.argv) == p.cmd.assigns && eq > 0 && assignRe.MatchString(w.text) &&
		(p.quoteAt < 0 || p.quoteAt > eq)
	p.buf.Reset()
	p.w = word{}
	p.inWord = false
	p.quoteAt = -1
	p.unquotedBrace = false
	if p.pend != nil {
		p.pend.target = w.text
		p.cmd.redirs = append(p.cmd.redirs, *p.pend)
		p.pend = nil
		return
	}
	p.cmd.argv = append(p.cmd.argv, w)
	if assignment {
		p.cmd.assigns++
	}
}

// endCommand closes the simple command being read.
func (p *parser) endCommand() {
	c := p.cmd
	p.cmd = simple{}
	for len(c.argv) > c.assigns && !c.argv[c.assigns].quoted && leadingReserved[c.argv[c.assigns].text] {
		c.argv = append(c.argv[:c.assigns:c.assigns], c.argv[c.assigns+1:]...)
	}
	if len(c.argv) == 0 && len(c.redirs) == 0 {
		return
	}
	if c.assigns > 0 {
		p.ctl("leading assignment")
	}
	if len(c.argv) > c.assigns {
		cw := c.argv[c.assigns]
		if cw.expand {
			p.ctl("expansion in the command word")
		}
		if cw.glob {
			p.ctl("glob in the command word")
		}
		if cw.tilde {
			p.ctl("tilde in the command word")
		}
	}
	c.pipeline, c.stage = p.pipeline, p.stage
	p.out.cmds = append(p.out.cmds, c)
	p.lastPipe = false
}

const (
	sepPlain = iota
	sepPipe
	sepParen
)

func (p *parser) sep(kind int) {
	p.endWord()
	p.endCommand()
	switch kind {
	case sepPipe:
		p.stage++
		p.lastPipe = true
	case sepParen:
		if !p.lastPipe {
			p.newPipeline()
		}
	default:
		p.newPipeline()
	}
}

func (p *parser) newPipeline() {
	p.st.pipelines++
	p.pipeline, p.stage, p.lastPipe = p.st.pipelines, 0, false
}

func (p *parser) merge(inner string) {
	if p.depth+1 > maxNesting {
		p.out.unreadable = true
		return
	}
	sub := parseScript(inner, p.depth+1, p.st)
	p.out.cmds = append(p.out.cmds, sub.cmds...)
	if sub.unreadable {
		p.out.unreadable = true
	}
}

func (p *parser) run() {
	s := p.s
	for p.i < len(s) {
		c := s[p.i]
		switch c {
		case ' ', '\t', '\r':
			p.endWord()
			p.i++
		case '\n':
			p.ctl("newline")
			p.sep(sepPlain)
			p.i++
		case '\\':
			if p.i+1 >= len(s) {
				p.out.unreadable = true
				p.ctl("unparseable text")
				p.i++
				break
			}
			if s[p.i+1] == '\n' {
				p.ctl("line continuation")
				p.i += 2
				break
			}
			p.quote()
			p.add(string(s[p.i+1]))
			p.i += 2
		case '\'':
			p.quote()
			end := strings.IndexByte(s[p.i+1:], '\'')
			if end < 0 {
				p.out.unreadable = true
				p.ctl("unparseable text")
				p.add(s[p.i+1:])
				p.i = len(s)
				break
			}
			p.add(s[p.i+1 : p.i+1+end])
			p.i += end + 2
		case '"':
			p.quote()
			p.i++
			p.doubleQuoted()
		case '`':
			p.startWord()
			p.backtick()
		case '$':
			p.dollar(false)
		case ';':
			p.ctl(";")
			p.sep(sepPlain)
			p.i++
		case '&':
			switch {
			case strings.HasPrefix(s[p.i:], "&&"):
				p.ctl("&&")
				p.sep(sepPlain)
				p.i += 2
			case strings.HasPrefix(s[p.i:], "&>>"):
				p.redirect("&>>", true, 3)
			case strings.HasPrefix(s[p.i:], "&>"):
				p.redirect("&>", true, 2)
			default:
				p.ctl("&")
				p.sep(sepPlain)
				p.i++
			}
		case '|':
			switch {
			case strings.HasPrefix(s[p.i:], "||"):
				p.ctl("||")
				p.sep(sepPlain)
				p.i += 2
			case strings.HasPrefix(s[p.i:], "|&"):
				p.ctl("|")
				p.sep(sepPipe)
				p.i += 2
			default:
				p.ctl("|")
				p.sep(sepPipe)
				p.i++
			}
		case '(', ')':
			p.ctl("subshell")
			p.sep(sepParen)
			p.i++
		case '>', '<':
			p.fdDigits()
			p.operator()
		case '#':
			if p.inWord {
				p.add("#")
				p.i++
				break
			}
			p.ctl("comment")
			for p.i < len(s) && s[p.i] != '\n' {
				p.i++
			}
		case '*', '?', '[':
			p.w.glob = true
			p.add(string(c))
			p.i++
		case '{':
			p.unquotedBrace = true
			p.add("{")
			p.i++
		case '~':
			if !p.inWord {
				p.w.tilde = true
			}
			p.add("~")
			p.i++
		default:
			p.add(string(c))
			p.i++
		}
	}
	p.sep(sepPlain)
	if p.pend != nil {
		p.out.unreadable = true
	}
	if p.out.unreadable {
		p.ctl("unparseable text")
	}
}

// fdDigits drops a file descriptor number written right before a redirection (the 2 of 2>).
func (p *parser) fdDigits() {
	if p.inWord && !p.w.quoted {
		t := p.buf.String()
		if _, err := strconv.Atoi(t); err == nil {
			p.buf.Reset()
			p.w = word{}
			p.inWord = false
			p.quoteAt = -1
			return
		}
	}
	p.endWord()
}

func (p *parser) redirect(op string, write bool, n int) {
	p.endWord()
	p.ctl("redirection")
	p.pend = &redirect{op: op, write: write}
	p.i += n
}

func (p *parser) operator() {
	s := p.s[p.i:]
	switch {
	case strings.HasPrefix(s, "<<<"):
		p.redirect("<<<", false, 3)
	case strings.HasPrefix(s, "<<-"), strings.HasPrefix(s, "<<"):
		op := "<<"
		if strings.HasPrefix(s, "<<-") {
			op = "<<-"
		}
		p.ctl("heredoc")
		p.redirect(op, false, len(op))
		p.pend.heredoc = true
	case strings.HasPrefix(s, "<("), strings.HasPrefix(s, ">("):
		p.ctl("process substitution")
		p.i++
	case strings.HasPrefix(s, ">>"):
		p.redirect(">>", true, 2)
	case strings.HasPrefix(s, ">&"):
		p.redirect(">&", true, 2)
	case strings.HasPrefix(s, ">|"):
		p.redirect(">|", true, 2)
	case strings.HasPrefix(s, "<&"):
		p.redirect("<&", false, 2)
	case strings.HasPrefix(s, "<>"):
		p.redirect("<>", true, 2)
	case s[0] == '>':
		p.redirect(">", true, 1)
	default:
		p.redirect("<", false, 1)
	}
}

// doubleQuoted reads up to the closing quote; p.i is just after the opening one.
func (p *parser) doubleQuoted() {
	s := p.s
	for p.i < len(s) {
		switch c := s[p.i]; c {
		case '"':
			p.i++
			return
		case '\\':
			if p.i+1 < len(s) && strings.IndexByte("$`\"\\\n", s[p.i+1]) >= 0 {
				if s[p.i+1] != '\n' {
					p.add(string(s[p.i+1]))
				}
				p.i += 2
				continue
			}
			p.add("\\")
			p.i++
		case '$':
			p.dollar(true)
		case '`':
			p.backtick()
		default:
			p.add(string(c))
			p.i++
		}
	}
	p.out.unreadable = true
	p.ctl("unparseable text")
}

func (p *parser) backtick() {
	s := p.s
	var inner strings.Builder
	j := p.i + 1
	closed := false
	for j < len(s) {
		if s[j] == '\\' && j+1 < len(s) && strings.IndexByte("`$\\", s[j+1]) >= 0 {
			inner.WriteByte(s[j+1])
			j += 2
			continue
		}
		if s[j] == '`' {
			closed = true
			break
		}
		inner.WriteByte(s[j])
		j++
	}
	p.w.expand = true
	p.ctl("backtick")
	p.add(s[p.i:min(j+1, len(s))])
	p.merge(inner.String())
	if !closed {
		p.out.unreadable = true
	}
	p.i = min(j+1, len(s))
}

// scanGroup finds the end of a group opened at s[open] (with the given closing byte), skipping
// quotes and nested groups; it returns the index of the closing byte or -1.
func scanGroup(s string, open int, closer byte) int {
	opener := s[open]
	depth := 0
	for j := open; j < len(s); j++ {
		switch c := s[j]; c {
		case '\\':
			j++
		case '\'':
			if k := strings.IndexByte(s[j+1:], '\''); k >= 0 {
				j += k + 1
			} else {
				return -1
			}
		case '"':
			for j++; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' {
					j++
				}
			}
		case opener:
			depth++
		case closer:
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

func (p *parser) dollar(inDQ bool) {
	s := p.s
	next := byte(0)
	if p.i+1 < len(s) {
		next = s[p.i+1]
	}
	switch {
	case next == '(':
		end := scanGroup(s, p.i+1, ')')
		p.w.expand = true
		p.ctl("command substitution")
		if end < 0 {
			p.add(s[p.i:])
			p.merge(s[p.i+2:])
			p.out.unreadable = true
			p.i = len(s)
			return
		}
		p.add(s[p.i : end+1])
		p.merge(s[p.i+2 : end])
		p.i = end + 1
	case next == '{':
		end := scanGroup(s, p.i+1, '}')
		p.w.expand = true
		p.ctl("parameter expansion")
		if end < 0 {
			p.add(s[p.i:])
			p.out.unreadable = true
			p.i = len(s)
			return
		}
		p.add(s[p.i : end+1])
		p.merge(s[p.i+2 : end])
		p.i = end + 1
	case next == '\'' && !inDQ:
		p.quote()
		j := p.i + 2
		for j < len(s) && s[j] != '\'' {
			if s[j] == '\\' {
				j++
			}
			j++
		}
		p.w.expand = true
		p.ctl("ANSI-C quoting")
		if j >= len(s) {
			p.out.unreadable = true
			p.add(ansiC(s[p.i+2:]))
			p.i = len(s)
			return
		}
		p.add(ansiC(s[p.i+2 : j]))
		p.i = j + 1
	case next == '"' && !inDQ:
		p.i++ // $"..." is a double-quoted string
	case next == '_' || next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z':
		j := p.i + 1
		for j < len(s) && (s[j] == '_' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
			j++
		}
		p.w.expand = true
		p.add(s[p.i:j])
		p.i = j
	case next != 0 && strings.IndexByte("0123456789@*#?!$-", next) >= 0:
		p.w.expand = true
		p.add(s[p.i : p.i+2])
		p.i += 2
	default:
		p.add("$")
		p.i++
	}
}

// ansiC decodes the escapes of $'...' that can spell a command name.
func ansiC(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch c := s[i]; {
		case c == 'n':
			b.WriteByte('\n')
		case c == 't':
			b.WriteByte('\t')
		case c == 'r':
			b.WriteByte('\r')
		case c == 'x':
			j := i + 1
			for j < len(s) && j < i+3 && isHex(s[j]) {
				j++
			}
			if v, err := strconv.ParseUint(s[i+1:j], 16, 8); err == nil && j > i+1 {
				b.WriteByte(byte(v))
				i = j - 1
			} else {
				b.WriteString("\\x")
			}
		case c >= '0' && c <= '7':
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			v, _ := strconv.ParseUint(s[i:j], 8, 16)
			b.WriteByte(byte(v))
			i = j - 1
		case c == 'u' || c == 'U':
			n := 4
			if c == 'U' {
				n = 8
			}
			j := i + 1
			for j < len(s) && j < i+1+n && isHex(s[j]) {
				j++
			}
			if v, err := strconv.ParseUint(s[i+1:j], 16, 32); err == nil && j > i+1 {
				b.WriteRune(rune(v))
				i = j - 1
			}
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// ---- views: what a deny or ask rule sees ----

// view is one command a deny or ask rule is tested against: a simple command, or the command
// inside a wrapper or an sh -c script.
type view struct {
	argv   []string
	piped  []string // names of the commands that receive its output through a pipe
	redirs []redirect
}

func (s script) texts(c simple) []string {
	out := make([]string, 0, len(c.argv))
	for _, w := range c.argv[c.assigns:] {
		out = append(out, w.text)
	}
	return out
}

// views lists every command of the script, with the wrappers and shell scripts looked into.
func (s script) views(shDepth int) []view {
	exp := make([][]view, len(s.cmds))
	for i, c := range s.cmds {
		exp[i] = expandArgv(s.texts(c), c.redirs, shDepth)
	}
	var out []view
	for i, c := range s.cmds {
		var after []string
		for j, d := range s.cmds {
			if j != i && d.pipeline == c.pipeline && d.stage > c.stage {
				for _, v := range exp[j] {
					after = append(after, base(v.argv))
				}
			}
		}
		for _, v := range exp[i] {
			v.piped = append(v.piped, after...)
			out = append(out, v)
		}
	}
	return out
}

func base(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return filepath.Base(argv[0])
}

func expandArgv(argv []string, redirs []redirect, shDepth int) []view {
	if len(argv) == 0 {
		return []view{{redirs: redirs}}
	}
	out := []view{{argv: argv, redirs: redirs}}
	if g := stripGitOptions(argv); g != nil {
		out = append(out, view{argv: g, redirs: redirs})
	}
	cur := argv
	for hop := 0; hop < maxWrappers && len(cur) > 0; hop++ {
		inner, scriptText, isShell := unwrap(cur)
		if isShell {
			if shDepth < maxShellHops {
				st := &pstate{}
				sub := parseScript(scriptText, 0, st)
				out = append(out, sub.views(shDepth+1)...)
			}
			break
		}
		if inner == nil {
			break
		}
		cur = inner
		out = append(out, view{argv: cur, redirs: redirs})
		if g := stripGitOptions(cur); g != nil {
			out = append(out, view{argv: g, redirs: redirs})
		}
	}
	return out
}

// stripGitOptions returns `git <subcommand> ...` for `git -C dir -c k=v --no-pager <subcommand> ...`
// so a rule on `git push` also sees `git -C /repo push`; nil when there is nothing to strip.
func stripGitOptions(argv []string) []string {
	if len(argv) < 2 || filepath.Base(argv[0]) != "git" || !strings.HasPrefix(argv[1], "-") {
		return nil
	}
	i := 1
	for i < len(argv) && strings.HasPrefix(argv[i], "-") {
		switch argv[i] {
		case "-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path", "--super-prefix":
			i++
		}
		i++
	}
	if i >= len(argv) {
		return nil
	}
	return append([]string{argv[0]}, argv[i:]...)
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}

// unwrap looks through one wrapper: it returns the command it runs, or for a shell the script
// it runs.
func unwrap(argv []string) (inner []string, scriptText string, isShell bool) {
	name := filepath.Base(argv[0])
	a := argv[1:]
	skipOpts := func(withArg string) []string {
		for len(a) > 0 {
			o := a[0]
			if o == "--" {
				return a[1:]
			}
			if len(o) < 2 || o[0] != '-' {
				return a
			}
			if strings.Contains(withArg, " "+o+" ") && len(a) > 1 {
				a = a[2:]
			} else {
				a = a[1:]
			}
		}
		return a
	}
	switch {
	case name == "env":
		for len(a) > 0 {
			o := a[0]
			switch {
			case o == "--":
				a = a[1:]
				return nonEmpty(a), "", false
			case o == "-S" || o == "--split-string":
				if len(a) > 1 {
					return nonEmpty(append(strings.Fields(a[1]), a[2:]...)), "", false
				}
				return nil, "", false
			case o == "-u" || o == "-C" || o == "--unset" || o == "--chdir":
				if len(a) < 2 {
					return nil, "", false
				}
				a = a[2:]
			case len(o) > 1 && o[0] == '-':
				a = a[1:]
			case assignRe.MatchString(o):
				a = a[1:]
			default:
				return nonEmpty(a), "", false
			}
		}
		return nil, "", false
	case name == "command" || name == "nohup" || name == "time" || name == "exec":
		withArg := ""
		if name == "exec" {
			withArg = " -a "
		}
		return nonEmpty(skipOpts(withArg)), "", false
	case name == "timeout":
		rest := skipOpts(" -s -k ")
		if len(rest) < 1 {
			return nil, "", false
		}
		return nonEmpty(rest[1:]), "", false
	case name == "sudo" || name == "doas":
		rest := skipOpts(" -u -g -h -p -C -D -R -T -U -r -t ")
		for len(rest) > 0 && assignRe.MatchString(rest[0]) {
			rest = rest[1:]
		}
		return nonEmpty(rest), "", false
	case name == "xargs":
		return nonEmpty(skipOpts(" -I -n -P -L -d -E -s -a ")), "", false
	case name == "eval":
		return nil, strings.Join(a, " "), len(a) > 0
	case shells[name]:
		for i := 0; i < len(a); i++ {
			o := a[i]
			switch {
			case o == "--":
				return nil, "", false
			case o == "-o" || o == "+o" || o == "-O" || o == "+O":
				i++
			case len(o) > 1 && o[0] == '-' && o[1] != '-' && strings.Contains(o, "c"):
				if i+1 < len(a) {
					return nil, a[i+1], true
				}
				return nil, "", false
			case len(o) > 1 && (o[0] == '-' || o[0] == '+'):
			default:
				return nil, "", false
			}
		}
	}
	return nil, "", false
}

func nonEmpty(a []string) []string {
	if len(a) == 0 {
		return nil
	}
	return a
}
