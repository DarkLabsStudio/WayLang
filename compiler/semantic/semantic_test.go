package semantic

import (
	"strings"
	"testing"

	"waylang/compiler/ast"
	"waylang/compiler/parser"
	"waylang/compiler/types"
)

func checkSrc(t *testing.T, src string) (*Program, []string) {
	t.Helper()
	f, pdiags := parser.ParseFile("test.way", []byte(src))
	if len(pdiags) > 0 {
		var msgs []string
		for _, d := range pdiags {
			msgs = append(msgs, d.Msg)
		}
		return nil, msgs
	}
	prog, diags := Check(f, "test.way", []byte(src))
	var msgs []string
	for _, d := range diags {
		msgs = append(msgs, d.Msg)
	}
	return prog, msgs
}

func mustCheck(t *testing.T, src string) *Program {
	t.Helper()
	prog, msgs := checkSrc(t, src)
	if len(msgs) > 0 {
		t.Fatalf("unexpected diagnostics:\n%s", strings.Join(msgs, "\n"))
	}
	return prog
}

func TestHelloWorldChecks(t *testing.T) {
	prog := mustCheck(t, "package main\n\nfunc main():\n    print(\"Hello, World!\")\n")
	if prog.Main == nil {
		t.Fatal("main not found")
	}
	if len(prog.Funcs) != 1 {
		t.Fatalf("funcs = %d", len(prog.Funcs))
	}
}

func TestTypeInference(t *testing.T) {
	src := `package main

func main():
    x := 10
    y := 2.5
    s := "hi"
    b := x > 3
    n: int = 7
    f: float = 1
`
	prog := mustCheck(t, src)
	body := prog.Main.Decl.Body.Stmts

	want := []types.Kind{types.Int, types.Float, types.String, types.Bool}
	for i, w := range want {
		def := body[i].(*ast.DefineStmt)
		got := prog.TypeOf(def.Values[0])
		if got.Kind() != w {
			t.Errorf("statement %d: type = %s, want kind %v", i, got, w)
		}
	}
	if got := prog.TypeOf(body[5].(*ast.VarDeclStmt).Value); got.Kind() != types.Float {
		t.Errorf("float literal in float context has type %s", got)
	}
}

func TestConstantsAndArrayLength(t *testing.T) {
	src := `package main

const Max := 2 + 3

struct Buffer:
    data: [Max]int
    label: string = "buf"

func main():
    b := Buffer{data: [0, 0, 0, 0, 0]}
    print(b.label, len(b.data))
`
	prog := mustCheck(t, src)
	st := prog.Structs["Buffer"]
	if st == nil {
		t.Fatal("Buffer not declared")
	}
	arr, ok := st.Fields[0].Type.(*types.Array)
	if !ok || arr.Len != 5 {
		t.Fatalf("array type = %#v", st.Fields[0].Type)
	}
}

func TestMultipleReturns(t *testing.T) {
	src := `package main

func divide(a: int, b: int) -> (int, string):
    if b == 0:
        return 0, "division by zero"
    return a / b, ""

func main():
    q, err := divide(10, 2)
    print(q, err)
`
	prog := mustCheck(t, src)
	fn := prog.FuncByName["main.divide"]
	if fn == nil {
		t.Fatal("divide not declared")
	}
	if len(fn.Sig.Results) != 2 {
		t.Fatalf("results = %d", len(fn.Sig.Results))
	}
	names := prog.StmtSyms[prog.Main.Decl.Body.Stmts[0].(*ast.DefineStmt)]
	if len(names) != 2 || names[0].Type.Kind() != types.Int || names[1].Type.Kind() != types.String {
		t.Fatalf("define symbols = %+v", names)
	}
}

func TestStructsMethodsAndFields(t *testing.T) {
	src := `package main

struct Point:
    x: int
    y: int

func (p: Point) sum() -> int:
    return p.x + p.y

func main():
    p := Point{x: 1, y: 2}
    print(p.sum())
`
	prog := mustCheck(t, src)
	st := prog.Structs["Point"]
	if st == nil || len(st.Fields) != 2 {
		t.Fatalf("Point = %+v", st)
	}
	if st.Method("sum") == nil {
		t.Fatal("method not attached")
	}
	method := prog.FuncByName["main.Point.sum"]
	if method == nil || method.Slot < 0 {
		t.Fatalf("method info = %+v", method)
	}
}

