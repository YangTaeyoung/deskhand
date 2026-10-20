package qa

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kinds of a YAML node.
const (
	nodeNull = iota
	nodeScalar
	nodeMap
	nodeSeq
)

// node is a parsed YAML value with the line it starts on.
type node struct {
	kind  int
	line  int
	value string
	// quoted marks a quoted or block scalar, which is always text.
	quoted bool
	keys   []*node
	vals   []*node
	items  []*node
}

// yline is a line of the source: its number, its leading spaces and the
// text after them.
type yline struct {
	num    int
	indent int
	text   string
}

// yamlParser parses the YAML subset of docs/local-api.md §10.
type yamlParser struct {
	file  string
	lines []yline
	pos   int
}

func (p *yamlParser) errf(line int, format string, a ...any) error {
	return &ScenarioError{File: p.file, Line: line, Msg: fmt.Sprintf(format, a...)}
}

// parseYAML parses one document of src; file names the source in errors.
func parseYAML(file string, src []byte) (*node, error) {
	p := &yamlParser{file: file}
	if !utf8.Valid(src) {
		return nil, p.errf(1, "시나리오가 UTF-8 글이 아닙니다. UTF-8로 저장해 다시 실행하세요")
	}
	text := strings.TrimPrefix(string(src), "\ufeff")
	raw := strings.Split(text, "\n")
	started, ended := false, false
	for i, r := range raw {
		r = strings.TrimSuffix(r, "\r")
		indent := len(r) - len(strings.TrimLeft(r, " "))
		l := yline{num: i + 1, indent: indent, text: r[indent:]}
		if indent == 0 && isMarker(l.text, "---") {
			if started || ended {
				return nil, p.errf(l.num, "한 파일에는 문서 하나만 씁니다. --- 뒤의 시나리오는 다른 파일로 나누세요")
			}
			l.text = ""
		}
		if indent == 0 && isMarker(l.text, "...") {
			ended = true
			l.text = ""
		}
		if indent == 0 && strings.HasPrefix(l.text, "%") {
			return nil, p.errf(l.num, "YAML 지시문(%%)은 쓸 수 없습니다. 그 줄을 지우세요")
		}
		if !blank(l.text) {
			if ended {
				return nil, p.errf(l.num, "문서가 ...로 끝난 뒤에 내용이 있습니다. 한 파일에는 문서 하나만 씁니다")
			}
			started = true
		}
		p.lines = append(p.lines, l)
	}
	p.skipBlank()
	if p.eof() {
		return &node{kind: nodeNull, line: 1}, nil
	}
	root, err := p.block(p.cur().indent)
	if err != nil {
		return nil, err
	}
	p.skipBlank()
	if !p.eof() {
		return nil, p.indentError(p.cur())
	}
	return root, nil
}

// isMarker reports whether text is the document marker m, alone or
// followed by a comment.
func isMarker(text, m string) bool {
	if !strings.HasPrefix(text, m) {
		return false
	}
	rest := strings.TrimLeft(text[len(m):], " \t")
	return rest == "" || len(rest) < len(text[len(m):]) && rest[0] == '#'
}

// blank reports whether a line holds nothing but spaces, tabs and a comment.
func blank(text string) bool {
	t := strings.TrimLeft(text, " \t")
	return t == "" || t[0] == '#'
}

func (p *yamlParser) eof() bool  { return p.pos >= len(p.lines) }
func (p *yamlParser) cur() yline { return p.lines[p.pos] }
func (p *yamlParser) skipBlank() {
	for !p.eof() && blank(p.cur().text) {
		p.pos++
	}
}
func isSeqItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ") || strings.HasPrefix(text, "-\t")
}
func (p *yamlParser) indentError(l yline) error {
	return p.errf(l.num, "들여쓰기가 맞지 않습니다. 같은 수준의 키와 목록 항목은 같은 칸에서 시작하고, 여러 줄 글은 | 나 >로 쓰세요")
}

