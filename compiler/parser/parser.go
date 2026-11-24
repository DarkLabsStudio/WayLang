// Package parser builds a WayLang AST from tokens.
//
// The parser is a hand-written recursive descent parser with precedence
// climbing for binary expressions. Every syntax error becomes a diagnostic
// carrying a source position; the parser recovers at statement boundaries so
// that a single mistake does not hide the rest of the file.
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"waylang/compiler/ast"
	"waylang/compiler/diag"
	"waylang/compiler/lexer"
	"waylang/compiler/token"
)

const maxDepth = 400

// ParseFile parses one source file.
func ParseFile(path string, src []byte) (*ast.File, []diag.Diagnostic) {
	toks, lexDiags := lexer.Lex(path, src)
	p := &Parser{file: path, src: src, toks: toks}
	p.diags.Diags = append(p.diags.Diags, lexDiags...)
	f := p.parseFile()
	return f, p.diags.Diags
}

// ParseExprAt parses a single expression from src, mapping positions onto base.
// It is used for f-string interpolations.
func ParseExprAt(file string, src string, base token.Position) (ast.Expr, []diag.Diagnostic) {
	toks, lexDiags := lexer.Lex(file, []byte(src))
	for i := range toks {
		if toks[i].Pos.Line == 0 {
			continue
		}
		toks[i].Pos.Line = base.Line
		toks[i].End.Line = base.Line
		toks[i].Pos.Column = base.Column + (toks[i].Pos.Column - 1)
		toks[i].End.Column = base.Column + (toks[i].End.Column - 1)
	}
	p := &Parser{file: file, src: []byte(src), toks: toks}
	p.diags.Diags = append(p.diags.Diags, lexDiags...)
	expr := p.parseExpr(0)
	if !p.at(token.Newline) && !p.at(token.EOF) {
		p.errorf(p.cur().Pos, "unexpected %s after expression", p.cur().Describe())
	}
	return expr, p.diags.Diags
}

// Parser holds parsing state.
type Parser struct {
	file  string
	src   []byte
	toks  []token.Token
	i     int
	depth int
	diags diag.List
}

// --- token helpers --------------------------------------------------------

func (p *Parser) cur() token.Token {
	if p.i >= len(p.toks) {
		if len(p.toks) == 0 {
			return token.Token{Kind: token.EOF}
		}
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.i]
}

func (p *Parser) peek(n int) token.Token {
	i := p.i + n
	if i >= len(p.toks) || i < 0 {
		if len(p.toks) == 0 {
			return token.Token{Kind: token.EOF}
		}
		return p.toks[len(p.toks)-1]
	}
	return p.toks[i]
}

func (p *Parser) at(k token.Kind) bool { return p.cur().Kind == k }

func (p *Parser) advance() token.Token {
	t := p.cur()
	if p.i < len(p.toks) {
		p.i++
	}
	return t
}

func (p *Parser) accept(k token.Kind) (token.Token, bool) {
	if p.at(k) {
		return p.advance(), true
	}
	return token.Token{}, false
}

// expect consumes a token of the given kind, reporting an error otherwise.
func (p *Parser) expect(k token.Kind, context string) token.Token {
	if p.at(k) {
		return p.advance()
	}
	t := p.cur()
	p.errorfHint(t.Pos, fmt.Sprintf("insert %q", k.String()),
		"expected '%s' %s, found %s", k.String(), context, t.Describe())
	return token.Token{Kind: k, Pos: t.Pos, End: t.Pos}
}

func (p *Parser) errorf(pos token.Position, format string, args ...any) {
	p.diags.Errorf(pos, format, args...)
}

func (p *Parser) errorfHint(pos token.Position, hint, format string, args ...any) {
	p.diags.ErrorfHint(pos, hint, format, args...)
}

func (p *Parser) skipNewlines() {
	for p.at(token.Newline) {
		p.advance()
	}
}

// endStatement consumes a statement terminator.
func (p *Parser) endStatement() {
	if p.at(token.Newline) {
		p.advance()
		return
	}
	if p.at(token.Dedent) || p.at(token.EOF) {
		return
	}
	// A compound statement (one that ends with a block) is terminated by its
	// closing Dedent rather than by a Newline token.
	if p.i > 0 {
		switch p.toks[p.i-1].Kind {
		case token.Dedent, token.Newline:
			return
		}
	}
	t := p.cur()
	p.errorfHint(t.Pos, "add a newline after the statement", "unexpected %s at end of statement", t.Describe())
	// Recover: skip to the end of the logical line.
	for !p.at(token.Newline) && !p.at(token.EOF) && !p.at(token.Dedent) {
		p.advance()
	}
	if p.at(token.Newline) {
		p.advance()
	}
}

