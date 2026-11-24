package ir

import (
	"fmt"
	"sort"

	"waylang/compiler/ast"
	"waylang/compiler/semantic"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// TypeInfo describes runtime type information emitted by the backend.
type TypeInfo struct {
	Symbol  string
	Name    string
	Kind    int64
	Methods map[int]string // method slot -> function symbol
}

// Lower converts a checked program into IR.
func Lower(prog *semantic.Program) (*Module, error) {
	mod := &Module{Name: prog.Package, MethodSlots: len(prog.MethodSlots)}
	l := &lowerer{prog: prog, mod: mod, tiSyms: map[string]string{}}
	l.initTypeInfos()
	for _, g := range prog.Globals {
		mod.Globals = append(mod.Globals, &Global{Symbol: g.Symbol, Ty: g.Type})
	}
	for _, fn := range prog.Funcs {
		if err := l.lowerFunc(fn); err != nil {
			return nil, err
		}
	}
	if err := l.lowerGlobalInit(); err != nil {
		return nil, err
	}
	return mod, nil
}

type lowerer struct {
	prog    *semantic.Program
	mod     *Module
	tiSyms  map[string]string
	tiOrder []*TypeInfo
}

// initTypeInfos registers runtime type information for primitive types.
func (l *lowerer) initTypeInfos() {
	l.typeInfo(types.IntT)
	l.typeInfo(types.FloatT)
	l.typeInfo(types.BoolT)
	l.typeInfo(types.StringT)
	l.typeInfo(types.ErrorT)
	for _, st := range l.prog.StructOrder {
		l.typeInfo(st)
	}
}

// typeInfo returns (creating if needed) the type-info symbol for t.
func (l *lowerer) typeInfo(t types.Type) string {
	key := typeKey(t)
	if sym, ok := l.tiSyms[key]; ok {
		return sym
	}
	ti := &TypeInfo{Methods: map[int]string{}}
	switch tt := t.(type) {
	case *types.Basic:
		switch tt.Kind() {
		case types.Int:
			ti.Kind, ti.Name = 1, "int"
		case types.Float:
			ti.Kind, ti.Name = 2, "float"
		case types.Bool:
			ti.Kind, ti.Name = 3, "bool"
		case types.String:
			ti.Kind, ti.Name = 4, "string"
		case types.Error:
			ti.Kind, ti.Name = 8, "error"
		case types.Any:
			ti.Kind, ti.Name = 10, "any"
		default:
			ti.Kind, ti.Name = 1, tt.String()
		}
	case *types.Struct:
		ti.Kind, ti.Name = 5, tt.Name
		for _, name := range tt.MethodNames {
			for slot, slotName := range l.prog.MethodSlots {
				if slotName == name {
					ti.Methods[slot] = l.prog.Package + "." + tt.Name + "." + name
				}
			}
		}
	case *types.Slice:
		ti.Kind, ti.Name = 6, tt.String()
	case *types.Map:
		ti.Kind, ti.Name = 7, tt.String()
	case *types.Func:
		ti.Kind, ti.Name = 9, tt.String()
	case *types.Interface:
		ti.Kind, ti.Name = 10, tt.Name
	default:
		ti.Kind, ti.Name = 5, t.String()
	}
	ti.Symbol = fmt.Sprintf("way.ti.%d", len(l.tiOrder))
	l.mod.TypeInfos = append(l.mod.TypeInfos, ti)
	l.tiOrder = append(l.tiOrder, ti)
	l.tiSyms[key] = ti.Symbol
	return ti.Symbol
}

func typeKey(t types.Type) string {
	if t == nil {
		return "nil"
	}
	return t.String()
}

// --- function lowering ----------------------------------------------------

type fnLower struct {
	l  *lowerer
	fn *semantic.FuncInfo
	f  *Function
	b  *Builder

	vars            map[*semantic.Symbol]Value
	breakTargets    []*Block
	continueTargets []*Block
	defers          []deferSite
	err             error
}

type deferSite struct {
	callee   string
	argSlots []Value
	recvSlot Value
	hasRecv  bool
}

func (l *lowerer) lowerFunc(fn *semantic.FuncInfo) error {
	f := &Function{Name: fn.Name, Symbol: fn.Symbol}
	if fn == l.prog.Main {
		f.IsMain = true
		f.Symbol = "way_user_main"
	}
	fl := &fnLower{l: l, fn: fn, f: f, vars: map[*semantic.Symbol]Value{}}
	fl.b = NewBuilder(f)

	if fn.RecvSym != nil {
		p := Value{Kind: ValueTemp, Name: "self", Ty: fn.RecvSym.Type, Addr: true}
		f.Params = append(f.Params, p)
		fl.vars[fn.RecvSym] = p
	}
	for i, sym := range fn.ParamSyms {
		pt := sym.Type
		if i < len(fn.Sig.Params) {
			pt = fn.Sig.Params[i]
		}
		p := Value{Kind: ValueTemp, Name: fmt.Sprintf("arg%d", i), Ty: pt}
		f.Params = append(f.Params, p)
		slot := fl.b.Alloca(pt)
		fl.b.Store(slot, p)
		fl.vars[sym] = slot
	}
	f.Results = fn.Sig.Results

	if f.IsMain && len(l.prog.Globals) > 0 {
		fl.b.Call("way.globals.init", nil, types.VoidT)
	}

	fl.stmts(fn.Decl.Body.Stmts)
	if !fl.b.Cur.Terminated {
		fl.ret(nil, nil)
	}
	if fl.err != nil {
		return fl.err
	}
	l.mod.Funcs = append(l.mod.Funcs, f)
	return nil
}

// lowerGlobalInit emits the package-level initializer function. Every global
// is registered as a GC root, then initialized in declaration order.
func (l *lowerer) lowerGlobalInit() error {
	if len(l.prog.Globals) == 0 {
		return nil
	}
	f := &Function{Name: "init", Symbol: "way.globals.init"}
	fl := &fnLower{l: l, f: f, vars: map[*semantic.Symbol]Value{}}
	fl.b = NewBuilder(f)
	for _, g := range l.prog.Globals {
		ptr := fl.b.GlobalPtr(g.Symbol, g.Type)
		fl.b.Call("way_register_global", []Value{ptr}, types.VoidT)
		if g.Init == nil {
			continue
		}
		v := fl.coerce(fl.eval(g.Init), g.Type)
		fl.b.Store(ptr, v)
	}
	fl.b.Ret(nil)
	if fl.err != nil {
		return fl.err
	}
	l.mod.Funcs = append(l.mod.Funcs, f)
	return nil
}

// --- statements -----------------------------------------------------------

func (fl *fnLower) stmts(list []ast.Stmt) {
	for _, s := range list {
		if fl.b.Cur.Terminated {
			return
		}
		fl.stmt(s)
	}
}

func (fl *fnLower) safepoint() {
	fl.b.Call("way_gc_safepoint", nil, types.VoidT)
}

func (fl *fnLower) stmt(s ast.Stmt) {
	if s == nil {
		return
	}
	fl.safepoint()
	switch s := s.(type) {
	case *ast.ExprStmt:
		fl.eval(s.X)
	case *ast.DefineStmt:
		fl.define(s)
	case *ast.VarDeclStmt:
		fl.varDecl(s)
	case *ast.AssignStmt:
		fl.assignStmt(s)
	case *ast.ReturnStmt:
		fl.returnStmt(s)
	case *ast.IfStmt:
		fl.ifStmt(s)
	case *ast.WhileStmt:
		fl.whileStmt(s)
	case *ast.ForStmt:
		fl.forStmt(s)
	case *ast.ForInStmt:
		fl.forInStmt(s)
	case *ast.SwitchStmt:
		fl.switchStmt(s)
	case *ast.BreakStmt:
		if n := len(fl.breakTargets); n > 0 {
			fl.b.Jump(fl.breakTargets[n-1])
		}
	case *ast.ContinueStmt:
		if n := len(fl.continueTargets); n > 0 {
			fl.b.Jump(fl.continueTargets[n-1])
		}
	case *ast.DeferStmt:
		fl.deferStmt(s)
	case *ast.ConstDecl:
		// Constants are folded at use sites.
	case *ast.BlockStmt:
		fl.stmts(s.Stmts)
	}
}

func (fl *fnLower) define(s *ast.DefineStmt) {
	syms := fl.l.prog.StmtSyms[s]
	if len(syms) == 0 {
		for _, v := range s.Values {
			fl.eval(v)
		}
		return
	}
	if len(s.Values) == 1 && len(syms) > 1 {
		tup := fl.eval(s.Values[0])
		for i, sym := range syms {
			slot := fl.b.Alloca(sym.Type)
			fl.b.Store(slot, fl.b.Extract(tup, i, sym.Type))
			fl.vars[sym] = slot
		}
		return
	}
	for i, sym := range syms {
		var v Value
		if i < len(s.Values) {
			v = fl.coerce(fl.eval(s.Values[i]), sym.Type)
		} else {
			v = zeroValue(sym.Type)
		}
		slot := fl.b.Alloca(sym.Type)
		fl.b.Store(slot, v)
		fl.vars[sym] = slot
	}
}

func (fl *fnLower) varDecl(s *ast.VarDeclStmt) {
	syms := fl.l.prog.StmtSyms[s]
	if len(syms) != 1 {
		return
	}
	sym := syms[0]
	var v Value
	if s.Value != nil {
		v = fl.coerce(fl.eval(s.Value), sym.Type)
	} else {
		v = zeroValue(sym.Type)
	}
	slot := fl.b.Alloca(sym.Type)
	fl.b.Store(slot, v)
	fl.vars[sym] = slot
}

func (fl *fnLower) assignStmt(s *ast.AssignStmt) {
	if len(s.Targets) == 1 && len(s.Values) == 1 && s.Op != token.Assign {
		fl.compoundAssign(s.Targets[0], s.Op, s.Values[0])
		return
	}
	var vals []Value
	if len(s.Values) == 1 && len(s.Targets) > 1 {
		tup := fl.eval(s.Values[0])
		for i := range s.Targets {
			t := fl.l.prog.TypeOf(s.Targets[i])
			vals = append(vals, fl.b.Extract(tup, i, t))
		}
	} else {
		for i, v := range s.Values {
			var want types.Type
			if i < len(s.Targets) {
				want = fl.l.prog.TypeOf(s.Targets[i])
			}
			vals = append(vals, fl.coerce(fl.eval(v), want))
		}
	}
	for i, tgt := range s.Targets {
		if i >= len(vals) {
			break
		}
		fl.storeTo(tgt, vals[i])
	}
}

func (fl *fnLower) compoundAssign(target ast.Expr, op token.Kind, rhs ast.Expr) {
	// Map elements cannot be addressed; read, combine and write back.
	if idx, ok := target.(*ast.IndexExpr); ok {
		if _, isMap := fl.l.prog.TypeOf(idx.X).(*types.Map); isMap {
			cur := fl.eval(target)
			val := fl.eval(rhs)
			res := fl.binaryValue(indexOp(op), cur, val, fl.l.prog.TypeOf(target))
			fl.storeTo(target, res)
			return
		}
	}
	addr := fl.addr(target)
	cur := fl.b.Load(addr)
	val := fl.coerce(fl.eval(rhs), addr.Ty)
	res := fl.binaryValue(indexOp(op), cur, val, addr.Ty)
	fl.b.Store(addr, res)
}

// indexOp maps a compound assignment operator to its binary operator.
func indexOp(op token.Kind) token.Kind {
	switch op {
	case token.Plus, token.Minus, token.Star, token.Slash, token.Percent:
		// Desugared ++ and -- use the plain operator.
		return op
	case token.PlusEq:
		return token.Plus
	case token.MinusEq:
		return token.Minus
	case token.StarEq:
		return token.Star
	case token.SlashEq:
		return token.Slash
	case token.PercentEq:
		return token.Percent
	}
	return token.Plus
}

// storeTo stores a value into an assignable expression.
func (fl *fnLower) storeTo(target ast.Expr, v Value) {
	if idx, ok := target.(*ast.IndexExpr); ok {
		if m, isMap := fl.l.prog.TypeOf(idx.X).(*types.Map); isMap {
			fl.mapSet(fl.eval(idx.X), m, fl.eval(idx.Index), v)
			return
		}
	}
	addr := fl.addr(target)
	fl.b.Store(addr, fl.coerce(v, addr.Ty))
}

func (fl *fnLower) returnStmt(s *ast.ReturnStmt) {
	results := fl.fn.Sig.Results
	var vals []Value
	if len(s.Values) == 1 && len(results) > 1 {
		tup := fl.eval(s.Values[0])
		for i, t := range results {
			vals = append(vals, fl.b.Extract(tup, i, t))
		}
	} else {
		for i, v := range s.Values {
			var want types.Type
			if i < len(results) {
				want = results[i]
			}
			vals = append(vals, fl.coerce(fl.eval(v), want))
		}
	}
	fl.ret(vals, results)
}

// ret runs deferred calls and returns from the function.
func (fl *fnLower) ret(vals []Value, results []types.Type) {
	for i := len(fl.defers) - 1; i >= 0; i-- {
		site := fl.defers[i]
		var args []Value
		if site.hasRecv {
			args = append(args, fl.b.Load(site.recvSlot))
		}
		for _, slot := range site.argSlots {
			args = append(args, fl.b.Load(slot))
		}
		fl.b.Call(site.callee, args, types.VoidT)
	}
	if len(vals) == 0 && len(results) > 0 {
		for _, t := range results {
			vals = append(vals, zeroValue(t))
		}
	}
	fl.b.Ret(vals)
}

func (fl *fnLower) ifStmt(s *ast.IfStmt) {
	thenB := fl.f.NewBlock("if.then")
	elseB := fl.f.NewBlock("if.else")
	endB := fl.f.NewBlock("if.end")
	cond := fl.eval(s.Cond)
	fl.b.Branch(cond, thenB, elseB)

	fl.b.SetBlock(thenB)
	fl.stmts(s.Then.Stmts)
	if !fl.b.Cur.Terminated {
		fl.b.Jump(endB)
	}

	fl.b.SetBlock(elseB)
	for i, branch := range s.Elifs {
		next := fl.f.NewBlock("elif.cond")
		body := fl.f.NewBlock("elif.body")
		if i == 0 {
			c := fl.eval(branch.Cond)
			fl.b.Branch(c, body, next)
		} else {
			fl.b.SetBlock(next)
			c := fl.eval(branch.Cond)
			fl.b.Branch(c, body, fl.f.NewBlock("elif.next"))
			next = fl.b.Cur
		}
		fl.b.SetBlock(body)
		fl.stmts(branch.Body.Stmts)
		if !fl.b.Cur.Terminated {
			fl.b.Jump(endB)
		}
		fl.b.SetBlock(next)
	}
	if s.Else != nil {
		fl.stmts(s.Else.Stmts)
	}
	if !fl.b.Cur.Terminated {
		fl.b.Jump(endB)
	}
	fl.b.SetBlock(endB)
}

func (fl *fnLower) whileStmt(s *ast.WhileStmt) {
	condB := fl.f.NewBlock("while.cond")
	bodyB := fl.f.NewBlock("while.body")
	endB := fl.f.NewBlock("while.end")
	fl.b.Jump(condB)
	fl.b.SetBlock(condB)
	cond := fl.eval(s.Cond)
	fl.b.Branch(cond, bodyB, endB)

	fl.b.SetBlock(bodyB)
	fl.breakTargets = append(fl.breakTargets, endB)
	fl.continueTargets = append(fl.continueTargets, condB)
	fl.stmts(s.Body.Stmts)
	fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
	fl.continueTargets = fl.continueTargets[:len(fl.continueTargets)-1]
	if !fl.b.Cur.Terminated {
		fl.b.Jump(condB)
	}
	fl.b.SetBlock(endB)
}

func (fl *fnLower) forStmt(s *ast.ForStmt) {
	if s.Init != nil {
		fl.stmt(s.Init)
	}
	condB := fl.f.NewBlock("for.cond")
	bodyB := fl.f.NewBlock("for.body")
	postB := fl.f.NewBlock("for.post")
	endB := fl.f.NewBlock("for.end")
	fl.b.Jump(condB)

	fl.b.SetBlock(condB)
	if s.Cond != nil {
		cond := fl.eval(s.Cond)
		fl.b.Branch(cond, bodyB, endB)
	} else {
		fl.b.Jump(bodyB)
	}

	fl.b.SetBlock(bodyB)
	fl.breakTargets = append(fl.breakTargets, endB)
	fl.continueTargets = append(fl.continueTargets, postB)
	fl.stmts(s.Body.Stmts)
	fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
	fl.continueTargets = fl.continueTargets[:len(fl.continueTargets)-1]
	if !fl.b.Cur.Terminated {
		fl.b.Jump(postB)
	}

	fl.b.SetBlock(postB)
	if s.Post != nil {
		fl.stmt(s.Post)
	}
	if !fl.b.Cur.Terminated {
		fl.b.Jump(condB)
	}
	fl.b.SetBlock(endB)
}

func (fl *fnLower) switchStmt(s *ast.SwitchStmt) {
	endB := fl.f.NewBlock("switch.end")
	var subjectSlot Value
	var subjectType types.Type
	if s.Subject != nil {
		subjectType = fl.l.prog.TypeOf(s.Subject)
		v := fl.eval(s.Subject)
		subjectSlot = fl.b.Alloca(subjectType)
		fl.b.Store(subjectSlot, v)
	} else {
		subjectType = types.BoolT
	}

	defaultB := endB
	if s.Default != nil {
		defaultB = fl.f.NewBlock("switch.default")
	}
	next := fl.f.NewBlock("switch.case.test")
	fl.b.Jump(next)

	var bodyJumps []struct {
		body *Block
		cl   *ast.CaseClause
	}
	for _, c := range s.Cases {
		body := fl.f.NewBlock("switch.case.body")
		bodyJumps = append(bodyJumps, struct {
			body *Block
			cl   *ast.CaseClause
		}{body, c})
		fl.b.SetBlock(next)
		var matched Value
		for i, valExpr := range c.Values {
			val := fl.coerce(fl.eval(valExpr), subjectType)
			var cur Value
			if s.Subject != nil {
				cur = fl.b.Load(subjectSlot)
				cur = fl.coerce(cur, val.Ty)
			} else {
				cur = val
				val = BoolConst(true)
			}
			cmp := fl.compare(token.Eq, cur, val)
			if matched.IsZero() {
				matched = cmp
			} else {
				matched = fl.b.Arith(token.Or, matched, cmp, types.BoolT)
			}
			_ = i
		}
		next = fl.f.NewBlock("switch.case.test")
		fl.b.Branch(matched, body, next)
	}
	fl.b.SetBlock(next)
	fl.b.Jump(defaultB)

	for _, bj := range bodyJumps {
		fl.b.SetBlock(bj.body)
		fl.breakTargets = append(fl.breakTargets, endB)
		fl.stmts(bj.cl.Body.Stmts)
		fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
		if !fl.b.Cur.Terminated {
			fl.b.Jump(endB)
		}
	}
	if s.Default != nil {
		fl.b.SetBlock(defaultB)
		fl.breakTargets = append(fl.breakTargets, endB)
		fl.stmts(s.Default.Stmts)
		fl.breakTargets = fl.breakTargets[:len(fl.breakTargets)-1]
		if !fl.b.Cur.Terminated {
			fl.b.Jump(endB)
		}
	}
	fl.b.SetBlock(endB)
}

func (fl *fnLower) deferStmt(s *ast.DeferStmt) {
	call, ok := s.Call.(*ast.CallExpr)
	if !ok {
		fl.err = fmt.Errorf("defer requires a function call")
		return
	}
	info := fl.l.prog.Calls[call]
	if info == nil {
		fl.err = fmt.Errorf("defer requires a resolvable function call")
		return
	}
	site := deferSite{}
	switch info.Kind {
	case semantic.CallDirect:
		site.callee = info.Func.Symbol
		for i, arg := range call.Args {
			var want types.Type
			if i < len(info.Func.Sig.Params) {
				want = info.Func.Sig.Params[i]
			}
			v := fl.coerce(fl.eval(arg), want)
			slot := fl.b.Alloca(v.Ty)
			fl.b.Store(slot, v)
			site.argSlots = append(site.argSlots, slot)
		}
		for i := len(call.Args); i < len(info.Func.Sig.Params); i++ {
			if info.Func.Defaults == nil || info.Func.Defaults[i] == nil {
				break
			}
			v := fl.coerce(fl.eval(info.Func.Defaults[i]), info.Func.Sig.Params[i])
			slot := fl.b.Alloca(v.Ty)
			fl.b.Store(slot, v)
			site.argSlots = append(site.argSlots, slot)
		}
	case semantic.CallMethod:
		member := call.Fn.(*ast.MemberExpr)
		recv := fl.addr(member.X)
		site.callee = info.Func.Symbol
		site.hasRecv = true
		site.recvSlot = fl.b.Alloca(recv.Ty)
		fl.b.Store(site.recvSlot, recv)
		for i, arg := range call.Args {
			var want types.Type
			if i < len(info.Func.Sig.Params) {
				want = info.Func.Sig.Params[i]
			}
			v := fl.coerce(fl.eval(arg), want)
			slot := fl.b.Alloca(v.Ty)
			fl.b.Store(slot, v)
			site.argSlots = append(site.argSlots, slot)
		}
	default:
		fl.err = fmt.Errorf("defer only supports direct function and method calls")
		return
	}
	fl.defers = append(fl.defers, site)
}

// --- helpers --------------------------------------------------------------

func zeroValue(t types.Type) Value {
	if t == nil {
		t = types.IntT
	}
	return Value{Kind: ValueZero, Ty: t}
}

// sigResultType collapses a signature's results into one type.
func sigResultType(sig *types.Func) types.Type {
	switch len(sig.Results) {
	case 0:
		return types.VoidT
	case 1:
		return sig.Results[0]
	}
	return &types.Tuple{Elems: sig.Results}
}

// sortedMethodSlots returns method slots in ascending order.
func sortedMethodSlots(m map[int]string) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
