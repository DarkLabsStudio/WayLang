// Package ir defines WayLang's mid-level intermediate representation.
//
// The IR is typed, operates on whole semantic values (never raw bytes), and is
// deliberately simple: functions are lists of basic blocks, and blocks contain
// allocas, loads, stores, arithmetic, conversions, calls and terminators.
// Aggregate locals live in stack slots; LLVM's mem2reg pass turns those slots
// into registers, so this representation optimises well without needing SSA
// construction here.
package ir

import (
	"fmt"
	"strings"

	"waylang/compiler/token"
	"waylang/compiler/types"
)

// ValueKind classifies a value.
type ValueKind uint8

const (
	ValueInvalid ValueKind = iota
	ValueTemp
	ValueFunc
	ValueGlobal
	ValueInt
	ValueFloat
	ValueBool
	ValueString
	ValueNil
	// ValueZero is the zero value of Value.Ty (0, null, false, or
	// zeroinitializer for aggregates).
	ValueZero
)

// Value is an IR operand.
type Value struct {
	Kind ValueKind
	Name string
	Ty   types.Type
	// Addr is true when the value is the address of a Ty, not a Ty itself.
	Addr bool
	I    int64
	F    float64
	B    bool
	Str  string
}

// IsZero reports whether the value is unset.
func (v Value) IsZero() bool { return v.Kind == ValueInvalid }

func (v Value) String() string {
	switch v.Kind {
	case ValueTemp:
		if v.Addr {
			return "%" + v.Name
		}
		return "%" + v.Name
	case ValueFunc, ValueGlobal:
		return "@" + v.Name
	case ValueInt:
		return fmt.Sprintf("%d", v.I)
	case ValueFloat:
		return fmt.Sprintf("%g", v.F)
	case ValueBool:
		if v.B {
			return "true"
		}
		return "false"
	case ValueString:
		return fmt.Sprintf("%q", v.Str)
	case ValueNil:
		return "nil"
	}
	return "<invalid>"
}

// Temp creates a temporary value.
func Temp(name string, t types.Type) Value {
	return Value{Kind: ValueTemp, Name: name, Ty: t}
}

// IntConst creates an integer constant.
func IntConst(v int64) Value { return Value{Kind: ValueInt, I: v, Ty: types.IntT} }

// FloatConst creates a float constant.
func FloatConst(v float64) Value { return Value{Kind: ValueFloat, F: v, Ty: types.FloatT} }

// BoolConst creates a boolean constant.
func BoolConst(v bool) Value { return Value{Kind: ValueBool, B: v, Ty: types.BoolT} }

// StringConst creates a string constant.
func StringConst(s string) Value { return Value{Kind: ValueString, Str: s, Ty: types.StringT} }

// NilConst creates a typed nil constant.
func NilConst(t types.Type) Value { return Value{Kind: ValueNil, Ty: t} }

// Inst is a non-terminator instruction.
type Inst interface {
	isInst()
}

// Alloca reserves stack storage for a value of Result.Ty.
type Alloca struct{ Result Value }

// Load loads Result.Ty from Ptr.
type Load struct {
	Result Value
	Ptr    Value
}

// Store stores Val into Ptr.
type Store struct {
	Ptr Value
	Val Value
}

// FieldAddr computes the address of a struct field.
type FieldAddr struct {
	Result  Value
	Base    Value
	Index   int
	FieldTy types.Type
}

// ElemAddr computes the address of an array/slice element or buffer slot.
type ElemAddr struct {
	Result Value
	Base   Value
	Index  Value
	Elem   types.Type
}

// GlobalAddr takes the address of a global variable.
type GlobalAddr struct{ Result Value }

// FuncAddr takes the address of a function.
type FuncAddr struct{ Result Value }

// Arith is a binary arithmetic or bitwise operation.
type Arith struct {
	Result Value
	Op     token.Kind
	X, Y   Value
}

// Compare is a comparison producing a bool.
type Compare struct {
	Result Value
	Op     token.Kind
	X, Y   Value
}

// Convert is an explicit value conversion.
type Convert struct {
	Result Value
	X      Value
	To     types.Type
}

// Call calls a function. Indirect holds a function pointer when non-zero.
type Call struct {
	Result   Value
	Callee   string
	Args     []Value
	Indirect Value
	Ret      types.Type
}

// Extract reads a field of an aggregate value (a slice field or a tuple
// element).
type Extract struct {
	Result Value
	Agg    Value
	Index  int
}

// MakeSlice builds a slice value from a data pointer, length and capacity.
type MakeSlice struct {
	Result   Value
	Data     Value
	Len      Value
	Capacity Value
}