func (p *yamlParser) checkTab(l yline) error {
	if strings.HasPrefix(l.text, "\t") {
		return p.errf(l.num, "탭으로 들여 쓸 수 없습니다. 공백으로 들여 쓰세요")
	}
	return nil
}

// block parses the node that starts on the current line, whose indent is
// indent.
func (p *yamlParser) block(indent int) (*node, error) {
	l := p.cur()
	if err := p.checkTab(l); err != nil {
		return nil, err
	}
	if isSeqItem(l.text) {
		return p.seq(indent)
	}
	if _, _, _, ok, err := p.splitKey(l); err != nil {
		return nil, err
	} else if ok {
		return p.mapping(indent)
	}
	if c := l.text[0]; c == '|' || c == '>' {
		return nil, p.errf(l.num, "블록 값(| 또는 >)은 키: 뒤나 - 뒤에만 씁니다")
	}
	p.pos++
	return p.inline(l, l.text, indent)
}

// splitKey splits a "key: value" line into its key and the text after the
// colon; ok is false when the line is not a mapping entry.
func (p *yamlParser) splitKey(l yline) (key string, quoted bool, rest string, ok bool, err error) {
	text := l.text
	switch text[0] {
	case '"', '\'':
		k, after, err := p.quoted(l.num, text)
		if err != nil {
			return "", false, "", false, err
		}
		after = strings.TrimLeft(after, " \t")
		if strings.HasPrefix(after, ":") && (len(after) == 1 || after[1] == ' ' || after[1] == '\t') {
			return k, true, after[1:], true, nil
		}
		return "", false, "", false, nil
	case '[', '{', '#':
		return "", false, "", false, nil
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '#' && i > 0 && (text[i-1] == ' ' || text[i-1] == '\t') {
			return "", false, "", false, nil
		}
		if c == ':' && (i+1 == len(text) || text[i+1] == ' ' || text[i+1] == '\t') {
			key = strings.TrimRight(text[:i], " \t")
			if err := p.indicator(l.num, key); err != nil {
				return "", false, "", false, err
			}
			return key, false, text[i+1:], true, nil
		}
	}
	return "", false, "", false, nil
}

// indicator refuses the YAML features this parser leaves out when s, a
// plain key or value, starts with their indicator.
func (p *yamlParser) indicator(line int, s string) error {
	if s == "" {
		return nil
	}
	switch s[0] {
	case '&':
		return p.errf(line, "앵커(&)는 쓸 수 없습니다. 값을 그대로 다시 쓰거나 vars와 ${이름}을 쓰세요")
	case '*':
		return p.errf(line, "별칭(*)은 쓸 수 없습니다. 값을 그대로 다시 쓰거나 vars와 ${이름}을 쓰세요")
	case '!':
		return p.errf(line, "태그(!)는 쓸 수 없습니다. 태그를 지우고 값만 쓰세요")
	case '?':
		return p.errf(line, "? 키는 쓸 수 없습니다. 키: 값 형식으로 쓰세요")
	case '%', '@', '`':
		return p.errf(line, "%q로 시작하는 값은 따옴표로 감싸세요", s[:1])
	}
	return nil
}

// mapping parses a block mapping whose keys start at indent.
func (p *yamlParser) mapping(indent int) (*node, error) {
	n := &node{kind: nodeMap, line: p.cur().num}
	seen := map[string]bool{}
	for {
		p.skipBlank()
		if p.eof() {
			return n, nil
		}
		l := p.cur()
		if l.indent < indent {
			return n, nil
		}
		if l.indent > indent {
			return nil, p.indentError(l)
		}
		if err := p.checkTab(l); err != nil {
			return nil, err
		}
		if isSeqItem(l.text) {
			return nil, p.errf(l.num, "매핑 안에 목록 항목(-)이 있습니다. 앞 키 아래로 들여 쓰거나 키: 값으로 쓰세요")
		}
		key, quoted, rest, ok, err := p.splitKey(l)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, p.errf(l.num, "%q는 키: 값 형식이 아닙니다. 키 뒤에 \": \"를 쓰세요", cut(l.text, 40))
		}
		if seen[key] {
			return nil, p.errf(l.num, "키 %q가 두 번 나옵니다. 하나만 남기세요", key)
		}
		seen[key] = true
		p.pos++
		val, err := p.value(l, rest, indent)
		if err != nil {
			return nil, err
		}
		n.keys = append(n.keys, &node{kind: nodeScalar, line: l.num, value: key, quoted: quoted})
		n.vals = append(n.vals, val)
	}
}

