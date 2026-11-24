# WayLang

WayLang is a statically typed, garbage-collected general-purpose programming language that compiles
ahead of time to native executables. It pairs Python-style indentation with Go-like keywords, supports
gradual typing through `any` and structural interfaces, and produces machine code through LLVM.

The compiler is written in Go. It checks and lowers `.way` source to textual LLVM IR, then drives
`clang` to link the IR with a small C runtime that provides allocation, a mark-and-sweep collector,
strings, maps, dynamic values and the standard library builtins.

```way
package main

import "strings"

struct Point:
    x: int
    y: int

interface Shape:
    area() -> int

func (p: Point) area() -> int:
    return p.x * p.y

func main():
    p := Point{x: 3, y: 4}
    shape: Shape = p
    println(f"area={shape.area()}")
```

```console
$ way run examples/shapes.way
circle area=12.5664
rect area=12
```

## Status

WayLang is early but real: programs compile to native executables and run, with no interpreter and no
Python underneath. The pipeline below is implemented end to end and covered by tests.

| Area | State |
| --- | --- |
| Indentation-aware lexer, diagnostics with source context | Working |
| Parser + AST, error recovery | Working |
| Name resolution, type checking, constant folding | Working |
| LLVM IR lowering and native code generation | Working |
| Runtime: allocation, mark-sweep GC, strings, maps, dynamic values | Working |
| CLI (`run`, `build`, `check`, `fmt`, `test`, `init`, `mod tidy`, `get`) | Working |
| Standard library (`strings`, `math`, `os`, `time`) | Working |

Known limitations are listed at the end.

## Requirements

