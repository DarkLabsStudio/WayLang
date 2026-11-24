// Package ast defines the abstract syntax tree produced by the WayLang parser.
package ast

import "waylang/compiler/token"

// Node is any syntax tree node.
type Node interface {
	Pos() token.Position
}

// Expr is an expression node.
type Expr interface {
	Node
	exprNode()
}

// Stmt is a statement node.
type Stmt interface {
	Node
	stmtNode()
}

// Decl is a top-level declaration.
type Decl interface {
	Node
	declNode()
}

// TypeExpr is a syntactic type.
type TypeExpr interface {
	Node
	typeNode()
}

// File is a parsed source file.
type File struct {
	Path       string
	Package    string
	PackagePos token.Position
	Imports    []*ImportDecl
	Decls      []Decl
}

func (f *File) Pos() token.Position { return f.PackagePos }

// ImportDecl is `import "path"` with an optional alias.
type ImportDecl struct {
	Pos0  token.Position
	Alias string
	Path  string
}

func (d *ImportDecl) Pos() token.Position { return d.Pos0 }
func (d *ImportDecl) declNode()           {}

// FuncDecl is a function or method declaration.
type FuncDecl struct {
	Pos0    token.Position
	Name    string
	Recv    *Param // non-nil for methods
	Params  []*Param
	Results []TypeExpr
	Body    *BlockStmt
}

func (d *FuncDecl) Pos() token.Position { return d.Pos0 }
func (d *FuncDecl) declNode()           {}

// StructDecl declares a struct type.
type StructDecl struct {
	Pos0   token.Position
	Name   string
	Fields []*Field
}

func (d *StructDecl) Pos() token.Position { return d.Pos0 }
func (d *StructDecl) declNode()           {}

// Field is a struct field.
type Field struct {
	Pos0    token.Position
	Name    string
	Type    TypeExpr
	Default Expr
}

func (f *Field) Pos() token.Position { return f.Pos0 }

// InterfaceDecl declares an interface type.
type InterfaceDecl struct {
	Pos0    token.Position
	Name    string
	Methods []*MethodSig
}

func (d *InterfaceDecl) Pos() token.Position { return d.Pos0 }
func (d *InterfaceDecl) declNode()           {}

// MethodSig is a method signature inside an interface.
type MethodSig struct {
	Pos0    token.Position
	Name    string
	Params  []*Param
	Results []TypeExpr
}

func (m *MethodSig) Pos() token.Position { return m.Pos0 }

// ConstDecl declares a constant (top level or local).
type ConstDecl struct {
	Pos0  token.Position
	Name  string
	Type  TypeExpr
	Value Expr
}

func (d *ConstDecl) Pos() token.Position { return d.Pos0 }
func (d *ConstDecl) declNode()           {}
func (d *ConstDecl) stmtNode()           {}

// GlobalDecl declares a package-level variable.
type GlobalDecl struct {
	Pos0  token.Position
	Name  string
	Type  TypeExpr
	Value Expr
}

func (d *GlobalDecl) Pos() token.Position { return d.Pos0 }
func (d *GlobalDecl) declNode()           {}

// Param is a function parameter or method receiver.
type Param struct {
	Pos0    token.Position
	Name    string
	Type    TypeExpr // nil when inferred from a default value
	Default Expr
}

func (p *Param) Pos() token.Position { return p.Pos0 }

// --- type expressions -----------------------------------------------------

type NamedType struct {
	Pos0 token.Position
	Name string
}

func (t *NamedType) Pos() token.Position { return t.Pos0 }
func (t *NamedType) typeNode()           {}

// ArrayType is `[N]T`.
type ArrayType struct {
	Pos0 token.Position
	Len  Expr
	Elem TypeExpr
}

func (t *ArrayType) Pos() token.Position { return t.Pos0 }
func (t *ArrayType) typeNode()           {}

// SliceType is `[]T`.
type SliceType struct {
	Pos0 token.Position
	Elem TypeExpr
}

func (t *SliceType) Pos() token.Position { return t.Pos0 }
func (t *SliceType) typeNode()           {}

// MapType is `map[K]V`.
type MapType struct {
	Pos0  token.Position
	Key   TypeExpr
	Value TypeExpr
}

func (t *MapType) Pos() token.Position { return t.Pos0 }
func (t *MapType) typeNode()           {}

// FuncTypeExpr is `func(A, B) -> R`.
type FuncTypeExpr struct {
	Pos0    token.Position
	Params  []TypeExpr
	Results []TypeExpr
}

