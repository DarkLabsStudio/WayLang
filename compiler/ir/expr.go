package ir

import (
	"fmt"

	"waylang/compiler/ast"
	"waylang/compiler/semantic"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// eval lowers an expression to a value.
func (fl *fnLower) eval(e ast.Expr) Value {
	switch e := e.(type) {
	case *ast.IntLit:
		if fl.isFloatExpr(e) {
			return FloatConst(float64(e.Value))
		}
		return Value{Kind: ValueInt, I: e.Value, Ty: types.IntT}
	case *ast.FloatLit:
		return FloatConst(e.Value)
	case *ast.StringLit:
		return StringConst(e.Value)
	case *ast.RuneLit:
		return Value{Kind: ValueInt, I: int64(e.Value), Ty: types.RuneT}
	case *ast.BoolLit:
		return BoolConst(e.Value)
	case *ast.NilLit:
		return NilConst(fl.l.prog.TypeOf(e))
	case *ast.FStringLit:
		return fl.fstring(e)
	case *ast.Ident:
		return fl.identValue(e)
	case *ast.UnaryExpr:
		return fl.unary(e)
	case *ast.BinaryExpr:
		return fl.binary(e)
	case *ast.CallExpr:
		return fl.callExpr(e)
	case *ast.MemberExpr:
		if info := fl.l.prog.Members[e]; info != nil && info.Kind == semantic.MemberField {
			return fl.b.Load(fl.addr(e))
		}
		fl.fail(e.Pos(), "unsupported member expression")
		return zeroValue(fl.l.prog.TypeOf(e))
	case *ast.IndexExpr:
		return fl.indexExpr(e)
	case *ast.SliceExpr:
		return fl.sliceExpr(e)
	case *ast.StructLit:
		return fl.structLit(e)
	case *ast.ArrayLit:
		return fl.arrayLit(e)
	case *ast.MapLit:
		return fl.mapLit(e)
	case *ast.FuncLit:
		info := fl.l.prog.FuncLits[e]
		if info == nil {
			fl.fail(e.Pos(), "anonymous function was not lowered")
			return zeroValue(types.AnyT)
		}
		return Value{Kind: ValueFunc, Name: info.Symbol, Ty: info.Sig}
	}
	fl.fail(e.Pos(), "unsupported expression")
	return zeroValue(fl.l.prog.TypeOf(e))
}

func (fl *fnLower) fail(pos token.Position, format string, args ...any) {
	if fl.err == nil {
		fl.err = fmt.Errorf("%s:%d:%d: %s", pos.File, pos.Line, pos.Column, fmt.Sprintf(format, args...))
	}
}

func (fl *fnLower) isFloatExpr(e ast.Expr) bool {
	t := fl.l.prog.TypeOf(e)
	return t != nil && t.Kind() == types.Float
}

func (fl *fnLower) identValue(e *ast.Ident) Value {
	sym := fl.l.prog.Idents[e]
	if sym == nil {
		return zeroValue(fl.l.prog.TypeOf(e))
	}
	switch sym.Kind {
	case semantic.SymConst:
		if sym.Const != nil {
			return constValue(sym.Const)
		}
		return zeroValue(sym.Type)
	case semantic.SymFunc:
		if sym.Func == nil {
			return zeroValue(sym.Type)
		}
		return Value{Kind: ValueFunc, Name: sym.Func.Symbol, Ty: sym.Func.Sig}
	}
	if slot, ok := fl.vars[sym]; ok {
		if slot.Addr {
			return fl.b.Load(slot)
		}
		return slot
	}
	if sym.Global != nil {
		ptr := fl.b.GlobalPtr(sym.Global.Symbol, sym.Global.Type)
		return fl.b.Load(ptr)
	}
	return zeroValue(sym.Type)
}

func constValue(c *semantic.Constant) Value {
	switch c.Kind {
	case types.Float:
		return FloatConst(c.Float)
	case types.String:
		return StringConst(c.Str)
	case types.Bool:
		return BoolConst(c.Bool)
	case types.Rune:
		return Value{Kind: ValueInt, I: c.Int, Ty: types.RuneT}
	case types.Byte:
		return Value{Kind: ValueInt, I: c.Int, Ty: types.ByteT}
	}
	return Value{Kind: ValueInt, I: c.Int, Ty: types.IntT}
}

func (fl *fnLower) unary(e *ast.UnaryExpr) Value {
	t := fl.l.prog.TypeOf(e)
	v := fl.eval(e.X)
	switch e.Op {
	case token.Minus:
		if t != nil && t.Kind() == types.Any {
			return fl.b.Call("way_any_neg", []Value{fl.coerce(v, types.AnyT)}, types.AnyT)
		}
		return fl.b.Arith(token.Minus, zeroValue(v.Ty), v, v.Ty)
	case token.Not:
		return fl.b.Cmp(token.Eq, v, BoolConst(false))
	}
	return v
}

func (fl *fnLower) binary(e *ast.BinaryExpr) Value {
	result := fl.l.prog.TypeOf(e)
	lt := fl.l.prog.TypeOf(e.X)
	rt := fl.l.prog.TypeOf(e.Y)

	switch e.Op {
	case token.And, token.Or:
		return fl.shortCircuit(e, result)
	}
	switch e.Op {
	case token.Eq, token.Ne, token.Lt, token.Le, token.Gt, token.Ge:
		return fl.compare(e.Op, fl.eval(e.X), fl.eval(e.Y))
	}
	if isDynamic(lt) || isDynamic(rt) {
		x := fl.coerce(fl.eval(e.X), types.AnyT)
		y := fl.coerce(fl.eval(e.Y), types.AnyT)
		name := "way_any_add"
		switch e.Op {
		case token.Minus:
			name = "way_any_sub"
		case token.Star:
			name = "way_any_mul"
		case token.Slash:
			name = "way_any_div"
		case token.Percent:
			name = "way_any_mod"
		}
		return fl.b.Call(name, []Value{x, y}, result)
	}

	x := fl.eval(e.X)
	y := fl.eval(e.Y)
	if e.Op == token.Plus && lt != nil && lt.Kind() == types.String {
		return fl.b.Call("way_str_concat", []Value{x, y}, types.StringT)
	}
	return fl.b.Arith(e.Op, x, y, result)
}

func isDynamic(t types.Type) bool {
	if t == nil {
		return false
	}
	return t.Kind() == types.Any || t.Kind() == types.InterfaceKind
}

// shortCircuit lowers && and || with real control flow.
func (fl *fnLower) shortCircuit(e *ast.BinaryExpr, result types.Type) Value {
	slot := fl.b.Alloca(types.BoolT)
	left := fl.eval(e.X)
	rhsB := fl.f.NewBlock("logic.rhs")
	endB := fl.f.NewBlock("logic.end")
	if e.Op == token.And {
		fl.b.Store(slot, left)
		fl.b.Branch(left, rhsB, endB)
	} else {
		fl.b.Store(slot, left)
		fl.b.Branch(left, endB, rhsB)
	}
	fl.b.SetBlock(rhsB)
	right := fl.eval(e.Y)
	fl.b.Store(slot, right)
	fl.b.Jump(endB)
	fl.b.SetBlock(endB)
	return fl.b.Load(slot)
}

// compare emits an equality or ordering comparison.
// binaryValue applies a binary operator to two already-evaluated values.
func (fl *fnLower) binaryValue(op token.Kind, x, y Value, result types.Type) Value {
	if isDynamic(x.Ty) || isDynamic(y.Ty) {
		a := fl.coerce(x, types.AnyT)
		b := fl.coerce(y, types.AnyT)
		name := "way_any_add"
		switch op {
		case token.Minus:
			name = "way_any_sub"
		case token.Star:
			name = "way_any_mul"
		case token.Slash:
			name = "way_any_div"
		case token.Percent:
			name = "way_any_mod"
		}
		return fl.b.Call(name, []Value{a, b}, result)
	}
	if op == token.Plus && x.Ty != nil && x.Ty.Kind() == types.String {
		return fl.b.Call("way_str_concat", []Value{x, y}, types.StringT)
	}
	return fl.b.Arith(op, x, y, result)
}

func (fl *fnLower) compare(op token.Kind, x, y Value) Value {
	if isDynamic(x.Ty) || isDynamic(y.Ty) || x.Kind == ValueNil || y.Kind == ValueNil && isDynamic(otherOf(x, y).Ty) {
		x = fl.coerce(x, types.AnyT)
		y = fl.coerce(y, types.AnyT)
		name := "way_any_eq"
		switch op {
		case token.Ne:
			name = "way_any_ne"
		case token.Lt:
			name = "way_any_lt"
		case token.Le:
			name = "way_any_le"
		case token.Gt:
			name = "way_any_gt"
		case token.Ge:
			name = "way_any_ge"
		}
		return fl.b.Call(name, []Value{x, y}, types.BoolT)
	}
	if x.Ty != nil && x.Ty.Kind() == types.String && y.Ty != nil && y.Ty.Kind() == types.String {
		switch op {
		case token.Eq:
			return fl.b.Call("way_str_eq", []Value{x, y}, types.BoolT)
		case token.Ne:
			eq := fl.b.Call("way_str_eq", []Value{x, y}, types.BoolT)
			return fl.b.Cmp(token.Eq, eq, BoolConst(false))
		default:
			cmp := fl.b.Call("way_str_cmp", []Value{x, y}, types.IntT)
			return fl.b.Cmp(op, cmp, IntConst(0))
		}
	}
	return fl.b.Cmp(op, x, y)
}

func otherOf(x, y Value) Value {
	if x.Kind == ValueNil {
		return y
	}
	return x
}

// coerce converts v for storage in a position of type to.
func (fl *fnLower) coerce(v Value, to types.Type) Value {
	if to == nil {
		return v
	}
	if v.Ty == nil {
		v.Ty = to
	}
	if types.Identical(v.Ty, to) {
		return v
	}
	if to.Kind() == types.Any || to.Kind() == types.InterfaceKind {
		if v.Ty.Kind() == types.Any || v.Ty.Kind() == types.InterfaceKind {
			return v
		}
		if v.Kind == ValueZero || v.Kind == ValueNil {
			return zeroValue(to)
		}
		return fl.box(v, to)
	}
	if to.Kind() == types.Nil {
		return NilConst(to)
	}
	return v
}

// box wraps a concrete value in a {data, typeinfo} box.
func (fl *fnLower) box(v Value, to types.Type) Value {
	ti := fl.l.typeInfo(v.Ty)
	tiVal := Value{Kind: ValueGlobal, Name: ti, Ty: types.AnyT}
	var data Value
	switch v.Ty.Kind() {
	case types.String, types.Error:
		// Strings and errors are already heap objects; the box points at them.
		data = v
	default:
		data = fl.b.BoxValue(v, ti)
	}
	return fl.b.MakeStruct(to, data, tiVal)
}

// convert performs an explicit conversion.
func (fl *fnLower) convert(v Value, to types.Type) Value {
	if to == nil {
		return v
	}
	if types.Identical(v.Ty, to) {
		return v
	}
	if to.Kind() == types.Any || to.Kind() == types.InterfaceKind {
		return fl.coerce(v, to)
	}
	if to.Kind() == types.Nil {
		return NilConst(to)
	}
	if isDynamic(v.Ty) {
		return fl.b.UnboxValue(v, to)
	}
	if v.Ty != nil && v.Ty.Kind() == types.String && to.Kind() == types.String {
		return v
	}
	return fl.b.Convert(v, to)
}

// --- calls ----------------------------------------------------------------

func (fl *fnLower) callExpr(e *ast.CallExpr) Value {
	info := fl.l.prog.Calls[e]
	if info == nil {
		fl.fail(e.Pos(), "call was not resolved")
		return zeroValue(fl.l.prog.TypeOf(e))
	}
	ret := fl.l.prog.TypeOf(e)
	switch info.Kind {
	case semantic.CallDirect:
		args := fl.buildArgs(e.Args, info.Params, info.Func.Defaults)
		return fl.b.Call(info.Func.Symbol, args, ret)
	case semantic.CallMethod:
		member := e.Fn.(*ast.MemberExpr)
		self := fl.addr(member.X)
		args := append([]Value{self}, fl.buildArgs(e.Args, info.Params, info.Func.Defaults)...)
		return fl.b.Call(info.Func.Symbol, args, ret)
	case semantic.CallIface:
		member := e.Fn.(*ast.MemberExpr)
		recv := fl.eval(member.X)
		args := fl.buildArgs(e.Args, info.Params, nil)
		return fl.b.IfaceCall(recv, info.Slot, args, ret)
	case semantic.CallStd:
		args := fl.buildArgs(e.Args, info.Params, nil)
		return fl.b.Call(info.StdRuntime, args, ret)
	case semantic.CallConvert:
		return fl.convert(fl.eval(e.Args[0]), info.ConvertTo)
	case semantic.CallBuiltin:
		return fl.builtinCall(e, info.Builtin)
	case semantic.CallFuncValue:
		fn := fl.eval(e.Fn)
		args := fl.buildArgs(e.Args, info.Params, nil)
		return fl.b.CallIndirect(fn, args, ret)
	}
	fl.fail(e.Pos(), "unsupported call")
	return zeroValue(ret)
}

// buildArgs evaluates call arguments, filling omitted parameters from defaults.
func (fl *fnLower) buildArgs(args []ast.Expr, params []types.Type, defaults []ast.Expr) []Value {
	var out []Value
	for i, a := range args {
		var want types.Type
		if i < len(params) {
			want = params[i]
		}
		out = append(out, fl.coerce(fl.eval(a), want))
	}
	for i := len(args); i < len(params); i++ {
		if i < len(defaults) && defaults[i] != nil {
			out = append(out, fl.coerce(fl.eval(defaults[i]), params[i]))
			continue
		}
		out = append(out, zeroValue(params[i]))
	}
	return out
}

func (fl *fnLower) builtinCall(e *ast.CallExpr, name string) Value {
	switch name {
	case "print", "println":
		for i, arg := range e.Args {
			if i > 0 {
				fl.printString(" ")
			}
			fl.printValue(fl.eval(arg), fl.l.prog.TypeOf(arg))
		}
		if name == "println" {
			fl.printString("\n")
		}
		return zeroValue(types.VoidT)
	case "len":
		t := fl.l.prog.TypeOf(e.Args[0])
		switch tt := t.(type) {
		case *types.Slice:
			return fl.sliceLen(fl.eval(e.Args[0]))
		case *types.Array:
			return IntConst(tt.Len)
		case *types.Map:
			return fl.b.Call("way_map_len", []Value{fl.eval(e.Args[0])}, types.IntT)
		}
		return fl.b.Call("way_str_len", []Value{fl.eval(e.Args[0])}, types.IntT)
	case "range":
		fl.fail(e.Pos(), "range() can only be used directly in a for loop")
		return IntConst(0)
	case "type":
		t := fl.l.prog.TypeOf(e.Args[0])
		if isDynamic(t) {
			box := fl.coerce(fl.eval(e.Args[0]), types.AnyT)
			return fl.b.Call("way_any_type_name", []Value{box}, types.StringT)
		}
		return StringConst(t.String())
	case "error":
		msg := fl.eval(e.Args[0])
		return fl.b.Call("way_error_new", []Value{msg}, types.ErrorT)
	}
	fl.fail(e.Pos(), "unsupported builtin %q", name)
	return zeroValue(types.AnyT)
}

// --- printing -------------------------------------------------------------

func (fl *fnLower) printString(s string) {
	fl.b.Call("way_print_str", []Value{StringConst(s)}, types.VoidT)
}

func (fl *fnLower) printValue(v Value, t types.Type) {
	if t == nil || v.Ty == nil {
		return
	}
	switch t.Kind() {
	case types.String:
		fl.b.Call("way_print_str", []Value{v}, types.VoidT)
	case types.Error:
		fl.b.Call("way_print_str", []Value{v}, types.VoidT)
	case types.Int:
		fl.b.Call("way_print_int", []Value{v}, types.VoidT)
	case types.Rune, types.Byte:
		fl.b.Call("way_print_int", []Value{fl.b.Convert(v, types.IntT)}, types.VoidT)
	case types.Float:
		fl.b.Call("way_print_float", []Value{v}, types.VoidT)
	case types.Bool:
		fl.b.Call("way_print_bool", []Value{v}, types.VoidT)
	case types.Any:
		fl.b.Call("way_print_any", []Value{v}, types.VoidT)
	case types.InterfaceKind:
		fl.b.Call("way_print_any", []Value{fl.coerce(v, types.AnyT)}, types.VoidT)
	case types.ArrayKind:
		fl.printSequence(fl.addrTemp(v, t), IntConst(t.(*types.Array).Len), t.(*types.Array).Elem)
	case types.SliceKind:
		fl.printSequence(fl.sliceData(v, t.(*types.Slice).Elem), fl.sliceLen(v), t.(*types.Slice).Elem)
	case types.MapKind:
		fl.printMap(v, t.(*types.Map))
	case types.StructKind:
		st := t.(*types.Struct)
		fl.printString(st.Name + "{")
		base := fl.addrTemp(v, t)
		for i, f := range st.Fields {
			if i > 0 {
				fl.printString(", ")
			}
			fl.printString(f.Name + ": ")
			fl.printValue(fl.b.Load(fl.b.FieldPtr(base, i, f.Type)), f.Type)
		}
		fl.printString("}")
	}
}

// addrTemp materialises a value in a stack slot and returns its address.
func (fl *fnLower) addrTemp(v Value, t types.Type) Value {
	slot := fl.b.Alloca(t)
	fl.b.Store(slot, v)
	return slot
}

func (fl *fnLower) printSequence(data, length Value, elem types.Type) {
	fl.printString("[")
	idxSlot := fl.b.Alloca(types.IntT)
	fl.b.Store(idxSlot, IntConst(0))
	cond := fl.f.NewBlock("print.cond")
	body := fl.f.NewBlock("print.body")
	end := fl.f.NewBlock("print.end")
	fl.b.Jump(cond)
	fl.b.SetBlock(cond)
	idx := fl.b.Load(idxSlot)
	fl.b.Branch(fl.b.Cmp(token.Lt, idx, length), body, end)

	fl.b.SetBlock(body)
	idx = fl.b.Load(idxSlot)
	isFirst := fl.b.Cmp(token.Eq, idx, IntConst(0))
	sepB := fl.f.NewBlock("print.sep")
	elB := fl.f.NewBlock("print.elem")
	fl.b.Branch(isFirst, elB, sepB)
	fl.b.SetBlock(sepB)
	fl.printString(", ")
	fl.b.Jump(elB)
	fl.b.SetBlock(elB)
	idx = fl.b.Load(idxSlot)
	elemPtr := fl.b.ElemPtr(data, idx, elem)
	fl.printValue(fl.b.Load(elemPtr), elem)
	fl.b.Store(idxSlot, fl.b.Arith(token.Plus, idx, IntConst(1), types.IntT))
	fl.b.Jump(cond)
	fl.b.SetBlock(end)
	fl.printString("]")
}

func (fl *fnLower) printMap(m Value, mt *types.Map) {
	fl.printString("{")
	count := fl.b.Call("way_map_cap", []Value{m}, types.IntT)
	keySlot := fl.b.Alloca(mt.Key)
	valSlot := fl.b.Alloca(mt.Elem)
	iSlot := fl.b.Alloca(types.IntT)
	fl.b.Store(iSlot, IntConst(0))
	firstSlot := fl.b.Alloca(types.BoolT)
	fl.b.Store(firstSlot, BoolConst(true))

	cond := fl.f.NewBlock("map.cond")
	probe := fl.f.NewBlock("map.probe")
	body := fl.f.NewBlock("map.body")
	incr := fl.f.NewBlock("map.incr")
	end := fl.f.NewBlock("map.end")
	fl.b.Jump(cond)
	fl.b.SetBlock(cond)
	i := fl.b.Load(iSlot)
	fl.b.Branch(fl.b.Cmp(token.Lt, i, count), probe, end)
	fl.b.SetBlock(probe)
	found := fl.b.Call("way_map_entry", []Value{m, i, keySlot, valSlot}, types.BoolT)
	fl.b.Branch(found, body, incr)

	fl.b.SetBlock(body)
	first := fl.b.Load(firstSlot)
	sepB := fl.f.NewBlock("map.sep")
	fl.b.Branch(first, incr, sepB)
	fl.b.SetBlock(sepB)
	fl.printString(", ")
	fl.b.Store(firstSlot, BoolConst(false))
	fl.b.Jump(incr)
	fl.b.SetBlock(incr)
	// print entry then advance
	i = fl.b.Load(iSlot)
	fl.b.Store(iSlot, fl.b.Arith(token.Plus, i, IntConst(1), types.IntT))
	fl.b.Jump(cond)

	fl.b.SetBlock(end)
	fl.printString("}")
}

// --- literals and composites ---------------------------------------------

func (fl *fnLower) fstring(e *ast.FStringLit) Value {
	var parts []Value
	for _, part := range e.Parts {
		if part.Expr == nil {
			parts = append(parts, StringConst(part.Text))
			continue
		}
		v := fl.eval(part.Expr)
		parts = append(parts, fl.valueToString(v, fl.l.prog.TypeOf(part.Expr)))
	}
	if len(parts) == 0 {
		return StringConst("")
	}
	acc := parts[0]
	for _, p := range parts[1:] {
		acc = fl.b.Call("way_str_concat", []Value{acc, p}, types.StringT)
	}
	return acc
}

// valueToString converts a value to a string for interpolation.
func (fl *fnLower) valueToString(v Value, t types.Type) Value {
	if t == nil {
		return StringConst("")
	}
	switch t.Kind() {
	case types.String:
		return v
	case types.Error:
		return v
	case types.Int:
		return fl.b.Call("way_str_from_int", []Value{v}, types.StringT)
	case types.Rune:
		return fl.b.Call("way_str_from_rune", []Value{v}, types.StringT)
	case types.Byte:
		return fl.b.Call("way_str_from_int", []Value{fl.b.Convert(v, types.IntT)}, types.StringT)
	case types.Float:
		return fl.b.Call("way_str_from_float", []Value{v}, types.StringT)
	case types.Bool:
		return fl.b.Call("way_str_from_bool", []Value{v}, types.StringT)
	case types.Any, types.InterfaceKind:
		return fl.b.Call("way_any_to_string", []Value{fl.coerce(v, types.AnyT)}, types.StringT)
	}
	fl.fail(token.Position{}, "cannot interpolate a value of type %s", t)
	return StringConst("")
}

func (fl *fnLower) structLit(e *ast.StructLit) Value {
	t := fl.l.prog.TypeOf(e)
	st, ok := t.(*types.Struct)
	if !ok {
		return zeroValue(t)
	}
	slot := fl.b.Alloca(st)
	provided := map[string]ast.Expr{}
	for _, f := range e.Fields {
		provided[f.Name] = f.Value
	}
	for i, field := range st.Fields {
		var v Value
		if expr, ok := provided[field.Name]; ok {
			v = fl.coerce(fl.eval(expr), field.Type)
		} else if def, ok := fl.l.prog.FieldDefaults[field]; ok {
			v = fl.coerce(fl.eval(def), field.Type)
		} else {
			v = zeroValue(field.Type)
		}
		fl.b.Store(fl.b.FieldPtr(slot, i, field.Type), v)
	}
	return fl.b.Load(slot)
}

func (fl *fnLower) arrayLit(e *ast.ArrayLit) Value {
	t := fl.l.prog.TypeOf(e)
	switch tt := t.(type) {
	case *types.Slice:
		count := IntConst(int64(len(e.Values)))
		data := fl.b.SeqAlloc(count, tt.Elem)
		for i, val := range e.Values {
			v := fl.coerce(fl.eval(val), tt.Elem)
			fl.b.Store(fl.b.ElemPtr(data, IntConst(int64(i)), tt.Elem), v)
		}
		return fl.b.MakeSlice(data, count, count, tt)
	case *types.Array:
		slot := fl.b.Alloca(tt)
		for i, val := range e.Values {
			v := fl.coerce(fl.eval(val), tt.Elem)
			fl.b.Store(fl.b.ElemPtr(slot, IntConst(int64(i)), tt.Elem), v)
		}
		return fl.b.Load(slot)
	}
	return zeroValue(t)
}

func (fl *fnLower) mapLit(e *ast.MapLit) Value {
	t := fl.l.prog.TypeOf(e)
	mt, ok := t.(*types.Map)
	if !ok {
		return zeroValue(t)
	}
	m := fl.b.NewMap(mt.Key, mt.Elem)
	for _, entry := range e.Entries {
		k := fl.coerce(fl.eval(entry.Key), mt.Key)
		v := fl.coerce(fl.eval(entry.Value), mt.Elem)
		fl.mapSet(m, mt, k, v)
	}
	return m
}

func (fl *fnLower) mapSet(m Value, mt *types.Map, key, val Value) {
	keySlot := fl.b.Alloca(mt.Key)
	fl.b.Store(keySlot, key)
	valSlot := fl.b.Alloca(mt.Elem)
	fl.b.Store(valSlot, val)
	fl.b.Call("way_map_set", []Value{m, keySlot, valSlot}, types.VoidT)
}

func (fl *fnLower) mapGet(m Value, mt *types.Map, key Value) Value {
	keySlot := fl.b.Alloca(mt.Key)
	fl.b.Store(keySlot, key)
	valSlot := fl.b.Alloca(mt.Elem)
	fl.b.Store(valSlot, zeroValue(mt.Elem))
	fl.b.Call("way_map_get_into", []Value{m, keySlot, valSlot}, types.BoolT)
	return fl.b.Load(valSlot)
}

// --- addressing -----------------------------------------------------------

// addr returns the address of an assignable expression.
func (fl *fnLower) addr(e ast.Expr) Value {
	switch e := e.(type) {
	case *ast.Ident:
		sym := fl.l.prog.Idents[e]
		if sym == nil {
			return fl.materialize(zeroValue(fl.l.prog.TypeOf(e)))
		}
		if slot, ok := fl.vars[sym]; ok {
			if slot.Addr {
				return slot
			}
			return fl.materialize(slot)
		}
		if sym.Global != nil {
			return fl.b.GlobalPtr(sym.Global.Symbol, sym.Global.Type)
		}
		return fl.materialize(fl.identValue(e))
	case *ast.MemberExpr:
		info := fl.l.prog.Members[e]
		if info != nil && info.Kind == semantic.MemberField {
			base := fl.addr(e.X)
			field := base.Ty.(*types.Struct).Fields[info.FieldIndex]
			return fl.b.FieldPtr(base, info.FieldIndex, field.Type)
		}
		return fl.materialize(fl.eval(e))
	case *ast.IndexExpr:
		xt := fl.l.prog.TypeOf(e.X)
		if arr, ok := xt.(*types.Array); ok {
			base := fl.addr(e.X)
			idx := fl.eval(e.Index)
			fl.boundsCheck(idx, IntConst(arr.Len))
			return fl.b.ElemPtr(base, idx, arr.Elem)
		}
		if sl, ok := xt.(*types.Slice); ok {
			s := fl.eval(e.X)
			idx := fl.eval(e.Index)
			fl.boundsCheck(idx, fl.sliceLen(s))
			return fl.b.ElemPtr(fl.sliceData(s, sl.Elem), idx, sl.Elem)
		}
		return fl.materialize(fl.eval(e))
	}
	return fl.materialize(fl.eval(e))
}

func (fl *fnLower) materialize(v Value) Value {
	if v.Ty == nil {
		v.Ty = types.IntT
	}
	slot := fl.b.Alloca(v.Ty)
	fl.b.Store(slot, v)
	return slot
}

func (fl *fnLower) indexExpr(e *ast.IndexExpr) Value {
	xt := fl.l.prog.TypeOf(e.X)
	switch t := xt.(type) {
	case *types.Array:
		base := fl.addr(e.X)
		idx := fl.eval(e.Index)
		fl.boundsCheck(idx, IntConst(t.Len))
		return fl.b.Load(fl.b.ElemPtr(base, idx, t.Elem))
	case *types.Slice:
		s := fl.eval(e.X)
		idx := fl.eval(e.Index)
		fl.boundsCheck(idx, fl.sliceLen(s))
		return fl.b.Load(fl.b.ElemPtr(fl.sliceData(s, t.Elem), idx, t.Elem))
	case *types.Map:
		return fl.mapGet(fl.eval(e.X), t, fl.eval(e.Index))
	}
	if xt != nil && xt.Kind() == types.String {
		return fl.b.Call("way_str_index", []Value{fl.eval(e.X), fl.eval(e.Index)}, types.ByteT)
	}
	return zeroValue(fl.l.prog.TypeOf(e))
}

func (fl *fnLower) sliceExpr(e *ast.SliceExpr) Value {
	xt := fl.l.prog.TypeOf(e.X)
	switch t := xt.(type) {
	case *types.Slice:
		s := fl.eval(e.X)
		low := IntConst(0)
		if e.Low != nil {
			low = fl.eval(e.Low)
		}
		high := fl.sliceLen(s)
		if e.High != nil {
			high = fl.eval(e.High)
		}
		count := fl.b.Arith(token.Minus, high, low, types.IntT)
		data := fl.b.ElemPtr(fl.sliceData(s, t.Elem), low, t.Elem)
		return fl.b.MakeSlice(data, count, count, t)
	case *types.Array:
		base := fl.addr(e.X)
		low := IntConst(0)
		if e.Low != nil {
			low = fl.eval(e.Low)
		}
		high := IntConst(t.Len)
		if e.High != nil {
			high = fl.eval(e.High)
		}
		sliceT := &types.Slice{Elem: t.Elem}
		count := fl.b.Arith(token.Minus, high, low, types.IntT)
		data := fl.b.ElemPtr(base, low, t.Elem)
		return fl.b.MakeSlice(data, count, count, sliceT)
	}
	if xt != nil && xt.Kind() == types.String {
		s := fl.eval(e.X)
		low := IntConst(0)
		if e.Low != nil {
			low = fl.eval(e.Low)
		}
		high := fl.b.Call("way_str_len", []Value{s}, types.IntT)
		if e.High != nil {
			high = fl.eval(e.High)
		}
		return fl.b.Call("way_str_substr", []Value{s, low, high}, types.StringT)
	}
	return zeroValue(fl.l.prog.TypeOf(e))
}

// --- slice helpers --------------------------------------------------------

func (fl *fnLower) sliceLen(s Value) Value {
	return fl.b.Extract(s, 1, types.IntT)
}

func (fl *fnLower) sliceData(s Value, elem types.Type) Value {
	return fl.b.ExtractPtr(s, 0, elem)
}

func (fl *fnLower) boundsCheck(idx, limit Value) {
	fl.b.Call("way_bounds_check", []Value{idx, limit}, types.VoidT)
}

// --- for-in ---------------------------------------------------------------

func (fl *fnLower) forInStmt(s *ast.ForInStmt) {
	syms := fl.l.prog.StmtSyms[s]
	if len(syms) == 0 {
		return
	}
	if call, ok := s.Iter.(*ast.CallExpr); ok {
		if info := fl.l.prog.Calls[call]; info != nil && info.Kind == semantic.CallBuiltin && info.Builtin == "range" && len(call.Args) == 1 {
			fl.rangeLoop(s, syms, call.Args[0])
			return
		}
	}
	iter := fl.l.prog.TypeOf(s.Iter)
	switch t := iter.(type) {
	case *types.Array:
		base := fl.addr(s.Iter)
		fl.indexedLoop(base, IntConst(t.Len), false, t.Elem, syms, s.Body)
	case *types.Slice:
		sVal := fl.eval(s.Iter)
		fl.indexedLoop(fl.sliceData(sVal, t.Elem), fl.sliceLen(sVal), false, t.Elem, syms, s.Body)
	case *types.Map:
		fl.mapLoop(s, syms, fl.eval(s.Iter), t)
	default:
		if iter != nil && iter.Kind() == types.String {
			fl.stringLoop(s, syms, fl.eval(s.Iter))
			return
		}
		fl.fail(s.Pos(), "cannot iterate over %s", iter)
	}
}

func (fl *fnLower) rangeLoop(s *ast.ForInStmt, syms []*semantic.Symbol, bound ast.Expr) {
	bt := fl.l.prog.TypeOf(bound)
	if bt != nil && bt.Kind() == types.Int {
		limit := fl.eval(bound)
		nullData := Value{Kind: ValueZero, Ty: types.IntT}
		fl.indexedLoop(nullData, limit, true, types.IntT, syms, s.Body)
		return
	}
	switch t := bt.(type) {
	case *types.Slice:
		sVal := fl.eval(bound)
		fl.indexedLoop(fl.sliceData(sVal, t.Elem), fl.sliceLen(sVal), false, t.Elem, syms, s.Body)
	case *types.Array:
		base := fl.addr(bound)
		fl.indexedLoop(base, IntConst(t.Len), false, t.Elem, syms, s.Body)
	default:
		if bt != nil && bt.Kind() == types.String {
			fl.stringLoop(s, syms, fl.eval(bound))
			return
		}
		fl.fail(s.Pos(), "range() is not supported for %s", bt)
	}
}

// indexedLoop iterates an index from 0 to limit, exposing either the index
// (indexOnly) or index+element (two variables) or just the element (one
// variable).
func (fl *fnLower) indexedLoop(data, limit Value, indexOnly bool, elem types.Type, syms []*semantic.Symbol, body *ast.BlockStmt) {
	idxSlot := fl.b.Alloca(types.IntT)
	fl.b.Store(idxSlot, IntConst(0))
	symSlots := fl.allocSymSlots(syms)

	cond := fl.f.NewBlock("forin.cond")
	bodyB := fl.f.NewBlock("forin.body")
	incr := fl.f.NewBlock("forin.incr")
	end := fl.f.NewBlock("forin.end")
	fl.b.Jump(cond)
	fl.b.SetBlock(cond)
	idx := fl.b.Load(idxSlot)
	fl.b.Branch(fl.b.Cmp(token.Lt, idx, limit), bodyB, end)

	fl.b.SetBlock(bodyB)
	idx = fl.b.Load(idxSlot)
	if indexOnly {
		for _, slot := range symSlots {
			fl.b.Store(slot, idx)
		}
	} else if len(syms) == 1 {
		elemPtr := fl.b.ElemPtr(data, idx, elem)
		fl.b.Store(symSlots[0], fl.b.Load(elemPtr))
	} else {
		fl.b.Store(symSlots[0], idx)
		elemPtr := fl.b.ElemPtr(data, idx, elem)
		fl.b.Store(symSlots[1], fl.b.Load(elemPtr))
	}
	fl.breakTargets = append(fl.breakTargets, end)
	fl.continueTargets = append(fl.continueTargets, incr)
	fl.stmts(body.Stmts)
	fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
	fl.continueTargets = fl.continueTargets[:len(fl.continueTargets)-1]
	if !fl.b.Cur.Terminated {
		fl.b.Jump(incr)
	}

	fl.b.SetBlock(incr)
	fl.b.Store(idxSlot, fl.b.Arith(token.Plus, fl.b.Load(idxSlot), IntConst(1), types.IntT))
	fl.b.Jump(cond)
	fl.b.SetBlock(end)
}

func (fl *fnLower) stringLoop(s *ast.ForInStmt, syms []*semantic.Symbol, str Value) {
	idxSlot := fl.b.Alloca(types.IntT)
	fl.b.Store(idxSlot, IntConst(0))
	symSlots := fl.allocSymSlots(syms)
	limit := fl.b.Call("way_str_rune_len", []Value{str}, types.IntT)

	cond := fl.f.NewBlock("str.cond")
	bodyB := fl.f.NewBlock("str.body")
	incr := fl.f.NewBlock("str.incr")
	end := fl.f.NewBlock("str.end")
	fl.b.Jump(cond)
	fl.b.SetBlock(cond)
	idx := fl.b.Load(idxSlot)
	fl.b.Branch(fl.b.Cmp(token.Lt, idx, limit), bodyB, end)

	fl.b.SetBlock(bodyB)
	idx = fl.b.Load(idxSlot)
	r := fl.b.Call("way_str_rune_at", []Value{str, idx}, types.RuneT)
	if len(syms) == 1 {
		fl.b.Store(symSlots[0], r)
	} else {
		fl.b.Store(symSlots[0], idx)
		fl.b.Store(symSlots[1], r)
	}
	fl.breakTargets = append(fl.breakTargets, end)
	fl.continueTargets = append(fl.continueTargets, incr)
	fl.stmts(s.Body.Stmts)
	fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
	fl.continueTargets = fl.continueTargets[:len(fl.continueTargets)-1]
	if !fl.b.Cur.Terminated {
		fl.b.Jump(incr)
	}

	fl.b.SetBlock(incr)
	fl.b.Store(idxSlot, fl.b.Arith(token.Plus, fl.b.Load(idxSlot), IntConst(1), types.IntT))
	fl.b.Jump(cond)
	fl.b.SetBlock(end)
}

func (fl *fnLower) mapLoop(s *ast.ForInStmt, syms []*semantic.Symbol, m Value, mt *types.Map) {
	symSlots := fl.allocSymSlots(syms)
	keySlot := fl.b.Alloca(mt.Key)
	valSlot := fl.b.Alloca(mt.Elem)
	iSlot := fl.b.Alloca(types.IntT)
	fl.b.Store(iSlot, IntConst(0))
	count := fl.b.Call("way_map_cap", []Value{m}, types.IntT)

	cond := fl.f.NewBlock("map.cond")
	probe := fl.f.NewBlock("map.probe")
	bodyB := fl.f.NewBlock("map.body")
	incr := fl.f.NewBlock("map.incr")
	end := fl.f.NewBlock("map.end")
	fl.b.Jump(cond)
	fl.b.SetBlock(cond)
	fl.b.Branch(fl.b.Cmp(token.Lt, fl.b.Load(iSlot), count), probe, end)
	fl.b.SetBlock(probe)
	found := fl.b.Call("way_map_entry", []Value{m, fl.b.Load(iSlot), keySlot, valSlot}, types.BoolT)
	fl.b.Branch(found, bodyB, incr)

	fl.b.SetBlock(bodyB)
	if len(syms) == 1 {
		fl.b.Store(symSlots[0], fl.b.Load(keySlot))
	} else {
		fl.b.Store(symSlots[0], fl.b.Load(keySlot))
		fl.b.Store(symSlots[1], fl.b.Load(valSlot))
	}
	fl.breakTargets = append(fl.breakTargets, end)
	fl.continueTargets = append(fl.continueTargets, incr)
	fl.stmts(s.Body.Stmts)
	fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
	fl.continueTargets = fl.continueTargets[:len(fl.continueTargets)-1]
	if !fl.b.Cur.Terminated {
		fl.b.Jump(incr)
	}

	fl.b.SetBlock(incr)
	fl.b.Store(iSlot, fl.b.Arith(token.Plus, fl.b.Load(iSlot), IntConst(1), types.IntT))
	fl.b.Jump(cond)
	fl.b.SetBlock(end)
}

func (fl *fnLower) allocSymSlots(syms []*semantic.Symbol) []Value {
	slots := make([]Value, len(syms))
	for i, sym := range syms {
		slot := fl.b.Alloca(sym.Type)
		fl.b.Store(slot, zeroValue(sym.Type))
		fl.vars[sym] = slot
		slots[i] = slot
	}
	return slots
}
