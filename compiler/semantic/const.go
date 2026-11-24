package semantic

import (
	"math"

	"waylang/compiler/ast"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// evalConst evaluates a compile-time constant expression.
func (a *Analyzer) evalConst(e ast.Expr) (*Constant, bool) {
	switch e := e.(type) {
	case *ast.IntLit:
		return &Constant{Kind: types.Int, Int: e.Value}, true
	case *ast.FloatLit:
		return &Constant{Kind: types.Float, Float: e.Value}, true
	case *ast.StringLit:
		return &Constant{Kind: types.String, Str: e.Value}, true
	case *ast.BoolLit:
		return &Constant{Kind: types.Bool, Bool: e.Value}, true
	case *ast.RuneLit:
		return &Constant{Kind: types.Rune, Int: int64(e.Value)}, true
	case *ast.Ident:
		if sym := a.lookup(e.Name); sym != nil && sym.Kind == SymConst && sym.Const != nil {
			c := *sym.Const
			return &c, true
		}
	case *ast.UnaryExpr:
		c, ok := a.evalConst(e.X)
		if !ok {
			return nil, false
		}
		switch e.Op {
		case token.Minus:
			switch c.Kind {
			case types.Int, types.Rune, types.Byte:
				return &Constant{Kind: types.Int, Int: -c.Int}, true
			case types.Float:
				return &Constant{Kind: types.Float, Float: -c.Float}, true
			}
		case token.Not:
			if c.Kind == types.Bool {
				return &Constant{Kind: types.Bool, Bool: !c.Bool}, true
			}
		case token.Plus:
			return c, true
		}
	case *ast.BinaryExpr:
		l, ok := a.evalConst(e.X)
		if !ok {
			return nil, false
		}
		r, ok := a.evalConst(e.Y)
		if !ok {
			return nil, false
		}
		return evalBinaryConst(e.Op, l, r)
	case *ast.CallExpr:
		// len("literal") is constant.
		if id, ok := e.Fn.(*ast.Ident); ok && id.Name == "len" && len(e.Args) == 1 {
			if s, ok := e.Args[0].(*ast.StringLit); ok {
				return &Constant{Kind: types.Int, Int: int64(len(s.Value))}, true
			}
		}
	}
	return nil, false
}

func evalBinaryConst(op token.Kind, l, r *Constant) (*Constant, bool) {
	bothInt := (l.Kind == types.Int || l.Kind == types.Rune || l.Kind == types.Byte) &&
		(r.Kind == types.Int || r.Kind == types.Rune || r.Kind == types.Byte)
	bothNum := bothInt || (l.Kind == types.Float || l.Kind == types.Int) && (r.Kind == types.Float || r.Kind == types.Int)
	toFloat := func(c *Constant) float64 {
		if c.Kind == types.Float {
			return c.Float
		}
		return float64(c.Int)
	}
	switch op {
	case token.Plus:
		switch {
		case l.Kind == types.String && r.Kind == types.String:
			return &Constant{Kind: types.String, Str: l.Str + r.Str}, true
		case bothInt:
			return &Constant{Kind: types.Int, Int: l.Int + r.Int}, true
		case bothNum:
			return &Constant{Kind: types.Float, Float: toFloat(l) + toFloat(r)}, true
		}
	case token.Minus:
		if bothInt {
			return &Constant{Kind: types.Int, Int: l.Int - r.Int}, true
		}
		if bothNum {
			return &Constant{Kind: types.Float, Float: toFloat(l) - toFloat(r)}, true
		}
	case token.Star:
		if bothInt {
			return &Constant{Kind: types.Int, Int: l.Int * r.Int}, true
		}
		if bothNum {
			return &Constant{Kind: types.Float, Float: toFloat(l) * toFloat(r)}, true
		}
	case token.Slash:
		if bothInt {
			if r.Int == 0 {
				return nil, false
			}
			return &Constant{Kind: types.Int, Int: l.Int / r.Int}, true
		}
		if bothNum {
			if toFloat(r) == 0 {
				return nil, false
			}
			return &Constant{Kind: types.Float, Float: toFloat(l) / toFloat(r)}, true
		}
	case token.Percent:
		if bothInt && r.Int != 0 {
			return &Constant{Kind: types.Int, Int: l.Int % r.Int}, true
		}
	case token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		var cmp int
		switch {
		case l.Kind == types.String && r.Kind == types.String:
			switch {
			case l.Str < r.Str:
				cmp = -1
			case l.Str > r.Str:
				cmp = 1
			}
		case l.Kind == types.Bool && r.Kind == types.Bool:
			if l.Bool == r.Bool {
				cmp = 0
			} else {
				cmp = 1
			}
		case bothInt:
			switch {
			case l.Int < r.Int:
				cmp = -1
			case l.Int > r.Int:
				cmp = 1
			}
		case bothNum:
			a, b := toFloat(l), toFloat(r)
			switch {
			case a < b:
				cmp = -1
			case a > b:
				cmp = 1
			}
		default:
			return nil, false
		}
		var b bool
		switch op {
		case token.Eq:
			b = cmp == 0
		case token.Ne:
			b = cmp != 0
		case token.Lt:
			b = cmp < 0
		case token.Le:
			b = cmp <= 0
		case token.Gt:
			b = cmp > 0
		case token.Ge:
			b = cmp >= 0
		}
		return &Constant{Kind: types.Bool, Bool: b}, true
	case token.And:
		if l.Kind == types.Bool && r.Kind == types.Bool {
			return &Constant{Kind: types.Bool, Bool: l.Bool && r.Bool}, true
		}
	case token.Or:
		if l.Kind == types.Bool && r.Kind == types.Bool {
			return &Constant{Kind: types.Bool, Bool: l.Bool || r.Bool}, true
		}
	}
	return nil, false
}

var _ = math.MaxInt64
