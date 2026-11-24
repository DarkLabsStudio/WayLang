// Package lexer turns WayLang source text into a token stream.
//
// WayLang uses Python-style significant indentation. The lexer is therefore
// responsible for maintaining a stack of indentation widths and emitting
// Indent/Dedent tokens, as well as Newline tokens at logical line ends.
// Newlines inside (), [] and {} are ignored, which lets long expressions span
// lines without continuation markers.
package lexer

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"waylang/compiler/diag"
	"waylang/compiler/token"
)

const tabWidth = 8

// Lexer scans a single source file.
type Lexer struct {
	file string
	src  []byte

	off  int // byte offset
	line int // 1-based
	col  int // 1-based, measured in runes

	indents []int // indentation width stack; always starts with 0
	tokens  []token.Token
	diags   diag.List

	parenDepth  int
	atLineStart bool
	prevKind    token.Kind
	lineTokens  int // tokens emitted on the current logical line

	indentStyle byte // 0, ' ' or '\t': first non-zero indentation style seen
}

// New creates a lexer for the given file name and source bytes.
func New(file string, src []byte) *Lexer {
	return &Lexer{
		file:        file,
		src:         src,
		line:        1,
		col:         1,
		indents:     []int{0},
		atLineStart: true,
	}
}

// Lex scans source and returns its tokens plus any diagnostics. The returned
// token slice always ends with an EOF token.
func Lex(file string, src []byte) ([]token.Token, []diag.Diagnostic) {
	l := New(file, src)
	toks := l.Scan()
	return toks, l.diags.Diags
}

// Diagnostics returns diagnostics gathered so far.
func (l *Lexer) Diagnostics() []diag.Diagnostic { return l.diags.Diags }

func (l *Lexer) eof() bool { return l.off >= len(l.src) }

func (l *Lexer) cur() byte {
	if l.eof() {
		return 0
	}
	return l.src[l.off]
}

func (l *Lexer) peek(n int) byte {
	i := l.off + n
	if i >= len(l.src) {
		return 0
	}
	return l.src[i]
}

// advance consumes one rune and updates the position.
func (l *Lexer) advance() rune {
	if l.eof() {
		return 0
	}
	r, size := utf8.DecodeRune(l.src[l.off:])
	l.off += size
	if r == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return r
}

func (l *Lexer) pos() token.Position {
	return token.Position{File: l.file, Line: l.line, Column: l.col, Offset: l.off}
}

func (l *Lexer) emit(kind token.Kind, start token.Position, startOff int, lit string) {
	text := string(l.src[startOff:l.off])
	text = strings.TrimRight(text, "\r\n")
	l.tokens = append(l.tokens, token.Token{
		Kind:   kind,
		Lexeme: text,
		Lit:    lit,
		Pos:    start,
		End:    l.pos(),
	})
	l.prevKind = kind
	if kind != token.Newline && kind != token.Indent && kind != token.Dedent && kind != token.EOF {
		l.lineTokens++
	}
}

func (l *Lexer) errorAt(pos token.Position, format string, args ...any) {
	l.diags.Errorf(pos, format, args...)
}

// Scan runs the lexer to completion.
func (l *Lexer) Scan() []token.Token {
	for !l.eof() {
		if l.atLineStart {
			l.atLineStart = false
			if l.scanLineStart() {
				continue
			}
		}
		l.skipHorizontalSpace()
		if l.eof() {
			break
		}
		c := l.cur()
		switch {
		case c == '\n':
			l.endLine()
			continue
		case c == '\r' && l.peek(1) == '\n':
			l.endLine()
			continue
		case c == '\\' && (l.peek(1) == '\n' || (l.peek(1) == '\r' && l.peek(2) == '\n')):
			// Explicit line continuation: consume the backslash and newline
			// without emitting Newline, then skip the next line's indentation.
			l.advance()
			if l.cur() == '\r' {
				l.advance()
			}
			if l.cur() == '\n' {
				l.advance()
			}
			l.skipHorizontalSpace()
			continue
		case c == '#':
			l.skipToEOL()
			continue
		case c == '/' && l.peek(1) == '/':
			l.skipToEOL()
			continue
		}
		l.scanToken()
	}

	l.finish()
	return l.tokens
}

