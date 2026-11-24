// Package llvm translates WayLang's IR into textual LLVM IR and drives the
// LLVM toolchain (clang/opt/llc) to produce native code.
//
// The backend targets LLVM 15+ with opaque pointers. It emits readable IR so
// that `way build --emit-ir` output can be inspected and so that compilation
// does not depend on a Go binding to LLVM's C++ libraries.
package llvm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"waylang/compiler/ir"
	"waylang/compiler/semantic"
	"waylang/compiler/token"
	"waylang/compiler/types"
)

// Emit translates a lowered module into LLVM IR text.
func Emit(mod *ir.Module, prog *semantic.Program) (string, error) {
	e := &emitter{
		mod:     mod,
		prog:    prog,
		strs:    map[string]string{},
		decls:   map[string]*runtimeDecl{},
		structs: map[string]bool{},
	}
	return e.run()
}

type runtimeDecl struct {
	name   string
	params []string
	ret    string
}

type emitter struct {
	mod  *ir.Module
	prog *semantic.Program

	body     strings.Builder
	strs     map[string]string
	strOrder []string
	decls    map[string]*runtimeDecl
	structs  map[string]bool
	curFn    *ir.Function
	// retSeq numbers the aggregate values built by multi-value returns so
	// that each return site gets unique SSA names.
	retSeq int
}

func (e *emitter) run() (string, error) {
	var head strings.Builder
	head.WriteString("; WayLang generated LLVM IR\n")
	head.WriteString("source_filename = \"waylang\"\n\n")

	// Named struct types must be declared before use.
	head.WriteString("%way.slice = type { ptr, i64, i64 }\n")
	head.WriteString("%way.box = type { ptr, ptr }\n")
	head.WriteString("%way.ti = type { ptr, i64, i64, ptr }\n")
	for _, st := range e.prog.StructOrder {
		fields := make([]string, len(st.Fields))
		for i, f := range st.Fields {
			lt, err := e.typ(f.Type)
			if err != nil {
				return "", err
			}
			fields[i] = lt
		}
		fmt.Fprintf(&head, "%%struct.%s = type { %s }\n", st.Name, strings.Join(fields, ", "))
	}
	head.WriteString("\n")

	// Globals.
	for _, g := range e.mod.Globals {
		lt, err := e.typ(g.Ty)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&head, "@%s = global %s zeroinitializer\n", g.Symbol, lt)
	}

	for _, fn := range e.mod.Funcs {
		if err := e.emitFunc(fn); err != nil {
			return "", err
		}
	}

	var out strings.Builder
	out.WriteString(head.String())
	out.WriteString(e.body.String())

	// Runtime declarations.
	names := make([]string, 0, len(e.decls))
	for n := range e.decls {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := e.decls[n]
		fmt.Fprintf(&out, "declare %s @%s(%s)\n", d.ret, d.name, strings.Join(d.params, ", "))
	}
	out.WriteString("\n")

	// Type information and vtables. Their names are string literals, so this
	// must come before the literals themselves are rendered.
	nslots := e.mod.MethodSlots
	if nslots == 0 {
		nslots = 1
	}
	for _, ti := range e.mod.TypeInfos {
		nameSym := e.stringSymbol(ti.Name)
		if len(ti.Methods) == 0 {
			fmt.Fprintf(&out, "@%s = constant %%way.ti { ptr @%s, i64 %d, i64 %d, ptr null }\n",
				ti.Symbol, nameSym, ti.Kind, nslots)
			continue
		}
		entries := make([]string, nslots)
		for i := range entries {
			entries[i] = "ptr null"
		}
		for slot, fn := range ti.Methods {
			if slot < nslots {
				entries[slot] = "ptr @" + fn
			}
		}
		fmt.Fprintf(&out, "@%s.vt = constant [%d x ptr] [%s]\n", ti.Symbol, nslots, strings.Join(entries, ", "))
		fmt.Fprintf(&out, "@%s = constant %%way.ti { ptr @%s, i64 %d, i64 %d, ptr @%s.vt }\n",
			ti.Symbol, nameSym, ti.Kind, nslots, ti.Symbol)
	}
	out.WriteString("\n")

	// String literals.
	for _, lit := range e.strOrder {
		sym := e.strs[lit]
		var b strings.Builder
		for i := 0; i < len(lit); i++ {
			c := lit[i]
			if c >= 32 && c < 127 && c != '"' && c != '\\' {
				b.WriteByte(c)
			} else {
				fmt.Fprintf(&b, "\\%02X", c)
			}
		}
		b.WriteString("\\00")
		fmt.Fprintf(&out, "@%s = private unnamed_addr constant { i64, [%d x i8] } { i64 %d, [%d x i8] c\"%s\" }\n",
			sym, len(lit)+1, len(lit), len(lit)+1, b.String())
	}
	return out.String(), nil
}

