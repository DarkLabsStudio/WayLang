// Package token defines the lexical vocabulary of WayLang.
//
// The lexer produces a flat stream of tokens; the parser consumes it. Tokens
// carry their source position so that every later stage can report precise
// diagnostics.
package token

import "fmt"

// Kind is the lexical class of a token.
type Kind int

const (
	Illegal Kind = iota
	EOF
	Newline // logical end of statement
	Indent  // start of an indented block
	Dedent  // end of an indented block

	Ident   // name
	Int     // 42, 0xFF, 0b1010, 1_000
	Float   // 3.14, 1e9
	String  // "text"
	FString // f"text {expr}"
	Rune    // 'a'

	// Keywords.
	Package
	Import
	Func
	Return
	If
	Elif
	Else
	For
	While
	Switch
	Case
	Default
	Break
	Continue
	Struct
	Interface
	Const
	True
	False
	Nil
	Defer
	In
	Type // reserved for future type aliases

	// Operators and punctuation.
	Plus      // +
	Minus     // -
	Star      // *
	Slash     // /
	Percent   // %
	Assign    // =
	Define    // :=
	Eq        // ==
	Ne        // !=
	Lt        // <
	Le        // <=
	Gt        // >
	Ge        // >=
	And       // &&
	Or        // ||
	Not       // !
	Amp       // &
	Pipe      // |
	Caret     // ^
	Shl       // <<
	Shr       // >>
	PlusEq    // +=
	MinusEq   // -=
	StarEq    // *=
	SlashEq   // /=
	PercentEq // %=
	Arrow     // ->
	Inc       // ++
	Dec       // --
	Dot       // .
	Comma     // ,
	Colon     // :
	Semicolon // ;
	LParen    // (
	RParen    // )
	LBracket  // [
	RBracket  // ]
	LBrace    // {
	RBrace    // }
)

// MaxKind exists so that lookup tables can be sized without magic numbers.
const MaxKind = RBrace + 1

var kindNames = map[Kind]string{
	Illegal: "ILLEGAL", EOF: "EOF", Newline: "NEWLINE", Indent: "INDENT", Dedent: "DEDENT",
	Ident: "IDENT", Int: "INT", Float: "FLOAT", String: "STRING", FString: "FSTRING", Rune: "RUNE",
	Package: "package", Import: "import", Func: "func", Return: "return", If: "if", Elif: "elif",
	Else: "else", For: "for", While: "while", Switch: "switch", Case: "case", Default: "default",
	Break: "break", Continue: "continue", Struct: "struct", Interface: "interface", Const: "const",
	True: "true", False: "false", Nil: "nil", Defer: "defer", In: "in", Type: "type",
	Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%", Assign: "=", Define: ":=",
	Eq: "==", Ne: "!=", Lt: "<", Le: "<=", Gt: ">", Ge: ">=", And: "&&", Or: "||", Not: "!",
	Amp: "&", Pipe: "|", Caret: "^", Shl: "<<", Shr: ">>", PlusEq: "+=", MinusEq: "-=",
	StarEq: "*=", SlashEq: "/=", PercentEq: "%=", Arrow: "->", Inc: "++", Dec: "--",
	Dot: ".", Comma: ",", Colon: ":", Semicolon: ";", LParen: "(", RParen: ")",
	LBracket: "[", RBracket: "]", LBrace: "{", RBrace: "}",
}

// String returns a human-readable name for the kind, used by diagnostics.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// IsKeyword reports whether k denotes a reserved word.
func (k Kind) IsKeyword() bool {
	return k >= Package && k <= Type
}

// Keywords maps source text to keyword kinds.
var Keywords = map[string]Kind{
	"package":   Package,
	"import":    Import,
	"func":      Func,
	"return":    Return,
	"if":        If,
	"elif":      Elif,
	"else":      Else,
	"for":       For,
	"while":     While,
	"switch":    Switch,
	"case":      Case,
	"default":   Default,
	"break":     Break,
	"continue":  Continue,
	"struct":    Struct,
	"interface": Interface,
	"const":     Const,
	"true":      True,
	"false":     False,
	"nil":       Nil,
	"defer":     Defer,
	"in":        In,
}

// Position is a point in a source file. Lines and columns are 1-based; Offset
// is a 0-based byte offset, used for caret rendering.
type Position struct {
	File   string
	Line   int
	Column int
	Offset int
}

func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
}

// IsValid reports whether the position refers to an actual location.
func (p Position) IsValid() bool { return p.Line > 0 }

// Token is a single lexical unit.
type Token struct {
	Kind   Kind
	Lexeme string   // exact source text
	Lit    string   // decoded literal value (strings, f-strings) or raw numeral
	Pos    Position // position of the first character
	End    Position // position just past the last character
}

// Text returns the token text suitable for messages.
func (t Token) Text() string {
	switch t.Kind {
	case Newline:
		return "end of line"
	case Indent:
		return "indent"
	case Dedent:
		return "dedent"
	case EOF:
		return "end of file"
	}
	if t.Lexeme != "" {
		return t.Lexeme
	}
	return t.Kind.String()
}

// Describe renders a token for inclusion in parser diagnostics.
func (t Token) Describe() string {
	if t.Kind == Ident || t.Kind == Int || t.Kind == Float || t.Kind == String || t.Kind == Rune || t.Kind == FString {
		return fmt.Sprintf("%s %q", t.Kind.String(), t.Lexeme)
	}
	return fmt.Sprintf("%q", t.Text())
}

// OpPrecedence returns the binding power of binary operators. A zero value
// means the kind is not a binary operator.
func OpPrecedence(k Kind) int {
	switch k {
	case Or:
		return 1
	case And:
		return 2
	case Eq, Ne:
		return 3
	case Lt, Le, Gt, Ge:
		return 4
	case Plus, Minus, Pipe, Caret:
		return 5
	case Star, Slash, Percent, Shl, Shr, Amp:
		return 6
	}
	return 0
}
