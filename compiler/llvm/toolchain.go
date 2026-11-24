package llvm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Toolchain locates the LLVM toolchain and the WayLang runtime sources, and
// drives clang to turn generated IR into a native executable.
//
// WayLang does not link against LLVM's C++ libraries; it emits portable textual
// LLVM IR and invokes the clang driver, which lets the compiler work with any
// maintained LLVM 15+ installation (including llvm-mingw on Windows).
type Toolchain struct {
	// Clang is the path to the clang executable.
	Clang string
	// Root is the WayLang project root that holds runtime/.
	Root string
}

// RuntimeSources lists the C sources that make up the runtime, in build order.
var RuntimeSources = []string{
	"runtime/memory/memory.c",
	"runtime/gc/gc.c",
	"runtime/dynamic/dynamic.c",
	"runtime/builtins/strings.c",
	"runtime/builtins/collections.c",
	"runtime/builtins/app.c",
	"runtime/scheduler/scheduler.c",
}

// FindToolchain locates clang and the project root.
//
// Search order for clang: WAY_LLVM, PATH, then LLVM installations under the
// project (`.toolchain/*/bin/clang`) and the platform's default LLVM location.
func FindToolchain() (*Toolchain, error) {
	root, err := FindRoot()
	if err != nil {
		return nil, err
	}
	clang, err := findClang(root)
	if err != nil {
		return nil, err
	}
	return &Toolchain{Clang: clang, Root: root}, nil
}

// FindRoot locates the WayLang root directory containing runtime/runtime.h.
// It checks WAY_ROOT, the working directory, and the executable's directory,
// walking up a few levels from each.
func FindRoot() (string, error) {
	if v := os.Getenv("WAY_ROOT"); v != "" {
		if isRoot(v) {
			return filepath.Abs(v)
		}
	}
	var starts []string
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	for _, start := range starts {
		dir := start
		for i := 0; i < 8; i++ {
			if isRoot(dir) {
				return dir, nil
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("cannot find the WayLang root (runtime/runtime.h); set WAY_ROOT to the project directory")
}

func isRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "runtime", "runtime.h"))
	return err == nil
}

func findClang(root string) (string, error) {
	exe := "clang"
	if runtime.GOOS == "windows" {
		exe = "clang.exe"
	}
	if v := os.Getenv("WAY_LLVM"); v != "" {
		if info, err := os.Stat(v); err == nil {
			if info.IsDir() {
				candidate := filepath.Join(v, "bin", exe)
				if _, err := os.Stat(candidate); err == nil {
					return candidate, nil
				}
				candidate = filepath.Join(v, exe)
				if _, err := os.Stat(candidate); err == nil {
					return candidate, nil
				}
			} else {
				return v, nil
			}
		}
		return "", fmt.Errorf("WAY_LLVM=%s does not point to clang or an LLVM directory", v)
	}
	if p, err := exec.LookPath("clang"); err == nil {
		return p, nil
	}
	patterns := []string{
		filepath.Join(root, ".toolchain", "*", "bin", exe),
		filepath.Join(root, ".toolchain", "*", "bin", "clang"),
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		sort.Strings(matches)
		if len(matches) > 0 {
			return matches[len(matches)-1], nil
		}
	}
	var defaults []string
	if runtime.GOOS == "windows" {
		defaults = []string{
			`C:\Program Files\LLVM\bin\clang.exe`,
			`C:\Program Files (x86)\LLVM\bin\clang.exe`,
		}
	} else {
		defaults = []string{"/usr/bin/clang", "/usr/local/bin/clang", "/opt/homebrew/opt/llvm/bin/clang"}
	}
	for _, candidate := range defaults {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cannot find clang; install LLVM 15+ or set WAY_LLVM to the clang executable")
}

// RuntimeObjects compiles the runtime sources into object files under
// root/build/rt, reusing objects that are newer than their sources.
func (t *Toolchain) RuntimeObjects() ([]string, error) {
	outDir := filepath.Join(t.Root, "build", "rt")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	objects := make([]string, 0, len(RuntimeSources))
	for _, src := range RuntimeSources {
		srcPath := filepath.Join(t.Root, filepath.FromSlash(src))
		info, err := os.Stat(srcPath)
		if err != nil {
			return nil, fmt.Errorf("runtime source %s: %w", src, err)
		}
		obj := filepath.Join(outDir, strings.ReplaceAll(src, "/", "_")+".o")
		if objInfo, err := os.Stat(obj); err == nil && objInfo.ModTime().After(info.ModTime()) {
			objects = append(objects, obj)
			continue
		}
		args := []string{"-std=c11", "-O2", "-c", srcPath, "-o", obj}
		if out, err := run(t.Clang, args...); err != nil {
			return nil, fmt.Errorf("compiling %s:\n%s", src, out)
		}
		objects = append(objects, obj)
	}
	return objects, nil
}

// CompileIR compiles generated LLVM IR text and links it with the runtime into
// an executable at outPath.
func (t *Toolchain) CompileIR(irText, outPath string) (string, error) {
	objects, err := t.RuntimeObjects()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "waylang-build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	irPath := filepath.Join(dir, "module.ll")
	if err := os.WriteFile(irPath, []byte(irText), 0o644); err != nil {
		return "", err
	}
	args := []string{"-O2", irPath, "-o", outPath}
	args = append(args, objects...)
	if runtime.GOOS != "windows" {
		args = append(args, "-lm")
	}
	if out, err := run(t.Clang, args...); err != nil {
		return irText, fmt.Errorf("linking %s:\n%s", outPath, out)
	}
	return irText, nil
}

// Version reports the clang version string.
func (t *Toolchain) Version() string {
	out, err := run(t.Clang, "--version")
	if err != nil {
		return "unknown"
	}
	line := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	return strings.TrimSpace(line)
}

func run(bin string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, err
	}
	return text, nil
}