// value parses the value of a mapping entry on line l: rest is the text
// after the colon and indent the indent of the key.
func (p *yamlParser) value(l yline, rest string, indent int) (*node, error) {
	r := strings.TrimLeft(rest, " \t")
	if r == "" || r[0] == '#' {
		p.skipBlank()
		if !p.eof() {
			next := p.cur()
			if next.indent > indent {
				return p.block(next.indent)
			}
			if next.indent == indent && isSeqItem(next.text) {
				return p.seq(indent)
			}
		}
		return &node{kind: nodeNull, line: l.num}, nil
	}
	if r[0] == '|' || r[0] == '>' {
		return p.blockScalar(l, r, indent)
	}
	return p.inline(l, r, indent)
}

// seq parses a block sequence whose dashes are at indent.
func (p *yamlParser) seq(indent int) (*node, error) {
	n := &node{kind: nodeSeq, line: p.cur().num}
	for {
		p.skipBlank()
		if p.eof() {
			return n, nil
		}
		l := p.cur()
		if l.indent < indent {
			return n, nil
		}
		if l.indent > indent {
			return nil, p.indentError(l)
		}
		if err := p.checkTab(l); err != nil {
			return nil, err
		}
		if !isSeqItem(l.text) {
			return n, nil
		}
		rest := l.text[1:]
		trimmed := strings.TrimLeft(rest, " \t")
		var item *node
		var err error
		switch {
		case trimmed == "" || trimmed[0] == '#':
			p.pos++
			p.skipBlank()
			if !p.eof() && p.cur().indent > indent {
				item, err = p.block(p.cur().indent)
			} else {
				item = &node{kind: nodeNull, line: l.num}
			}
		case trimmed[0] == '|' || trimmed[0] == '>':
			p.pos++
			item, err = p.blockScalar(l, trimmed, indent)
		default:
			col := indent + 1 + len(rest) - len(trimmed)
			p.lines[p.pos].indent = col
			p.lines[p.pos].text = trimmed
			item, err = p.block(col)
		}
		if err != nil {
			return nil, err
		}
		n.items = append(n.items, item)
	}
}

