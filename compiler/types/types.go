// Package types defines WayLang's static type representation and the rules for
// assignability, conversion and interface satisfaction.
//
// WayLang is gradually typed: every expression has a static type, and `any`
// erases it at runtime while keeping a dynamic type tag. The types package is
// independent of the AST so that the IR and backend can share it.
package types

import (
	"fmt"
	"strings"
)

// Kind classifies a type.
type Kind uint8

const (
	Invalid Kind = iota
	Void
	Bool
	Int
	Float
	String
	Byte
	Rune
	Error
	Any
	Nil
	ArrayKind
	SliceKind
	MapKind
	StructKind
	FuncKind
	InterfaceKind
	RangeKind
	TupleKind
)

// Type is a WayLang type.
type Type interface {
	Kind() Kind
	String() string
}

// Basic is a primitive type. The package exposes one singleton per kind.
type Basic struct{ k Kind }

func (b *Basic) Kind() Kind { return b.k }

func (b *Basic) String() string {
	switch b.k {
	case Void:
		return "void"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Float:
		return "float"
	case String:
		return "string"
	case Byte:
		return "byte"
	case Rune:
		return "rune"
	case Error:
		return "error"
	case Any:
		return "any"
	case Nil:
		return "nil"
	}
	return "invalid"
}

// Primitive singletons.
var (
	VoidT   = &Basic{Void}
	BoolT   = &Basic{Bool}
	IntT    = &Basic{Int}
	FloatT  = &Basic{Float}
	StringT = &Basic{String}
	ByteT   = &Basic{Byte}
	RuneT   = &Basic{Rune}
	ErrorT  = &Basic{Error}
	AnyT    = &Basic{Any}
	NilT    = &Basic{Nil}
)

// Array is a fixed-size array type.
type Array struct {
	Len  int64
	Elem Type
}

func (t *Array) Kind() Kind     { return ArrayKind }
func (t *Array) String() string { return fmt.Sprintf("[%d]%s", t.Len, t.Elem) }

// Slice is a dynamically sized sequence.
type Slice struct{ Elem Type }

func (t *Slice) Kind() Kind     { return SliceKind }
func (t *Slice) String() string { return "[]" + t.Elem.String() }

// Map is a hash map type.
type Map struct {
	Key  Type
	Elem Type
}

func (t *Map) Kind() Kind     { return MapKind }
func (t *Map) String() string { return fmt.Sprintf("map[%s]%s", t.Key, t.Elem) }

// Field is a struct field.
type Field struct {
	Name string
	Type Type
}

// Struct is a nominal struct type.
type Struct struct {
	Name        string
	Fields      []*Field
	Methods     map[string]*Func
	MethodNames []string
	// Pkg is the defining package; two structs are identical only when both
	// name and package match.
	Pkg string
}

func (t *Struct) Kind() Kind     { return StructKind }
func (t *Struct) String() string { return t.Name }