func (t *FuncTypeExpr) Pos() token.Position { return t.Pos0 }
func (t *FuncTypeExpr) typeNode()           {}

// --- statements -----------------------------------------------------------

// BlockStmt is a sequence of statements indented under a colon.
type BlockStmt struct {
	Pos0  token.Position
	Stmts []Stmt
}

func (s *BlockStmt) Pos() token.Position { return s.Pos0 }
func (s *BlockStmt) stmtNode()           {}

// ExprStmt is an expression evaluated for its effect.
type ExprStmt struct {
	Pos0 token.Position
	X    Expr
}

func (s *ExprStmt) Pos() token.Position { return s.Pos0 }
func (s *ExprStmt) stmtNode()           {}

// DefineStmt is `a, b := expr, expr`.
type DefineStmt struct {
	Pos0   token.Position
	Names  []string
	Values []Expr
}

func (s *DefineStmt) Pos() token.Position { return s.Pos0 }
func (s *DefineStmt) stmtNode()           {}

// AssignStmt is `target op values`, where Op may be `=`, `+=`, etc.
type AssignStmt struct {
	Pos0    token.Position
	Targets []Expr
	Op      token.Kind
	Values  []Expr
}

func (s *AssignStmt) Pos() token.Position { return s.Pos0 }
func (s *AssignStmt) stmtNode()           {}

// VarDeclStmt is `name: Type = value` inside a function.
type VarDeclStmt struct {
	Pos0  token.Position
	Name  string
	Type  TypeExpr
	Value Expr
}

func (s *VarDeclStmt) Pos() token.Position { return s.Pos0 }
func (s *VarDeclStmt) stmtNode()           {}

// ReturnStmt returns zero or more values.
type ReturnStmt struct {
	Pos0   token.Position
	Values []Expr
}

func (s *ReturnStmt) Pos() token.Position { return s.Pos0 }
func (s *ReturnStmt) stmtNode()           {}

// ElifBranch is one `elif` clause.
type ElifBranch struct {
	Cond Expr
	Body *BlockStmt
}

// IfStmt is `if / elif / else`.
type IfStmt struct {
	Pos0  token.Position
	Cond  Expr
	Then  *BlockStmt
	Elifs []*ElifBranch
	Else  *BlockStmt
}

func (s *IfStmt) Pos() token.Position { return s.Pos0 }
func (s *IfStmt) stmtNode()           {}

// WhileStmt is `while cond:`.
type WhileStmt struct {
	Pos0 token.Position
	Cond Expr
	Body *BlockStmt
}

func (s *WhileStmt) Pos() token.Position { return s.Pos0 }
func (s *WhileStmt) stmtNode()           {}

// ForStmt is a C-style loop with optional init/cond/post.
type ForStmt struct {
	Pos0 token.Position
	Init Stmt
	Cond Expr
	Post Stmt
	Body *BlockStmt
}

func (s *ForStmt) Pos() token.Position { return s.Pos0 }
func (s *ForStmt) stmtNode()           {}

// ForInStmt is `for x in iterable:` or `for k, v in iterable:`.
type ForInStmt struct {
	Pos0 token.Position
	Key  string
	Val  string // empty for single-variable form
	Iter Expr
	Body *BlockStmt
}

func (s *ForInStmt) Pos() token.Position { return s.Pos0 }
func (s *ForInStmt) stmtNode()           {}

// SwitchStmt is `switch subject:` with case clauses.
type SwitchStmt struct {
	Pos0    token.Position
	Subject Expr
	Cases   []*CaseClause
	Default *BlockStmt
}

func (s *SwitchStmt) Pos() token.Position { return s.Pos0 }
func (s *SwitchStmt) stmtNode()           {}

// CaseClause is one `case v, w:` clause.
type CaseClause struct {
	Pos0   token.Position
	Values []Expr
	Body   *BlockStmt
}

func (c *CaseClause) Pos() token.Position { return c.Pos0 }

// BreakStmt, ContinueStmt, DeferStmt.
type BreakStmt struct{ Pos0 token.Position }

func (s *BreakStmt) Pos() token.Position { return s.Pos0 }
func (s *BreakStmt) stmtNode()           {}

type ContinueStmt struct{ Pos0 token.Position }

func (s *ContinueStmt) Pos() token.Position { return s.Pos0 }
func (s *ContinueStmt) stmtNode()           {}

type DeferStmt struct {
	Pos0 token.Position
	Call Expr
}

func (s *DeferStmt) Pos() token.Position { return s.Pos0 }
func (s *DeferStmt) stmtNode()           {}