// --- file and declarations ------------------------------------------------

func (p *Parser) parseFile() *ast.File {
	f := &ast.File{Path: p.file}
	p.skipNewlines()
	if !p.at(token.Package) {
		p.errorf(p.cur().Pos, "every WayLang file must start with a package declaration, e.g. \"package main\"")
		p.synchronize()
	}
	if p.at(token.Package) {
		p.advance()
		name := p.expect(token.Ident, "after 'package'")
		f.Package = name.Lexeme
		f.PackagePos = name.Pos
		p.endStatement()
	}
	for p.at(token.Import) {
		f.Imports = append(f.Imports, p.parseImport())
	}
	for !p.at(token.EOF) {
		p.skipNewlines()
		if p.at(token.EOF) {
			break
		}
		switch p.cur().Kind {
		case token.Func:
			f.Decls = append(f.Decls, p.parseFuncDecl())
		case token.Struct:
			f.Decls = append(f.Decls, p.parseStructDecl())
		case token.Interface:
			f.Decls = append(f.Decls, p.parseInterfaceDecl())
		case token.Const:
			f.Decls = append(f.Decls, p.parseConstDecl())
		case token.Ident:
			f.Decls = append(f.Decls, p.parseGlobalDecl())
		case token.Dedent:
			// Stray dedent: consume to keep making progress.
			p.advance()
		default:
			p.errorf(p.cur().Pos, "expected a top-level declaration (func, struct, interface, const or var), found %s", p.cur().Describe())
			p.synchronize()
		}
	}
	return f
}

func (p *Parser) parseImport() *ast.ImportDecl {
	pos := p.expect(token.Import, "at import").Pos
	d := &ast.ImportDecl{Pos0: pos}
	if p.at(token.Ident) {
		d.Alias = p.advance().Lexeme
	}
	if !p.at(token.String) {
		p.errorf(p.cur().Pos, "expected an import path string, found %s", p.cur().Describe())
		p.endStatement()
		return d
	}
	t := p.advance()
	d.Path = t.Lit
	if strings.HasPrefix(d.Path, "std/") {
		p.errorfHint(t.Pos, "import the package name directly, e.g. \"fmt\"", "standard library paths are written without the std/ prefix")
	}
	p.endStatement()
	return d
}

func (p *Parser) parseFuncDecl() *ast.FuncDecl {
	pos := p.expect(token.Func, "at function declaration").Pos
	d := &ast.FuncDecl{Pos0: pos}
	if p.at(token.LParen) {
		// Method receiver: func (recv: Type) name(...)
		save := p.i
		p.advance()
		if p.at(token.Ident) {
			name := p.advance()
			if _, ok := p.accept(token.Colon); ok {
				typ := p.parseType()
				p.expect(token.RParen, "to close the method receiver")
				d.Recv = &ast.Param{Pos0: name.Pos, Name: name.Lexeme, Type: typ}
			} else {
				p.i = save
			}
		} else {
			p.i = save
		}
	}
	name := p.expect(token.Ident, "as function name")
	d.Name = name.Lexeme
	d.Params = p.parseParams()
	d.Results = p.parseResults()
	d.Body = p.parseSuite()
	return d
}

func (p *Parser) parseParams() []*ast.Param {
	var params []*ast.Param
	if _, ok := p.accept(token.LParen); !ok {
		p.errorf(p.cur().Pos, "expected '(' to start the parameter list, found %s", p.cur().Describe())
		return params
	}
	for !p.at(token.RParen) && !p.at(token.EOF) {
		if p.at(token.Newline) {
			p.advance()
			continue
		}
		name := p.expect(token.Ident, "as parameter name")
		param := &ast.Param{Pos0: name.Pos, Name: name.Lexeme}
		if _, ok := p.accept(token.Colon); ok {
			param.Type = p.parseType()
		}
		if _, ok := p.accept(token.Assign); ok {
			param.Default = p.parseExpr(0)
		}
		params = append(params, param)
		if _, ok := p.accept(token.Comma); ok {
			continue
		}
		if !p.at(token.RParen) {
			p.errorf(p.cur().Pos, "expected ',' or ')' in parameter list, found %s", p.cur().Describe())
			// Skip to the next plausible boundary.
			for !p.at(token.Comma) && !p.at(token.RParen) && !p.at(token.EOF) && !p.at(token.Newline) {
				p.advance()
			}
		}
	}
	p.expect(token.RParen, "to close the parameter list")
	return params
}