// blockScalar parses a | or > value whose header h is on line l; its
// lines are indented more than indent.
func (p *yamlParser) blockScalar(l yline, h string, indent int) (*node, error) {
	folded := h[0] == '>'
	h = h[1:]
	chomp := byte(0)
	if h != "" && (h[0] == '-' || h[0] == '+') {
		chomp, h = h[0], h[1:]
	}
	if h != "" && h[0] >= '1' && h[0] <= '9' {
		return nil, p.errf(l.num, "블록 값의 들여쓰기 숫자는 쓸 수 없습니다. 숫자를 빼고 내용을 들여 쓰세요")
	}
	if !blank(h) || h != "" && h[0] != ' ' && h[0] != '\t' {
		return nil, p.errf(l.num, "블록 값 표시(| 또는 >) 뒤에는 -, + 또는 주석만 씁니다. 내용은 다음 줄부터 들여 쓰세요")
	}
	var lines []string
	block := -1
	for !p.eof() {
		nl := p.cur()
		if strings.TrimLeft(nl.text, " \t") == "" {
			lines = append(lines, "")
			p.pos++
			continue
		}
		if block < 0 {
			if nl.indent <= indent {
				break
			}
			block = nl.indent
		}
		if nl.indent < block {
			break
		}
		lines = append(lines, strings.Repeat(" ", nl.indent-block)+nl.text)
		p.pos++
	}
	last := len(lines) - 1
	for last >= 0 && lines[last] == "" {
		last--
	}
	body, trailing := lines[:last+1], len(lines)-1-last
	var b strings.Builder
	for i, line := range body {
		switch {
		case i == 0:
		case !folded:
			b.WriteByte('\n')
		case line == "":
			b.WriteByte('\n')
			continue
		case body[i-1] == "":
		case strings.HasPrefix(line, " ") || strings.HasPrefix(body[i-1], " "):
			b.WriteByte('\n')
		default:
			b.WriteByte(' ')
		}
		b.WriteString(line)
	}
	s := b.String()
	switch {
	case chomp == '-' || len(body) == 0:
	case chomp == '+':
		s += strings.Repeat("\n", trailing+1)
	default:
		s += "\n"
	}
	return &node{kind: nodeScalar, line: l.num, value: s, quoted: true}, nil
}

// inline parses a value written on line l from text: a quoted or plain
// scalar, or a flow collection that may go on over the next lines.
func (p *yamlParser) inline(l yline, text string, indent int) (*node, error) {
	if err := p.indicator(l.num, text); err != nil {
		return nil, err
	}
	switch text[0] {
	case '"', '\'':
		v, after, err := p.quoted(l.num, text)
		if err != nil {
			return nil, err
		}
		if !blank(after) || after != "" && after[0] != ' ' && after[0] != '\t' {
			return nil, p.errf(l.num, "따옴표 값 뒤에 %q가 있습니다. 값 전체를 따옴표로 감싸세요", cut(strings.TrimSpace(after), 20))
		}
		return &node{kind: nodeScalar, line: l.num, value: v, quoted: true}, nil
	case '[', '{':
		f := &flowParser{p: p, s: text, line: l.num}
		n, err := f.value()
		if err != nil {
			return nil, err
		}
		f.skipSpace(false)
		if rest := f.s[f.i:]; !blank(rest) {
			return nil, p.errf(f.line, "흐름 값 뒤에 %q가 있습니다. 지우거나 다음 줄로 옮기세요", cut(strings.TrimSpace(rest), 20))
		}
		return n, nil
	}
	v := plain(text)
	if strings.Contains(v, ": ") || strings.HasSuffix(v, ":") {
		return nil, p.errf(l.num, "값 %q에 \": \"가 있습니다. 값을 따옴표로 감싸세요", cut(v, 40))
	}
	return scalarNode(l.num, v), nil
}

// plain returns a plain scalar without its comment and trailing spaces.
func plain(text string) string {
	for i := 1; i < len(text); i++ {
		if text[i] == '#' && (text[i-1] == ' ' || text[i-1] == '\t') {
			text = text[:i]
			break
		}
	}
	return strings.TrimRight(text, " \t")
}

// scalarNode returns the node of a plain scalar: null for "", "~" and
// null.
func scalarNode(line int, v string) *node {
	switch v {
	case "", "~", "null", "Null", "NULL":
		return &node{kind: nodeNull, line: line}
	}
	return &node{kind: nodeScalar, line: line, value: v}
}

