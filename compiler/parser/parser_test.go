package parser

import (
	"strings"
	"testing"

	"waylang/compiler/ast"
)

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	f, diags := ParseFile("test.way", []byte(src))
	if len(diags) > 0 {
		for _, d := range diags {
			t.Errorf("unexpected diagnostic: %s", d.Error())
		}
		t.Fatalf("parse failed")
	}
	return f
}

func TestHelloWorld(t *testing.T) {
	src := `package main

func main():
    print("Hello, World!")
`
	f := mustParse(t, src)
	if f.Package != "main" {
		t.Fatalf("package = %q", f.Package)
	}
	if len(f.Decls) != 1 {
		t.Fatalf("decls = %d", len(f.Decls))
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("decl is %T", f.Decls[0])
	}
	if fn.Name != "main" || len(fn.Params) != 0 || len(fn.Results) != 0 {
		t.Fatalf("unexpected signature: %+v", fn)
	}
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("body statements = %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("statement is %T", fn.Body.Stmts[0])
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expression is %T", es.X)
	}
	if id, ok := call.Fn.(*ast.Ident); !ok || id.Name != "print" {
		t.Fatalf("callee = %#v", call.Fn)
	}
	lit, ok := call.Args[0].(*ast.StringLit)
	if !ok || lit.Value != "Hello, World!" {
		t.Fatalf("argument = %#v", call.Args[0])
	}
}

func TestFunctionSignature(t *testing.T) {
	src := `package main

func add(a: int, b: int) -> int:
    return a + b

func greet(name: string = "World"):
    print(f"Hello, {name}!")
`
	f := mustParse(t, src)
	add := f.Decls[0].(*ast.FuncDecl)
	if len(add.Params) != 2 || add.Params[0].Name != "a" {
		t.Fatalf("params = %+v", add.Params)
	}
	if nt, ok := add.Params[0].Type.(*ast.NamedType); !ok || nt.Name != "int" {
		t.Fatalf("param type = %#v", add.Params[0].Type)
	}
	if len(add.Results) != 1 {
		t.Fatalf("results = %d", len(add.Results))
	}
	greet := f.Decls[1].(*ast.FuncDecl)
	if greet.Params[0].Default == nil {
		t.Fatal("default value not parsed")
	}
	ret := greet.Body.Stmts[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	fs, ok := ret.Args[0].(*ast.FStringLit)
	if !ok {
		t.Fatalf("f-string argument is %T", ret.Args[0])
	}
	if len(fs.Parts) != 3 {
		t.Fatalf("f-string parts = %d", len(fs.Parts))
	}
	if fs.Parts[0].Text != "Hello, " || fs.Parts[1].Expr == nil || fs.Parts[2].Text != "!" {
		t.Fatalf("f-string parts = %+v", fs.Parts)
	}
	if id, ok := fs.Parts[1].Expr.(*ast.Ident); !ok || id.Name != "name" {
		t.Fatalf("interpolated expression = %#v", fs.Parts[1].Expr)
	}
}

func TestStructAndMethods(t *testing.T) {
	src := `package main

struct Point:
    x: int
    y: int = 0
    label: string

struct NamedPoint:
    point: Point
    name: string

func (p: Point) sum() -> int:
    return p.x + p.y

func main():
    p := Point{x: 1, y: 2, label: "origin"}
    print(p.sum())
`
	f := mustParse(t, src)
	st := f.Decls[0].(*ast.StructDecl)
	if st.Name != "Point" || len(st.Fields) != 3 {
		t.Fatalf("struct = %+v", st)
	}
	if st.Fields[1].Default == nil {
		t.Fatal("field default not parsed")
	}
	method := f.Decls[2].(*ast.FuncDecl)
	if method.Recv == nil || method.Recv.Name != "p" {
		t.Fatalf("receiver = %+v", method.Recv)
	}
	if rt, ok := method.Recv.Type.(*ast.NamedType); !ok || rt.Name != "Point" {
		t.Fatalf("receiver type = %#v", method.Recv.Type)
	}
	body := f.Decls[3].(*ast.FuncDecl).Body
	define := body.Stmts[0].(*ast.DefineStmt)
	lit, ok := define.Values[0].(*ast.StructLit)
	if !ok || len(lit.Fields) != 3 || lit.Type.Name != "Point" {
		t.Fatalf("struct literal = %#v", define.Values[0])
	}
	call := body.Stmts[1].(*ast.ExprStmt).X.(*ast.CallExpr)
	if _, ok := call.Args[0].(*ast.CallExpr).Fn.(*ast.MemberExpr); !ok {
		t.Fatalf("method call = %#v", call.Args[0])
	}
}

func TestInterfaceDecl(t *testing.T) {
	src := `package main

interface Stringer:
    to_string() -> string
    equals(other: any) -> bool
`
	f := mustParse(t, src)
	iface := f.Decls[0].(*ast.InterfaceDecl)
	if iface.Name != "Stringer" || len(iface.Methods) != 2 {
		t.Fatalf("interface = %+v", iface)
	}
	if iface.Methods[1].Params[0].Name != "other" {
		t.Fatalf("method params = %+v", iface.Methods[1].Params)
	}
}

func TestControlFlow(t *testing.T) {
	src := `package main

func main():
    total := 0
    for i := 0; i < 10; i++:
        total += i
    for n in range(3):
        print(n)
    for k, v in m:
        print(k, v)
    while total > 0:
        total -= 1
        if total == 5:
            continue
        if total == 3:
            break
    switch total:
        case 0:
            print("zero")
        case 1, 2:
            print("small")
        default:
            print("big")
    if total == 0:
        print("a")
    elif total == 1:
        print("b")
    else:
        print("c")
    defer print("done")
`
	f := mustParse(t, src)
	stmts := f.Decls[0].(*ast.FuncDecl).Body.Stmts

	forStmt, ok := stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("stmt 1 = %T", stmts[1])
	}
	if forStmt.Init == nil || forStmt.Cond == nil || forStmt.Post == nil {
		t.Fatalf("incomplete three-part for: %+v", forStmt)
	}

	fi, ok := stmts[2].(*ast.ForInStmt)
	if !ok || fi.Key != "" || fi.Val != "n" {
		t.Fatalf("single-variable for-in = %#v", stmts[2])
	}
	call, ok := fi.Iter.(*ast.CallExpr)
	if !ok {
		t.Fatalf("iterable = %T", fi.Iter)
	}
	if id, _ := call.Fn.(*ast.Ident); id == nil || id.Name != "range" {
		t.Fatalf("range call = %#v", call.Fn)
	}

	fi2, ok := stmts[3].(*ast.ForInStmt)
	if !ok || fi2.Key != "k" || fi2.Val != "v" {
		t.Fatalf("two-variable for-in = %#v", stmts[3])
	}

	while, ok := stmts[4].(*ast.WhileStmt)
	if !ok || len(while.Body.Stmts) != 3 {
		t.Fatalf("while = %#v", stmts[4])
	}
	if _, ok := while.Body.Stmts[1].(*ast.IfStmt).Then.Stmts[0].(*ast.ContinueStmt); !ok {
		t.Fatalf("continue not parsed: %#v", while.Body.Stmts[1])
	}
	if _, ok := while.Body.Stmts[2].(*ast.IfStmt).Then.Stmts[0].(*ast.BreakStmt); !ok {
		t.Fatalf("break not parsed: %#v", while.Body.Stmts[2])
	}

	sw, ok := stmts[5].(*ast.SwitchStmt)
	if !ok {
		t.Fatalf("switch = %T", stmts[5])
	}
	if len(sw.Cases) != 2 || sw.Default == nil {
		t.Fatalf("switch clauses = %d, default nil? %v", len(sw.Cases), sw.Default == nil)
	}
	if len(sw.Cases[1].Values) != 2 {
		t.Fatalf("second case values = %d", len(sw.Cases[1].Values))
	}

	ifs, ok := stmts[6].(*ast.IfStmt)
	if !ok || len(ifs.Elifs) != 1 || ifs.Else == nil {
		t.Fatalf("if/elif/else = %#v", stmts[6])
	}

	if _, ok := stmts[7].(*ast.DeferStmt); !ok {
		t.Fatalf("defer = %T", stmts[7])
	}
}

