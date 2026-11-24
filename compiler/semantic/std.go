package semantic

import (
	"path"
	"sort"

	"waylang/compiler/types"
)

// StdFunc is a standard library function implemented by the native runtime.
type StdFunc struct {
	Name    string
	Runtime string // C symbol in the WayLang runtime
	Params  []types.Type
	Results []types.Type
}

// Sig returns the function's type.
func (f *StdFunc) Sig() *types.Func {
	return &types.Func{Params: f.Params, Results: f.Results}
}

// StdPackage is a standard library package.
type StdPackage struct {
	Name  string
	Funcs map[string]*StdFunc
}

// FuncNames returns the package's function names in sorted order.
func (p *StdPackage) FuncNames() []string {
	names := make([]string, 0, len(p.Funcs))
	for n := range p.Funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func str() types.Type     { return types.StringT }
func integer() types.Type { return types.IntT }
func boolean() types.Type { return types.BoolT }

// StdPackages is the built-in standard library surfaced to programs.
//
// These packages are thin, typed front ends over the native runtime; the
// runtime symbol is what the backend calls.
var StdPackages = func() map[string]*StdPackage {
	strs := &StdPackage{Name: "strings", Funcs: map[string]*StdFunc{}}
	add := func(p *StdPackage, name, runtime string, params []types.Type, results []types.Type) {
		p.Funcs[name] = &StdFunc{Name: name, Runtime: runtime, Params: params, Results: results}
	}
	add(strs, "upper", "way_strings_upper", []types.Type{str()}, []types.Type{str()})
	add(strs, "lower", "way_strings_lower", []types.Type{str()}, []types.Type{str()})
	add(strs, "trim", "way_strings_trim", []types.Type{str()}, []types.Type{str()})
	add(strs, "contains", "way_strings_contains", []types.Type{str(), str()}, []types.Type{boolean()})
	add(strs, "has_prefix", "way_strings_has_prefix", []types.Type{str(), str()}, []types.Type{boolean()})
	add(strs, "has_suffix", "way_strings_has_suffix", []types.Type{str(), str()}, []types.Type{boolean()})
	add(strs, "index", "way_strings_index", []types.Type{str(), str()}, []types.Type{integer()})
	add(strs, "replace", "way_strings_replace", []types.Type{str(), str(), str()}, []types.Type{str()})
	add(strs, "repeat", "way_strings_repeat", []types.Type{str(), integer()}, []types.Type{str()})

	maths := &StdPackage{Name: "math", Funcs: map[string]*StdFunc{}}
	add(maths, "sqrt", "way_math_sqrt", []types.Type{types.FloatT}, []types.Type{types.FloatT})
	add(maths, "pow", "way_math_pow", []types.Type{types.FloatT, types.FloatT}, []types.Type{types.FloatT})
	add(maths, "floor", "way_math_floor", []types.Type{types.FloatT}, []types.Type{types.FloatT})
	add(maths, "ceil", "way_math_ceil", []types.Type{types.FloatT}, []types.Type{types.FloatT})
	add(maths, "round", "way_math_round", []types.Type{types.FloatT}, []types.Type{types.FloatT})
	add(maths, "abs", "way_math_abs", []types.Type{types.FloatT}, []types.Type{types.FloatT})
	add(maths, "min", "way_math_min", []types.Type{integer(), integer()}, []types.Type{integer()})
	add(maths, "max", "way_math_max", []types.Type{integer(), integer()}, []types.Type{integer()})

	oss := &StdPackage{Name: "os", Funcs: map[string]*StdFunc{}}
	add(oss, "exit", "way_os_exit", []types.Type{integer()}, nil)
	add(oss, "getenv", "way_os_getenv", []types.Type{str()}, []types.Type{str()})
	add(oss, "args", "way_os_args", nil, []types.Type{&types.Slice{Elem: types.StringT}})

	tim := &StdPackage{Name: "time", Funcs: map[string]*StdFunc{}}
	add(tim, "now", "way_time_now", nil, []types.Type{integer()})
	add(tim, "sleep", "way_time_sleep", []types.Type{integer()}, nil)

	return map[string]*StdPackage{
		"strings": strs,
		"math":    maths,
		"os":      oss,
		"time":    tim,
	}
}()

// LookupStdPackage finds a standard library package by import path.
func LookupStdPackage(importPath string) *StdPackage {
	return StdPackages[importPath]
}

// StdPackageName returns the default local name for an import path.
func StdPackageName(importPath string) string {
	return path.Base(importPath)
}
