// Package diag provides compiler diagnostics: structured errors that carry a
// source position, a message, and the ability to render a source snippet with
// a caret.
package diag

import (
	"fmt"
	"sort"
	"strings"

	"waylang/compiler/token"
)

// Severity levels.
type Severity int

const (
	Error Severity = iota
	Warning
	Note
)

func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	case Note:
		return "note"
	}
	return "error"
}

// Diagnostic is a single compiler message.
type Diagnostic struct {
	Severity Severity
	Pos      token.Position
	Msg      string
	// Hint is an optional suggested fix shown after the message.
	Hint string
}

// Error implements the error interface.
func (d Diagnostic) Error() string {
	if d.Pos.IsValid() {
		return fmt.Sprintf("%s: %s: %s", d.Pos.String(), d.Severity, d.Msg)
	}
	return fmt.Sprintf("%s: %s", d.Severity, d.Msg)
}

// List accumulates diagnostics and implements error.
type List struct {
	Diags []Diagnostic
}

// Add appends a diagnostic.
func (l *List) Add(sev Severity, pos token.Position, format string, args ...any) {
	l.Diags = append(l.Diags, Diagnostic{Severity: sev, Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

// AddHint appends a diagnostic with a suggested fix.
func (l *List) AddHint(sev Severity, pos token.Position, hint, format string, args ...any) {
	l.Diags = append(l.Diags, Diagnostic{Severity: sev, Pos: pos, Msg: fmt.Sprintf(format, args...), Hint: hint})
}

// Errorf records an error.
func (l *List) Errorf(pos token.Position, format string, args ...any) {
	l.Add(Error, pos, format, args...)
}

// ErrorfHint records an error with a hint.
func (l *List) ErrorfHint(pos token.Position, hint, format string, args ...any) {
	l.AddHint(Error, pos, hint, format, args...)
}

// Warnf records a warning.
func (l *List) Warnf(pos token.Position, format string, args ...any) {
	l.Add(Warning, pos, format, args...)
}

// HasErrors reports whether any diagnostic has error severity.
func (l *List) HasErrors() bool {
	for _, d := range l.Diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}

// Len returns the number of diagnostics.
func (l *List) Len() int { return len(l.Diags) }

// Error implements the error interface.
func (l *List) Error() string {
	if len(l.Diags) == 0 {
		return "no diagnostics"
	}
	parts := make([]string, 0, len(l.Diags))
	for _, d := range l.Diags {
		parts = append(parts, d.Error())
	}
	return strings.Join(parts, "\n")
}

// Sorted returns the diagnostics ordered by file, line, column.
func (l *List) Sorted() []Diagnostic {
	out := append([]Diagnostic(nil), l.Diags...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return out
}

// Format renders a diagnostic with the offending source line and a caret.
// src is the full contents of the file the position refers to.
func Format(d Diagnostic, src string) string {
	var b strings.Builder
	if d.Pos.IsValid() {
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", d.Pos.File, d.Pos.Line, d.Pos.Column, d.Severity, d.Msg)
	} else {
		fmt.Fprintf(&b, "%s: %s\n", d.Severity, d.Msg)
	}
	if line := LineText(src, d.Pos.Line); line != "" {
		gutter := fmt.Sprintf("%4d | ", d.Pos.Line)
		b.WriteString(gutter)
		b.WriteString(line)
		b.WriteString("\n")
		b.WriteString(strings.Repeat(" ", len(gutter)))
		col := d.Pos.Column - 1
		if col < 0 {
			col = 0
		}
		// Preserve tabs so the caret lines up with the source.
		pad := 0
		for i, r := range line {
			if i >= col {
				break
			}
			if r == '\t' {
				b.WriteString("\t")
			} else {
				b.WriteString(" ")
			}
			pad++
		}
		b.WriteString("^")
		b.WriteString("\n")
	}
	if d.Hint != "" {
		fmt.Fprintf(&b, "hint: %s\n", d.Hint)
	}
	return b.String()
}

// LineText returns the 1-based line of src without its terminator.
func LineText(src string, line int) string {
	if line <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			if cur == line {
				return strings.TrimRight(src[start:i], "\r")
			}
			cur++
			start = i + 1
		}
	}
	if cur == line {
		return strings.TrimRight(src[start:], "\r")
	}
	return ""
}

// SortNone is a marker used by tests that need stable ordering.
var SortNone = false