// skipHorizontalSpace consumes spaces, tabs and form feeds.
func (l *Lexer) skipHorizontalSpace() {
	for !l.eof() {
		switch l.cur() {
		case ' ', '\t', '\f', '\v':
			l.advance()
		default:
			return
		}
	}
}

func (l *Lexer) skipToEOL() {
	for !l.eof() && l.cur() != '\n' {
		l.advance()
	}
}

// endLine consumes a newline. Outside brackets it terminates the logical line
// and arms indentation scanning for the next one.
func (l *Lexer) endLine() {
	if l.cur() == '\r' {
		l.advance()
	}
	if l.cur() == '\n' {
		l.advance()
	}
	if l.parenDepth > 0 {
		return
	}
	if l.lineTokens > 0 && l.prevKind != token.Newline {
		pos := l.pos()
		l.emit(token.Newline, pos, l.off, "")
	}
	l.lineTokens = 0
	l.atLineStart = true
}

// scanLineStart processes indentation at the start of a logical line.
// It returns true when the line was blank or comment-only (already consumed).
func (l *Lexer) scanLineStart() bool {
	start := l.pos()
	startOff := l.off
	width := 0
	hasSpace, hasTab := false, false
loop:
	for !l.eof() {
		switch l.cur() {
		case ' ':
			width++
			hasSpace = true
			l.advance()
		case '\t':
			width += tabWidth - width%tabWidth
			hasTab = true
			l.advance()
		case '\f', '\v':
			l.advance()
		default:
			break loop
		}
	}

	if hasSpace && hasTab {
		l.errorAt(start, "inconsistent indentation: mixing tabs and spaces in the same indent")
	} else if width > 0 {
		style := byte(' ')
		if hasTab {
			style = '\t'
		}
		if l.indentStyle == 0 {
			l.indentStyle = style
		} else if l.indentStyle != style {
			what := "spaces"
			if style == '\t' {
				what = "tabs"
			}
			earlier := "spaces"
			if l.indentStyle == '\t' {
				earlier = "tabs"
			}
			l.errorAt(start, "inconsistent indentation: this block is indented with %s but earlier blocks used %s", what, earlier)
		}
	}

	// Blank line or comment-only line: consume it and do not touch indentation.
	if l.eof() {
		return true
	}
	if c := l.cur(); c == '\n' || c == '\r' {
		l.skipToEOL()
		if l.cur() == '\r' {
			l.advance()
		}
		if l.cur() == '\n' {
			l.advance()
		}
		l.atLineStart = true
		return true
	}
	if l.cur() == '#' || (l.cur() == '/' && l.peek(1) == '/') {
		l.skipToEOL()
		if l.cur() == '\r' {
			l.advance()
		}
		if l.cur() == '\n' {
			l.advance()
		}
		l.atLineStart = true
		return true
	}

	top := l.indents[len(l.indents)-1]
	switch {
	case width > top:
		l.indents = append(l.indents, width)
		l.emit(token.Indent, start, startOff, "")
	case width < top:
		for len(l.indents) > 1 && width < l.indents[len(l.indents)-1] {
			l.indents = l.indents[:len(l.indents)-1]
			l.emit(token.Dedent, start, startOff, "")
		}
		if width != l.indents[len(l.indents)-1] {
			l.errorAt(start, "unindent does not match any outer indentation level")
			// Recover by adopting the closest level so parsing may continue.
			l.indents[len(l.indents)-1] = width
		}
	}
	l.skipHorizontalSpace()
	return false
}

