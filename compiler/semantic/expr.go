package semantic

import (
	"fmt"

	"waylang/compiler/ast"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// annotate records an expression's type and returns it.
func (a *Analyzer) annotate(e ast.Expr, t types.Type) types.Type {
	if t == nil {
		t = types.AnyT
	}
	a.prog.ExprTypes[e] = t
	return t
}

// resultType collapses a signature's results into a single type.
func resultType(sig *types.Func) types.Type {
	switch len(sig.Results) {
	case 0:
		return types.VoidT
	case 1:
		return sig.Results[0]
	}
	return &types.Tuple{Elems: sig.Results}
}

// checkExpr type-checks an expression. want is the expected type (possibly nil)
// and is used to adapt integer literals to float contexts and to type empty
// composite literals.
func (a *Analyzer) checkExpr(e ast.Expr, want types.Type) types.Type {
	switch e := e.(type) {
	case *ast.IntLit:
		if want != nil && want.Kind() == types.Float {
			return a.annotate(e, types.FloatT)
		}
		return a.annotate(e, types.IntT)
	case *ast.FloatLit:
		return a.annotate(e, types.FloatT)
	case *ast.StringLit:
		return a.annotate(e, types.StringT)
	case *ast.RuneLit:
		return a.annotate(e, types.RuneT)
	case *ast.BoolLit:
		return a.annotate(e, types.BoolT)
	case *ast.NilLit:
		if want != nil && types.IsNilable(want) {
			return a.annotate(e, want)
		}
		return a.annotate(e, types.NilT)
	case *ast.FStringLit:
		for _, part := range e.Parts {
			if part.Expr != nil {
				a.checkExpr(part.Expr, nil)
			}
		}
		return a.annotate(e, types.StringT)
	case *ast.Ident:
		return a.checkIdent(e)
	case *ast.UnaryExpr:
		return a.checkUnary(e)
	case *ast.BinaryExpr:
		return a.checkBinary(e)
	case *ast.CallExpr:
		return a.checkCall(e, want)
	case *ast.MemberExpr:
		return a.checkMember(e)
	case *ast.IndexExpr:
		return a.checkIndex(e)
	case *ast.SliceExpr:
		return a.checkSlice(e)
	case *ast.StructLit:
		return a.checkStructLit(e)
	case *ast.ArrayLit:
		return a.checkArrayLit(e, want)
	case *ast.MapLit:
		return a.checkMapLit(e, want)
	case *ast.FuncLit:
		return a.checkFuncLit(e)
	}
	a.errf(e.Pos(), "unsupported expression")
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkIdent(e *ast.Ident) types.Type {
	sym := a.lookup(e.Name)
	if sym == nil {
		a.errHint(e.Pos(), "declare it first, or check the spelling", "undefined name %q", e.Name)
		return a.annotate(e, types.AnyT)
	}
	a.prog.Idents[e] = sym
	switch sym.Kind {
	case SymType:
		a.errHint(e.Pos(), "types are only usable in declarations, conversions and literals",
			"type %q is not a value", e.Name)
	case SymPackage:
		a.errf(e.Pos(), "package %q is not a value", e.Name)
	case SymBuiltin:
		a.errf(e.Pos(), "builtin %q must be called", e.Name)
	case SymVar:
		// Closures may not capture outer locals.
		if a.currentFunc != nil && sym.Owner != nil && sym.Owner != a.currentFunc && sym.Global == nil {
			a.errHint(e.Pos(), "pass the value as a parameter instead",
				"anonymous function captures local variable %q, which is not supported yet", e.Name)
		}
	}
	if sym.Type == nil {
		return a.annotate(e, types.AnyT)
	}
	return a.annotate(e, sym.Type)
}

func (a *Analyzer) checkUnary(e *ast.UnaryExpr) types.Type {
	t := a.checkExpr(e.X, nil)
	switch e.Op {
	case token.Minus:
		if t.Kind() == types.Float {
			return a.annotate(e, types.FloatT)
		}
		if types.IsInteger(t) {
			return a.annotate(e, types.IntT)
		}
		if t.Kind() == types.Any {
			return a.annotate(e, types.AnyT)
		}
		a.errf(e.Pos(), "cannot negate a value of type %s", t)
	case token.Not:
		if t.Kind() == types.Bool {
			return a.annotate(e, types.BoolT)
		}
		a.errf(e.Pos(), "operator ! requires bool, got %s", t)
	case token.Plus:
		if types.IsNumeric(t) {
			return a.annotate(e, t)
		}
		a.errf(e.Pos(), "unary + requires a numeric value, got %s", t)
	}
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkBinary(e *ast.BinaryExpr) types.Type {
	switch e.Op {
	case token.And, token.Or:
		lt := a.checkExpr(e.X, types.BoolT)
		rt := a.checkExpr(e.Y, types.BoolT)
		if lt.Kind() != types.Bool || rt.Kind() != types.Bool {
			a.errf(e.Pos(), "operator %s requires bool operands, got %s and %s", e.Op, lt, rt)
		}
		return a.annotate(e, types.BoolT)
	}

	lt := a.checkExpr(e.X, nil)
	rt := a.checkExpr(e.Y, nil)

	if e.Op == token.Eq || e.Op == token.Ne {
		return a.checkEquality(e, lt, rt)
	}

	if lt.Kind() == types.Any || rt.Kind() == types.Any {
		// Dynamic dispatch through the runtime for any-typed operands.
		return a.annotate(e, types.AnyT)
	}

	if e.Op == token.Lt || e.Op == token.Le || e.Op == token.Gt || e.Op == token.Ge {
		if types.IsNumeric(lt) && types.IsNumeric(rt) {
			if lt.Kind() == types.Float || rt.Kind() == types.Float {
				if !a.adaptToFloat(e.X, lt) || !a.adaptToFloat(e.Y, rt) {
					a.errHint(e.Pos(), "convert the integer operand with float(x)",
						"operator %s mixes int and float operands", e.Op)
				}
			}
			return a.annotate(e, types.BoolT)
		}
		if lt.Kind() == types.String && rt.Kind() == types.String {
			return a.annotate(e, types.BoolT)
		}
		a.errf(e.Pos(), "operator %s is not defined for %s and %s", e.Op, lt, rt)
		return a.annotate(e, types.BoolT)
	}

	switch e.Op {
	case token.Plus:
		if lt.Kind() == types.String && rt.Kind() == types.String {
			return a.annotate(e, types.StringT)
		}
	case token.Percent, token.Shl, token.Shr, token.Amp, token.Pipe, token.Caret:
		if !types.IsInteger(lt) || !types.IsInteger(rt) {
			a.errf(e.Pos(), "operator %s requires integer operands, got %s and %s", e.Op, lt, rt)
			return a.annotate(e, types.AnyT)
		}
		return a.annotate(e, types.IntT)
	}

	if types.IsNumeric(lt) && types.IsNumeric(rt) {
		if lt.Kind() == types.Float || rt.Kind() == types.Float {
			// Adapt integer literals to float; anything else needs a conversion.
			if !a.adaptToFloat(e.X, lt) || !a.adaptToFloat(e.Y, rt) {
				a.errHint(e.Pos(), "convert the integer operand with float(x)",
					"operator %s mixes int and float operands", e.Op)
			}
			return a.annotate(e, types.FloatT)
		}
		if e.Op == token.Slash {
			return a.annotate(e, types.IntT)
		}
		return a.annotate(e, types.IntT)
	}

	a.errf(e.Pos(), "operator %s is not defined for %s and %s", e.Op, lt, rt)
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkEquality(e *ast.BinaryExpr, lt, rt types.Type) types.Type {
	if lt.Kind() == types.Nil || rt.Kind() == types.Nil {
		other := lt
		if lt.Kind() == types.Nil {
			other = rt
		}
		if !types.IsNilable(other) && other.Kind() != types.Any {
			a.errf(e.Pos(), "cannot compare %s with nil", other)
		}
		return a.annotate(e, types.BoolT)
	}
	if lt.Kind() == types.Any || rt.Kind() == types.Any {
		return a.annotate(e, types.BoolT)
	}
	if !types.IsComparable(lt) || !types.IsComparable(rt) {
		a.errf(e.Pos(), "values of type %s and %s cannot be compared", lt, rt)
		return a.annotate(e, types.BoolT)
	}
	if ok, _ := types.Assignable(lt, rt); !ok {
		if ok2, _ := types.Assignable(rt, lt); !ok2 && !(types.IsInteger(lt) && types.IsInteger(rt)) {
			a.errf(e.Pos(), "cannot compare %s with %s", lt, rt)
		}
	}
	return a.annotate(e, types.BoolT)
}

// adaptToFloat marks an integer literal operand as float. It reports whether
// the operand is usable in a float expression.
func (a *Analyzer) adaptToFloat(e ast.Expr, t types.Type) bool {
	if t.Kind() == types.Float {
		return true
	}
	if il, ok := e.(*ast.IntLit); ok {
		a.annotate(il, types.FloatT)
		return true
	}
	return false
}

func (a *Analyzer) checkIndex(e *ast.IndexExpr) types.Type {
	xt := a.checkExpr(e.X, nil)
	// Map keys are typed, so the index must match the key type; sequences are
	// indexed by int.
	if m, ok := xt.(*types.Map); ok {
		kt := a.checkExpr(e.Index, m.Key)
		if ok, why := types.Assignable(m.Key, kt); !ok {
			a.errf(e.Index.Pos(), "map key: %s", why)
		}
		return a.annotate(e, m.Elem)
	}
	a.checkExpr(e.Index, types.IntT)
	idx := a.prog.ExprTypes[e.Index]
	if idx != nil && idx.Kind() != types.Int && idx.Kind() != types.Any {
		a.errf(e.Index.Pos(), "index must be an int, got %s", idx)
	}
	switch t := xt.(type) {
	case *types.Array:
		return a.annotate(e, t.Elem)
	case *types.Slice:
		return a.annotate(e, t.Elem)
	case *types.Map:
		return a.annotate(e, t.Elem)
	}
	if xt.Kind() == types.String {
		return a.annotate(e, types.ByteT)
	}
	if xt.Kind() == types.Any {
		a.errf(e.Pos(), "indexing an `any` value requires a conversion to a concrete type")
		return a.annotate(e, types.AnyT)
	}
	a.errf(e.Pos(), "cannot index a value of type %s", xt)
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkSlice(e *ast.SliceExpr) types.Type {
	xt := a.checkExpr(e.X, nil)
	if e.Low != nil {
		a.checkExpr(e.Low, types.IntT)
	}
	if e.High != nil {
		a.checkExpr(e.High, types.IntT)
	}
	switch t := xt.(type) {
	case *types.Array:
		return a.annotate(e, &types.Slice{Elem: t.Elem})
	case *types.Slice:
		return a.annotate(e, t)
	}
	if xt.Kind() == types.String {
		return a.annotate(e, types.StringT)
	}
	a.errf(e.Pos(), "cannot slice a value of type %s", xt)
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkStructLit(e *ast.StructLit) types.Type {
	declared := a.typeNames[e.Type.Name]
	st, ok := declared.(*types.Struct)
	if !ok {
		a.errf(e.Type.Pos(), "%q is not a struct type", e.Type.Name)
		for _, f := range e.Fields {
			a.checkExpr(f.Value, nil)
		}
		return a.annotate(e, types.AnyT)
	}
	seen := map[string]bool{}
	for _, f := range e.Fields {
		idx := st.FieldIndex(f.Name)
		if idx < 0 {
			a.errf(f.Pos(), "struct %s has no field %q", st.Name, f.Name)
			a.checkExpr(f.Value, nil)
			continue
		}
		if seen[f.Name] {
			a.errf(f.Pos(), "field %q is set more than once", f.Name)
		}
		seen[f.Name] = true
		want := st.Fields[idx].Type
		got := a.checkExpr(f.Value, want)
		if ok, why := types.Assignable(want, got); !ok {
			a.errf(f.Value.Pos(), "field %q: %s", f.Name, why)
		}
	}
	for _, field := range st.Fields {
		if seen[field.Name] {
			continue
		}
		if _, hasDefault := a.prog.FieldDefaults[field]; !hasDefault {
			a.errf(e.Pos(), "missing value for field %q of struct %s", field.Name, st.Name)
		}
	}
	return a.annotate(e, st)
}

func (a *Analyzer) checkArrayLit(e *ast.ArrayLit, want types.Type) types.Type {
	var elem types.Type
	if want != nil {
		switch w := want.(type) {
		case *types.Slice:
			elem = w.Elem
		case *types.Array:
			elem = w.Elem
		}
	}
	if len(e.Values) == 0 && elem == nil {
		a.errf(e.Pos(), "cannot infer the element type of an empty array literal; write []int{} or add elements")
		return a.annotate(e, &types.Slice{Elem: types.AnyT})
	}
	if elem == nil {
		elem = a.checkExpr(e.Values[0], nil)
		if elem.Kind() == types.Nil || elem.Kind() == types.Void {
			a.errf(e.Values[0].Pos(), "array elements cannot have type %s", elem)
			elem = types.AnyT
		}
		// Numeric literal elements in a float context should all be floats.
	} else {
		a.checkExpr(e.Values[0], elem)
	}
	for i := 1; i < len(e.Values); i++ {
		got := a.checkExpr(e.Values[i], elem)
		if ok, why := types.Assignable(elem, got); !ok {
			a.errf(e.Values[i].Pos(), "element %d: %s", i+1, why)
		}
	}
	if arr, ok := want.(*types.Array); ok {
		if int64(len(e.Values)) != arr.Len {
			a.errf(e.Pos(), "array literal has %d elements but [%d]%s needs %d", len(e.Values), arr.Len, arr.Elem, arr.Len)
		}
		return a.annotate(e, arr)
	}
	return a.annotate(e, &types.Slice{Elem: elem})
}

func (a *Analyzer) checkMapLit(e *ast.MapLit, want types.Type) types.Type {
	var keyT, elemT types.Type
	if m, ok := want.(*types.Map); ok {
		keyT, elemT = m.Key, m.Elem
	}
	if len(e.Entries) == 0 && keyT == nil {
		a.errf(e.Pos(), "cannot infer the type of an empty map literal; write map[string]int{} or add entries")
		return a.annotate(e, &types.Map{Key: types.StringT, Elem: types.AnyT})
	}
	if keyT == nil {
		keyT = a.checkExpr(e.Entries[0].Key, nil)
		elemT = a.checkExpr(e.Entries[0].Value, nil)
	}
	for i, entry := range e.Entries {
		kt := a.checkExpr(entry.Key, keyT)
		if ok, why := types.Assignable(keyT, kt); !ok {
			a.errf(entry.Key.Pos(), "map key %d: %s", i+1, why)
		}
		vt := a.checkExpr(entry.Value, elemT)
		if ok, why := types.Assignable(elemT, vt); !ok {
			a.errf(entry.Value.Pos(), "map value %d: %s", i+1, why)
		}
	}
	if !types.IsComparable(keyT) {
		a.errf(e.Pos(), "map key type %s is not comparable", keyT)
	}
	return a.annotate(e, &types.Map{Key: keyT, Elem: elemT})
}

func (a *Analyzer) checkFuncLit(e *ast.FuncLit) types.Type {
	sig := a.buildSignature(e.Params, e.Results, true)
	if sig == nil {
		return a.annotate(e, types.AnyT)
	}
	info := &FuncInfo{
		Name:   fmt.Sprintf("func%d", len(a.prog.Funcs)),
		Symbol: fmt.Sprintf("%s.anon.%d", a.prog.Package, len(a.prog.Funcs)),
		Decl:   &ast.FuncDecl{Pos0: e.Pos(), Name: fmt.Sprintf("func%d", len(a.prog.Funcs)), Params: e.Params, Results: e.Results, Body: e.Body},
		Sig:    sig,
		Slot:   -1,
	}
	a.prog.Funcs = append(a.prog.Funcs, info)
	a.prog.FuncByName[info.Symbol] = info
	a.prog.FuncLits[e] = info
	a.checkFuncBody(info)
	a.prog.ExprTypes[e] = sig
	return sig
}

func (a *Analyzer) checkMember(e *ast.MemberExpr) types.Type {
	// Package-qualified function used as a value.
	if id, ok := e.X.(*ast.Ident); ok {
		if sym := a.lookup(id.Name); sym != nil && sym.Kind == SymPackage {
			a.prog.Idents[id] = sym
			f := sym.Package.Funcs[e.Name]
			if f == nil {
				a.errf(e.Pos(), "package %s has no function %q", sym.Package.Name, e.Name)
				return a.annotate(e, types.AnyT)
			}
			a.prog.Members[e] = &MemberInfo{Kind: MemberPackage, StdFunc: f, Package: sym.Package}
			return a.annotate(e, f.Sig())
		}
	}
	t := a.checkExpr(e.X, nil)
	if st, ok := t.(*types.Struct); ok {
		if idx := st.FieldIndex(e.Name); idx >= 0 {
			a.prog.Members[e] = &MemberInfo{Kind: MemberField, FieldIndex: idx}
			return a.annotate(e, st.Fields[idx].Type)
		}
		if st.Method(e.Name) != nil {
			a.errHint(e.Pos(), "call the method directly: "+e.Name+"(...)",
				"method values are not supported yet")
			return a.annotate(e, types.AnyT)
		}
		a.errf(e.Pos(), "struct %s has no field or method %q", st.Name, e.Name)
		return a.annotate(e, types.AnyT)
	}
	if it, ok := t.(*types.Interface); ok {
		if idx := interfaceMethodIndex(it, e.Name); idx >= 0 {
			a.errHint(e.Pos(), "call the method directly: "+e.Name+"(...)",
				"method values are not supported yet")
			return a.annotate(e, types.AnyT)
		}
		a.errf(e.Pos(), "interface %s has no method %q", it.Name, e.Name)
		return a.annotate(e, types.AnyT)
	}
	a.errf(e.Pos(), "cannot select %q from a value of type %s", e.Name, t)
	return a.annotate(e, types.AnyT)
}

func interfaceMethodIndex(it *types.Interface, name string) int {
	for i, n := range it.MethodNames {
		if n == name {
			return i
		}
	}
	return -1
}

// --- calls ----------------------------------------------------------------

func (a *Analyzer) checkCall(e *ast.CallExpr, want types.Type) types.Type {
	if id, ok := e.Fn.(*ast.Ident); ok {
		sym := a.lookup(id.Name)
		if sym == nil {
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			a.errHint(id.Pos(), "define it with func "+id.Name+"(...): or import its package",
				"undefined function %q", id.Name)
			return a.annotate(e, types.AnyT)
		}
		a.prog.Idents[id] = sym
		switch sym.Kind {
		case SymType:
			if sym.TypeArg != nil && sym.TypeArg.Kind() == types.Error {
				// `error(msg)` constructs an error value.
				return a.checkBuiltinCall(e, "error")
			}
			return a.checkConversion(e, sym.TypeArg)
		case SymBuiltin:
			return a.checkBuiltinCall(e, sym.Builtin)
		case SymFunc:
			return a.checkDirectCall(e, sym.Func)
		case SymVar:
			if sig, ok := sym.Type.(*types.Func); ok {
				a.checkArgList(e, sig.Params, nil, id.Name)
				a.prog.Calls[e] = &CallInfo{Kind: CallFuncValue, Params: sig.Params}
				return a.annotate(e, resultType(sig))
			}
			a.errf(id.Pos(), "%q is not a function (it has type %s)", id.Name, sym.Type)
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			return a.annotate(e, types.AnyT)
		}
		a.errf(id.Pos(), "%q is not callable", id.Name)
		for _, arg := range e.Args {
			a.checkExpr(arg, nil)
		}
		return a.annotate(e, types.AnyT)
	}

	if m, ok := e.Fn.(*ast.MemberExpr); ok {
		if pid, ok := m.X.(*ast.Ident); ok {
			if sym := a.lookup(pid.Name); sym != nil && sym.Kind == SymPackage {
				a.prog.Idents[pid] = sym
				f := sym.Package.Funcs[m.Name]
				if f == nil {
					a.errf(m.Pos(), "package %s has no function %q", sym.Package.Name, m.Name)
					for _, arg := range e.Args {
						a.checkExpr(arg, nil)
					}
					return a.annotate(e, types.AnyT)
				}
				a.prog.Members[m] = &MemberInfo{Kind: MemberPackage, StdFunc: f, Package: sym.Package}
				a.checkArgList(e, f.Params, nil, sym.Package.Name+"."+f.Name)
				a.prog.Calls[e] = &CallInfo{Kind: CallStd, StdRuntime: f.Runtime, Params: f.Params}
				return a.annotate(e, resultType(f.Sig()))
			}
		}
		return a.checkMethodCall(e, m)
	}

	ft := a.checkExpr(e.Fn, nil)
	if sig, ok := ft.(*types.Func); ok {
		a.checkArgList(e, sig.Params, nil, "function value")
		a.prog.Calls[e] = &CallInfo{Kind: CallFuncValue, Params: sig.Params}
		return a.annotate(e, resultType(sig))
	}
	a.errf(e.Fn.Pos(), "expression of type %s is not callable", ft)
	for _, arg := range e.Args {
		a.checkExpr(arg, nil)
	}
	return a.annotate(e, types.AnyT)
}

func (a *Analyzer) checkDirectCall(e *ast.CallExpr, fn *FuncInfo) types.Type {
	if fn == nil {
		return a.annotate(e, types.AnyT)
	}
	a.checkArgList(e, fn.Sig.Params, fn.Defaults, fn.Name)
	a.prog.Calls[e] = &CallInfo{Kind: CallDirect, Func: fn, Params: fn.Sig.Params}
	return a.annotate(e, resultType(fn.Sig))
}

func (a *Analyzer) checkMethodCall(e *ast.CallExpr, m *ast.MemberExpr) types.Type {
	recvType := a.checkExpr(m.X, nil)
	switch rt := recvType.(type) {
	case *types.Struct:
		sig := rt.Method(m.Name)
		if sig == nil {
			a.errf(m.Pos(), "struct %s has no method %q", rt.Name, m.Name)
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			return a.annotate(e, types.AnyT)
		}
		info := a.prog.FuncByName[a.prog.Package+"."+rt.Name+"."+m.Name]
		if info == nil {
			a.errf(m.Pos(), "internal error: method %s.%s has no compiled body", rt.Name, m.Name)
			return a.annotate(e, types.AnyT)
		}
		a.prog.Members[m] = &MemberInfo{Kind: MemberMethod, Method: info}
		a.checkArgList(e, sig.Params, info.Defaults, rt.Name+"."+m.Name)
		a.prog.Calls[e] = &CallInfo{Kind: CallMethod, Func: info, Params: sig.Params}
		return a.annotate(e, resultType(sig))
	case *types.Interface:
		idx := interfaceMethodIndex(rt, m.Name)
		if idx < 0 {
			a.errf(m.Pos(), "interface %s has no method %q", rt.Name, m.Name)
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			return a.annotate(e, types.AnyT)
		}
		sig := rt.Methods[idx]
		a.prog.Members[m] = &MemberInfo{Kind: MemberMethod}
		a.checkArgList(e, sig.Params, nil, rt.Name+"."+m.Name)
		a.prog.Calls[e] = &CallInfo{Kind: CallIface, Slot: a.prog.MethodSlot(m.Name), Params: sig.Params}
		return a.annotate(e, resultType(sig))
	}
	if recvType.Kind() == types.Any {
		a.errHint(m.Pos(), "convert it to a concrete type first", "method calls on an `any` value are not supported")
		for _, arg := range e.Args {
			a.checkExpr(arg, nil)
		}
		return a.annotate(e, types.AnyT)
	}
	a.errf(m.Pos(), "type %s has no methods", recvType)
	for _, arg := range e.Args {
		a.checkExpr(arg, nil)
	}
	return a.annotate(e, types.AnyT)
}

// checkArgList checks call arguments against parameter types and verifies that
// omitted arguments have defaults.
func (a *Analyzer) checkArgList(e *ast.CallExpr, params []types.Type, defaults []ast.Expr, callee string) {
	if len(e.Args) > len(params) {
		a.errf(e.Pos(), "%s expects %d argument(s), got %d", callee, len(params), len(e.Args))
	}
	for i, arg := range e.Args {
		var want types.Type
		if i < len(params) {
			want = params[i]
		}
		got := a.checkExpr(arg, want)
		if want != nil {
			if ok, why := types.Assignable(want, got); !ok {
				a.errf(arg.Pos(), "argument %d of %s: %s", i+1, callee, why)
			}
		}
	}
	if len(e.Args) < len(params) {
		for i := len(e.Args); i < len(params); i++ {
			if i >= len(defaults) || defaults[i] == nil {
				a.errf(e.Pos(), "%s expects %d argument(s), got %d", callee, len(params), len(e.Args))
				break
			}
		}
	}
}

func (a *Analyzer) checkConversion(e *ast.CallExpr, dst types.Type) types.Type {
	if dst == nil {
		return a.annotate(e, types.AnyT)
	}
	if len(e.Args) != 1 {
		a.errf(e.Pos(), "conversion to %s takes exactly one argument", dst)
		for _, arg := range e.Args {
			a.checkExpr(arg, nil)
		}
		return a.annotate(e, dst)
	}
	src := a.checkExpr(e.Args[0], dst)
	if !types.Convertible(dst, src) {
		a.errf(e.Pos(), "cannot convert %s to %s", src, dst)
	}
	a.prog.Calls[e] = &CallInfo{Kind: CallConvert, ConvertTo: dst}
	return a.annotate(e, dst)
}

func (a *Analyzer) checkBuiltinCall(e *ast.CallExpr, name string) types.Type {
	switch name {
	case "print", "println":
		for _, arg := range e.Args {
			a.checkExpr(arg, nil)
		}
		a.prog.Calls[e] = &CallInfo{Kind: CallBuiltin, Builtin: name}
		return a.annotate(e, types.VoidT)
	case "len":
		if len(e.Args) != 1 {
			a.errf(e.Pos(), "len takes exactly one argument")
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			return a.annotate(e, types.IntT)
		}
		t := a.checkExpr(e.Args[0], nil)
		switch t.Kind() {
		case types.String, types.SliceKind, types.ArrayKind, types.MapKind:
		default:
			a.errf(e.Args[0].Pos(), "len is not defined for %s", t)
		}
		a.prog.Calls[e] = &CallInfo{Kind: CallBuiltin, Builtin: "len"}
		return a.annotate(e, types.IntT)
	case "range":
		if len(e.Args) != 1 {
			a.errf(e.Pos(), "range takes exactly one argument")
			for _, arg := range e.Args {
				a.checkExpr(arg, nil)
			}
			return a.annotate(e, &types.Range{Elem: types.IntT})
		}
		t := a.checkExpr(e.Args[0], nil)
		a.prog.Calls[e] = &CallInfo{Kind: CallBuiltin, Builtin: "range"}
		switch tt := t.(type) {
		case *types.Slice:
			return a.annotate(e, &types.Range{Elem: tt.Elem, WithValue: true})
		case *types.Array:
			return a.annotate(e, &types.Range{Elem: tt.Elem, WithValue: true})
		}
		if t.Kind() == types.String {
			return a.annotate(e, &types.Range{Elem: types.RuneT, WithValue: true})
		}
		if !types.IsInteger(t) {
			a.errf(e.Args[0].Pos(), "range is not defined for %s", t)
		}
		return a.annotate(e, &types.Range{Elem: types.IntT})
	case "type":
		if len(e.Args) != 1 {
			a.errf(e.Pos(), "type takes exactly one argument")
		}
		for _, arg := range e.Args {
			a.checkExpr(arg, nil)
		}
		a.prog.Calls[e] = &CallInfo{Kind: CallBuiltin, Builtin: "type"}
		return a.annotate(e, types.StringT)
	case "error":
		if len(e.Args) != 1 {
			a.errf(e.Pos(), "error takes exactly one message argument")
		}
		for _, arg := range e.Args {
			got := a.checkExpr(arg, types.StringT)
			if got.Kind() != types.String {
				a.errf(arg.Pos(), "error message must be a string, got %s", got)
			}
		}
		a.prog.Calls[e] = &CallInfo{Kind: CallBuiltin, Builtin: "error"}
		return a.annotate(e, types.ErrorT)
	}
	a.errf(e.Pos(), "unsupported builtin %q", name)
	return a.annotate(e, types.AnyT)
}
