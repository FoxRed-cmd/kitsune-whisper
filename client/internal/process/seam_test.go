package process_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoChildStartsOutsideThisPackage is the regression guard for the Windows
// console-window flash: a plain exec.Command in a GUI-subsystem Client gives the
// child a fresh, visible console. The platform fix is one flag, so the failure
// mode is a new call site that never learned about it, not a wrong flag.
func TestNoChildStartsOutsideThisPackage(t *testing.T) {
	root := filepath.Join("..", "..")
	ownDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolve the package directory: %v", err)
	}
	var offenders []string
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "testdata", "vendor", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if filepath.Dir(absolute) == ownDir {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			if selector.Sel.Name != "Command" && selector.Sel.Name != "CommandContext" {
				return true
			}
			position := fset.Position(call.Pos())
			offenders = append(offenders, position.String())
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the Client sources: %v", err)
	}
	for _, offender := range offenders {
		t.Errorf("%s starts a child with exec.Command; use internal/process.Command so Windows does not open a console window", offender)
	}
}