// --- types ---------------------------------------------------------------

func (e *emitter) typ(t types.Type) (string, error) {
	if t == nil {
		return "ptr", nil
	}
	switch t.Kind() {
	case types.Void:
		return "void", nil
	case types.Bool:
		return "i1", nil
	case types.Int:
		return "i64", nil
	case types.Float:
		return "double", nil
	case types.String, types.Error, types.MapKind, types.FuncKind:
		return "ptr", nil
	case types.Byte:
		return "i8", nil
	case types.Rune:
		return "i32", nil
	case types.Any, types.InterfaceKind:
		return "%way.box", nil
	case types.SliceKind:
		return "%way.slice", nil
	case types.ArrayKind:
		at := t.(*types.Array)
		elem, err := e.typ(at.Elem)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("[%d x %s]", at.Len, elem), nil
	case types.StructKind:
		st := t.(*types.Struct)
		if st.Name == "" {
			return "ptr", nil
		}
		return "%struct." + st.Name, nil
	case types.TupleKind:
		tt := t.(*types.Tuple)
		parts := make([]string, len(tt.Elems))
		for i, el := range tt.Elems {
			p, err := e.typ(el)
			if err != nil {
				return "", err
			}
			parts[i] = p
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	case types.Nil, types.Invalid, types.RangeKind:
		return "ptr", nil
	}
	return "", fmt.Errorf("llvm: unsupported type %s", t)
}

func (e *emitter) zero(t types.Type) string {
	switch t.Kind() {
	case types.Bool:
		return "false"
	case types.Int, types.Byte, types.Rune:
		return "0"
	case types.Float:
		return "0.0"
	case types.String, types.Error, types.MapKind, types.FuncKind, types.Nil:
		return "null"
	}
	return "zeroinitializer"
}

// --- functions -----------------------------------------------------------

func (e *emitter) emitFunc(fn *ir.Function) error {
	e.curFn = fn
	var retT string
	if len(fn.Results) == 0 {
		retT = "void"
	} else if len(fn.Results) == 1 {
		t, err := e.typ(fn.Results[0])
		if err != nil {
			return err
		}
		retT = t
	} else {
		parts := make([]string, len(fn.Results))
		for i, r := range fn.Results {
			t, err := e.typ(r)
			if err != nil {
				return err
			}
			parts[i] = t
		}
		retT = "{ " + strings.Join(parts, ", ") + " }"
	}

	params := make([]string, len(fn.Params))
	for i, p := range fn.Params {
		t, err := e.typ(p.Ty)
		if err != nil {
			return err
		}
		if p.Addr {
			t = "ptr"
		}
		params[i] = fmt.Sprintf("%s %%%s", t, p.Name)
	}

	fmt.Fprintf(&e.body, "\ndefine %s @%s(%s) {\n", retT, fn.Symbol, strings.Join(params, ", "))
	for _, blk := range fn.Blocks {
		fmt.Fprintf(&e.body, "%s:\n", blk.Name)
		for _, inst := range blk.Insts {
			if err := e.emitInst(inst); err != nil {
				return err
			}
		}
		if blk.Term != nil {
			if err := e.emitInst(blk.Term); err != nil {
				return err
			}
		}
	}
	e.body.WriteString("}\n")
	return nil
}

func (e *emitter) emitInst(i ir.Inst) error {
	switch i := i.(type) {
	case *ir.Alloca:
		t, err := e.typ(i.Result.Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  %%%s = alloca %s\n", i.Result.Name, t)
	case *ir.Load:
		t, err := e.typ(i.Result.Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  %%%s = load %s, ptr %s\n", i.Result.Name, t, e.val(i.Ptr))
	case *ir.Store:
		t, err := e.typ(i.Val.Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  store %s %s, ptr %s\n", t, e.val(i.Val), e.val(i.Ptr))
	case *ir.FieldAddr:
		st, ok := i.Base.Ty.(*types.Struct)
		if !ok {
			return fmt.Errorf("llvm: field access on non-struct %s", i.Base.Ty)
		}
		fmt.Fprintf(&e.body, "  %%%s = getelementptr inbounds %%struct.%s, ptr %s, i32 0, i32 %d\n",
			i.Result.Name, st.Name, e.val(i.Base), i.Index)
	case *ir.ElemAddr:
		t, err := e.typ(i.Elem)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  %%%s = getelementptr inbounds %s, ptr %s, i64 %s\n",
			i.Result.Name, t, e.val(i.Base), e.val(i.Index))
	case *ir.GlobalAddr:
		t, err := e.typ(i.Result.Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  %%%s = getelementptr %s, ptr @%s, i32 0\n", i.Result.Name, t, i.Result.Name)
	case *ir.FuncAddr:
		fmt.Fprintf(&e.body, "  %%%s = bitcast ptr @%s to ptr\n", i.Result.Name, i.Result.Name)
	case *ir.Arith:
		return e.emitArith(i)
	case *ir.Compare:
		return e.emitCompare(i)
	case *ir.Convert:
		return e.emitConvert(i)
	case *ir.Extract:
		aggT, err := e.typ(i.Agg.Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  %%%s = extractvalue %s %s, %d\n", i.Result.Name, aggT, e.val(i.Agg), i.Index)
	case *ir.MakeSlice:
		fmt.Fprintf(&e.body, "  %%%s.0 = insertvalue %%way.slice undef, ptr %s, 0\n", i.Result.Name, e.val(i.Data))
		fmt.Fprintf(&e.body, "  %%%s.1 = insertvalue %%way.slice %%%s.0, i64 %s, 1\n", i.Result.Name, i.Result.Name, e.val(i.Len))
		fmt.Fprintf(&e.body, "  %%%s = insertvalue %%way.slice %%%s.1, i64 %s, 2\n", i.Result.Name, i.Result.Name, e.val(i.Capacity))
	case *ir.MakeStruct:
		return e.emitMakeStruct(i)
	case *ir.Box:
		t, err := e.typ(i.Val.Ty)
		if err != nil {
			return err
		}
		// Aggregates live in memory, so load them before copying into the box.
		val := e.val(i.Val)
		if i.Val.Addr {
			fmt.Fprintf(&e.body, "  %%%s.val = load %s, ptr %s\n", i.Result.Name, t, val)
			val = "%" + i.Result.Name + ".val"
		}
		fmt.Fprintf(&e.body, "  %%%s = call ptr @way_box_new(i64 %s, ptr @%s)\n", i.Result.Name, e.sizeOf(i.Val.Ty), i.TI)
		fmt.Fprintf(&e.body, "  store %s %s, ptr %%%s\n", t, val, i.Result.Name)
		e.need("way_box_new", []string{"i64", "ptr"}, "ptr")
	case *ir.Unbox:
		t, err := e.typ(i.To)
		if err != nil {
			return err
		}
		ti := e.progTypeInfo(i.To)
		fmt.Fprintf(&e.body, "  %%%s.data = extractvalue %%way.box %s, 0\n", i.Result.Name, e.val(i.Box))
		fmt.Fprintf(&e.body, "  %%%s.ti = extractvalue %%way.box %s, 1\n", i.Result.Name, e.val(i.Box))
		fmt.Fprintf(&e.body, "  %%%s.ptr = call ptr @way_box_assert(ptr %%%s.data, ptr %%%s.ti, ptr @%s)\n",
			i.Result.Name, i.Result.Name, i.Result.Name, ti)
		fmt.Fprintf(&e.body, "  %%%s = load %s, ptr %%%s.ptr\n", i.Result.Name, t, i.Result.Name)
		e.need("way_box_assert", []string{"ptr", "ptr", "ptr"}, "ptr")
	case *ir.SeqAlloc:
		fmt.Fprintf(&e.body, "  %%%s = call ptr @way_seq_alloc(i64 %s, i64 %s)\n", i.Result.Name, e.val(i.Count), e.sizeOf(i.Elem))
		e.need("way_seq_alloc", []string{"i64", "i64"}, "ptr")
	case *ir.MapNew:
		if err := e.emitMapNew(i); err != nil {
			return err
		}
	case *ir.Call:
		return e.emitCall(i)
	case *ir.IfaceCall:
		return e.emitIfaceCall(i)
	case *ir.Ret:
		return e.emitRet(i)
	case *ir.Jump:
		fmt.Fprintf(&e.body, "  br label %%%s\n", i.Target.Name)
	case *ir.Branch:
		fmt.Fprintf(&e.body, "  br i1 %s, label %%%s, label %%%s\n", e.val(i.Cond), i.Then.Name, i.Else.Name)
	default:
		return fmt.Errorf("llvm: unsupported instruction %T", i)
	}
	return nil
}

func (e *emitter) emitArith(i *ir.Arith) error {
	t, err := e.typ(i.Result.Ty)
	if err != nil {
		return err
	}
	var op string
	isFloat := i.Result.Ty.Kind() == types.Float
	switch i.Op {
	case token.Plus:
		op = map[bool]string{true: "fadd", false: "add"}[isFloat]
	case token.Minus:
		op = map[bool]string{true: "fsub", false: "sub"}[isFloat]
	case token.Star:
		op = map[bool]string{true: "fmul", false: "mul"}[isFloat]
	case token.Slash:
		op = map[bool]string{true: "fdiv", false: "sdiv"}[isFloat]
	case token.Percent:
		op = map[bool]string{true: "frem", false: "srem"}[isFloat]
	case token.Amp:
		op = "and"
	case token.Pipe:
		op = "or"
	case token.Caret:
		op = "xor"
	case token.Shl:
		op = "shl"
	case token.Shr:
		op = "ashr"
	case token.And:
		op = "and"
	case token.Or:
		op = "or"
	default:
		return fmt.Errorf("llvm: unsupported arithmetic operator %s", i.Op)
	}
	fmt.Fprintf(&e.body, "  %%%s = %s %s %s, %s\n", i.Result.Name, op, t, e.val(i.X), e.val(i.Y))
	return nil
}

func (e *emitter) emitCompare(i *ir.Compare) error {
	t, err := e.typ(i.X.Ty)
	if err != nil {
		return err
	}
	isFloat := i.X.Ty != nil && i.X.Ty.Kind() == types.Float
	var op string
	if isFloat {
		switch i.Op {
		case token.Eq:
			op = "oeq"
		case token.Ne:
			op = "one"
		case token.Lt:
			op = "olt"
		case token.Le:
			op = "ole"
		case token.Gt:
			op = "ogt"
		case token.Ge:
			op = "oge"
		}
		fmt.Fprintf(&e.body, "  %%%s = fcmp %s %s %s, %s\n", i.Result.Name, op, t, e.val(i.X), e.val(i.Y))
		return nil
	}
	switch i.Op {
	case token.Eq:
		op = "eq"
	case token.Ne:
		op = "ne"
	case token.Lt:
		op = "slt"
	case token.Le:
		op = "sle"
	case token.Gt:
		op = "sgt"
	case token.Ge:
		op = "sge"
	default:
		return fmt.Errorf("llvm: unsupported comparison %s", i.Op)
	}
	fmt.Fprintf(&e.body, "  %%%s = icmp %s %s %s, %s\n", i.Result.Name, op, t, e.val(i.X), e.val(i.Y))
	return nil
}

func (e *emitter) emitConvert(i *ir.Convert) error {
	from := i.X.Ty
	to := i.To
	fromT, err := e.typ(from)
	if err != nil {
		return err
	}
	toT, err := e.typ(to)
	if err != nil {
		return err
	}
	call := func(name string, params []string, ret string, args string) {
		fmt.Fprintf(&e.body, "  %%%s = call %s @%s(%s)\n", i.Result.Name, ret, name, args)
		e.need(name, params, ret)
	}
	switch {
	case from.Kind() == types.Int && to.Kind() == types.Float:
		fmt.Fprintf(&e.body, "  %%%s = sitofp i64 %s to double\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Float && to.Kind() == types.Int:
		fmt.Fprintf(&e.body, "  %%%s = fptosi double %s to i64\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Int && to.Kind() == types.Bool:
		fmt.Fprintf(&e.body, "  %%%s = icmp ne i64 %s, 0\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Bool && to.Kind() == types.Int:
		fmt.Fprintf(&e.body, "  %%%s = zext i1 %s to i64\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Float && to.Kind() == types.Bool:
		fmt.Fprintf(&e.body, "  %%%s = fcmp one double %s, 0.0\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Bool && to.Kind() == types.Float:
		fmt.Fprintf(&e.body, "  %%%s = uitofp i1 %s to double\n", i.Result.Name, e.val(i.X))
	case types.IsInteger(from) && types.IsInteger(to):
		fmt.Fprintf(&e.body, "  %%%s = %s %s %s to %s\n", i.Result.Name,
			intCastOp(from, to), fromT, e.val(i.X), toT)
	case types.IsInteger(from) && to.Kind() == types.Float:
		ext := e.val(i.X)
		if fromT != "i64" {
			fmt.Fprintf(&e.body, "  %%%s.ext = sext %s %s to i64\n", i.Result.Name, fromT, ext)
			ext = "%" + i.Result.Name + ".ext"
		}
		fmt.Fprintf(&e.body, "  %%%s = sitofp i64 %s to double\n", i.Result.Name, ext)
	case types.IsInteger(from) && to.Kind() == types.String:
		if from.Kind() == types.Rune {
			call("way_str_from_rune", []string{"i32"}, "ptr", "i32 "+e.val(i.X))
			return nil
		}
		ext := e.val(i.X)
		if fromT != "i64" {
			fmt.Fprintf(&e.body, "  %%%s.ext = sext %s %s to i64\n", i.Result.Name, fromT, ext)
			ext = "%" + i.Result.Name + ".ext"
		}
		call("way_str_from_int", []string{"i64"}, "ptr", "i64 "+ext)
		return nil
	case from.Kind() == types.Float && to.Kind() == types.String:
		call("way_str_from_float", []string{"double"}, "ptr", "double "+e.val(i.X))
		return nil
	case from.Kind() == types.Bool && to.Kind() == types.String:
		call("way_str_from_bool", []string{"i1"}, "ptr", "i1 "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.Int:
		call("way_str_to_int", []string{"ptr"}, "i64", "ptr "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.Float:
		call("way_str_to_float", []string{"ptr"}, "double", "ptr "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.Bool:
		call("way_str_to_bool", []string{"ptr"}, "i1", "ptr "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.Byte:
		call("way_str_to_byte", []string{"ptr"}, "i8", "ptr "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.Rune:
		call("way_str_to_rune", []string{"ptr"}, "i32", "ptr "+e.val(i.X))
		return nil
	case from.Kind() == types.String && to.Kind() == types.String:
		fmt.Fprintf(&e.body, "  %%%s = bitcast ptr %s to ptr\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Error && to.Kind() == types.String:
		fmt.Fprintf(&e.body, "  %%%s = bitcast ptr %s to ptr\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.String && to.Kind() == types.Error:
		fmt.Fprintf(&e.body, "  %%%s = bitcast ptr %s to ptr\n", i.Result.Name, e.val(i.X))
	case from.Kind() == types.Any || from.Kind() == types.InterfaceKind:
		return e.emitUnboxConvert(i)
	default:
		return fmt.Errorf("llvm: unsupported conversion from %s to %s", from, to)
	}
	return nil
}

func intCastOp(from, to types.Type) string {
	fs := intBits(from)
	ts := intBits(to)
	switch {
	case fs == ts:
		return "bitcast"
	case ts > fs:
		if from.Kind() == types.Bool {
			return "zext"
		}
		return "sext"
	default:
		return "trunc"
	}
}

func intBits(t types.Type) int {
	switch t.Kind() {
	case types.Byte:
		return 8
	case types.Rune:
		return 32
	}
	return 64
}

// emitUnboxConvert converts from any/interface to a concrete scalar.
func (e *emitter) emitUnboxConvert(i *ir.Convert) error {
	to := i.To
	switch to.Kind() {
	case types.String:
		fmt.Fprintf(&e.body, "  %%%s = call ptr @way_any_to_string(%%way.box %s)\n", i.Result.Name, e.val(i.X))
		e.need("way_any_to_string", []string{"%way.box"}, "ptr")
	case types.Int:
		fmt.Fprintf(&e.body, "  %%%s = call i64 @way_any_as_int(%%way.box %s)\n", i.Result.Name, e.val(i.X))
		e.need("way_any_as_int", []string{"%way.box"}, "i64")
	case types.Float:
		fmt.Fprintf(&e.body, "  %%%s = call double @way_any_as_float(%%way.box %s)\n", i.Result.Name, e.val(i.X))
		e.need("way_any_as_float", []string{"%way.box"}, "double")
	case types.Bool:
		fmt.Fprintf(&e.body, "  %%%s = call i1 @way_any_as_bool(%%way.box %s)\n", i.Result.Name, e.val(i.X))
		e.need("way_any_as_bool", []string{"%way.box"}, "i1")
	default:
		return fmt.Errorf("llvm: cannot convert any to %s; use a type assertion", to)
	}
	return nil
}

func (e *emitter) emitMakeStruct(i *ir.MakeStruct) error {
	t, err := e.typ(i.Result.Ty)
	if err != nil {
		return err
	}
	// A box ({data, typeinfo}) stores pointers, while a declared struct holds
	// values; the IR flags aggregates with Addr, so they are loaded first.
	box := i.Result.Ty != nil &&
		(i.Result.Ty.Kind() == types.Any || i.Result.Ty.Kind() == types.InterfaceKind)
	cur := "undef"
	for idx, f := range i.Fields {
		name := i.Result.Name
		if idx < len(i.Fields)-1 {
			name = fmt.Sprintf("%s.%d", i.Result.Name, idx)
		}
		ft := mustType(e, f.Ty)
		val := e.val(f)
		switch {
		case box:
			ft = "ptr"
		case f.Addr:
			loaded := fmt.Sprintf("%s.v%d", name, idx)
			fmt.Fprintf(&e.body, "  %%%s = load %s, ptr %s\n", loaded, ft, val)
			val = "%" + loaded
		}
		fmt.Fprintf(&e.body, "  %%%s = insertvalue %s %s, %s %s, %d\n", name, t, cur, ft, val, idx)
		cur = "%" + name
	}
	return nil
}

func mustType(e *emitter, t types.Type) string {
	s, err := e.typ(t)
	if err != nil {
		panic(err)
	}
	return s
}

// boxRuntimeFuncs lists runtime functions that take dynamic values by
// reference and, for the arithmetic helpers, return one through a hidden
// result pointer. Win64 passes aggregates larger than 8 bytes indirectly, so
// these must not be declared with by-value %way.box parameters: `byval` and
// `sret` make LLVM emit the pointer-based C ABI.
var boxRuntimeFuncs = map[string]int{
	"way_print_any":     1,
	"way_any_to_string": 1,
	"way_any_type_name": 1,
	"way_any_as_int":    1,
	"way_any_as_float":  1,
	"way_any_as_bool":   1,
	"way_type_id":       1,
	"way_any_neg":       1,
	"way_any_add":       2,
	"way_any_sub":       2,
	"way_any_mul":       2,
	"way_any_div":       2,
	"way_any_mod":       2,
	"way_any_eq":        2,
	"way_any_ne":        2,
	"way_any_lt":        2,
	"way_any_le":        2,
	"way_any_gt":        2,
	"way_any_ge":        2,
}

func (e *emitter) emitCall(i *ir.Call) error {
	ret, err := e.typ(i.Ret)
	if err != nil {
		return err
	}
	if _, ok := boxRuntimeFuncs[i.Callee]; ok && i.Indirect.IsZero() {
		return e.emitBoxCall(i, ret)
	}
	args := make([]string, len(i.Args))
	argTypes := make([]string, len(i.Args))
	for k, a := range i.Args {
		t, err := e.typ(a.Ty)
		if err != nil {
			return err
		}
		if a.Addr {
			t = "ptr"
		}
		argTypes[k] = t
		args[k] = fmt.Sprintf("%s %s", t, e.val(a))
	}
	callee := "@" + i.Callee
	if !i.Indirect.IsZero() {
		callee = e.val(i.Indirect)
	} else if !e.defined(i.Callee) {
		e.need(i.Callee, argTypes, ret)
	}
	if ret == "void" {
		fmt.Fprintf(&e.body, "  call void %s(%s)\n", callee, strings.Join(args, ", "))
		return nil
	}
	fmt.Fprintf(&e.body, "  %%%s = call %s %s(%s)\n", i.Result.Name, ret, callee, strings.Join(args, ", "))
	return nil
}

// emitBoxCall calls a dynamic-value runtime function using the pointer ABI the
// C runtime is compiled with.
func (e *emitter) emitBoxCall(i *ir.Call, ret string) error {
	var args, argTypes []string
	for k, a := range i.Args {
		slot := fmt.Sprintf("%s.a%d", i.Result.Name, k)
		if a.Addr {
			fmt.Fprintf(&e.body, "  %%%s = load %%way.box, ptr %s\n", slot, e.val(a))
		} else {
			fmt.Fprintf(&e.body, "  %%%s.tmp = alloca %%way.box\n", slot)
			fmt.Fprintf(&e.body, "  store %%way.box %s, ptr %%%s.tmp\n", e.val(a), slot)
			fmt.Fprintf(&e.body, "  %%%s = load %%way.box, ptr %%%s.tmp\n", slot, slot)
		}
		name := slot + ".slot"
		fmt.Fprintf(&e.body, "  %%%s = alloca %%way.box\n", name)
		fmt.Fprintf(&e.body, "  store %%way.box %%%s, ptr %%%s\n", slot, name)
		argTypes = append(argTypes, "ptr byval(%way.box)")
		args = append(args, fmt.Sprintf("ptr byval(%%way.box) %%%s", name))
	}
	if ret == "%way.box" {
		out := i.Result.Name + ".out"
		fmt.Fprintf(&e.body, "  %%%s = alloca %%way.box\n", out)
		argTypes = append([]string{"ptr sret(%way.box)"}, argTypes...)
		args = append([]string{fmt.Sprintf("ptr sret(%%way.box) %%%s", out)}, args...)
		e.need(i.Callee, argTypes, "void")
		fmt.Fprintf(&e.body, "  call void @%s(%s)\n", i.Callee, strings.Join(args, ", "))
		fmt.Fprintf(&e.body, "  %%%s = load %%way.box, ptr %%%s\n", i.Result.Name, out)
		return nil
	}
	e.need(i.Callee, argTypes, ret)
	if ret == "void" {
		fmt.Fprintf(&e.body, "  call void @%s(%s)\n", i.Callee, strings.Join(args, ", "))
		return nil
	}
	fmt.Fprintf(&e.body, "  %%%s = call %s @%s(%s)\n", i.Result.Name, ret, i.Callee, strings.Join(args, ", "))
	return nil
}

func (e *emitter) emitIfaceCall(i *ir.IfaceCall) error {
	ret, err := e.typ(i.Ret)
	if err != nil {
		return err
	}
	args := make([]string, len(i.Args))
	argTypes := make([]string, len(i.Args))
	for k, a := range i.Args {
		t, err := e.typ(a.Ty)
		if err != nil {
			return err
		}
		if a.Addr {
			t = "ptr"
		}
		argTypes[k] = t
		args[k] = fmt.Sprintf("%s %s", t, e.val(a))
	}
	n := i.Result.Name
	fmt.Fprintf(&e.body, "  %%%s.data = extractvalue %%way.box %s, 0\n", n, e.val(i.Recv))
	fmt.Fprintf(&e.body, "  %%%s.ti = extractvalue %%way.box %s, 1\n", n, e.val(i.Recv))
	fmt.Fprintf(&e.body, "  %%%s.m = getelementptr inbounds %%way.ti, ptr %%%s.ti, i32 0, i32 3\n", n, n)
	fmt.Fprintf(&e.body, "  %%%s.vt = load ptr, ptr %%%s.m\n", n, n)
	fmt.Fprintf(&e.body, "  %%%s.slot = getelementptr ptr, ptr %%%s.vt, i64 %d\n", n, n, i.Slot)
	fmt.Fprintf(&e.body, "  %%%s.fn = load ptr, ptr %%%s.slot\n", n, n)
	all := append([]string{"ptr %" + n + ".data"}, args...)
	if ret == "void" {
		fmt.Fprintf(&e.body, "  call void %%%s.fn(%s)\n", n, strings.Join(all, ", "))
		return nil
	}
	fmt.Fprintf(&e.body, "  %%%s = call %s %%%s.fn(%s)\n", n, ret, n, strings.Join(all, ", "))
	return nil
}

func (e *emitter) emitRet(i *ir.Ret) error {
	if len(i.Vals) == 0 {
		e.body.WriteString("  ret void\n")
		return nil
	}
	if len(i.Vals) == 1 {
		t, err := e.typ(i.Vals[0].Ty)
		if err != nil {
			return err
		}
		fmt.Fprintf(&e.body, "  ret %s %s\n", t, e.val(i.Vals[0]))
		return nil
	}
	parts := make([]string, len(i.Vals))
	for k, v := range i.Vals {
		t, err := e.typ(v.Ty)
		if err != nil {
			return err
		}
		parts[k] = t
	}
	structT := "{ " + strings.Join(parts, ", ") + " }"
	cur := "undef"
	seq := e.retSeq
	e.retSeq++
	for k, v := range i.Vals {
		name := fmt.Sprintf("ret.%d.%d", seq, k)
		t := parts[k]
		fmt.Fprintf(&e.body, "  %%%s = insertvalue %s %s, %s %s, %d\n", name, structT, cur, t, e.val(v), k)
		cur = "%" + name
	}
	fmt.Fprintf(&e.body, "  ret %s %s\n", structT, cur)
	return nil
}

func (e *emitter) emitMapNew(i *ir.MapNew) error {
	var hashFn, eqFn string
	switch i.Key.Kind() {
	case types.String:
		hashFn, eqFn = "way_hash_str", "way_eq_str"
	case types.Int:
		hashFn, eqFn = "way_hash_int", "way_eq_int"
	case types.Bool:
		hashFn, eqFn = "way_hash_bool", "way_eq_bool"
	default:
		return fmt.Errorf("llvm: map keys of type %s are not supported yet; use string, int or bool", i.Key)
	}
	n := i.Result.Name
	fmt.Fprintf(&e.body, "  %%%s = call ptr @way_map_new(i64 %s, i64 %s, ptr @%s, ptr @%s)\n", n, e.sizeOf(i.Key), e.sizeOf(i.Elem), hashFn, eqFn)
	e.need("way_map_new", []string{"i64", "i64", "ptr", "ptr"}, "ptr")
	e.need(hashFn, []string{"ptr"}, "i64")
	e.need(eqFn, []string{"ptr", "ptr"}, "i1")
	return nil
}

// --- values --------------------------------------------------------------

func (e *emitter) val(v ir.Value) string {
	switch v.Kind {
	case ir.ValueTemp:
		return "%" + v.Name
	case ir.ValueFunc, ir.ValueGlobal:
		return "@" + v.Name
	case ir.ValueInt:
		return strconv.FormatInt(v.I, 10)
	case ir.ValueFloat:
		s := strconv.FormatFloat(v.F, 'g', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case ir.ValueBool:
		if v.B {
			return "true"
		}
		return "false"
	case ir.ValueString:
		return "@" + e.stringSymbol(v.Str)
	case ir.ValueNil:
		return e.zero(v.Ty)
	case ir.ValueZero:
		return e.zero(v.Ty)
	}
	return "null"
}

// sizeOf renders the size in bytes of a value as an i64 operand. Scalar types
// have a fixed size; aggregates use a constant getelementptr expression, since
// getelementptr itself only accepts pointer element types.
func (e *emitter) sizeOf(t types.Type) string {
	if t == nil {
		return "8"
	}
	switch t.Kind() {
	case types.Bool, types.Byte:
		return "1"
	case types.Rune:
		return "4"
	case types.Int, types.Float, types.String, types.Error, types.MapKind, types.FuncKind:
		return "8"
	}
	lt, err := e.typ(t)
	if err != nil {
		return "8"
	}
	return "ptrtoint (ptr getelementptr (" + lt + ", ptr null, i32 1) to i64)"
}

func (e *emitter) stringSymbol(s string) string {
	if sym, ok := e.strs[s]; ok {
		return sym
	}
	sym := fmt.Sprintf("way.str.%d", len(e.strOrder))
	e.strs[s] = sym
	e.strOrder = append(e.strOrder, s)
	return sym
}

func (e *emitter) progTypeInfo(t types.Type) string {
	for _, ti := range e.mod.TypeInfos {
		if ti.Name == t.String() {
			return ti.Symbol
		}
	}
	if len(e.mod.TypeInfos) > 0 {
		return e.mod.TypeInfos[0].Symbol
	}
	return "way.ti.0"
}

// defined reports whether the module defines this symbol, in which case it
// must not also be declared.
func (e *emitter) defined(name string) bool {
	for _, fn := range e.mod.Funcs {
		if fn.Symbol == name {
			return true
		}
	}
	return false
}

func (e *emitter) need(name string, params []string, ret string) {
	if _, ok := e.decls[name]; ok {
		return
	}
	e.decls[name] = &runtimeDecl{name: name, params: params, ret: ret}
}
