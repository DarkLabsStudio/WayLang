package semantic

import (
	"waylang/compiler/ast"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// checkBlock checks statements in a fresh scope.
func (a *Analyzer) checkBlock(b *ast.BlockStmt) {
	if b == nil {
		return
	}
	a.push()
	defer a.pop()
	for _, s := range b.Stmts {
		a.checkStmt(s)
	}
}

func (a *Analyzer) checkStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.ExprStmt:
		a.checkExpr(s.X, nil)
	case *ast.DefineStmt:
		a.checkDefine(s)
	case *ast.VarDeclStmt:
		a.checkVarDecl(s)
	case *ast.AssignStmt:
		a.checkAssign(s)
	case *ast.ReturnStmt:
		a.checkReturn(s)
	case *ast.IfStmt:
		a.checkIf(s)
	case *ast.WhileStmt:
		a.checkWhile(s)
	case *ast.ForStmt:
		a.checkFor(s)
	case *ast.ForInStmt:
		a.checkForIn(s)
	case *ast.SwitchStmt:
		a.checkSwitch(s)
	case *ast.BreakStmt:
		if a.loopDepth == 0 && a.switchDepth == 0 {
			a.errf(s.Pos(), "break is only allowed inside a loop or switch")
		}
	case *ast.ContinueStmt:
		if a.loopDepth == 0 {
			a.errf(s.Pos(), "continue is only allowed inside a loop")
		}
	case *ast.DeferStmt:
		if a.currentFunc == nil {
			a.errf(s.Pos(), "defer is only allowed inside a function")
			return
		}
		a.checkExpr(s.Call, nil)
		if _, ok := s.Call.(*ast.CallExpr); !ok {
			a.errf(s.Pos(), "defer requires a function call")
		}
		a.currentFunc.UsesDefer = true
		a.prog.UsesDefer = true
	case *ast.ConstDecl:
		c, ok := a.evalConst(s.Value)
		if !ok {
			a.errf(s.Pos(), "constant %q must have a value known at compile time", s.Name)
			return
		}
		ct := a.resolveType(s.Type)
		if s.Type == nil {
			ct = basicOf(c.Kind)
		}
		if ct == nil {
			return
		}
		cv := *c
		a.declare(&Symbol{Name: s.Name, Kind: SymConst, Type: ct, Const: &cv}, s.Pos())
	case *ast.BlockStmt:
		a.checkBlock(s)
	}
}

func (a *Analyzer) checkDefine(s *ast.DefineStmt) {
	var actual []types.Type
	if len(s.Values) == 1 {
		t := a.checkExpr(s.Values[0], nil)
		if tup, ok := t.(*types.Tuple); ok {
			actual = tup.Elems
		} else {
			actual = []types.Type{t}
		}
	} else {
		for _, v := range s.Values {
			actual = append(actual, a.checkExpr(v, nil))
		}
	}
	if len(actual) != len(s.Names) {
		a.errf(s.Pos(), "declaration defines %d variables but supplies %d values", len(s.Names), len(actual))
	}
	var syms []*Symbol
	for i, name := range s.Names {
		var t types.Type = types.AnyT
		if i < len(actual) {
			t = actual[i]
		}
		if t.Kind() == types.Void {
			a.errf(s.Pos(), "variable %q cannot have type void", name)
			t = types.AnyT
		}
		sym := &Symbol{Name: name, Kind: SymVar, Type: t, Mutable: true, Owner: a.currentFunc, ParamIndex: -1}
		a.declare(sym, s.Pos())
		syms = append(syms, sym)
	}
	a.prog.StmtSyms[s] = syms
}

func (a *Analyzer) checkVarDecl(s *ast.VarDeclStmt) {
	t := a.resolveType(s.Type)
	if t == nil {
		return
	}
	if s.Value != nil {
		got := a.checkExpr(s.Value, t)
		if ok, why := types.Assignable(t, got); !ok {
			a.errf(s.Value.Pos(), "cannot initialize %q: %s", s.Name, why)
		}
	}
	sym := &Symbol{Name: s.Name, Kind: SymVar, Type: t, Mutable: true, Owner: a.currentFunc, ParamIndex: -1}
	a.declare(sym, s.Pos())
	a.prog.StmtSyms[s] = []*Symbol{sym}
}

// lvalueType resolves an assignment target and returns its type.
func (a *Analyzer) lvalueType(e ast.Expr) types.Type {
	switch e := e.(type) {
	case *ast.Ident:
		sym := a.lookup(e.Name)
		if sym == nil {
			a.errf(e.Pos(), "undefined name %q", e.Name)
			return types.AnyT
		}
		a.prog.Idents[e] = sym
		if sym.Kind != SymVar {
			a.errHint(e.Pos(), "constants cannot be reassigned", "cannot assign to %q", e.Name)
			return types.AnyT
		}
		if !sym.Mutable {
			a.errf(e.Pos(), "cannot assign to immutable variable %q", e.Name)
		}
		return sym.Type
	case *ast.IndexExpr:
		return a.checkIndex(e)
	case *ast.MemberExpr:
		if st, ok := a.checkExpr(e.X, nil).(*types.Struct); ok {
			if idx := st.FieldIndex(e.Name); idx >= 0 {
				a.prog.Members[e] = &MemberInfo{Kind: MemberField, FieldIndex: idx}
				return st.Fields[idx].Type
			}
		}
		a.errf(e.Pos(), "cannot assign to %q", e.Name)
		return types.AnyT
	case *ast.SliceExpr:
		a.errf(e.Pos(), "cannot assign to a slice expression")
		return types.AnyT
	}
	a.errf(e.Pos(), "invalid assignment target")
	return types.AnyT
}

