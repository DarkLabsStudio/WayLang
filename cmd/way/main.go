// Command way is the WayLang toolchain: it checks, compiles, runs and provides
// project scaffolding for .way source files.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"waylang/compiler/diag"
	"waylang/compiler/ir"
	"waylang/compiler/lexer"
	"waylang/compiler/linker"
	"waylang/compiler/llvm"
	"waylang/compiler/parser"
	"waylang/compiler/semantic"
)

const version = "0.1.0"

const usage = `way - the WayLang toolchain

usage: way <command> [arguments]

commands:
  version        print toolchain and LLVM versions
  run <file>     compile and run a program
  build <file>   compile a program to a native executable
  check <file>   lex, parse and type-check a program
  fmt <file>     normalize whitespace (trailing spaces, line endings, EOF newline)
  test [dir]     compile and run every *_test.way file
  init [dir]     create a new WayLang project
  mod tidy       validate the program's imports
  get            report that external modules are not supported yet

build flags:
  -o <path>          output executable path
  --emit-ir[=<path>] also write the generated LLVM IR
`

func main() {
	code := 0
	if err := dispatch(os.Args[1:]); err != nil {
		var ee *exitError
		if errors.As(err, &ee) {
			code = ee.code
		} else {
			fmt.Fprintln(os.Stderr, "way: "+err.Error())
			code = 1
		}
	}
	os.Exit(code)
}

type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit status %d", e.code) }

func dispatch(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version", "-v":
		return cmdVersion()
	case "run":
		return cmdRun(rest)
	case "build":
		return cmdBuild(rest)
	case "check":
		return cmdCheck(rest)
	case "fmt":
		return cmdFmt(rest)
	case "test":
		return cmdTest(rest)
	case "init":
		return cmdInit(rest)
	case "mod":
		if len(rest) > 0 && rest[0] == "tidy" {
			return cmdModTidy(rest[1:])
		}
		return fmt.Errorf("usage: way mod tidy [file]")
	case "get":
		return fmt.Errorf("way get: external dependencies are not supported yet; WayLang currently ships only its standard library")
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q; run `way help`", cmd)
	}
}

// --- shared compilation ---------------------------------------------------

// printDiags renders diagnostics with the offending source line and a caret.
// It reports whether any diagnostic was an error.
func printDiags(src string, diags []diag.Diagnostic) bool {
	hasErrors := false
	sorted := append([]diag.Diagnostic(nil), diags...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Pos.Line != sorted[j].Pos.Line {
			return sorted[i].Pos.Line < sorted[j].Pos.Line
		}
		return sorted[i].Pos.Column < sorted[j].Pos.Column
	})
	for _, d := range sorted {
		fmt.Fprint(os.Stderr, diag.Format(d, src))
		if d.Severity == diag.Error {
			hasErrors = true
		}
	}
	return hasErrors
}

// loadProgram lexes, parses and type-checks a single source file.
func loadProgram(path string) (*semantic.Program, string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	text := string(src)
	_, lexDiags := lexer.Lex(path, src)
	file, parseDiags := parser.ParseFile(path, src)
	all := append(append([]diag.Diagnostic{}, lexDiags...), parseDiags...)
	if len(all) > 0 && printDiags(text, all) {
		return nil, text, fmt.Errorf("%s: compilation failed", path)
	}
	prog, semDiags := semantic.Check(file, path, src)
	if len(semDiags) > 0 && printDiags(text, semDiags) {
		return nil, text, fmt.Errorf("%s: compilation failed", path)
	}
	if prog.Main == nil {
		return nil, text, fmt.Errorf("%s: package %q does not declare func main()", path, prog.Package)
	}
	return prog, text, nil
}

// emitIR compiles a file all the way to textual LLVM IR.
func emitIR(path string) (string, error) {
	prog, _, err := loadProgram(path)
	if err != nil {
		return "", err
	}
	mod, err := ir.Lower(prog)
	if err != nil {
		return "", err
	}
	text, err := llvm.Emit(mod, prog)
	if err != nil {
		return "", err
	}
	return text, nil
}

// resolveEntry finds the source file to compile: an explicit path, or main.way,
// or src/main.way in the working directory.
func resolveEntry(args []string) (string, error) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], nil
	}
	for _, candidate := range []string{"main.way", filepath.Join("src", "main.way")} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no input file: pass a .way file or create main.way")
}

func exeName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".way")
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(strings.ToLower(base), ".exe") {
			base += ".exe"
		}
	}
	return base
}

// --- commands -------------------------------------------------------------

