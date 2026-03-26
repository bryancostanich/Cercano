// Package document provides Go source file parsing and doc comment generation.
// It identifies undocumented exported symbols and supports inserting doc comments.
package document

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// SymbolKind identifies the type of Go symbol.
type SymbolKind string

const (
	// KindFunction represents a symbolic reference to a function definition within the agent's context.
	KindFunction SymbolKind = "function"
	// KindMethod represents a symbol kind identifier for methods in the codebase.
	KindMethod SymbolKind = "method"
	// KindType represents the symbol kind for a type declaration in the codebase.
	KindType SymbolKind = "type"
	// KindInterface symbol kind represents an interface declaration in Go code.
	KindInterface SymbolKind = "interface"
	// KindConst represents a constant declaration in the codebase.
	KindConst SymbolKind = "const"
	// KindVar represents a symbolic reference to a variable declaration in the codebase.
	KindVar SymbolKind = "var"
)

// Symbol represents an exported Go symbol that may need documentation.
type Symbol struct {
	Name      string
	Kind      SymbolKind
	Receiver  string // non-empty for methods
	StartLine int    // 1-based line number of the declaration
	EndLine   int    // 1-based line number of the last line
	Body      string // full source text of the declaration
	HasDoc    bool   // true if a doc comment already exists
}

// ParseGoFile parses a Go source file and returns all exported symbols
// with information about whether they already have doc comments.
func ParseGoFile(filePath string) ([]Symbol, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	return ParseGoSource(src, filePath)
}

// ParseGoSource parses Go source bytes and returns all exported symbols.
func ParseGoSource(src []byte, filename string) ([]Symbol, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse file: %w", err)
	}

	lines := strings.Split(string(src), "\n")
	var symbols []Symbol

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || !d.Name.IsExported() {
				continue
			}
			sym := Symbol{
				Name:      d.Name.Name,
				StartLine: fset.Position(d.Pos()).Line,
				EndLine:   fset.Position(d.End()).Line,
				HasDoc:    d.Doc != nil && len(d.Doc.List) > 0,
			}
			if d.Recv != nil && len(d.Recv.List) > 0 {
				sym.Kind = KindMethod
				sym.Receiver = receiverType(d.Recv.List[0].Type)
			} else {
				sym.Kind = KindFunction
			}
			sym.Body = extractLines(lines, sym.StartLine, sym.EndLine)
			symbols = append(symbols, sym)

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					// For grouped type blocks, use spec position; for single-spec, use GenDecl position
					startLine := fset.Position(d.Pos()).Line
					endLine := fset.Position(d.End()).Line
					if d.Lparen.IsValid() {
						startLine = fset.Position(s.Pos()).Line
						endLine = fset.Position(s.End()).Line
					}
					sym := Symbol{
						Name:      s.Name.Name,
						StartLine: startLine,
						EndLine:   endLine,
						HasDoc:    hasDocComment(d, s),
					}
					if _, ok := s.Type.(*ast.InterfaceType); ok {
						sym.Kind = KindInterface
					} else {
						sym.Kind = KindType
					}
					sym.Body = extractLines(lines, sym.StartLine, sym.EndLine)
					symbols = append(symbols, sym)

				case *ast.ValueSpec:
					for _, name := range s.Names {
						if !name.IsExported() {
							continue
						}
						kind := KindVar
						if d.Tok == token.CONST {
							kind = KindConst
						}
						// For grouped declarations, use spec position; for single-spec, use GenDecl position
						startLine := fset.Position(d.Pos()).Line
						endLine := fset.Position(d.End()).Line
						if d.Lparen.IsValid() {
							startLine = fset.Position(s.Pos()).Line
							endLine = fset.Position(s.End()).Line
						}
						sym := Symbol{
							Name:      name.Name,
							Kind:      kind,
							StartLine: startLine,
							EndLine:   endLine,
							HasDoc:    hasDocComment(d, s),
							Body:      extractLines(lines, startLine, endLine),
						}
						symbols = append(symbols, sym)
					}
				}
			}
		}
	}

	return symbols, nil
}

// UndocumentedSymbols filters to only symbols that lack doc comments.
func UndocumentedSymbols(symbols []Symbol) []Symbol {
	var result []Symbol
	for _, s := range symbols {
		if !s.HasDoc {
			result = append(result, s)
		}
	}
	return result
}

// hasDocComment checks if a GenDecl or its spec has a doc comment.
func hasDocComment(d *ast.GenDecl, spec ast.Spec) bool {
	// Single-spec GenDecl: doc is on the GenDecl itself
	if d.Doc != nil && len(d.Doc.List) > 0 {
		return true
	}
	// Multi-spec GenDecl: doc is on the individual spec
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc != nil && len(s.Doc.List) > 0
	case *ast.ValueSpec:
		return s.Doc != nil && len(s.Doc.List) > 0
	}
	return false
}

// receiverType extracts the type name from a method receiver expression.
func receiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// extractLines returns lines[start-1:end] joined by newlines (1-based).
func extractLines(lines []string, start, end int) string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}