// MakeStruct builds a struct/box value from field values.
type MakeStruct struct {
	Result Value
	Fields []Value
}

// Box allocates a heap box with the given type info and stores Val into it.
// Result is the address of the boxed value.
type Box struct {
	Result Value
	Val    Value
	TI     string
}

// Unbox extracts a concrete value of type To from a box value.
type Unbox struct {
	Result Value
	Box    Value
	To     types.Type
}

// SeqAlloc allocates a zeroed, garbage-collected array of Count elements.
type SeqAlloc struct {
	Result Value
	Count  Value
	Elem   types.Type
}

// MapNew allocates a map with runtime hashing for its key type.
type MapNew struct {
	Result Value
	Key    types.Type
	Elem   types.Type
}

// IfaceCall dispatches an interface method call through the receiver's vtable.
type IfaceCall struct {
	Result Value
	Recv   Value
	Slot   int
	Args   []Value
	Ret    types.Type
}

// Ret is a terminator returning values.
type Ret struct{ Vals []Value }

// Jump is an unconditional terminator.
type Jump struct{ Target *Block }

// Branch is a conditional terminator.
type Branch struct {
	Cond Value
	Then *Block
	Else *Block
}

func (*Alloca) isInst()     {}
func (*Load) isInst()       {}
func (*Store) isInst()      {}
func (*FieldAddr) isInst()  {}
func (*ElemAddr) isInst()   {}
func (*GlobalAddr) isInst() {}
func (*FuncAddr) isInst()   {}
func (*Arith) isInst()      {}
func (*Compare) isInst()    {}
func (*Convert) isInst()    {}
func (*Call) isInst()       {}
func (*Extract) isInst()    {}
func (*MakeSlice) isInst()  {}
func (*MakeStruct) isInst() {}
func (*IfaceCall) isInst()  {}
func (*Box) isInst()        {}
func (*Unbox) isInst()      {}
func (*SeqAlloc) isInst()   {}
func (*MapNew) isInst()     {}

// Terminator instructions.
func (*Ret) isInst()    {}
func (*Jump) isInst()   {}
func (*Branch) isInst() {}

// Block is a basic block.
type Block struct {
	Name       string
	Insts      []Inst
	Terminated bool
	// Term is the last instruction; always Ret, Jump or Branch once sealed.
	Term Inst
}

// Function is an IR function.
type Function struct {
	Name    string
	Symbol  string
	Params  []Value
	Results []types.Type
	Blocks  []*Block
	Entry   *Block
	// IsMain marks the program entry point.
	IsMain bool
	// Exported marks functions that other translation units may call.
	Exported bool
}

// Global is a package-level variable.
type Global struct {
	Symbol string
	Ty     types.Type
}

// Module is a lowered program.
type Module struct {
	Name    string
	Funcs   []*Function
	Globals []*Global
	// TypeInfos holds runtime type metadata for boxing and interfaces.
	TypeInfos []*TypeInfo
	// MethodSlots is the number of interface dispatch slots.
	MethodSlots int
}

// Builder constructs IR for one function.
type Builder struct {
	Fn  *Function
	Cur *Block
	n   int
}

// NewBuilder starts building a function.
func NewBuilder(fn *Function) *Builder {
	b := &Builder{Fn: fn}
	if fn.Entry == nil {
		fn.Entry = fn.NewBlock("entry")
	}
	b.Cur = fn.Entry
	return b
}

// NewBlock creates and appends a block.
func (f *Function) NewBlock(name string) *Block {
	blk := &Block{Name: fmt.Sprintf("%s.%d", name, len(f.Blocks))}
	f.Blocks = append(f.Blocks, blk)
	return blk
}

// SetBlock switches the builder to blk.
func (b *Builder) SetBlock(blk *Block) {
	b.Cur = blk
	if blk != nil && len(blk.Insts) > 0 {
		if _, ok := blk.Insts[len(blk.Insts)-1].(*Ret); ok {
			blk.Terminated = true
		}
	}
}

// Temp creates a fresh temporary of type t.
func (b *Builder) Temp(t types.Type) Value {
	b.n++
	return Temp(fmt.Sprintf("t%d", b.n), t)
}

// AddrTemp creates a fresh address temporary pointing at t.
func (b *Builder) AddrTemp(t types.Type) Value {
	v := b.Temp(t)
	v.Addr = true
	return v
}