func (p *Parser) parseResults() []ast.TypeExpr {
	if _, ok := p.accept(token.Arrow); !ok {
		return nil
	}
	var results []ast.TypeExpr
	if _, ok := p.accept(token.LParen); ok {
		for !p.at(token.RParen) && !p.at(token.EOF) {
			if p.at(token.Newline) {
				p.advance()
				continue
			}
			results = append(results, p.parseType())
			if _, ok := p.accept(token.Comma); !ok {
				break
			}
		}
		p.expect(token.RParen, "to close the result list")
		return results
	}
	results = append(results, p.parseType())
	return results
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	pos := p.expect(token.Struct, "at struct declaration").Pos
	name := p.expect(token.Ident, "as struct name")
	d := &ast.StructDecl{Pos0: pos, Name: name.Lexeme}
	p.expect(token.Colon, "after struct name")
	p.expect(token.Newline, "after ':'")
	if _, ok := p.accept(token.Indent); !ok {
		p.errorf(p.cur().Pos, "expected an indented block of struct fields")
		return d
	}
	for !p.at(token.Dedent) && !p.at(token.EOF) {
		p.skipNewlines()
		if p.at(token.Dedent) {
			break
		}
		fpos := p.cur().Pos
		fname := p.expect(token.Ident, "as field name")
		p.expect(token.Colon, "after field name")
		ftype := p.parseType()
		field := &ast.Field{Pos0: fpos, Name: fname.Lexeme, Type: ftype}
		if _, ok := p.accept(token.Assign); ok {
			field.Default = p.parseExpr(0)
		}
		d.Fields = append(d.Fields, field)
		p.endStatement()
	}
	p.expect(token.Dedent, "to close the struct body")
	return d
}

func (p *Parser) parseInterfaceDecl() *ast.InterfaceDecl {
	pos := p.expect(token.Interface, "at interface declaration").Pos
	name := p.expect(token.Ident, "as interface name")
	d := &ast.InterfaceDecl{Pos0: pos, Name: name.Lexeme}
	p.expect(token.Colon, "after interface name")
	p.expect(token.Newline, "after ':'")
	if _, ok := p.accept(token.Indent); !ok {
		p.errorf(p.cur().Pos, "expected an indented block of method signatures")
		return d
	}
	for !p.at(token.Dedent) && !p.at(token.EOF) {
		p.skipNewlines()
		if p.at(token.Dedent) {
			break
		}
		mpos := p.cur().Pos
		mname := p.expect(token.Ident, "as method name")
		sig := &ast.MethodSig{Pos0: mpos, Name: mname.Lexeme}
		sig.Params = p.parseParams()
		sig.Results = p.parseResults()
		p.endStatement()
		d.Methods = append(d.Methods, sig)
	}
	p.expect(token.Dedent, "to close the interface body")
	return d
}

func (p *Parser) parseConstDecl() *ast.ConstDecl {
	pos := p.expect(token.Const, "at const declaration").Pos
	name := p.expect(token.Ident, "as constant name")
	d := &ast.ConstDecl{Pos0: pos, Name: name.Lexeme}
	if _, ok := p.accept(token.Colon); ok {
		d.Type = p.parseType()
		if !p.at(token.Assign) && !p.at(token.Define) {
			p.errorf(p.cur().Pos, "constant %q must have a value", d.Name)
			p.endStatement()
			return d
		}
	}
	if _, ok := p.accept(token.Define); !ok {
		p.expect(token.Assign, "before constant value")
	}
	d.Value = p.parseExpr(0)
	p.endStatement()
	return d
}

func (p *Parser) parseGlobalDecl() *ast.GlobalDecl {
	pos := p.cur().Pos
	name := p.expect(token.Ident, "as variable name")
	p.expect(token.Colon, "after variable name")
	d := &ast.GlobalDecl{Pos0: pos, Name: name.Lexeme, Type: p.parseType()}
	if _, ok := p.accept(token.Assign); ok {
		d.Value = p.parseExpr(0)
	}
	p.endStatement()
	return d
}

// --- statements -----------------------------------------------------------

