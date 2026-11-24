package semantic

import (
	"waylang/compiler/ast"
	"waylang/compiler/diag"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// Check performs name resolution and type checking on a parsed file.
func Check(file *ast.File, path string, src []byte) (*Program, []diag.Diagnostic) {
	prog := NewProgram(file.Package, path)
	prog.Source = src
	a := &Analyzer{prog: prog, file: file, path: path}
	a.run()
	return prog, a.diags.Diags
}

// Analyzer holds checking state.
type Analyzer struct {
	prog  *Program
	file  *ast.File
	path  string
	diags diag.List

	scope *scope

	currentFunc *FuncInfo
	loopDepth   int
	switchDepth int

	typeNames map[string]types.Type
}

type scope struct {
	parent *scope
	names  map[string]*Symbol
}

// Run performs all checking passes.
func (a *Analyzer) run() {
	a.typeNames = map[string]types.Type{
		"int":    types.IntT,
		"float":  types.FloatT,
		"bool":   types.BoolT,
		"string": types.StringT,
		"byte":   types.ByteT,
		"rune":   types.RuneT,
		"void":   types.VoidT,
		"any":    types.AnyT,
		"error":  types.ErrorT,
	}
	a.scope = &scope{names: map[string]*Symbol{}}
	for name, t := range a.typeNames {
		a.scope.names[name] = &Symbol{Name: name, Kind: SymType, TypeArg: t}
	}

	a.declareImports()
	a.declareBuiltins()
	a.collectTypeDecls()
	a.declareConsts()
	a.resolveStructFields()
	a.resolveInterfaceMethods()
	a.declareFuncs()
	a.declareGlobals()
	a.checkDefaults()
	a.checkFuncDefaults()
	a.checkGlobalInits()
	a.checkBodies()
	a.checkEntryPoint()
}

func (a *Analyzer) errf(pos token.Position, format string, args ...any) {
	a.diags.Errorf(pos, format, args...)
}

func (a *Analyzer) errHint(pos token.Position, hint, format string, args ...any) {
	a.diags.ErrorfHint(pos, hint, format, args...)
}

// --- scopes ---------------------------------------------------------------

func (a *Analyzer) push() { a.scope = &scope{parent: a.scope, names: map[string]*Symbol{}} }

func (a *Analyzer) pop() { a.scope = a.scope.parent }

func (a *Analyzer) declare(sym *Symbol, pos token.Position) *Symbol {
	if sym.Name == "_" {
		return sym
	}
	if _, exists := a.scope.names[sym.Name]; exists {
		a.errHint(pos, "choose a different name or assign to the existing declaration",
			"%q is already declared in this scope", sym.Name)
		return sym
	}
	a.scope.names[sym.Name] = sym
	return sym
}

func (a *Analyzer) lookup(name string) *Symbol {
	for s := a.scope; s != nil; s = s.parent {
		if sym, ok := s.names[name]; ok {
			return sym
		}
	}
	return nil
}

// declareBuiltins registers the language's builtin functions.
func (a *Analyzer) declareBuiltins() {
	for _, name := range []string{"print", "println", "len", "range", "type"} {
		if _, exists := a.scope.names[name]; exists {
			continue
		}
		a.scope.names[name] = &Symbol{Name: name, Kind: SymBuiltin, Builtin: name}
	}
}

// declareConsts declares package-level constants before struct fields are
// resolved, so that constants can size arrays and fields.
func (a *Analyzer) declareConsts() {
	for _, d := range a.file.Decls {
		cd, ok := d.(*ast.ConstDecl)
		if !ok {
			continue
		}
		c, ok := a.evalConst(cd.Value)
		if !ok {
			a.errf(cd.Pos(), "constant %q must have a value known at compile time", cd.Name)
			continue
		}
		ct := a.resolveType(cd.Type)
		if cd.Type == nil {
			ct = basicOf(c.Kind)
		}
		if ct == nil {
			continue
		}
		if ok, why := types.Assignable(ct, basicOf(c.Kind)); !ok {
			a.errf(cd.Pos(), "cannot use constant value of type %s as %s: %s", basicOf(c.Kind), ct, why)
			continue
		}
		cv := *c
		a.declare(&Symbol{Name: cd.Name, Kind: SymConst, Type: ct, Const: &cv}, cd.Pos())
	}
}

func (a *Analyzer) declareBuiltin(name string, sig *types.Func) {
	a.scope.names[name] = &Symbol{Name: name, Kind: SymBuiltin, Type: sig, Builtin: name}
}

// --- imports --------------------------------------------------------------

func (a *Analyzer) declareImports() {
	for _, imp := range a.file.Imports {
		pkg := LookupStdPackage(imp.Path)
		if pkg == nil {
			a.errHint(imp.Pos(), "available packages: math, os, strings, time",
				"unknown package %q", imp.Path)
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = pkg.Name
		}
		if alias == "_" {
			continue
		}
		if _, exists := a.prog.Imports[alias]; exists {
			a.errf(imp.Pos(), "package %q is imported twice", alias)
			continue
		}
		a.prog.Imports[alias] = pkg
		a.declare(&Symbol{Name: alias, Kind: SymPackage, Package: pkg}, imp.Pos())
	}
}

// --- type declarations ----------------------------------------------------

func (a *Analyzer) collectTypeDecls() {
	for _, d := range a.file.Decls {
		switch d := d.(type) {
		case *ast.StructDecl:
			if _, exists := a.typeNames[d.Name]; exists {
				a.errf(d.Pos(), "type %q is already declared", d.Name)
				continue
			}
			st := &types.Struct{Name: d.Name, Pkg: a.prog.Package}
			a.typeNames[d.Name] = st
			a.prog.Structs[d.Name] = st
			a.prog.StructOrder = append(a.prog.StructOrder, st)
			a.scope.names[d.Name] = &Symbol{Name: d.Name, Kind: SymType, TypeArg: st}
		case *ast.InterfaceDecl:
			if _, exists := a.typeNames[d.Name]; exists {
				a.errf(d.Pos(), "type %q is already declared", d.Name)
				continue
			}
			it := &types.Interface{Name: d.Name, Pkg: a.prog.Package}
			a.typeNames[d.Name] = it
			a.prog.Interfaces[d.Name] = it
			a.prog.InterfaceOrder = append(a.prog.InterfaceOrder, it)
			a.scope.names[d.Name] = &Symbol{Name: d.Name, Kind: SymType, TypeArg: it}
		}
	}
}

func (a *Analyzer) resolveStructFields() {
	for _, d := range a.file.Decls {
		sd, ok := d.(*ast.StructDecl)
		if !ok {
			continue
		}
		st := a.prog.Structs[sd.Name]
		if st == nil {
			continue
		}
		seen := map[string]bool{}
		for _, f := range sd.Fields {
			if seen[f.Name] {
				a.errf(f.Pos(), "duplicate field %q in struct %s", f.Name, st.Name)
				continue
			}
			seen[f.Name] = true
			ft := a.resolveType(f.Type)
			if ft == nil {
				continue
			}
			if ft.Kind() == types.Void {
				a.errf(f.Pos(), "field %q cannot have type void", f.Name)
				continue
			}
			field := &types.Field{Name: f.Name, Type: ft}
			st.Fields = append(st.Fields, field)
			if f.Default != nil {
				a.prog.FieldDefaults[field] = f.Default
			}
		}
	}
	// Reject structs that contain themselves by value.
	for _, st := range a.prog.StructOrder {
		if cycle := findValueCycle(st, map[*types.Struct]bool{}, map[*types.Struct]bool{}); cycle != nil {
			a.errf(token.Position{File: a.path, Line: 1, Column: 1},
				"invalid recursive type %s: a struct cannot contain itself by value; use []%s", st.Name, st.Name)
		}
	}
}

func findValueCycle(st *types.Struct, visiting, done map[*types.Struct]bool) *types.Struct {
	if done[st] {
		return nil
	}
	if visiting[st] {
		return st
	}
	visiting[st] = true
	for _, f := range st.Fields {
		if s, ok := f.Type.(*types.Struct); ok {
			if c := findValueCycle(s, visiting, done); c != nil {
				return c
			}
		}
		if arr, ok := f.Type.(*types.Array); ok {
			if s, ok := arr.Elem.(*types.Struct); ok {
				if c := findValueCycle(s, visiting, done); c != nil {
					return c
				}
			}
		}
	}
	visiting[st] = false
	done[st] = true
	return nil
}

func (a *Analyzer) resolveInterfaceMethods() {
	for _, d := range a.file.Decls {
		id, ok := d.(*ast.InterfaceDecl)
		if !ok {
			continue
		}
		it := a.prog.Interfaces[id.Name]
		if it == nil {
			continue
		}
		seen := map[string]bool{}
		for _, m := range id.Methods {
			if seen[m.Name] {
				a.errf(m.Pos(), "duplicate method %q in interface %s", m.Name, it.Name)
				continue
			}
			seen[m.Name] = true
			sig := a.buildSignature(m.Params, m.Results, false)
			if sig == nil {
				continue
			}
			it.MethodNames = append(it.MethodNames, m.Name)
			it.Methods = append(it.Methods, sig)
		}
	}
}

// buildSignature resolves parameter and result types. Defaults are not
// permitted for interface methods or function types.
func (a *Analyzer) buildSignature(params []*ast.Param, results []ast.TypeExpr, allowDefaults bool) *types.Func {
	sig := &types.Func{}
	seen := map[string]bool{}
	for _, p := range params {
		if p.Name == "_" {
			// Blank parameter is allowed.
		}
		if seen[p.Name] && p.Name != "_" && p.Name != "" {
			a.errf(p.Pos(), "duplicate parameter %q", p.Name)
		}
		seen[p.Name] = true

		pt := a.resolveType(p.Type)
		if p.Type == nil {
			if p.Default == nil {
				a.errHint(p.Pos(), "add a type, e.g. `"+p.Name+": int`",
					"parameter %q needs a type", p.Name)
				continue
			}
			pt = a.inferDefaultType(p.Default)
			if pt == nil {
				a.errHint(p.Pos(), "write the parameter type explicitly",
					"cannot infer the type of parameter %q from its default value", p.Name)
				continue
			}
		}
		if pt == nil {
			continue
		}
		if !allowDefaults && p.Default != nil {
			a.errf(p.Pos(), "default values are not allowed here")
		}
		sig.Params = append(sig.Params, pt)
		sig.ParamNames = append(sig.ParamNames, p.Name)
	}
	for _, r := range results {
		rt := a.resolveType(r)
		if rt == nil {
			continue
		}
		sig.Results = append(sig.Results, rt)
		sig.ResultNames = append(sig.ResultNames, "")
	}
	return sig
}

func (a *Analyzer) inferDefaultType(e ast.Expr) types.Type {
	switch v := e.(type) {
	case *ast.IntLit:
		return types.IntT
	case *ast.FloatLit:
		return types.FloatT
	case *ast.StringLit:
		return types.StringT
	case *ast.BoolLit:
		return types.BoolT
	case *ast.RuneLit:
		return types.RuneT
	case *ast.UnaryExpr:
		return a.inferDefaultType(v.X)
	}
	return nil
}

// resolveType converts a syntactic type to a semantic type.
func (a *Analyzer) resolveType(t ast.TypeExpr) types.Type {
	if t == nil {
		return nil
	}
	switch t := t.(type) {
	case *ast.NamedType:
		if st, ok := a.typeNames[t.Name]; ok {
			return st
		}
		a.errHint(t.Pos(), "built-in types are int, float, bool, string, byte, rune, any and error",
			"unknown type %q", t.Name)
		return nil
	case *ast.ArrayType:
		c, ok := a.evalConst(t.Len)
		if !ok {
			a.errf(t.Pos(), "array length must be a constant integer expression")
			return nil
		}
		if c.Kind != types.Int || c.Int < 0 {
			a.errf(t.Pos(), "array length must be a non-negative integer")
			return nil
		}
		elem := a.resolveType(t.Elem)
		if elem == nil {
			return nil
		}
		return &types.Array{Len: c.Int, Elem: elem}
	case *ast.SliceType:
		elem := a.resolveType(t.Elem)
		if elem == nil {
			return nil
		}
		return &types.Slice{Elem: elem}
	case *ast.MapType:
		key := a.resolveType(t.Key)
		elem := a.resolveType(t.Value)
		if key == nil || elem == nil {
			return nil
		}
		if !types.IsComparable(key) {
			a.errf(t.Pos(), "map key type %s is not comparable", key)
			return nil
		}
		return &types.Map{Key: key, Elem: elem}
	case *ast.FuncTypeExpr:
		var params []*ast.Param
		for _, p := range t.Params {
			params = append(params, &ast.Param{Pos0: p.Pos(), Type: p})
		}
		sig := a.buildSignature(params, t.Results, false)
		if sig == nil {
			return nil
		}
		return sig
	}
	a.errf(t.Pos(), "unsupported type syntax")
	return nil
}

// --- functions ------------------------------------------------------------

func (a *Analyzer) declareFuncs() {
	for _, d := range a.file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			continue
		}
		var recv *types.Struct
		if fd.Recv != nil {
			rt := a.resolveType(fd.Recv.Type)
			st, ok := rt.(*types.Struct)
			if !ok {
				a.errf(fd.Recv.Pos(), "method receiver must have a struct type")
				continue
			}
			recv = st
		}
		sig := a.buildSignature(fd.Params, fd.Results, true)
		if sig == nil {
			continue
		}
		info := &FuncInfo{Name: fd.Name, Decl: fd, Sig: sig, Recv: recv, Slot: -1}
		if recv != nil {
			if recv.Method(fd.Name) != nil {
				a.errf(fd.Pos(), "method %q is already declared on %s", fd.Name, recv.Name)
				continue
			}
			info.Symbol = a.prog.Package + "." + recv.Name + "." + fd.Name
			info.Slot = a.prog.MethodSlot(fd.Name)
			if recv.Methods == nil {
				recv.Methods = map[string]*types.Func{}
			}
			recv.Methods[fd.Name] = sig
			recv.MethodNames = append(recv.MethodNames, fd.Name)
		} else {
			if a.prog.FuncByName[fd.Name] != nil {
				a.errf(fd.Pos(), "function %q is already declared", fd.Name)
				continue
			}
			info.Symbol = a.prog.Package + "." + fd.Name
		}
		// Record parameter defaults so call sites can fill omitted arguments.
		info.Defaults = make([]ast.Expr, len(fd.Params))
		for i, p := range fd.Params {
			info.Defaults[i] = p.Default
		}
		a.prog.Funcs = append(a.prog.Funcs, info)
		a.prog.FuncByName[info.Symbol] = info
		if recv == nil && fd.Name == "main" {
			a.prog.Main = info
		}
		if recv == nil {
			a.declare(&Symbol{Name: fd.Name, Kind: SymFunc, Type: sig, Func: info}, fd.Pos())
		}
	}
}

