// Package semantic performs name resolution and type checking, and produces a
// typed Program that the IR generator lowers.
package semantic

import (
	"waylang/compiler/ast"
	"waylang/compiler/types"
)

// SymKind classifies a symbol.
type SymKind uint8

const (
	SymVar SymKind = iota
	SymConst
	SymFunc
	SymType
	SymPackage
	SymBuiltin
)

// Constant is a compile-time constant value.
type Constant struct {
	Kind  types.Kind
	Int   int64
	Float float64
	Str   string
	Bool  bool
}

// Symbol is a resolved name.
type Symbol struct {
	Name    string
	Kind    SymKind
	Type    types.Type
	Mutable bool
	Const   *Constant
	Func    *FuncInfo
	TypeArg types.Type // for SymType
	Package *StdPackage
	Global  *GlobalInfo
	// Builtin names for SymBuiltin (print, len, ...).
	Builtin string

	// Owner is the function that declares a local or parameter (nil for
	// package-level symbols).
	Owner *FuncInfo
	// ParamIndex is the parameter position, or -1 for non-parameters.
	ParamIndex int
	// IsReceiver marks a method receiver parameter.
	IsReceiver bool
}

// FuncInfo describes a declared function or method.
type FuncInfo struct {
	Name   string // source name
	Symbol string // backend symbol, e.g. "main.main" or "main.User.greet"
	Decl   *ast.FuncDecl
	Sig    *types.Func
	Recv   *types.Struct // non-nil for methods
	// Slot is the method's dispatch table slot (methods only, else -1).
	Slot int
	// UsesDefer records that the body contains defer statements.
	UsesDefer bool
	// Defaults holds the default-value expressions for parameters, aligned
	// with Sig.Params (nil entries have no default).
	Defaults []ast.Expr
	// ParamSyms holds the symbols for parameters in declaration order.
	ParamSyms []*Symbol
	// RecvSym is the receiver's symbol for methods.
	RecvSym *Symbol
}

// ParamSymbols maps a declaration's parameter idents to their symbols, used by
// codegen for stable naming.
type ParamSymbols map[*ast.Ident]*Symbol

// GlobalInfo describes a package-level variable.
type GlobalInfo struct {
	Name   string
	Symbol string
	Type   types.Type
	Decl   *ast.GlobalDecl
	Init   ast.Expr
}

// CallKind distinguishes how a call is lowered.
type CallKind uint8

const (
	CallDirect    CallKind = iota // statically known function
	CallMethod                    // struct method, receiver passed as self
	CallIface                     // interface dispatch through a vtable slot
	CallBuiltin                   // print, len, range, type, error
	CallStd                       // standard library function
	CallConvert                   // type conversion
	CallFuncValue                 // call through a function-typed value
)

// CallInfo is the resolved form of a call expression.
type CallInfo struct {
	Kind       CallKind
	Func       *FuncInfo
	Slot       int
	Builtin    string
	StdRuntime string
	ConvertTo  types.Type
	// Params are the expected argument types, used by lowering to coerce.
	Params []types.Type
}

// MemberKind distinguishes field access, method references and package
// qualifiers.
type MemberKind uint8

const (
	MemberField MemberKind = iota
	MemberMethod
	MemberPackage
)

// MemberInfo is the resolved form of a selector expression.
type MemberInfo struct {
	Kind       MemberKind
	FieldIndex int
	Method     *FuncInfo
	StdFunc    *StdFunc
	Package    *StdPackage
}

// Program is a fully checked module, ready for lowering.
type Program struct {
	Package string
	Source  []byte
	Path    string

	ExprTypes map[ast.Expr]types.Type
	Calls     map[*ast.CallExpr]*CallInfo
	Members   map[*ast.MemberExpr]*MemberInfo
	Idents    map[*ast.Ident]*Symbol
	// ParamOf maps a parameter ident to its owning function.
	ParamOf map[*ast.Ident]*FuncInfo
	// FieldDefaults holds default expressions for struct fields.
	FieldDefaults map[*types.Field]ast.Expr
	// FuncLits maps an anonymous function literal to its generated function.
	FuncLits map[*ast.FuncLit]*FuncInfo
	// StmtSyms holds the symbols declared by a statement, in source order.
	StmtSyms map[ast.Stmt][]*Symbol

	Structs        map[string]*types.Struct
	StructOrder    []*types.Struct
	Interfaces     map[string]*types.Interface
	InterfaceOrder []*types.Interface

	Funcs      []*FuncInfo
	FuncByName map[string]*FuncInfo
	Main       *FuncInfo

	Globals      []*GlobalInfo
	GlobalByName map[string]*GlobalInfo

	// MethodSlots is the program-wide method-name table used for interface
	// dispatch: an interface call loads slot N of the receiver's vtable.
	MethodSlots []string

	// Imports maps a local package alias to its standard library package.
	Imports map[string]*StdPackage

	// UsesDefer reports whether any function uses defer.
	UsesDefer bool
}

// TypeOf returns the annotated type of an expression.
func (p *Program) TypeOf(e ast.Expr) types.Type {
	return p.ExprTypes[e]
}

// NewProgram creates an empty program.
func NewProgram(pkg, path string) *Program {
	return &Program{
		Package:       pkg,
		Path:          path,
		ExprTypes:     map[ast.Expr]types.Type{},
		Calls:         map[*ast.CallExpr]*CallInfo{},
		Members:       map[*ast.MemberExpr]*MemberInfo{},
		Idents:        map[*ast.Ident]*Symbol{},
		ParamOf:       map[*ast.Ident]*FuncInfo{},
		FieldDefaults: map[*types.Field]ast.Expr{},
		FuncLits:      map[*ast.FuncLit]*FuncInfo{},
		StmtSyms:      map[ast.Stmt][]*Symbol{},
		Structs:       map[string]*types.Struct{},
		Interfaces:    map[string]*types.Interface{},
		FuncByName:    map[string]*FuncInfo{},
		GlobalByName:  map[string]*GlobalInfo{},
		Imports:       map[string]*StdPackage{},
	}
}

// MethodSlot returns the dispatch slot for a method name, allocating one if
// needed.
func (p *Program) MethodSlot(name string) int {
	for i, n := range p.MethodSlots {
		if n == name {
			return i
		}
	}
	p.MethodSlots = append(p.MethodSlots, name)
	return len(p.MethodSlots) - 1
}