// parseSuite parses `: NEWLINE INDENT stmt+ DEDENT`.
func (p *Parser) parseSuite() *ast.BlockStmt {
	block := &ast.BlockStmt{Pos0: p.cur().Pos}
	p.expect(token.Colon, "before the block")
	if p.at(token.Newline) {
		p.advance()
	} else {
		p.errorfHint(p.cur().Pos, "put the first statement of the block on its own indented line",
			"expected a newline after ':', found %s", p.cur().Describe())
	}
	if _, ok := p.accept(token.Indent); !ok {
		p.errorfHint(p.cur().Pos, "indent the block body by four spaces",
			"expected an indented block, found %s", p.cur().Describe())
		return block
	}
	for !p.at(token.Dedent) && !p.at(token.EOF) {
		p.skipNewlines()
		if p.at(token.Dedent) || p.at(token.EOF) {
			break
		}
		before := p.i
		if s := p.parseStmt(); s != nil {
			block.Stmts = append(block.Stmts, s)
		}
		if p.i == before {
			// Guarantee progress.
			p.advance()
		}
	}
	p.expect(token.Dedent, "to close the block")
	return block
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.cur().Kind {
	case token.Const:
		return p.parseConstDecl()
	case token.Return:
		return p.parseReturn()
	case token.If:
		return p.parseIf()
	case token.While:
		return p.parseWhile()
	case token.For:
		return p.parseFor()
	case token.Switch:
		return p.parseSwitch()
	case token.Break:
		pos := p.advance().Pos
		p.endStatement()
		return &ast.BreakStmt{Pos0: pos}
	case token.Continue:
		pos := p.advance().Pos
		p.endStatement()
		return &ast.ContinueStmt{Pos0: pos}
	case token.Defer:
		pos := p.advance().Pos
		call := p.parseExpr(0)
		if _, ok := call.(*ast.CallExpr); !ok {
			p.errorf(pos, "defer requires a function call")
		}
		p.endStatement()
		return &ast.DeferStmt{Pos0: pos, Call: call}
	case token.Func:
		p.errorf(p.cur().Pos, "function declarations are only allowed at the top level; use an anonymous function here")
		p.synchronize()
		return nil
	case token.Newline:
		p.advance()
		return nil
	}

	if p.looksLikeDefine() {
		return p.parseDefine()
	}
	if p.looksLikeVarDecl() {
		return p.parseVarDecl()
	}
	return p.parseSimpleStmt()
}

func (p *Parser) looksLikeDefine() bool {
	if !p.at(token.Ident) {
		return false
	}
	i := p.i
	if p.toks[i].Kind != token.Ident {
		return false
	}
	i++
	for i < len(p.toks) && p.toks[i].Kind == token.Comma {
		i++
		if i >= len(p.toks) || p.toks[i].Kind != token.Ident {
			return false
		}
		i++
	}
	return i < len(p.toks) && p.toks[i].Kind == token.Define
}

func (p *Parser) looksLikeVarDecl() bool {
	if !p.at(token.Ident) || p.peek(1).Kind != token.Colon {
		return false
	}
	switch p.peek(2).Kind {
	case token.Ident, token.LBracket, token.Func:
		return true
	}
	return false
}

func (p *Parser) parseDefine() ast.Stmt {
	pos := p.cur().Pos
	var names []string
	for {
		name := p.expect(token.Ident, "as variable name")
		names = append(names, name.Lexeme)
		if _, ok := p.accept(token.Comma); !ok {
			break
		}
	}
	p.expect(token.Define, "in short variable declaration")
	values := p.parseExprList()
	s := &ast.DefineStmt{Pos0: pos, Names: names, Values: values}
	if len(values) > 1 && len(names) != len(values) {
		p.errorf(pos, "declaration defines %d variables but supplies %d values", len(names), len(values))
	}
	p.endStatement()
	return s
}

func (p *Parser) parseVarDecl() ast.Stmt {
	pos := p.cur().Pos
	name := p.advance()
	d := &ast.VarDeclStmt{Pos0: pos, Name: name.Lexeme}
	p.expect(token.Colon, "after variable name")
	d.Type = p.parseType()
	if _, ok := p.accept(token.Assign); ok {
		d.Value = p.parseExpr(0)
	}
	p.endStatement()
	return d
}

func (p *Parser) parseSimpleStmt() ast.Stmt {
	s := p.parseSimpleStmtOnly()
	p.endStatement()
	return s
}