// --- expressions ----------------------------------------------------------

type Ident struct {
	Pos0 token.Position
	Name string
}

func (e *Ident) Pos() token.Position { return e.Pos0 }
func (e *Ident) exprNode()           {}

type IntLit struct {
	Pos0  token.Position
	Value int64
	Raw   string
}

func (e *IntLit) Pos() token.Position { return e.Pos0 }
func (e *IntLit) exprNode()           {}

type FloatLit struct {
	Pos0  token.Position
	Value float64
	Raw   string
}

func (e *FloatLit) Pos() token.Position { return e.Pos0 }
func (e *FloatLit) exprNode()           {}

type StringLit struct {
	Pos0  token.Position
	Value string
}

func (e *StringLit) Pos() token.Position { return e.Pos0 }
func (e *StringLit) exprNode()           {}

type RuneLit struct {
	Pos0  token.Position
	Value rune
}

func (e *RuneLit) Pos() token.Position { return e.Pos0 }
func (e *RuneLit) exprNode()           {}

type BoolLit struct {
	Pos0  token.Position
	Value bool
}

func (e *BoolLit) Pos() token.Position { return e.Pos0 }
func (e *BoolLit) exprNode()           {}

type NilLit struct{ Pos0 token.Position }

func (e *NilLit) Pos() token.Position { return e.Pos0 }
func (e *NilLit) exprNode()           {}

// FStringPart is either literal text or an interpolated expression.
type FStringPart struct {
	Text string
	Expr Expr
}

// FStringLit is an interpolated string.
type FStringLit struct {
	Pos0  token.Position
	Parts []FStringPart
}

func (e *FStringLit) Pos() token.Position { return e.Pos0 }
func (e *FStringLit) exprNode()           {}

type UnaryExpr struct {
	Pos0 token.Position
	Op   token.Kind
	X    Expr
}

func (e *UnaryExpr) Pos() token.Position { return e.Pos0 }
func (e *UnaryExpr) exprNode()           {}

type BinaryExpr struct {
	Pos0 token.Position
	Op   token.Kind
	X, Y Expr
}

func (e *BinaryExpr) Pos() token.Position { return e.Pos0 }
func (e *BinaryExpr) exprNode()           {}

// CallExpr is a function call. Fn may be a type name for conversions such as
// int(x), or a member expression for method calls.
type CallExpr struct {
	Pos0 token.Position
	Fn   Expr
	Args []Expr
}

func (e *CallExpr) Pos() token.Position { return e.Pos0 }
func (e *CallExpr) exprNode()           {}

type MemberExpr struct {
	Pos0 token.Position
	X    Expr
	Name string
}

func (e *MemberExpr) Pos() token.Position { return e.Pos0 }
func (e *MemberExpr) exprNode()           {}

type IndexExpr struct {
	Pos0  token.Position
	X     Expr
	Index Expr
}

func (e *IndexExpr) Pos() token.Position { return e.Pos0 }
func (e *IndexExpr) exprNode()           {}

// SliceExpr is `x[low:high]`; either bound may be nil.
type SliceExpr struct {
	Pos0 token.Position
	X    Expr
	Low  Expr
	High Expr
}

func (e *SliceExpr) Pos() token.Position { return e.Pos0 }
func (e *SliceExpr) exprNode()           {}

// StructLit is `Type{field: value, ...}`.
type StructLit struct {
	Pos0   token.Position
	Type   *NamedType
	Fields []*StructLitField
}

func (e *StructLit) Pos() token.Position { return e.Pos0 }
func (e *StructLit) exprNode()           {}

type StructLitField struct {
	Pos0  token.Position
	Name  string
	Value Expr
}

func (f *StructLitField) Pos() token.Position { return f.Pos0 }

// FuncLit is an anonymous function.
type FuncLit struct {
	Pos0    token.Position
	Params  []*Param
	Results []TypeExpr
	Body    *BlockStmt
}

func (e *FuncLit) Pos() token.Position { return e.Pos0 }
func (e *FuncLit) exprNode()           {}

// ArrayLit is `[a, b, c]`.
type ArrayLit struct {
	Pos0   token.Position
	Values []Expr
}

func (e *ArrayLit) Pos() token.Position { return e.Pos0 }
func (e *ArrayLit) exprNode()           {}

// MapEntry is a single key/value pair in a map literal.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// MapLit is `{key: value, ...}`.
type MapLit struct {
	Pos0    token.Position
	Entries []MapEntry
}

func (e *MapLit) Pos() token.Position { return e.Pos0 }
func (e *MapLit) exprNode()           {}