- Go 1.24 or newer.
- LLVM 15 or newer, specifically a `clang` executable. On Windows the bundled
  [llvm-mingw](https://github.com/mstorsjo/llvm-mingw) toolchain works well.

`way` finds clang in this order:

1. `WAY_LLVM` (a clang executable or an LLVM directory),
2. `clang` on `PATH`,
3. `.toolchain/*/bin/clang` inside the project,
4. the platform default LLVM location (for example `C:\Program Files\LLVM\bin\clang.exe`).

Runtime C sources are found under the project root; set `WAY_ROOT` if you run the binary from
somewhere the root cannot be discovered. Runtime objects are cached in `build/rt` and rebuilt when
their sources change.

## Quick start

```console
$ go build -o way ./cmd/way
$ ./way version
way 0.1.0
platform: windows/amd64
llvm: clang version 23.1.6 (...)

$ ./way run examples/hello.way
Hello, WayLang!

$ ./way build examples/fib.way
built fib.exe
$ ./fib.exe
0 1 1 2 3 5 8 13 21 34
```

Create a project with `way init`, which writes `way.mod` and a `main.way`:

```console
$ ./way init hello
created hello/way.mod and hello/main.way
$ ./way run hello/main.way
Hello, WayLang!
```

## Command line

| Command | Description |
| --- | --- |
| `way version` | Print the CLI, platform and LLVM versions. |
| `way run <file> [args...]` | Compile to a temporary executable and run it, forwarding `args` and the exit code. |
| `way build <file> [-o path] [--emit-ir[=path]]` | Produce a native executable; optionally also write the generated LLVM IR. |
| `way check <file>` | Lex, parse and type-check without generating code. |
| `way fmt <file>...` | Normalize line endings, trailing whitespace and the final newline. Refuses files that do not parse. |
| `way test [dir]` | Compile and run every `*_test.way` program; a test passes when it exits 0. |
| `way init [dir]` | Scaffold `way.mod` and `main.way`. |
| `way mod tidy` | Validate that every import names a standard package. |
| `way get` | Reports that external modules are not supported yet. |

With no file argument, `run`, `build`, `check` and `fmt` look for `main.way`, then `src/main.way`.

## The language

### Packages, imports and indentation

Every file starts with `package main`. Standard packages are imported by path and used through their
base name, or through an alias:

```way
package main

import "strings"
import "math"
```

Blocks are introduced by `:` and indented. Indentation is significant; mixing tabs and spaces at the
same level is a compile error. Blank lines and `#` comments do not affect indentation.

### Values and types

Primitive types are `int` (64-bit), `float` (64-bit), `bool`, `string`, `byte`, `rune`, plus `void`,
`error` and the dynamic type `any`. Composite types are `[N]T` (array), `[]T` (slice), `map[K]V`,
structs, interfaces and `func(T, ...) -> R`.

```way
count := 3             # inferred int
ratio: float = 2.5     # explicit type
name := "WayLang"
flags := [true, false]
grid: [2]int = [4, 9]
```

There are no implicit narrowing conversions; an integer literal adapts to a float context, but a
typed int does not convert to float implicitly. Use `float(x)` or `int(x)` explicitly.

### Functions

Parameters and results are typed; results follow `->`. Multiple results are written as
`-> (int, error)`. Trailing parameters may have defaults.

```way
func add(a: int, b: int) -> int:
    return a + b

func divmod(a: int, b: int) -> (int, int):
    return a / b, a % b

func greet(name: string, greeting: string = "Hello") -> string:
    return f"{greeting}, {name}!"

func main():
    q, r := divmod(17, 5)
    println(f"{q} remainder {r}")
    println(greet("WayLang"))
```

Anonymous functions are supported but must not capture local variables:

```way
double := func(x: int) -> int:
    return x * 2
println(double(21))
```

### Structs and methods

```way
struct Point:
    x: int
    y: int

func (p: Point) sum() -> int:
    return p.x + p.y

func main():
    p := Point{x: 1, y: 2}
    println(p.sum())
```

Struct literals name every field; nested structs work. Package-level variables are declared with
`name: Type = value` and are registered as collector roots.

### Interfaces

Interfaces are structural: a type satisfies an interface when it declares the required methods, and
dispatch goes through a vtable. `any` is the dynamic escape hatch: values box themselves with runtime
type information, arithmetic and comparisons dispatch on the boxed type, and `type(x)` reports the
dynamic type.

```way
interface Shape:
    area() -> float

struct Rect:
    w: float
    h: float

func (r: Rect) area() -> float:
    return r.w * r.h

func describe(s: Shape):
    println(f"area={s.area()}")

func main():
    describe(Rect{w: 3.0, h: 4.0})
```

### Statements

```way
total := 0
for i := 0; i < 10; i++:
    if i % 2 == 0:
        continue
    total += i

for n in range(3):
    print(n)

xs := [10, 20, 30]
for i, v in xs:
    println(f"{i}:{v}")

stock := {"apple": 10, "pear": 4}
for name, qty in stock:
    println(f"{name}: {qty}")

while total > 10:
    total -= 1

switch total:
    case 0:
        println("zero")
    case 1, 2:
        println("small")
    default:
        println("large")
```

Loops support `break` and `continue`. `range(n)` yields `0..n-1`; iterating a string yields runes,
a slice or array yields index and value, a map yields key and value in unspecified order.

### Strings and f-strings

Strings are immutable UTF-8 byte sequences. `len` is the byte length; `xs[i]` on a string yields a
byte. F-strings interpolate any printable value:

```way
name := "world"
println(f"Hello, {name}! {1 + 2}")
```

`strings` provides `upper`, `lower`, `trim`, `contains`, `has_prefix`, `has_suffix`, `index`,
`replace` and `repeat`.

### Errors and `nil`

Functions can return `error` alongside a value. `error("message")` creates an error and `nil` is the
absent value:

```way
func divide(a: int, b: int) -> (int, error):
    if b == 0:
        return 0, error("division by zero")
    return a / b, nil

func main():
    value, err := divide(9, 3)
    if err != nil:
        println("failed")
    else:
        println(f"value={value}")
```

### `defer`

`defer` runs a call when the enclosing function returns, in reverse order. It accepts direct function
and method calls; arguments are evaluated at the `defer` site.

```way
func report():
    println("done")

func main():
    defer report()
    println("working")
```

### Builtins and standard library

Builtins: `print`, `println`, `len`, `range`, `type`, `error`.

| Package | Functions |
| --- | --- |
| `strings` | `upper`, `lower`, `trim`, `contains`, `has_prefix`, `has_suffix`, `index`, `replace`, `repeat` |
| `math` | `sqrt`, `pow`, `floor`, `ceil`, `round`, `abs`, `min`, `max` |
| `os` | `exit`, `getenv`, `args` |
| `time` | `now` (milliseconds since the epoch), `sleep` |

## Memory management

Allocations are managed by a precise-safepoint, conservative-tracing mark-and-sweep collector. The
compiler emits safepoints at statement boundaries, where every live reference is reachable from the
stack, a registered global, or a reachable heap object. Roots are the native stack, a saved register
set and registered globals. Objects never move, so cycles are collected without updating pointers.

Every object has a 32-byte header carrying a magic value, mark bit, kind, payload size and type-info
pointer. Strings are `{ length, bytes[] }` payloads; slices are `{ data, len, cap }`; `any` values are
`{ data, typeinfo }` boxes.

Set `WAY_GC_THRESHOLD_BYTES` to force frequent collections, which is how the GC tests exercise the
collector:

```console
$ WAY_GC_THRESHOLD_BYTES=4096 ./way run tests/gc_test.way
```

## Project layout

```
cmd/way/            the way CLI
compiler/token/     token kinds and positions
compiler/diag/      diagnostics with source snippets and carets
compiler/lexer/     indentation-aware scanner (INDENT/DEDENT)
compiler/ast/       syntax tree
compiler/parser/    recursive-descent parser with error recovery
compiler/types/     type representation and assignability
compiler/semantic/  name resolution, type checking, constant folding
compiler/ir/        typed IR and lowering from the checked AST
compiler/llvm/      LLVM IR emission and toolchain discovery
compiler/linker/    final link with the runtime
runtime/memory/     allocation and heap bookkeeping
runtime/gc/         mark-and-sweep collector
runtime/dynamic/    any boxing, arithmetic, comparison, errors
runtime/builtins/   strings, maps, program entry point, os/time/math
runtime/scheduler/  explicit placeholder (no concurrency yet)
examples/           runnable programs
tests/              end-to-end test programs (*_test.way)
```

## How compilation works

1. The lexer produces tokens and INDENT/DEDENT markers; indentation errors carry positions.
2. The parser builds an AST and recovers from errors so one run reports many diagnostics.
3. Semantic analysis resolves names, checks types, folds constants and records call/method decisions.
4. Lowering produces an SSA-based IR with explicit control flow, boxing and GC safepoints.
5. The backend emits LLVM IR: declared structs, function definitions, string constants, type
   information and vtables, then links with the runtime through clang.

Each stage is isolated: only `compiler/llvm` knows about LLVM, and it talks to the toolchain rather
than linking LLVM libraries, so any maintained `clang` works.

## Testing

```console
$ go test ./...        # lexer, parser and semantic suites
$ ./way test tests     # compile and run the end-to-end programs
```

`tests/features_test.way` exercises structs, interfaces, maps, slices, f-strings, multiple returns,
default parameters, `defer`, anonymous functions, errors and `any`. `tests/gc_test.way` stresses the
collector with a low threshold.

## Limitations

These are not implemented yet, and unsupported constructs are reported as errors rather than silently
ignored:

- Closures that capture local variables (anonymous functions must be self-contained).
- Method values (`f := p.method`) and `defer` of builtins or arbitrary expressions.
- Goroutines, channels and the scheduler (`runtime/scheduler` is a placeholder).
- External modules and dependency resolution; `way get` reports this.
- Generic types and type parameters.
- Type assertions to unbox an `any` back into a concrete type; `any` values can be printed,
  compared and used in arithmetic, but not narrowed. Map keys beyond `string`, `int` and `bool`, and
  map element deletion.
- `range` accepts a single bound only.
- `way fmt` normalizes whitespace; it does not reformat the syntax tree.
- The test runner treats a program's exit status as the verdict; there is no assertion library yet.

## License

No license file has been added yet.