// quoted reads the quoted scalar at the start of s and returns it and the
// text after its closing quote.
func (p *yamlParser) quoted(line int, s string) (string, string, error) {
	q := s[0]
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case q == '\'' && c == '\'':
			if i+1 < len(s) && s[i+1] == '\'' {
				b.WriteByte('\'')
				i++
				continue
			}
			return b.String(), s[i+1:], nil
		case q == '"' && c == '"':
			return b.String(), s[i+1:], nil
		case q == '"' && c == '\\':
			if i+1 >= len(s) {
				return "", "", p.errf(line, "큰따옴표 값이 \\로 끝납니다. \\\\로 쓰거나 작은따옴표를 쓰세요")
			}
			r, n, err := unescape(s[i+1:])
			if err != nil {
				return "", "", p.errf(line, "%v. Windows 경로는 작은따옴표로 감싸거나 \\를 \\\\로 쓰세요", err)
			}
			b.WriteString(r)
			i += n
		case c == '\n':
			return "", "", p.errf(line, "따옴표 값이 한 줄 안에서 닫히지 않았습니다. 한 줄에 쓰거나 | 블록 값을 쓰세요")
		default:
			b.WriteByte(c)
		}
	}
	return "", "", p.errf(line, "닫는 %c가 없습니다. 값 끝에 %c를 쓰세요", q, q)
}

// unescape reads the escape after a backslash in s and returns its text and
// the number of bytes it took.
func unescape(s string) (string, int, error) {
	simple := map[byte]string{'0': "\x00", 'a': "\a", 'b': "\b", 't': "\t", '\t': "\t", 'n': "\n", 'v': "\v", 'f': "\f", 'r': "\r",
		'e': "\x1b", ' ': " ", '"': "\"", '/': "/", '\\': "\\", 'N': "\u0085", '_': " ", 'L': " ", 'P': " "}
	if r, ok := simple[s[0]]; ok {
		return r, 1, nil
	}
	size := map[byte]int{'x': 2, 'u': 4, 'U': 8}[s[0]]
	if size == 0 {
		return "", 0, fmt.Errorf("알 수 없는 이스케이프 \\%c입니다", s[0])
	}
	if len(s) < 1+size {
		return "", 0, fmt.Errorf("\\%c 뒤에 16진수 %d자리가 필요합니다", s[0], size)
	}
	v, err := strconv.ParseUint(s[1:1+size], 16, 32)
	if err != nil || !utf8.ValidRune(rune(v)) {
		return "", 0, fmt.Errorf("\\%s는 올바른 문자 코드가 아닙니다", s[:1+size])
	}
	return string(rune(v)), 1 + size, nil
}

// cut shortens s to n runes with "…".
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// flowParser parses a flow collection, reading more lines of p as it
// needs them.
type flowParser struct {
	p    *yamlParser
	s    string
	i    int
	line int
}

// more appends the next source line; it reports false at the end.
func (f *flowParser) more() bool {
	if f.p.eof() {
		return false
	}
	l := f.p.cur()
	f.p.pos++
	f.s += "\n" + strings.Repeat(" ", l.indent) + l.text
	return true
}

// skipSpace skips spaces, comments and, when lines is set, line ends
// (reading more lines as needed).
func (f *flowParser) skipSpace(lines bool) {
	for {
		for f.i < len(f.s) {
			c := f.s[f.i]
			switch {
			case c == ' ' || c == '\t' || c == '\r':
				f.i++
			case c == '#' && (f.i == 0 || f.s[f.i-1] == ' ' || f.s[f.i-1] == '\t' || f.s[f.i-1] == '\n'):
				for f.i < len(f.s) && f.s[f.i] != '\n' {
					f.i++
				}
			case c == '\n' && lines:
				f.i++
				f.line++
			default:
				return
			}
		}
		if !lines || !f.more() {
			return
		}
	}
}

// peek returns the next significant byte, reading more lines; 0 at the end.
func (f *flowParser) peek() byte {
	f.skipSpace(true)
	if f.i >= len(f.s) {
		return 0
	}
	return f.s[f.i]
}