func (a *Analyzer) checkAssign(s *ast.AssignStmt) {
	targetTypes := make([]types.Type, len(s.Targets))
	for i, t := range s.Targets {
		targetTypes[i] = a.lvalueType(t)
	}

	if s.Op == token.Assign {
		var actual []types.Type
		if len(s.Values) == 1 {
			t := a.checkExpr(s.Values[0], nil)
			if tup, ok := t.(*types.Tuple); ok {
				actual = tup.Elems
			} else {
				actual = []types.Type{t}
			}
		} else {
			for i, v := range s.Values {
				var want types.Type
				if i < len(targetTypes) {
					want = targetTypes[i]
				}
				actual = append(actual, a.checkExpr(v, want))
			}
		}
		if len(actual) != len(targetTypes) {
			a.errf(s.Pos(), "assignment has %d targets but %d values", len(targetTypes), len(actual))
			return
		}
		for i := range actual {
			if ok, why := types.Assignable(targetTypes[i], actual[i]); !ok {
				a.errf(s.Pos(), "assignment %d: %s", i+1, why)
			}
		}
		return
	}

	// Compound assignment (a += b), including the desugared ++ and --.
	if len(s.Targets) != 1 || len(s.Values) != 1 {
		a.errf(s.Pos(), "compound assignment needs exactly one target and one value")
		return
	}
	dst := targetTypes[0]
	want := dst
	if dst.Kind() == types.Any {
		want = nil
	}
	got := a.checkExpr(s.Values[0], want)
	switch s.Op {
	case token.Plus, token.PlusEq:
		if dst.Kind() == types.String {
			if got.Kind() != types.String {
				a.errf(s.Pos(), "cannot add %s to a string", got)
			}
			return
		}
		fallthrough
	case token.Minus, token.MinusEq, token.Star, token.StarEq, token.Slash, token.SlashEq, token.Percent, token.PercentEq:
		if !types.IsNumeric(dst) || !types.IsNumeric(got) {
			a.errf(s.Pos(), "operator %s requires numeric operands, got %s and %s", s.Op, dst, got)
			return
		}
		if dst.Kind() == types.Float && got.Kind() != types.Float {
			if !a.adaptToFloat(s.Values[0], got) {
				a.errf(s.Pos(), "cannot combine float and int with %s; convert explicitly", s.Op)
			}
		}
	default:
		a.errf(s.Pos(), "unsupported assignment operator %s", s.Op)
	}
}

func (a *Analyzer) checkReturn(s *ast.ReturnStmt) {
	fn := a.currentFunc
	if fn == nil {
		a.errf(s.Pos(), "return outside of a function")
		return
	}
	results := fn.Sig.Results
	if len(results) == 0 {
		if len(s.Values) > 0 {
			a.errf(s.Pos(), "%s does not return a value", fn.Name)
		}
		return
	}
	if len(s.Values) == 0 {
		a.errf(s.Pos(), "%s must return %d value(s)", fn.Name, len(results))
		return
	}
	if len(s.Values) == 1 && len(results) == 1 {
		got := a.checkExpr(s.Values[0], results[0])
		if ok, why := types.Assignable(results[0], got); !ok {
			a.errf(s.Pos(), "return value: %s", why)
		}
		return
	}
	if len(s.Values) == 1 {
		got := a.checkExpr(s.Values[0], nil)
		if tup, ok := got.(*types.Tuple); ok {
			if len(tup.Elems) != len(results) {
				a.errf(s.Pos(), "returning %d values but %s declares %d", len(tup.Elems), fn.Name, len(results))
				return
			}
			for i := range results {
				if ok, why := types.Assignable(results[i], tup.Elems[i]); !ok {
					a.errf(s.Pos(), "return value %d: %s", i+1, why)
				}
			}
			return
		}
		a.errf(s.Pos(), "returning 1 value but %s declares %d", fn.Name, len(results))
		return
	}
	if len(s.Values) != len(results) {
		a.errf(s.Pos(), "returning %d values but %s declares %d", len(s.Values), fn.Name, len(results))
		return
	}
	for i, v := range s.Values {
		got := a.checkExpr(v, results[i])
		if ok, why := types.Assignable(results[i], got); !ok {
			a.errf(v.Pos(), "return value %d: %s", i+1, why)
		}
	}
}