func (p *Parser) parseSimpleStmtOnly() ast.Stmt {
	pos := p.cur().Pos
	first := p.parseExpr(0)

	targets := []ast.Expr{first}
	for p.at(token.Comma) {
		p.advance()
		targets = append(targets, p.parseExpr(0))
	}

	if p.at(token.Inc) || p.at(token.Dec) {
		if len(targets) != 1 {
			p.errorf(pos, "'%s' requires exactly one target", p.cur().Kind.String())
		}
		op := token.Plus
		if p.at(token.Dec) {
			op = token.Minus
		}
		opPos := p.advance().Pos
		one := &ast.IntLit{Pos0: opPos, Value: 1, Raw: "1"}
		return &ast.AssignStmt{Pos0: pos, Targets: targets, Op: op, Values: []ast.Expr{one}}
	}

	switch p.cur().Kind {
	case token.Assign, token.PlusEq, token.MinusEq, token.StarEq, token.SlashEq, token.PercentEq:
		op := p.advance().Kind
		values := p.parseExprList()
		if len(values) != len(targets) {
			p.errorf(pos, "assignment has %d targets but %d values", len(targets), len(values))
		}
		return &ast.AssignStmt{Pos0: pos, Targets: targets, Op: op, Values: values}
	}

	if len(targets) > 1 {
		p.errorf(pos, "expected '=' after multiple assignment targets")
	}
	return &ast.ExprStmt{Pos0: pos, X: first}
}

func (p *Parser) parseReturn() ast.Stmt {
	pos := p.expect(token.Return, "at return").Pos
	s := &ast.ReturnStmt{Pos0: pos}
	if !p.at(token.Newline) && !p.at(token.EOF) && !p.at(token.Dedent) {
		s.Values = p.parseExprList()
	}
	p.endStatement()
	return s
}

func (p *Parser) parseIf() ast.Stmt {
	pos := p.expect(token.If, "at if").Pos
	cond := p.parseExpr(0)
	s := &ast.IfStmt{Pos0: pos, Cond: cond, Then: p.parseSuite()}
	for p.at(token.Elif) {
		p.advance()
		c := p.parseExpr(0)
		s.Elifs = append(s.Elifs, &ast.ElifBranch{Cond: c, Body: p.parseSuite()})
	}
	if _, ok := p.accept(token.Else); ok {
		s.Else = p.parseSuite()
	}
	return s
}

func (p *Parser) parseWhile() ast.Stmt {
	pos := p.expect(token.While, "at while").Pos
	cond := p.parseExpr(0)
	return &ast.WhileStmt{Pos0: pos, Cond: cond, Body: p.parseSuite()}
}

func (p *Parser) parseFor() ast.Stmt {
	pos := p.expect(token.For, "at for").Pos

	// for x in ...:
	if p.at(token.Ident) && p.peek(1).Kind == token.In {
		val := p.advance().Lexeme
		p.advance() // in
		iter := p.parseExpr(0)
		return &ast.ForInStmt{Pos0: pos, Val: val, Iter: iter, Body: p.parseSuite()}
	}
	// for k, v in ...:
	if p.at(token.Ident) && p.peek(1).Kind == token.Comma && p.peek(2).Kind == token.Ident && p.peek(3).Kind == token.In {
		key := p.advance().Lexeme
		p.advance()
		val := p.advance().Lexeme
		p.advance() // in
		iter := p.parseExpr(0)
		return &ast.ForInStmt{Pos0: pos, Key: key, Val: val, Iter: iter, Body: p.parseSuite()}
	}

	// Condition-only form: for cond:
	if p.at(token.Colon) {
		p.errorf(pos, "for loop is missing its condition")
		return &ast.ForStmt{Pos0: pos, Body: p.parseSuite()}
	}

	var init ast.Stmt
	if !p.at(token.Semicolon) {
		if p.looksLikeDefine() {
			init = p.parseDefineOnly()
		} else {
			init = p.parseSimpleStmtOnly()
		}
	}
	if !p.at(token.Semicolon) {
		// `for cond:` (while-style). Only an expression statement makes sense.
		if es, ok := init.(*ast.ExprStmt); ok && p.at(token.Colon) {
			return &ast.ForStmt{Pos0: pos, Cond: es.X, Body: p.parseSuite()}
		}
		p.errorfHint(p.cur().Pos, "use 'for init; condition; post:' or 'for x in range(n):'",
			"expected ';' in for loop, found %s", p.cur().Describe())
		return &ast.ForStmt{Pos0: pos, Body: p.parseSuite()}
	}
	p.advance() // ;

	var cond ast.Expr
	if !p.at(token.Semicolon) {
		cond = p.parseExpr(0)
	}
	p.expect(token.Semicolon, "after for condition")

	var post ast.Stmt
	if !p.at(token.Colon) {
		post = p.parseSimpleStmtOnly()
	}
	return &ast.ForStmt{Pos0: pos, Init: init, Cond: cond, Post: post, Body: p.parseSuite()}
}

