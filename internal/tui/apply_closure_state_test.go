package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// pageActionRunners hand their work closure's result to a completion callback
// that runs on the UI goroutine. Their work closures are the one place a
// func(ctx context.Context) error may assign a captured variable: the variable
// is declared beside the call for exactly that handoff, and nothing reads it
// until the completion does.
var pageActionRunners = map[string]bool{
	"runPageAction":     true,
	"runPageActionOnce": true,
}

// TestApplyClosuresDoNotWritePageState enforces docs/ui-rules.md's "An apply
// closure never writes page state". An apply closure runs on the pipeline's
// goroutine while the page's own callbacks read the same state on the UI
// goroutine, and it runs under Script Changes as well, where nothing reached
// the server. AG Listener Properties cleared its pending addresses at the end
// of its apply: a data race, and after Script Changes a page that showed
// addresses "To be added" but was no longer dirty, so the next Apply sent
// nothing.
//
// Every func(ctx context.Context) error literal in the package is checked,
// not only the ones a page returns as its propApply — telling those apart
// statically needs the whole load function traced, and the only other such
// closures are page-action work closures, exempted by call site above.
func TestApplyClosuresDoNotWritePageState(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing the package: %v", err)
	}
	checked := 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		// Object resolution on (the default): each identifier's Obj says
		// where it was declared, which is what tells a captured variable
		// from the closure's own.
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		exempt := map[*ast.FuncLit]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && pageActionRunners[sel.Sel.Name] {
				for _, arg := range call.Args {
					if lit, ok := arg.(*ast.FuncLit); ok {
						exempt[lit] = true
					}
				}
			}
			return true
		})

		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.FuncLit)
			if !ok || !isContextErrorFunc(lit.Type) || exempt[lit] {
				return true
			}
			checked++
			report := func(target ast.Expr, at token.Pos) {
				id := assignedRoot(target)
				if id == nil || id.Name == "_" || id.Obj == nil {
					return
				}
				if decl := id.Obj.Pos(); decl < lit.Pos() || decl > lit.End() {
					t.Errorf("%s: an apply-shaped closure assigns %s, which it captured; "+
						"an apply closure runs off the UI goroutine and under Script Changes, "+
						"so it never writes page state (docs/ui-rules.md)", fset.Position(at), id.Name)
				}
			}
			ast.Inspect(lit.Body, func(m ast.Node) bool {
				switch s := m.(type) {
				case *ast.AssignStmt:
					if s.Tok != token.DEFINE {
						for _, lhs := range s.Lhs {
							report(lhs, s.Pos())
						}
					}
				case *ast.IncDecStmt:
					report(s.X, s.Pos())
				}
				return true
			})
			return true
		})
	}
	// Without this, a parse that stopped resolving the closures would pass.
	if checked < 50 {
		t.Fatalf("only %d func(ctx context.Context) error closures found; the shape match has broken and this test proves nothing", checked)
	}
}

// isContextErrorFunc reports whether ft is func(<x> context.Context) error.
func isContextErrorFunc(ft *ast.FuncType) bool {
	if ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) > 1 ||
		ft.Results == nil || len(ft.Results.List) != 1 {
		return false
	}
	sel, ok := ft.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Context" {
		return false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "context" {
		return false
	}
	r, ok := ft.Results.List[0].Type.(*ast.Ident)
	return ok && r.Name == "error"
}

// assignedRoot is the variable an assignment target writes through: x for
// x, x.f, x[i], *x and (x).
func assignedRoot(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil
		}
	}
}