func (b *Builder) emit(i Inst) {
	if b.Cur == nil {
		panic("ir: no current block")
	}
	if b.Cur.Terminated {
		panic(fmt.Sprintf("ir: instruction after terminator in block %s", b.Cur.Name))
	}
	b.Cur.Insts = append(b.Cur.Insts, i)
}

// Terminate appends a terminator.
func (b *Builder) Terminate(i Inst) {
	if b.Cur == nil {
		panic("ir: no current block")
	}
	if b.Cur.Terminated {
		panic(fmt.Sprintf("ir: duplicate terminator in block %s", b.Cur.Name))
	}
	b.Cur.Term = i
	b.Cur.Terminated = true
}

// Alloca allocates stack storage.
func (b *Builder) Alloca(t types.Type) Value {
	res := b.AddrTemp(t)
	b.emit(&Alloca{Result: res})
	return res
}

// Load reads a value.
func (b *Builder) Load(ptr Value) Value {
	res := b.Temp(ptr.Ty)
	b.emit(&Load{Result: res, Ptr: ptr})
	return res
}

// Store writes a value.
func (b *Builder) Store(ptr, val Value) {
	b.emit(&Store{Ptr: ptr, Val: val})
}

// FieldPtr computes a field address.
func (b *Builder) FieldPtr(base Value, index int, fieldTy types.Type) Value {
	res := b.AddrTemp(fieldTy)
	b.emit(&FieldAddr{Result: res, Base: base, Index: index, FieldTy: fieldTy})
	return res
}

// ElemPtr computes an element address.
func (b *Builder) ElemPtr(base, index Value, elem types.Type) Value {
	res := b.AddrTemp(elem)
	b.emit(&ElemAddr{Result: res, Base: base, Index: index, Elem: elem})
	return res
}

// GlobalPtr takes a global's address.
func (b *Builder) GlobalPtr(symbol string, t types.Type) Value {
	res := b.AddrTemp(t)
	res.Name = symbol
	b.emit(&GlobalAddr{Result: res})
	return res
}

// FuncPtr takes a function's address.
func (b *Builder) FuncPtr(symbol string, sig *types.Func) Value {
	res := b.Temp(sig)
	res.Kind = ValueFunc
	res.Name = symbol
	b.emit(&FuncAddr{Result: res})
	return res
}

// Arith emits a binary operation.
func (b *Builder) Arith(op token.Kind, x, y Value, t types.Type) Value {
	res := b.Temp(t)
	b.emit(&Arith{Result: res, Op: op, X: x, Y: y})
	return res
}

// Cmp emits a comparison.
func (b *Builder) Cmp(op token.Kind, x, y Value) Value {
	res := b.Temp(types.BoolT)
	b.emit(&Compare{Result: res, Op: op, X: x, Y: y})
	return res
}

// Convert emits an explicit conversion.
func (b *Builder) Convert(x Value, to types.Type) Value {
	res := b.Temp(to)
	b.emit(&Convert{Result: res, X: x, To: to})
	return res
}

// Call emits a direct call.
func (b *Builder) Call(callee string, args []Value, ret types.Type) Value {
	res := b.Temp(ret)
	b.emit(&Call{Result: res, Callee: callee, Args: args, Ret: ret})
	return res
}

// CallIndirect emits an indirect call through a function pointer.
func (b *Builder) CallIndirect(fn Value, args []Value, ret types.Type) Value {
	res := b.Temp(ret)
	b.emit(&Call{Result: res, Args: args, Indirect: fn, Ret: ret})
	return res
}

// Extract reads an aggregate field.
func (b *Builder) Extract(agg Value, index int, t types.Type) Value {
	res := b.Temp(t)
	b.emit(&Extract{Result: res, Agg: agg, Index: index})
	return res
}

// ExtractPtr reads a pointer field of an aggregate (for example a slice's data
// pointer).
func (b *Builder) ExtractPtr(agg Value, index int, elem types.Type) Value {
	res := b.Temp(elem)
	res.Addr = true
	b.emit(&Extract{Result: res, Agg: agg, Index: index})
	return res
}

// MakeSlice builds a slice value.
func (b *Builder) MakeSlice(data, length, capacity Value, t types.Type) Value {
	res := b.Temp(t)
	b.emit(&MakeSlice{Result: res, Data: data, Len: length, Capacity: capacity})
	return res
}

// MakeStruct builds a struct value.
func (b *Builder) MakeStruct(t types.Type, fields ...Value) Value {
	res := b.Temp(t)
	b.emit(&MakeStruct{Result: res, Fields: fields})
	return res
}