func TestExpressionsAndLiterals(t *testing.T) {
	src := `package main

func main():
    xs := [1, 2, 3]
    m := {"a": 1, "b": 2}
    f := func(x: int) -> int:
        return x * 2
    y := xs[0] + m["a"] + f(3)
    z := y[0:2]
    v := (1 + 2) * 3
    b := !flag && y > 0 || v <= 10
`
	f := mustParse(t, src)
	stmts := f.Decls[0].(*ast.FuncDecl).Body.Stmts
	if arr, ok := stmts[0].(*ast.DefineStmt).Values[0].(*ast.ArrayLit); !ok || len(arr.Values) != 3 {
		t.Fatalf("array literal = %#v", stmts[0])
	}
	if ml, ok := stmts[1].(*ast.DefineStmt).Values[0].(*ast.MapLit); !ok || len(ml.Entries) != 2 {
		t.Fatalf("map literal = %#v", stmts[1])
	}
	if _, ok := stmts[2].(*ast.DefineStmt).Values[0].(*ast.FuncLit); !ok {
		t.Fatalf("func literal = %#v", stmts[2])
	}
	bin := stmts[3].(*ast.DefineStmt).Values[0].(*ast.BinaryExpr)
	if bin.Op.String() != "+" {
		t.Fatalf("left-associative sum root = %s", bin.Op)
	}
	if _, ok := stmts[4].(*ast.DefineStmt).Values[0].(*ast.SliceExpr); !ok {
		t.Fatalf("slice = %#v", stmts[4])
	}
	orExpr := stmts[6].(*ast.DefineStmt).Values[0].(*ast.BinaryExpr)
	if orExpr.Op.String() != "||" {
		t.Fatalf("logical or root = %s", orExpr.Op)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "missing package",
			src:  "func main():\n    print(1)\n",
			want: "package declaration",
		},
		{
			name: "missing colon after func",
			src:  "package main\n\nfunc main()\n    print(1)\n",
			want: "expected",
		},
		{
			name: "missing indented block",
			src:  "package main\n\nfunc main():\nprint(1)\n",
			want: "indented block",
		},
		{
			name: "unclosed call",
			src:  "package main\n\nfunc main():\n    print(1\n",
			want: "expected ')'",
		},
		{
			name: "bad statement",
			src:  "package main\n\nfunc main():\n    * 1\n",
			want: "expected an expression",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := ParseFile("bad.way", []byte(tt.src))
			if len(diags) == 0 {
				t.Fatalf("expected a diagnostic containing %q", tt.want)
			}
			found := false
			for _, d := range diags {
				if strings.Contains(d.Msg, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no diagnostic contains %q; got %v", tt.want, diags)
			}
		})
	}
}