// finish emits pending dedents and the EOF token.
func (l *Lexer) finish() {
	if l.lineTokens > 0 && l.prevKind != token.Newline {
		pos := l.pos()
		l.emit(token.Newline, pos, l.off, "")
	}
	pos := l.pos()
	for len(l.indents) > 1 {
		l.indents = l.indents[:len(l.indents)-1]
		l.emit(token.Dedent, pos, l.off, "")
	}
	l.tokens = append(l.tokens, token.Token{Kind: token.EOF, Pos: pos, End: pos})
	l.prevKind = token.EOF
}

// --- token scanning -------------------------------------------------------

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func (l *Lexer) decode() rune {
	if l.eof() {
		return 0
	}
	r, _ := utf8.DecodeRune(l.src[l.off:])
	return r
}

func (l *Lexer) scanToken() {
	start := l.pos()
	startOff := l.off
	c := l.cur()

	switch {
	case c == 'f' && l.peek(1) == '"':
		l.advance() // f
		l.scanString(start, startOff, true)
		return
	case c == '"':
		l.scanString(start, startOff, false)
		return
	case c == '\'':
		l.scanRune(start, startOff)
		return
	case c >= '0' && c <= '9':
		l.scanNumber(start, startOff)
		return
	case c == '_' || isIdentStart(l.decode()):
		for !l.eof() && isIdentPart(l.decode()) {
			l.advance()
		}
		text := string(l.src[startOff:l.off])
		if kw, ok := token.Keywords[text]; ok {
			l.emit(kw, start, startOff, "")
			return
		}
		l.emit(token.Ident, start, startOff, text)
		return
	}

	// Operators and punctuation, longest match first.
	two := ""
	if l.off+1 < len(l.src) {
		two = string(l.src[l.off : l.off+2])
	}
	twoChar := map[string]token.Kind{
		":=": token.Define, "->": token.Arrow, "==": token.Eq, "!=": token.Ne,
		"<=": token.Le, ">=": token.Ge, "&&": token.And, "||": token.Or,
		"++": token.Inc, "--": token.Dec, "+=": token.PlusEq, "-=": token.MinusEq,
		"*=": token.StarEq, "/=": token.SlashEq, "%=": token.PercentEq,
		"<<": token.Shl, ">>": token.Shr,
	}
	if k, ok := twoChar[two]; ok {
		l.advance()
		l.advance()
		l.emit(k, start, startOff, "")
		return
	}

	single := map[byte]token.Kind{
		'+': token.Plus, '-': token.Minus, '*': token.Star, '/': token.Slash,
		'%': token.Percent, '=': token.Assign, '<': token.Lt, '>': token.Gt,
		'!': token.Not, '&': token.Amp, '|': token.Pipe, '^': token.Caret,
		'.': token.Dot, ',': token.Comma, ':': token.Colon, ';': token.Semicolon,
		'(': token.LParen, ')': token.RParen, '[': token.LBracket,
		']': token.RBracket, '{': token.LBrace, '}': token.RBrace,
	}
	if k, ok := single[c]; ok {
		switch k {
		case token.LParen, token.LBracket, token.LBrace:
			l.parenDepth++
		case token.RParen, token.RBracket, token.RBrace:
			if l.parenDepth > 0 {
				l.parenDepth--
			} else {
				l.errorAt(start, "unmatched %q", string(c))
			}
		}
		l.advance()
		l.emit(k, start, startOff, "")
		return
	}

	r := l.advance()
	l.errorAt(start, "unexpected character %q", string(r))
	l.emit(token.Illegal, start, startOff, string(r))
}