// BoxValue heap-allocates a box holding v and returns its address.
func (b *Builder) BoxValue(v Value, ti string) Value {
	res := b.AddrTemp(v.Ty)
	b.emit(&Box{Result: res, Val: v, TI: ti})
	return res
}

// UnboxValue extracts a concrete value from a box.
func (b *Builder) UnboxValue(box Value, to types.Type) Value {
	res := b.Temp(to)
	b.emit(&Unbox{Result: res, Box: box, To: to})
	return res
}

// SeqAlloc allocates a GC array of count elements.
func (b *Builder) SeqAlloc(count Value, elem types.Type) Value {
	res := b.AddrTemp(elem)
	b.emit(&SeqAlloc{Result: res, Count: count, Elem: elem})
	return res
}

// NewMap allocates a map.
func (b *Builder) NewMap(key, elem types.Type) Value {
	res := b.Temp(&types.Map{Key: key, Elem: elem})
	b.emit(&MapNew{Result: res, Key: key, Elem: elem})
	return res
}

// IfaceCall calls an interface method.
func (b *Builder) IfaceCall(recv Value, slot int, args []Value, ret types.Type) Value {
	res := b.Temp(ret)
	b.emit(&IfaceCall{Result: res, Recv: recv, Slot: slot, Args: args, Ret: ret})
	return res
}

// Ret terminates the current block with a return.
func (b *Builder) Ret(vals []Value) { b.Terminate(&Ret{Vals: vals}) }

// Jump terminates the current block with an unconditional jump.
func (b *Builder) Jump(target *Block) { b.Terminate(&Jump{Target: target}) }

// Branch terminates the current block with a conditional branch.
func (b *Builder) Branch(cond Value, then, els *Block) {
	b.Terminate(&Branch{Cond: cond, Then: then, Else: els})
}

// String renders the module for debugging.
func (m *Module) String() string {
	var sb strings.Builder
	for _, g := range m.Globals {
		fmt.Fprintf(&sb, "global %s : %s\n", g.Symbol, g.Ty)
	}
	for _, fn := range m.Funcs {
		fmt.Fprintf(&sb, "func %s(", fn.Symbol)
		for i, p := range fn.Params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(p.String())
			sb.WriteString(": ")
			sb.WriteString(p.Ty.String())
		}
		sb.WriteString(")\n")
		for _, blk := range fn.Blocks {
			fmt.Fprintf(&sb, "  %s:\n", blk.Name)
			for _, inst := range blk.Insts {
				fmt.Fprintf(&sb, "    %s\n", formatInst(inst))
			}
		}
	}
	return sb.String()
}

func formatInst(i Inst) string {
	switch i := i.(type) {
	case *Alloca:
		return fmt.Sprintf("%s = alloca %s", i.Result, i.Result.Ty)
	case *Load:
		return fmt.Sprintf("%s = load %s", i.Result, i.Ptr)
	case *Store:
		return fmt.Sprintf("store %s -> %s", i.Val, i.Ptr)
	case *FieldAddr:
		return fmt.Sprintf("%s = field %s[%d]", i.Result, i.Base, i.Index)
	case *ElemAddr:
		return fmt.Sprintf("%s = elem %s[%s]", i.Result, i.Base, i.Index)
	case *GlobalAddr:
		return fmt.Sprintf("%s = addr @%s", i.Result, i.Result.Name)
	case *FuncAddr:
		return fmt.Sprintf("%s = funcaddr @%s", i.Result, i.Result.Name)
	case *Arith:
		return fmt.Sprintf("%s = %s %s, %s", i.Result, i.Op, i.X, i.Y)
	case *Compare:
		return fmt.Sprintf("%s = cmp %s %s, %s", i.Result, i.Op, i.X, i.Y)
	case *Convert:
		return fmt.Sprintf("%s = convert %s to %s", i.Result, i.X, i.To)
	case *Call:
		callee := i.Callee
		if !i.Indirect.IsZero() {
			callee = i.Indirect.String()
		}
		args := make([]string, len(i.Args))
		for k, a := range i.Args {
			args[k] = a.String()
		}
		return fmt.Sprintf("%s = call %s(%s)", i.Result, callee, strings.Join(args, ", "))
	case *Ret:
		parts := make([]string, len(i.Vals))
		for k, v := range i.Vals {
			parts[k] = v.String()
		}
		return "ret " + strings.Join(parts, ", ")
	case *Jump:
		return "jump " + i.Target.Name
	case *Branch:
		return fmt.Sprintf("branch %s ? %s : %s", i.Cond, i.Then.Name, i.Else.Name)
	}
	return "?"
}