// parseDefineOnly parses `a, b := expr, expr` without consuming a terminator.
func (p *Parser) parseDefineOnly() ast.Stmt {
	pos := p.cur().Pos
	var names []string
	for {
		name := p.expect(token.Ident, "as variable name")
		names = append(names, name.Lexeme)
		if _, ok := p.accept(token.Comma); !ok {
			break
		}
	}
	p.expect(token.Define, "in short variable declaration")
	values := p.parseExprList()
	if len(values) > 1 && len(names) != len(values) {
		p.errorf(pos, "declaration defines %d variables but supplies %d values", len(names), len(values))
	}
	return &ast.DefineStmt{Pos0: pos, Names: names, Values: values}
}

func (p *Parser) parseSwitch() ast.Stmt {
	pos := p.expect(token.Switch, "at switch").Pos
	s := &ast.SwitchStmt{Pos0: pos}
	if !p.at(token.Colon) {
		s.Subject = p.parseExpr(0)
	}
	p.expect(token.Colon, "after switch subject")
	p.expect(token.Newline, "after ':'")
	if _, ok := p.accept(token.Indent); !ok {
		p.errorf(p.cur().Pos, "expected an indented block of case clauses")
		return s
	}
	seenDefault := false
	for !p.at(token.Dedent) && !p.at(token.EOF) {
		p.skipNewlines()
		if p.at(token.Dedent) {
			break
		}
		if p.at(token.Case) {
			cpos := p.advance().Pos
			c := &ast.CaseClause{Pos0: cpos, Values: p.parseExprList()}
			c.Body = p.parseSuite()
			s.Cases = append(s.Cases, c)
			continue
		}
		if p.at(token.Default) {
			dpos := p.advance().Pos
			if seenDefault {
				p.errorf(dpos, "duplicate default clause in switch")
			}
			seenDefault = true
			s.Default = p.parseSuite()
			continue
		}
		p.errorf(p.cur().Pos, "expected 'case' or 'default' in switch, found %s", p.cur().Describe())
		p.synchronize()
	}
	p.expect(token.Dedent, "to close the switch body")
	return s
}

// synchronize skips tokens until the next statement boundary.
func (p *Parser) synchronize() {
	for !p.at(token.EOF) {
		switch p.cur().Kind {
		case token.Newline:
			p.advance()
			return
		case token.Dedent:
			return
		}
		p.advance()
	}
}

// --- expressions ----------------------------------------------------------

func (p *Parser) parseExprList() []ast.Expr {
	var out []ast.Expr
	out = append(out, p.parseExpr(0))
	for p.at(token.Comma) {
		p.advance()
		if p.at(token.Newline) {
			break
		}
		out = append(out, p.parseExpr(0))
	}
	return out
}

func (p *Parser) parseExpr(minPrec int) ast.Expr {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		p.errorf(p.cur().Pos, "expression is nested too deeply")
		p.advance()
		return &ast.NilLit{Pos0: p.cur().Pos}
	}

	left := p.parseUnary()
	for {
		op := p.cur().Kind
		prec := token.OpPrecedence(op)
		if prec == 0 || prec <= minPrec {
			break
		}
		opTok := p.advance()
		right := p.parseExpr(prec)
		left = &ast.BinaryExpr{Pos0: opTok.Pos, Op: op, X: left, Y: right}
	}
	return left
}

func (p *Parser) parseUnary() ast.Expr {
	switch p.cur().Kind {
	case token.Minus, token.Not, token.Plus:
		t := p.advance()
		return &ast.UnaryExpr{Pos0: t.Pos, Op: t.Kind, X: p.parseUnary()}
	}
	return p.parsePostfix()
}