// FieldIndex returns the index of the named field, or -1.
func (t *Struct) FieldIndex(name string) int {
	for i, f := range t.Fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// Method returns the method with the given name, or nil.
func (t *Struct) Method(name string) *Func {
	if t.Methods == nil {
		return nil
	}
	return t.Methods[name]
}

// Func is a function signature. Params excludes a method receiver.
type Func struct {
	Params  []Type
	Results []Type
	// ParamNames and ResultNames are informational.
	ParamNames  []string
	ResultNames []string
}

func (t *Func) Kind() Kind { return FuncKind }

func (t *Func) String() string {
	var b strings.Builder
	b.WriteString("func(")
	for i, p := range t.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteString(")")
	switch len(t.Results) {
	case 0:
	case 1:
		b.WriteString(" -> " + t.Results[0].String())
	default:
		b.WriteString(" -> (")
		for i, r := range t.Results {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(r.String())
		}
		b.WriteString(")")
	}
	return b.String()
}

// Interface is a structural interface type.
type Interface struct {
	Name        string
	MethodNames []string
	Methods     []*Func
	Pkg         string
}

func (t *Interface) Kind() Kind     { return InterfaceKind }
func (t *Interface) String() string { return t.Name }

// Range is the type produced by range().
type Range struct {
	Elem Type
	// WithValue is true for range over slices/maps/strings, which yields both
	// an index/key and a value.
	WithValue bool
}

func (t *Range) Kind() Kind     { return RangeKind }
func (t *Range) String() string { return "range" }

// Tuple is the type of a multi-value expression.
type Tuple struct{ Elems []Type }

func (t *Tuple) Kind() Kind { return TupleKind }

func (t *Tuple) String() string {
	parts := make([]string, len(t.Elems))
	for i, e := range t.Elems {
		parts[i] = e.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// --- predicates -----------------------------------------------------------

// IsNumeric reports whether t is int or float.
func IsNumeric(t Type) bool {
	k := t.Kind()
	return k == Int || k == Float
}

// IsInteger reports whether t is an integer-like type.
func IsInteger(t Type) bool {
	switch t.Kind() {
	case Int, Byte, Rune:
		return true
	}
	return false
}

// IsNilable reports whether nil may be assigned to t.
func IsNilable(t Type) bool {
	switch t.Kind() {
	case Any, Error, SliceKind, MapKind, FuncKind, InterfaceKind:
		return true
	case String:
		// A nil string is representable (null pointer) and behaves as "".
		return true
	}
	return false
}

// IsManaged reports whether values of t live on the garbage-collected heap, so
// the runtime must trace them.
func IsManaged(t Type) bool {
	switch t.Kind() {
	case String, SliceKind, MapKind, InterfaceKind, Any, Error, FuncKind:
		return true
	case ArrayKind:
		return IsManaged(t.(*Array).Elem)
	case StructKind:
		for _, f := range t.(*Struct).Fields {
			if IsManaged(f.Type) {
				return true
			}
		}
	}
	return false
}

// IsComparable reports whether t may be used as a map key or compared with ==.
func IsComparable(t Type) bool {
	switch t.Kind() {
	case Bool, Int, Float, String, Byte, Rune, Any, Error, Nil, InterfaceKind:
		return true
	case StructKind:
		for _, f := range t.(*Struct).Fields {
			if !IsComparable(f.Type) {
				return false
			}
		}
		return true
	}
	return false
}

// Identical reports whether a and b are the same type.
func Identical(a, b Type) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Kind() != b.Kind() {
		return false
	}
	switch at := a.(type) {
	case *Basic:
		return true
	case *Array:
		bt := b.(*Array)
		return at.Len == bt.Len && Identical(at.Elem, bt.Elem)
	case *Slice:
		return Identical(at.Elem, b.(*Slice).Elem)
	case *Map:
		bt := b.(*Map)
		return Identical(at.Key, bt.Key) && Identical(at.Elem, bt.Elem)
	case *Struct:
		bt := b.(*Struct)
		return at.Name == bt.Name && at.Pkg == bt.Pkg
	case *Func:
		bt := b.(*Func)
		if len(at.Params) != len(bt.Params) || len(at.Results) != len(bt.Results) {
			return false
		}
		for i := range at.Params {
			if !Identical(at.Params[i], bt.Params[i]) {
				return false
			}
		}
		for i := range at.Results {
			if !Identical(at.Results[i], bt.Results[i]) {
				return false
			}
		}
		return true
	case *Interface:
		bt := b.(*Interface)
		return at.Name == bt.Name && at.Pkg == bt.Pkg
	case *Range:
		return true
	case *Tuple:
		bt := b.(*Tuple)
		if len(at.Elems) != len(bt.Elems) {
			return false
		}
		for i := range at.Elems {
			if !Identical(at.Elems[i], bt.Elems[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// Implements reports whether concrete type t provides every method required by
// the interface iface. The check is structural.
func Implements(t Type, iface *Interface) bool {
	for i, name := range iface.MethodNames {
		want := iface.Methods[i]
		var have *Func
		switch tt := t.(type) {
		case *Struct:
			have = tt.Method(name)
		case *Interface:
			idx := tt.methodIndex(name)
			if idx >= 0 {
				have = tt.Methods[idx]
			}
		default:
			return false
		}
		if have == nil {
			return false
		}
		if !identicalSignature(have, want) {
			return false
		}
	}
	return true
}

func (t *Interface) methodIndex(name string) int {
	for i, n := range t.MethodNames {
		if n == name {
			return i
		}
	}
	return -1
}

func identicalSignature(a, b *Func) bool {
	if len(a.Params) != len(b.Params) || len(a.Results) != len(b.Results) {
		return false
	}
	for i := range a.Params {
		if !Identical(a.Params[i], b.Params[i]) {
			return false
		}
	}
	for i := range a.Results {
		if !Identical(a.Results[i], b.Results[i]) {
			return false
		}
	}
	return true
}

// Assignable reports whether a value of type src may be stored in a variable of
// type dst without an explicit conversion, and explains why not when rejected.
func Assignable(dst, src Type) (bool, string) {
	if dst == nil || src == nil {
		return false, "unknown type"
	}
	if Identical(dst, src) {
		return true, ""
	}
	if dst.Kind() == Any {
		return true, ""
	}
	if src.Kind() == Any {
		return false, "an `any` value needs an explicit conversion"
	}
	if src.Kind() == Nil {
		if IsNilable(dst) {
			return true, ""
		}
		return false, fmt.Sprintf("nil cannot be assigned to %s", dst)
	}
	if dst.Kind() == InterfaceKind {
		if Implements(src, dst.(*Interface)) {
			return true, ""
		}
		return false, fmt.Sprintf("%s does not implement interface %s", src, dst)
	}
	if dst.Kind() == Error {
		return false, "only nil or an error value can be assigned to error"
	}
	return false, fmt.Sprintf("cannot assign %s to %s", src, dst)
}

// Convertible reports whether an explicit conversion from src to dst exists.
func Convertible(dst, src Type) bool {
	if Identical(dst, src) {
		return true
	}
	if dst.Kind() == String {
		return true // any value has a printable form
	}
	switch dst.Kind() {
	case Int:
		return IsNumeric(src) || src.Kind() == String || src.Kind() == Rune || src.Kind() == Byte || src.Kind() == Bool
	case Float:
		return IsNumeric(src) || src.Kind() == String || src.Kind() == Bool
	case Bool:
		return IsNumeric(src) || src.Kind() == String || src.Kind() == Bool
	case Byte:
		return IsInteger(src) || src.Kind() == String
	case Rune:
		return IsInteger(src) || src.Kind() == String
	case Any, InterfaceKind, SliceKind, MapKind, Error:
		if dst.Kind() == Any {
			return true
		}
		ok, _ := Assignable(dst, src)
		return ok
	}
	return false
}