func TestInterfacesAreStructural(t *testing.T) {
	src := `package main

interface Stringer:
    to_string() -> string

struct User:
    name: string

func (u: User) to_string() -> string:
    return u.name

func describe(s: Stringer) -> string:
    return s.to_string()

func main():
    u := User{name: "ada"}
    s: Stringer = u
    print(describe(s))
`
	prog := mustCheck(t, src)
	iface := prog.Interfaces["Stringer"]
	if iface == nil || len(iface.Methods) != 1 {
		t.Fatalf("interface = %+v", iface)
	}
	if !types.Implements(prog.Structs["User"], iface) {
		t.Fatal("User should implement Stringer")
	}
}

func TestInterfaceNotImplemented(t *testing.T) {
	src := `package main

interface Stringer:
    to_string() -> string

struct User:
    name: string

func main():
    u := User{name: "ada"}
    s: Stringer = u
    print(s)
`
	prog, msgs := checkSrc(t, src)
	_ = prog
	if len(msgs) == 0 {
		t.Fatal("expected an error for a missing method")
	}
	found := false
	for _, m := range msgs {
		if strings.Contains(m, "does not implement") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'does not implement', got %v", msgs)
	}
}

func TestAnyIsDynamic(t *testing.T) {
	src := `package main

func main():
    v: any = "hello"
    v = 100
    v = 2.5
    v = nil
    n := v + 1
    print(n, type(v))
`
	mustCheck(t, src)
}

func TestRangeAndLoops(t *testing.T) {
	src := `package main

func main():
    xs := [1, 2, 3]
    total := 0
    for i, x in xs:
        total += i * x
    for i in range(10):
        total += i
    for x in range(3):
        total += x
    m := {"a": 1}
    for k, v in m:
        print(k, v)
    print(total)
`
	mustCheck(t, src)
}

func TestFStringInterpolation(t *testing.T) {
	src := `package main

func main():
    name := "world"
    age := 4
    print(f"hi {name}, you are {age * 2}!")
`
	mustCheck(t, src)
}

func TestSemanticErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "undefined name",
			src:  "package main\n\nfunc main():\n    print(missing)\n",
			want: "undefined name",
		},
		{
			name: "unknown type",
			src:  "package main\n\nfunc main():\n    x: Widget = 1\n    print(x)\n",
			want: "unknown type",
		},
		{
			name: "type mismatch",
			src:  "package main\n\nfunc main():\n    x: int = \"text\"\n    print(x)\n",
			want: "cannot assign",
		},
		{
			name: "assign to constant",
			src:  "package main\n\nconst N := 1\n\nfunc main():\n    N = 2\n",
			want: "cannot assign",
		},
		{
			name: "wrong argument count",
			src:  "package main\n\nfunc add(a: int, b: int) -> int:\n    return a + b\n\nfunc main():\n    print(add(1))\n",
			want: "expects 2 argument",
		},
		{
			name: "wrong argument type",
			src:  "package main\n\nfunc add(a: int) -> int:\n    return a\n\nfunc main():\n    print(add(\"x\"))\n",
			want: "cannot assign",
		},
		{
			name: "break outside loop",
			src:  "package main\n\nfunc main():\n    break\n",
			want: "break is only allowed",
		},
		{
			name: "wrong return count",
			src:  "package main\n\nfunc f() -> int:\n    return 1, 2\n\nfunc main():\n    print(f())\n",
			want: "returning",
		},
		{
			name: "missing struct field",
			src:  "package main\n\nstruct P:\n    x: int\n    y: int\n\nfunc main():\n    p := P{x: 1}\n    print(p.x)\n",
			want: "missing value for field",
		},
		{
			name: "unknown package",
			src:  "package main\n\nimport \"nope\"\n\nfunc main():\n    print(1)\n",
			want: "unknown package",
		},
		{
			name: "nil to int",
			src:  "package main\n\nfunc main():\n    x: int = nil\n    print(x)\n",
			want: "nil cannot be assigned",
		},
		{
			name: "string plus int",
			src:  "package main\n\nfunc main():\n    x := \"a\" + 1\n    print(x)\n",
			want: "not defined for",
		},
		{
			name: "missing main",
			src:  "package main\n\nfunc other():\n    print(1)\n",
			want: "must declare",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, msgs := checkSrc(t, tt.src)
			if len(msgs) == 0 {
				t.Fatalf("expected an error containing %q", tt.want)
			}
			found := false
			for _, m := range msgs {
				if strings.Contains(m, tt.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no diagnostic contains %q; got:\n%s", tt.want, strings.Join(msgs, "\n"))
			}
		})
	}
}
