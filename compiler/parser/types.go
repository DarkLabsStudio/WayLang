package parser

import (
	"waylang/compiler/ast"
	"waylang/compiler/token"
)

// parseType parses a syntactic type: a named type, []T, [N]T, map[K]V or a
// function type.
func (p *Parser) parseType() ast.TypeExpr {
	t := p.cur()
	switch t.Kind {
	case token.Ident:
		if t.Lexeme == "map" && p.peek(1).Kind == token.LBracket {
			p.advance()
			p.advance()
			key := p.parseType()
			p.expect(token.RBracket, "to close the map key type")
			value := p.parseType()
			return &ast.MapType{Pos0: t.Pos, Key: key, Value: value}
		}
		p.advance()
		return &ast.NamedType{Pos0: t.Pos, Name: t.Lexeme}
	case token.LBracket:
		p.advance()
		if _, ok := p.accept(token.RBracket); ok {
			return &ast.SliceType{Pos0: t.Pos, Elem: p.parseType()}
		}
		length := p.parseExpr(0)
		p.expect(token.RBracket, "to close the array length")
		return &ast.ArrayType{Pos0: t.Pos, Len: length, Elem: p.parseType()}
	case token.Func:
		p.advance()
		ft := &ast.FuncTypeExpr{Pos0: t.Pos}
		p.expect(token.LParen, "to start the function type parameters")
		for !p.at(token.RParen) && !p.at(token.EOF) {
			if p.at(token.Newline) {
				p.advance()
				continue
			}
			ft.Params = append(ft.Params, p.parseType())
			if _, ok := p.accept(token.Comma); !ok {
				break
			}
		}
		p.expect(token.RParen, "to close the function type parameters")
		ft.Results = p.parseResults()
		return ft
	}
	p.errorf(t.Pos, "expected a type, found %s", t.Describe())
	if !p.at(token.EOF) && !p.at(token.Newline) && !p.at(token.Dedent) && !p.at(token.RParen) && !p.at(token.Comma) {
		p.advance()
	}
	return &ast.NamedType{Pos0: t.Pos, Name: "?"}
}
