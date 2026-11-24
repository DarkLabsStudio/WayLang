package lexer

import (
	"strings"
	"testing"

	"waylang/compiler/token"
)

// kindsOf renders the token kinds as a space-separated string so expectations
// stay readable.
func kindsOf(toks []token.Token) string {
	names := make([]string, 0, len(toks))
	for _, t := range toks {
		names = append(names, t.Kind.String())
	}
	return strings.Join(names, " ")
}

func TestTokenStreams(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "hello world",
			src:  "package main\n\nfunc main():\n    print(\"Hello, World!\")\n",
			want: "package IDENT NEWLINE func IDENT ( ) : NEWLINE INDENT IDENT ( STRING ) NEWLINE DEDENT EOF",
		},
		{
			name: "nested blocks and dedents",
			src:  "if true:\n    if true:\n        x := 1\n    y := 2\nz := 3\n",
			want: "if true : NEWLINE INDENT if true : NEWLINE INDENT IDENT := INT NEWLINE DEDENT IDENT := INT NEWLINE DEDENT IDENT := INT NEWLINE EOF",
		},
		{
			name: "blank and comment lines keep block open",
			src:  "x := 1\n# a comment\n\n    # indented comment\ny := 2\n",
			want: "IDENT := INT NEWLINE IDENT := INT NEWLINE EOF",
		},
		{
			name: "brackets suppress newlines and indentation",
			src:  "x := f(\n  1,\n  2,\n)\n",
			want: "IDENT := IDENT ( INT , INT , ) NEWLINE EOF",
		},
		{
			name: "explicit backslash continuation",
			src:  "x := 1 + \\\n    2\n",
			want: "IDENT := INT + INT NEWLINE EOF",
		},
		{
			name: "operators longest match",
			src:  "a := x <= y && z != w\n",
			want: "IDENT := IDENT <= IDENT && IDENT != IDENT NEWLINE EOF",
		},
		{
			name: "keywords are distinct from identifiers",
			src:  "func foo(): return nil\n",
			want: "func IDENT ( ) : return nil NEWLINE EOF",
		},
		{
			name: "function with typed params and arrow",
			src:  "func add(a: int, b: int) -> int:\n    return a + b\n",
			want: "func IDENT ( IDENT : IDENT , IDENT : IDENT ) -> IDENT : NEWLINE INDENT return IDENT + IDENT NEWLINE DEDENT EOF",
		},
		{
			name: "no trailing newline",
			src:  "x := 1",
			want: "IDENT := INT NEWLINE EOF",
		},
		{
			name: "empty source",
			src:  "",
			want: "EOF",
		},
		{
			name: "nested f-string token",
			src:  "msg := f\"hi {name}!\"\n",
			want: "IDENT := FSTRING NEWLINE EOF",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, diags := Lex("test.way", []byte(tt.src))
			for _, d := range diags {
				t.Errorf("unexpected diagnostic: %s", d.Error())
			}
			if got := kindsOf(toks); got != tt.want {
				t.Errorf("token kinds mismatch\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestLiteralValues(t *testing.T) {
	tests := []struct {
		src   string
		kinds string
		lits  []string
	}{
		{"a := 42\n", "IDENT := INT NEWLINE EOF", []string{"42"}},
		{"a := 0xFF\n", "IDENT := INT NEWLINE EOF", []string{"0xFF"}},
		{"a := 0b1010\n", "IDENT := INT NEWLINE EOF", []string{"0b1010"}},
		{"a := 0o17\n", "IDENT := INT NEWLINE EOF", []string{"0o17"}},
		{"a := 1_000\n", "IDENT := INT NEWLINE EOF", []string{"1000"}},
		{"a := 3.5\n", "IDENT := FLOAT NEWLINE EOF", []string{"3.5"}},
		{"a := 1e9\n", "IDENT := FLOAT NEWLINE EOF", []string{"1e9"}},
		{"a := 2.5e-3\n", "IDENT := FLOAT NEWLINE EOF", []string{"2.5e-3"}},
		{"a := \"hi\\tthere\"\n", "IDENT := STRING NEWLINE EOF", []string{"hi\tthere"}},
		{"a := 'x'\n", "IDENT := RUNE NEWLINE EOF", []string{"x"}},
		{"a := \"line\\nend\"\n", "IDENT := STRING NEWLINE EOF", []string{"line\nend"}},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			toks, diags := Lex("test.way", []byte(tt.src))
			if len(diags) != 0 {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			if got := kindsOf(toks); got != tt.kinds {
				t.Fatalf("kinds mismatch\ngot:  %s\nwant: %s", got, tt.kinds)
			}
			var got []string
			for _, tok := range toks {
				switch tok.Kind {
				case token.Int, token.Float, token.String, token.Rune:
					got = append(got, tok.Lit)
				case token.FString:
					got = append(got, tok.Lit)
				}
			}
			if strings.Join(got, "|") != strings.Join(tt.lits, "|") {
				t.Fatalf("literals mismatch\ngot:  %q\nwant: %q", got, tt.lits)
			}
		})
	}
}

func TestPositions(t *testing.T) {
	src := []byte("x := 1\ny := 2\n")
	toks, _ := Lex("p.way", src)
	// x, :=, 1, NEWLINE, y, :=, 2, NEWLINE, EOF
	if toks[0].Pos.Line != 1 || toks[0].Pos.Column != 1 {
		t.Errorf("x position = %d:%d, want 1:1", toks[0].Pos.Line, toks[0].Pos.Column)
	}
	if toks[1].Pos.Column != 3 {
		t.Errorf(":= column = %d, want 3", toks[1].Pos.Column)
	}
	if toks[4].Pos.Line != 2 || toks[4].Pos.Column != 1 {
		t.Errorf("y position = %d:%d, want 2:1", toks[4].Pos.Line, toks[4].Pos.Column)
	}
}

func TestIndentationErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "mixed tabs and spaces in one indent",
			src:  "if true:\n \tx := 1\n",
			want: "inconsistent indentation",
		},
		{
			name: "indent style changes mid file",
			src:  "if true:\n    x := 1\n    if true:\n\t\ty := 2\n",
			want: "inconsistent indentation",
		},
		{
			name: "unindent to unknown level",
			src:  "if true:\n        x := 1\n    y := 2\n",
			want: "unindent does not match",
		},
		{
			name: "unterminated string",
			src:  "x := \"abc\n",
			want: "unterminated string",
		},
		{
			name: "unexpected character",
			src:  "x := @\n",
			want: "unexpected character",
		},
		{
			name: "unterminated rune",
			src:  "r := 'ab'\n",
			want: "exactly one character",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := Lex("bad.way", []byte(tt.src))
			if len(diags) == 0 {
				t.Fatalf("expected an error containing %q, got none", tt.want)
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.Msg, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected an error containing %q, got %v", tt.want, diags)
			}
		})
	}
}

func TestTabsUseEightColumnStops(t *testing.T) {
	// A tab-indented block is legal as long as the style is consistent.
	src := "if true:\n\tx := 1\n\tif true:\n\t\ty := 2\n"
	_, diags := Lex("tabs.way", []byte(src))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for consistently tab-indented file: %v", diags)
	}
}
