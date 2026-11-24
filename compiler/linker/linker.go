// Package linker drives the final native link: it hands generated LLVM IR to
// the toolchain together with the WayLang runtime objects.
package linker

import (
	"fmt"
	"os"
	"path/filepath"

	"waylang/compiler/llvm"
)

// Options controls a link.
type Options struct {
	// OutPath is the executable to produce.
	OutPath string
	// EmitIRPath, when non-empty, also writes the generated LLVM IR there.
	EmitIRPath string
}

// Link compiles and links irText into opts.OutPath.
func Link(irText string, opts Options) error {
	if opts.OutPath == "" {
		return fmt.Errorf("no output path")
	}
	if dir := filepath.Dir(opts.OutPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if opts.EmitIRPath != "" {
		if err := os.WriteFile(opts.EmitIRPath, []byte(irText), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", opts.EmitIRPath, err)
		}
	}
	tc, err := llvm.FindToolchain()
	if err != nil {
		return err
	}
	if _, err := tc.CompileIR(irText, opts.OutPath); err != nil {
		return err
	}
	return nil
}