func (f *flowParser) value() (*node, error) {
	c := f.peek()
	switch c {
	case 0:
		return nil, f.p.errf(f.line, "흐름 값이 끝나지 않았습니다. 닫는 ] 나 }를 쓰세요")
	case '[':
		return f.seq()
	case '{':
		return f.mapping()
	case '"', '\'':
		line := f.line
		v, after, err := f.p.quoted(line, f.s[f.i:])
		if err != nil {
			return nil, err
		}
		f.i = len(f.s) - len(after)
		return &node{kind: nodeScalar, line: line, value: v, quoted: true}, nil
	case ',', ']', '}', ':':
		return nil, f.p.errf(f.line, "흐름 값 안의 %q 앞에 값이 없습니다. 값을 쓰거나 %q를 지우세요", string(c), string(c))
	}
	return f.plain()
}

// plain reads a plain scalar of a flow collection.
func (f *flowParser) plain() (*node, error) {
	line := f.line
	start := f.i
	if err := f.p.indicator(line, f.s[start:]); err != nil {
		return nil, err
	}
	for f.i < len(f.s) {
		c := f.s[f.i]
		if c == ',' || c == ']' || c == '}' || c == '\n' {
			break
		}
		if c == '[' || c == '{' {
			return nil, f.p.errf(line, "흐름 값 안의 { } [ ] ,가 든 값은 따옴표로 감싸세요(예: '${이름}\\app.exe')")
		}
		if c == ':' && (f.i+1 == len(f.s) || strings.IndexByte(" \t\n,]}", f.s[f.i+1]) >= 0) {
			break
		}
		if c == '#' && f.i > start && (f.s[f.i-1] == ' ' || f.s[f.i-1] == '\t') {
			break
		}
		f.i++
	}
	return scalarNode(line, strings.TrimRight(f.s[start:f.i], " \t")), nil
}

func (f *flowParser) seq() (*node, error) {
	n := &node{kind: nodeSeq, line: f.line}
	f.i++
	for {
		if f.peek() == ']' {
			f.i++
			return n, nil
		}
		item, err := f.value()
		if err != nil {
			return nil, err
		}
		n.items = append(n.items, item)
		switch f.peek() {
		case ',':
			f.i++
		case ']':
			f.i++
			return n, nil
		case 0:
			return nil, f.p.errf(f.line, "목록이 닫히지 않았습니다. 끝에 ]를 쓰세요")
		default:
			return nil, f.p.errf(f.line, "목록 항목 사이에 ,를 쓰고 끝에 ]를 쓰세요")
		}
	}
}

func (f *flowParser) mapping() (*node, error) {
	n := &node{kind: nodeMap, line: f.line}
	f.i++
	seen := map[string]bool{}
	for {
		if f.peek() == '}' {
			f.i++
			return n, nil
		}
		key, err := f.value()
		if err != nil {
			return nil, err
		}
		if key.kind == nodeNull {
			key = &node{kind: nodeScalar, line: key.line, value: ""}
		}
		if key.kind != nodeScalar {
			return nil, f.p.errf(key.line, "흐름 매핑의 키는 글자여야 합니다. 키: 값 형식으로 쓰세요")
		}
		if f.peek() != ':' {
			return nil, f.p.errf(f.line, "흐름 매핑의 키 %q 뒤에 \": 값\"이 없습니다. 키: 값 형식으로 쓰세요", key.value)
		}
		f.i++
		if seen[key.value] {
			return nil, f.p.errf(key.line, "키 %q가 두 번 나옵니다. 하나만 남기세요", key.value)
		}
		seen[key.value] = true
		var val *node
		if c := f.peek(); c == ',' || c == '}' {
			val = &node{kind: nodeNull, line: f.line}
		} else if val, err = f.value(); err != nil {
			return nil, err
		}
		n.keys = append(n.keys, key)
		n.vals = append(n.vals, val)
		switch f.peek() {
		case ',':
			f.i++
		case '}':
			f.i++
			return n, nil
		case 0:
			return nil, f.p.errf(f.line, "매핑이 닫히지 않았습니다. 끝에 }를 쓰세요")
		default:
			return nil, f.p.errf(f.line, "매핑 항목 사이에 ,를 쓰고 끝에 }를 쓰세요")
		}
	}
}