func cmdVersion() error {
	fmt.Printf("way %s\n", version)
	fmt.Printf("platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	tc, err := llvm.FindToolchain()
	if err != nil {
		fmt.Printf("llvm: not found (%v)\n", err)
		return nil
	}
	fmt.Printf("llvm: %s\n", tc.Version())
	fmt.Printf("clang: %s\n", tc.Clang)
	fmt.Printf("root: %s\n", tc.Root)
	return nil
}

func cmdCheck(args []string) error {
	path, err := resolveEntry(args)
	if err != nil {
		return err
	}
	if _, _, err := loadProgram(path); err != nil {
		return err
	}
	fmt.Printf("ok: %s\n", path)
	return nil
}

func cmdBuild(args []string) error {
	out := ""
	emitPath := ""
	wantIR := false
	var files []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a path", a)
			}
			i++
			out = args[i]
		case strings.HasPrefix(a, "-o="):
			out = strings.TrimPrefix(a, "-o=")
		case a == "--emit-ir":
			wantIR = true
			emitPath = ""
		case strings.HasPrefix(a, "--emit-ir="):
			wantIR = true
			emitPath = strings.TrimPrefix(a, "--emit-ir=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown build flag %q", a)
		default:
			files = append(files, a)
		}
	}
	path, err := resolveEntry(files)
	if err != nil {
		return err
	}
	text, err := emitIR(path)
	if err != nil {
		return err
	}
	if out == "" {
		out = exeName(path)
	}
	if wantIR && emitPath == "" {
		emitPath = strings.TrimSuffix(out, filepath.Ext(out)) + ".ll"
	}
	if err := linker.Link(text, linker.Options{OutPath: out, EmitIRPath: emitPath}); err != nil {
		return err
	}
	fmt.Printf("built %s\n", out)
	if emitPath != "" {
		fmt.Printf("wrote %s\n", emitPath)
	}
	return nil
}

func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: way run <file> [args...]")
	}
	path := args[0]
	progArgs := args[1:]
	if i := indexOf(progArgs, "--"); i >= 0 {
		progArgs = progArgs[i+1:]
	}
	text, err := emitIR(path)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "waylang-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, exeName(path))
	if err := linker.Link(text, linker.Options{OutPath: exe}); err != nil {
		return err
	}
	cmd := exec.Command(exe, progArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return &exitError{code: ee.ExitCode()}
		}
		return err
	}
	return nil
}

func indexOf(list []string, target string) int {
	for i, v := range list {
		if v == target {
			return i
		}
	}
	return -1
}

func cmdFmt(args []string) error {
	var files []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		files = append(files, a)
	}
	if len(files) == 0 {
		path, err := resolveEntry(nil)
		if err != nil {
			return err
		}
		files = []string{path}
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Never rewrite a file that does not parse: a broken file should be
		// fixed by hand, not silently reshaped.
		_, parseDiags := parser.ParseFile(path, src)
		if printDiags(string(src), parseDiags) {
			return fmt.Errorf("%s: not formatted because it has syntax errors", path)
		}
		formatted := formatSource(src)
		if string(formatted) == string(src) {
			fmt.Printf("unchanged %s\n", path)
			continue
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return err
		}
		fmt.Printf("formatted %s\n", path)
	}
	return nil
}

// formatSource normalizes line endings, strips trailing whitespace and ensures
// exactly one final newline. Indentation is preserved: the language's
// indentation rules are semantic, so rewriting them would change meaning.
func formatSource(src []byte) []byte {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t")
	}
	out := strings.Join(lines, "\n")
	out = strings.TrimRight(out, "\n")
	return []byte(out + "\n")
}

func cmdTest(args []string) error {
	roots := args
	if len(roots) == 0 {
		if info, err := os.Stat("tests"); err == nil && info.IsDir() {
			roots = []string{"tests"}
		} else {
			roots = []string{"."}
		}
	}
	var files []string
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, root)
			continue
		}
		err = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !fi.IsDir() && strings.HasSuffix(path, "_test.way") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no *_test.way files found")
	}
	sort.Strings(files)
	failed := 0
	for _, path := range files {
		if err := runTestFile(path); err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed++
		} else {
			fmt.Printf("PASS %s\n", path)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d test programs failed", failed, len(files))
	}
	fmt.Printf("ok %d test program(s)\n", len(files))
	return nil
}

func runTestFile(path string) error {
	text, err := emitIR(path)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "waylang-test-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, exeName(path))
	if err := linker.Link(text, linker.Options{OutPath: exe}); err != nil {
		return err
	}
	cmd := exec.Command(exe)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdInit(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := filepath.Base(dir)
	if abs, err := filepath.Abs(dir); err == nil {
		name = filepath.Base(abs)
	}
	modPath := filepath.Join(dir, "way.mod")
	mainPath := filepath.Join(dir, "main.way")
	if _, err := os.Stat(modPath); err == nil {
		return fmt.Errorf("%s already exists", modPath)
	}
	if _, err := os.Stat(mainPath); err == nil {
		return fmt.Errorf("%s already exists", mainPath)
	}
	if err := os.WriteFile(modPath, []byte("module "+name+"\n"), 0o644); err != nil {
		return err
	}
	sample := "package main\n\nfunc main():\n    print(\"Hello, WayLang!\")\n"
	if err := os.WriteFile(mainPath, []byte(sample), 0o644); err != nil {
		return err
	}
	fmt.Printf("created %s and %s\n", modPath, mainPath)
	return nil
}

func cmdModTidy(args []string) error {
	path, err := resolveEntry(args)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, parseDiags := parser.ParseFile(path, src)
	if printDiags(string(src), parseDiags) {
		return fmt.Errorf("%s: compilation failed", path)
	}
	count := 0
	for _, imp := range file.Imports {
		count++
		if semantic.LookupStdPackage(imp.Path) == nil {
			return fmt.Errorf("import %q is not a standard package; external modules are not supported yet", imp.Path)
		}
	}
	fmt.Printf("%s: %d import(s), all standard\n", path, count)
	return nil
}