func (p *Parser) parsePostfix() ast.Expr {
	x := p.parsePrimary()
	for {
		switch p.cur().Kind {
		case token.LParen:
			pos := p.cur().Pos
			p.advance()
			var args []ast.Expr
			for !p.at(token.RParen) && !p.at(token.EOF) {
				if p.at(token.Newline) {
					p.advance()
					continue
				}
				args = append(args, p.parseExpr(0))
				if _, ok := p.accept(token.Comma); ok {
					continue
				}
				break
			}
			p.expect(token.RParen, "to close the call arguments")
			x = &ast.CallExpr{Pos0: pos, Fn: x, Args: args}
		case token.LBracket:
			pos := p.cur().Pos
			p.advance()
			var low, high ast.Expr
			hasColon := false
			if !p.at(token.Colon) && !p.at(token.RBracket) {
				low = p.parseExpr(0)
			}
			if _, ok := p.accept(token.Colon); ok {
				hasColon = true
				if !p.at(token.RBracket) {
					high = p.parseExpr(0)
				}
			}
			p.expect(token.RBracket, "to close the index")
			if hasColon {
				x = &ast.SliceExpr{Pos0: pos, X: x, Low: low, High: high}
			} else {
				if low == nil {
					p.errorf(pos, "index expression is missing its index")
				}
				x = &ast.IndexExpr{Pos0: pos, X: x, Index: low}
			}
		case token.Dot:
			p.advance()
			name := p.expect(token.Ident, "after '.'")
			x = &ast.MemberExpr{Pos0: name.Pos, X: x, Name: name.Lexeme}
		case token.LBrace:
			ident, ok := x.(*ast.Ident)
			if !ok {
				return x
			}
			x = p.parseStructLit(ident)
		default:
			return x
		}
	}
}

func (p *Parser) parseStructLit(ident *ast.Ident) ast.Expr {
	pos := p.cur().Pos
	p.advance() // {
	lit := &ast.StructLit{Pos0: pos, Type: &ast.NamedType{Pos0: ident.Pos0, Name: ident.Name}}
	for !p.at(token.RBrace) && !p.at(token.EOF) {
		if p.at(token.Newline) {
			p.advance()
			continue
		}
		fpos := p.cur().Pos
		name := p.expect(token.Ident, "as field name")
		p.expect(token.Colon, "after field name")
		value := p.parseExpr(0)
		lit.Fields = append(lit.Fields, &ast.StructLitField{Pos0: fpos, Name: name.Lexeme, Value: value})
		if _, ok := p.accept(token.Comma); ok {
			continue
		}
		break
	}
	p.expect(token.RBrace, "to close the struct literal")
	return lit
}

func (p *Parser) parsePrimary() ast.Expr {
	t := p.cur()
	switch t.Kind {
	case token.Int:
		p.advance()
		v, err := parseIntLiteral(t.Lit)
		if err != nil {
			p.errorf(t.Pos, "%s", err.Error())
			v = 0
		}
		return &ast.IntLit{Pos0: t.Pos, Value: v, Raw: t.Lit}
	case token.Float:
		p.advance()
		v, err := strconv.ParseFloat(t.Lit, 64)
		if err != nil {
			p.errorf(t.Pos, "invalid float literal %q", t.Lit)
		}
		return &ast.FloatLit{Pos0: t.Pos, Value: v, Raw: t.Lit}
	case token.String:
		p.advance()
		return &ast.StringLit{Pos0: t.Pos, Value: t.Lit}
	case token.Rune:
		p.advance()
		r := []rune(t.Lit)
		var rv rune
		if len(r) > 0 {
			rv = r[0]
		}
		return &ast.RuneLit{Pos0: t.Pos, Value: rv}
	case token.FString:
		p.advance()
		return p.parseFString(t)
	case token.True:
		p.advance()
		return &ast.BoolLit{Pos0: t.Pos, Value: true}
	case token.False:
		p.advance()
		return &ast.BoolLit{Pos0: t.Pos, Value: false}
	case token.Nil:
		p.advance()
		return &ast.NilLit{Pos0: t.Pos}
	case token.Ident:
		p.advance()
		return &ast.Ident{Pos0: t.Pos, Name: t.Lexeme}
	case token.LParen:
		p.advance()
		x := p.parseExpr(0)
		if p.at(token.Comma) {
			p.errorf(p.cur().Pos, "parenthesized expression lists are not supported; return multiple values directly")
			for p.at(token.Comma) {
				p.advance()
				p.parseExpr(0)
			}
		}
		p.expect(token.RParen, "to close the parenthesized expression")
		return x
	case token.LBracket:
		p.advance()
		lit := &ast.ArrayLit{Pos0: t.Pos}
		for !p.at(token.RBracket) && !p.at(token.EOF) {
			if p.at(token.Newline) {
				p.advance()
				continue
			}
			lit.Values = append(lit.Values, p.parseExpr(0))
			if _, ok := p.accept(token.Comma); ok {
				continue
			}
			break
		}
		p.expect(token.RBracket, "to close the array literal")
		return lit
	case token.LBrace:
		p.advance()
		lit := &ast.MapLit{Pos0: t.Pos}
		for !p.at(token.RBrace) && !p.at(token.EOF) {
			if p.at(token.Newline) {
				p.advance()
				continue
			}
			key := p.parseExpr(0)
			p.expect(token.Colon, "between map key and value")
			value := p.parseExpr(0)
			lit.Entries = append(lit.Entries, ast.MapEntry{Key: key, Value: value})
			if _, ok := p.accept(token.Comma); ok {
				continue
			}
			break
		}
		p.expect(token.RBrace, "to close the map literal")
		return lit
	case token.Func:
		p.advance()
		fn := &ast.FuncLit{Pos0: t.Pos}
		fn.Params = p.parseParams()
		fn.Results = p.parseResults()
		fn.Body = p.parseSuite()
		return fn
	}
	p.errorf(t.Pos, "expected an expression, found %s", t.Describe())
	if !p.at(token.EOF) && !p.at(token.Newline) && !p.at(token.Dedent) {
		p.advance()
	}
	return &ast.NilLit{Pos0: t.Pos}
}