// --- globals and constants ------------------------------------------------

func (a *Analyzer) declareGlobals() {
	for _, d := range a.file.Decls {
		switch d := d.(type) {
		case *ast.GlobalDecl:
			gt := a.resolveType(d.Type)
			if gt == nil {
				continue
			}
			info := &GlobalInfo{Name: d.Name, Symbol: a.prog.Package + ".global." + d.Name, Type: gt, Decl: d, Init: d.Value}
			a.prog.Globals = append(a.prog.Globals, info)
			a.prog.GlobalByName[d.Name] = info
			a.declare(&Symbol{Name: d.Name, Kind: SymVar, Type: gt, Mutable: true, Global: info}, d.Pos())
		}
	}
}

func basicOf(k types.Kind) types.Type {
	switch k {
	case types.Int:
		return types.IntT
	case types.Float:
		return types.FloatT
	case types.String:
		return types.StringT
	case types.Bool:
		return types.BoolT
	case types.Rune:
		return types.RuneT
	case types.Byte:
		return types.ByteT
	}
	return nil
}

// checkDefaults validates constant struct field default values.
func (a *Analyzer) checkDefaults() {
	for _, st := range a.prog.StructOrder {
		for _, f := range st.Fields {
			def, ok := a.prog.FieldDefaults[f]
			if !ok {
				continue
			}
			c, ok := a.evalConst(def)
			if !ok {
				a.errf(def.Pos(), "default value for field %q must be a constant", f.Name)
				continue
			}
			if ok, why := types.Assignable(f.Type, basicOf(c.Kind)); !ok {
				a.errf(def.Pos(), "default value for field %q is invalid: %s", f.Name, why)
			}
		}
	}
}