func (a *Analyzer) checkIf(s *ast.IfStmt) {
	cond := a.checkExpr(s.Cond, types.BoolT)
	if cond.Kind() != types.Bool {
		a.errf(s.Cond.Pos(), "if condition must be bool, got %s", cond)
	}
	a.checkBlock(s.Then)
	for _, branch := range s.Elifs {
		c := a.checkExpr(branch.Cond, types.BoolT)
		if c.Kind() != types.Bool {
			a.errf(branch.Cond.Pos(), "elif condition must be bool, got %s", c)
		}
		a.checkBlock(branch.Body)
	}
	if s.Else != nil {
		a.checkBlock(s.Else)
	}
}

func (a *Analyzer) checkWhile(s *ast.WhileStmt) {
	cond := a.checkExpr(s.Cond, types.BoolT)
	if cond.Kind() != types.Bool {
		a.errf(s.Cond.Pos(), "while condition must be bool, got %s", cond)
	}
	a.loopDepth++
	a.checkBlock(s.Body)
	a.loopDepth--
}

func (a *Analyzer) checkFor(s *ast.ForStmt) {
	a.push()
	defer a.pop()
	if s.Init != nil {
		a.checkStmt(s.Init)
	}
	if s.Cond != nil {
		cond := a.checkExpr(s.Cond, types.BoolT)
		if cond.Kind() != types.Bool {
			a.errf(s.Cond.Pos(), "for condition must be bool, got %s", cond)
		}
	}
	if s.Post != nil {
		a.checkStmt(s.Post)
	}
	a.loopDepth++
	a.checkBlock(s.Body)
	a.loopDepth--
}

func (a *Analyzer) checkForIn(s *ast.ForInStmt) {
	iter := a.checkExpr(s.Iter, nil)
	single := s.Key == ""

	var keyT, valT types.Type
	switch it := iter.(type) {
	case *types.Range:
		if !it.WithValue {
			keyT = types.IntT
			if !single {
				// range(n) yields only an index
				valT = types.IntT
			}
		} else {
			keyT = types.IntT
			valT = it.Elem
			if single {
				keyT = it.Elem
				valT = nil
			}
		}
	case *types.Array:
		keyT = types.IntT
		valT = it.Elem
		if single {
			keyT = it.Elem
			valT = nil
		}
	case *types.Slice:
		keyT = types.IntT
		valT = it.Elem
		if single {
			keyT = it.Elem
			valT = nil
		}
	case *types.Map:
		keyT = it.Key
		valT = it.Elem
		if single {
			keyT = it.Key
			valT = nil
		}
	default:
		if iter.Kind() == types.String {
			keyT = types.IntT
			valT = types.RuneT
			if single {
				keyT = types.RuneT
				valT = nil
			}
		} else if iter.Kind() == types.Any {
			a.errf(s.Iter.Pos(), "cannot iterate over an `any` value; convert it to a concrete type first")
			keyT, valT = types.AnyT, types.AnyT
		} else {
			a.errf(s.Iter.Pos(), "cannot iterate over a value of type %s", iter)
			keyT, valT = types.AnyT, types.AnyT
		}
	}

	a.push()
	defer a.pop()
	var syms []*Symbol
	if single {
		sym := &Symbol{Name: s.Val, Kind: SymVar, Type: keyT, Mutable: true, Owner: a.currentFunc, ParamIndex: -1}
		a.declare(sym, s.Pos())
		syms = append(syms, sym)
	} else {
		ks := &Symbol{Name: s.Key, Kind: SymVar, Type: keyT, Mutable: true, Owner: a.currentFunc, ParamIndex: -1}
		vs := &Symbol{Name: s.Val, Kind: SymVar, Type: valT, Mutable: true, Owner: a.currentFunc, ParamIndex: -1}
		a.declare(ks, s.Pos())
		a.declare(vs, s.Pos())
		syms = append(syms, ks, vs)
	}
	a.prog.StmtSyms[s] = syms
	a.loopDepth++
	a.checkBlock(s.Body)
	a.loopDepth--
}

func (a *Analyzer) checkSwitch(s *ast.SwitchStmt) {
	var subject types.Type = types.BoolT
	if s.Subject != nil {
		subject = a.checkExpr(s.Subject, nil)
		if !types.IsComparable(subject) {
			a.errf(s.Subject.Pos(), "switch subject of type %s cannot be compared", subject)
		}
	}
	a.switchDepth++
	defer func() { a.switchDepth-- }()
	for _, c := range s.Cases {
		for _, v := range c.Values {
			if s.Subject == nil {
				got := a.checkExpr(v, types.BoolT)
				if got.Kind() != types.Bool {
					a.errf(v.Pos(), "case value must be bool, got %s", got)
				}
				continue
			}
			got := a.checkExpr(v, subject)
			if ok, why := types.Assignable(subject, got); !ok {
				a.errf(v.Pos(), "case value: %s", why)
			}
		}
		a.checkBlock(c.Body)
	}
	if s.Default != nil {
		a.checkBlock(s.Default)
	}
}