func parseIntLiteral(lit string) (int64, error) {
	s := strings.ReplaceAll(lit, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		s, base = s[2:], 16
	case strings.HasPrefix(s, "0b"), strings.HasPrefix(s, "0B"):
		s, base = s[2:], 2
	case strings.HasPrefix(s, "0o"), strings.HasPrefix(s, "0O"):
		s, base = s[2:], 8
	}
	v, err := strconv.ParseInt(s, base, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid integer literal %q", lit)
	}
	return v, nil
}

// parseFString splits an f-string into literal text and interpolated
// expressions, parsing each expression with its source position.
func (p *Parser) parseFString(tok token.Token) ast.Expr {
	raw := tok.Lit
	lit := &ast.FStringLit{Pos0: tok.Pos}
	var text strings.Builder

	flush := func() error {
		if text.Len() == 0 {
			return nil
		}
		decoded, err := token.Unescape(text.String())
		if err != nil {
			return err
		}
		lit.Parts = append(lit.Parts, ast.FStringPart{Text: decoded})
		text.Reset()
		return nil
	}

	for i := 0; i < len(raw); {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) {
			text.WriteByte(c)
			text.WriteByte(raw[i+1])
			i += 2
			continue
		}
		if c == '{' {
			if i+1 < len(raw) && raw[i+1] == '{' {
				text.WriteByte('{')
				i += 2
				continue
			}
			depth := 1
			j := i + 1
			inStr := false
			for j < len(raw) {
				ch := raw[j]
				if inStr {
					if ch == '\\' {
						j += 2
						continue
					}
					if ch == '"' {
						inStr = false
					}
				} else {
					switch ch {
					case '"':
						inStr = true
					case '{':
						depth++
					case '}':
						depth--
					}
				}
				if depth == 0 {
					break
				}
				j++
			}
			if depth != 0 {
				p.errorf(tok.Pos, "unterminated interpolation in f-string")
				break
			}
			exprSrc := raw[i+1 : j]
			if strings.TrimSpace(exprSrc) == "" {
				p.errorf(tok.Pos, "empty interpolation in f-string")
				i = j + 1
				continue
			}
			if err := flush(); err != nil {
				p.errorf(tok.Pos, "%s", err.Error())
			}
			base := tok.Pos
			base.Column += 2 + i + countRunes(raw[:i+1])
			expr, diags := ParseExprAt(p.file, exprSrc, base)
			p.diags.Diags = append(p.diags.Diags, diags...)
			lit.Parts = append(lit.Parts, ast.FStringPart{Expr: expr})
			i = j + 1
			continue
		}
		if c == '}' {
			if i+1 < len(raw) && raw[i+1] == '}' {
				text.WriteByte('}')
				i += 2
				continue
			}
			p.errorf(tok.Pos, "unmatched '}' in f-string; write '}}' for a literal brace")
			i++
			continue
		}
		text.WriteByte(c)
		i++
	}
	if err := flush(); err != nil {
		p.errorf(tok.Pos, "%s", err.Error())
	}
	if len(lit.Parts) == 0 {
		lit.Parts = append(lit.Parts, ast.FStringPart{Text: ""})
	}
	return lit
}

func countRunes(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