// checkFuncDefaults validates constant parameter defaults.
func (a *Analyzer) checkFuncDefaults() {
	for _, fn := range a.prog.Funcs {
		for i, d := range fn.Defaults {
			if d == nil || i >= len(fn.Sig.Params) {
				continue
			}
			c, ok := a.evalConst(d)
			if !ok {
				a.errf(d.Pos(), "default value for parameter %d of %s must be a constant", i+1, fn.Name)
				continue
			}
			if ok, why := types.Assignable(fn.Sig.Params[i], basicOf(c.Kind)); !ok {
				a.errf(d.Pos(), "default value for parameter %d of %s: %s", i+1, fn.Name, why)
			}
		}
	}
}

// checkGlobalInits type-checks package-level initializer expressions.
func (a *Analyzer) checkGlobalInits() {
	for _, g := range a.prog.Globals {
		if g.Init == nil {
			continue
		}
		got := a.checkExpr(g.Init, g.Type)
		if ok, why := types.Assignable(g.Type, got); !ok {
			a.errf(g.Init.Pos(), "cannot initialize global %q: %s", g.Name, why)
		}
	}
}

// checkEntryPoint requires func main() in package main.
func (a *Analyzer) checkEntryPoint() {
	if a.prog.Package != "main" {
		return
	}
	if a.prog.Main == nil {
		a.errf(token.Position{File: a.path, Line: 1, Column: 1},
			"package main must declare: func main():")
		return
	}
	if len(a.prog.Main.Sig.Params) != 0 || len(a.prog.Main.Sig.Results) != 0 {
		a.errf(a.prog.Main.Decl.Pos(), "main must take no parameters and return no values")
	}
}