func (l *Lexer) scanNumber(start token.Position, startOff int) {
	if l.cur() == '0' {
		switch l.peek(1) {
		case 'x', 'X':
			l.advance()
			l.advance()
			if !isHex(l.cur()) {
				l.errorAt(start, "hexadecimal literal has no digits")
			}
			for isHex(l.cur()) || l.cur() == '_' {
				l.advance()
			}
			l.finishNumber(start, startOff)
			return
		case 'b', 'B':
			l.advance()
			l.advance()
			if l.cur() != '0' && l.cur() != '1' {
				l.errorAt(start, "binary literal has no digits")
			}
			for l.cur() == '0' || l.cur() == '1' || l.cur() == '_' {
				l.advance()
			}
			l.finishNumber(start, startOff)
			return
		case 'o', 'O':
			l.advance()
			l.advance()
			if l.cur() < '0' || l.cur() > '7' {
				l.errorAt(start, "octal literal has no digits")
			}
			for (l.cur() >= '0' && l.cur() <= '7') || l.cur() == '_' {
				l.advance()
			}
			l.finishNumber(start, startOff)
			return
		}
	}
	for isDigit(l.cur()) || l.cur() == '_' {
		l.advance()
	}
	if l.cur() == '.' && isDigit(l.peek(1)) {
		l.advance()
		for isDigit(l.cur()) || l.cur() == '_' {
			l.advance()
		}
	}
	if l.cur() == 'e' || l.cur() == 'E' {
		save := l.off
		saveLine, saveCol := l.line, l.col
		l.advance()
		if l.cur() == '+' || l.cur() == '-' {
			l.advance()
		}
		if !isDigit(l.cur()) {
			// Not an exponent after all (e.g. an identifier follows); rewind.
			l.off = save
			l.line, l.col = saveLine, saveCol
		} else {
			for isDigit(l.cur()) || l.cur() == '_' {
				l.advance()
			}
		}
	}
	l.finishNumber(start, startOff)
}

func (l *Lexer) finishNumber(start token.Position, startOff int) {
	text := string(l.src[startOff:l.off])
	lit := strings.ReplaceAll(text, "_", "")
	if isFloatText(lit) {
		l.emit(token.Float, start, startOff, lit)
		return
	}
	l.emit(token.Int, start, startOff, lit)
}

func isFloatText(s string) bool {
	return strings.ContainsAny(s, ".eE") && !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X")
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHex(c byte) bool {
	return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

// scanString scans a double-quoted string. When interp is true the token is an
// FString; its Lit holds the raw inner text so the parser can split literal
// segments from interpolation expressions.
func (l *Lexer) scanString(start token.Position, startOff int, interp bool) {
	l.advance() // opening quote
	var sb strings.Builder
	terminated := false
	for !l.eof() {
		c := l.cur()
		if c == '\n' || c == '\r' {
			break
		}
		if c == '\\' {
			sb.WriteByte(c)
			l.advance()
			if l.eof() {
				break
			}
			sb.WriteByte(l.cur())
			l.advance()
			continue
		}
		if c == '"' {
			l.advance()
			terminated = true
			break
		}
		sb.WriteRune(l.advance())
	}
	if !terminated {
		l.errorAt(start, "unterminated string literal")
	}
	if interp {
		l.emit(token.FString, start, startOff, sb.String())
		return
	}
	decoded, err := token.Unescape(sb.String())
	if err != nil {
		l.errorAt(start, "%s", err.Error())
		decoded = sb.String()
	}
	l.emit(token.String, start, startOff, decoded)
}

func (l *Lexer) scanRune(start token.Position, startOff int) {
	l.advance() // opening quote
	var sb strings.Builder
	count := 0
	terminated := false
	for !l.eof() {
		c := l.cur()
		if c == '\n' || c == '\r' {
			break
		}
		if c == '\\' {
			sb.WriteByte(c)
			l.advance()
			if l.eof() {
				break
			}
			sb.WriteByte(l.cur())
			l.advance()
			count++
			continue
		}
		if c == '\'' {
			l.advance()
			terminated = true
			break
		}
		sb.WriteRune(l.advance())
		count++
	}
	if !terminated {
		l.errorAt(start, "unterminated rune literal")
	} else if count != 1 {
		l.errorAt(start, "rune literal must contain exactly one character")
	}
	decoded, err := token.Unescape(sb.String())
	if err != nil {
		l.errorAt(start, "%s", err.Error())
		decoded = sb.String()
	}
	l.emit(token.Rune, start, startOff, decoded)
}