// checkBodies type-checks every function body.
func (a *Analyzer) checkBodies() {
	for _, fn := range a.prog.Funcs {
		a.checkFuncBody(fn)
	}
}

func (a *Analyzer) checkFuncBody(fn *FuncInfo) {
	savedScope := a.scope
	savedFunc := a.currentFunc
	savedLoop, savedSwitch := a.loopDepth, a.switchDepth
	a.scope = &scope{parent: savedScope, names: map[string]*Symbol{}}
	a.currentFunc = fn
	a.loopDepth = 0
	a.switchDepth = 0

	a.declareParams(fn)
	a.checkBlock(fn.Decl.Body)

	a.currentFunc = savedFunc
	a.scope = savedScope
	a.loopDepth, a.switchDepth = savedLoop, savedSwitch
}

// declareParams creates symbols for a method receiver and all parameters.
func (a *Analyzer) declareParams(fn *FuncInfo) {
	fd := fn.Decl
	if fd.Recv != nil {
		t := a.resolveType(fd.Recv.Type)
		if t == nil {
			t = types.AnyT
		}
		sym := &Symbol{Name: fd.Recv.Name, Kind: SymVar, Type: t, Mutable: true, Owner: fn, ParamIndex: -1, IsReceiver: true}
		a.declare(sym, fd.Recv.Pos())
		fn.RecvSym = sym
	}
	for i, p := range fd.Params {
		t := a.paramType(fn, p, i)
		sym := &Symbol{Name: p.Name, Kind: SymVar, Type: t, Mutable: true, Owner: fn, ParamIndex: i}
		a.declare(sym, p.Pos())
		fn.ParamSyms = append(fn.ParamSyms, sym)
	}
}

// paramType resolves a parameter's type, falling back to its default value.
func (a *Analyzer) paramType(fn *FuncInfo, p *ast.Param, index int) types.Type {
	if p.Type != nil {
		if t := a.resolveType(p.Type); t != nil {
			return t
		}
	}
	if p.Default != nil {
		if t := a.inferDefaultType(p.Default); t != nil {
			return t
		}
	}
	if index < len(fn.Sig.Params) {
		return fn.Sig.Params[index]
	}
	return types.AnyT
}
