package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A page or dialog builds its grid once — `grid := controls.NewDataGrid()`
// followed by a SetData — and then redraws it from callbacks: a Revert, an Add,
// a Remove, a toggle, an OnSelectRow. The first call is correct; every later one
// is not. SetSource clears the cell cursor, the scroll position and any column
// the user dragged wider, so a bare SetData from a callback throws all three
// away — and from inside the grid's own callback it also undoes the move that
// fired it, which makes propsheet.GridRow report "not handled" and Form move
// focus out of the grid on the first arrow key.
//
// redrawGrid keeps the view (a redraw of the same rows); resetGrid keeps the
// widths and places the cursor deliberately (the row *set* changed). See
// prop_grid_helpers.go for both, and prop_grid_reset_test.go for what
// distinguishes them.
//
// # Why the rule is the declaration site and not nesting depth
//
// This test used to glob *props*.go and flag SetData nested two function
// literals deep — the shape of the seventeen hand-rolled SetData +
// SetSelectedRow pages, whose callbacks sit inside a page's `load` closure.
// Both halves of that rule let real sites through. The New-X and Add-X dialogs
// build their grid in a plain method rather than a page load, so their button
// callbacks are only one literal deep and were indistinguishable from a
// legitimate first SetData; and none of those files is named *props*.go.
// ag_add_listener_dialog.go, new_endpoint_dialog.go, effective_perms_page.go,
// securables_matrix.go, server_permissions_matrix.go and ag_dashboard.go all
// shipped a bare SetData under the old rule.
//
// What actually separates the two is not how deep the call is but whether the
// grid already existed when it ran: the initial SetData is in the same function
// body as the NewDataGrid that made it, and every redraw reaches a grid built
// somewhere outside. That holds however the callback is nested.
//
// # What this does not cover
//
// Only grids held in a local variable. A panel keeping its grid in a struct
// field (QueryPanel, DetailBrowser, Activity Monitor, the Query Store panel)
// has no declaration site to compare against, and several of those SetData
// calls are deliberate — a result set whose columns are new each time is
// exactly what SetData is for. Those are reviewed by hand.

// TestNoBareSetDataOnAGridBuiltElsewhere fails on a SetData reaching a grid
// declared outside the function it is called from — a redraw of a grid the user
// may already have navigated, which is redrawGrid's or resetGrid's job.
func TestNoBareSetDataOnAGridBuiltElsewhere(t *testing.T) {
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	initial, redraws := 0, 0
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		// body is the innermost function body enclosing the node being walked,
		// which is what a grid's declaration is looked for in.
		var body *ast.BlockStmt
		var walk func(ast.Node) bool
		walk = func(n ast.Node) bool {
			var inner *ast.BlockStmt
			switch v := n.(type) {
			case *ast.FuncDecl:
				inner = v.Body
			case *ast.FuncLit:
				inner = v.Body
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "SetData" {
					return true
				}
				// A grid held in a struct field, not a local — out of scope,
				// see the comment above.
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				if body != nil && declaresLocal(body, id.Name) {
					initial++
					return true
				}
				redraws++
				t.Errorf("%s: SetData on %s, which is built outside this function — "+
					"use redrawGrid to keep the user's cursor, scroll and dragged widths, "+
					"or resetGrid(…, row) when the row set changed",
					fset.Position(v.Pos()), id.Name)
				return true
			default:
				return true
			}
			if inner == nil {
				return false
			}
			outer := body
			body = inner
			ast.Inspect(inner, walk)
			body = outer
			return false
		}
		ast.Inspect(f, walk)
	}
	// Without this the test passes vacuously the day the call shape changes
	// and no SetData is recognised any more. redraws is reported for the same
	// reason the errors above are.
	if initial == 0 {
		t.Fatalf("no local-grid SetData calls found (%d redraws) — this test has "+
			"stopped checking anything", redraws)
	}
}

// declaresLocal reports whether body declares name with :=, not counting
// declarations inside a nested function literal — those are a different
// variable as far as an enclosing call is concerned.
func declaresLocal(body *ast.BlockStmt, name string) bool {
	found := false
	walk := func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			return true
		}
		for _, lhs := range as.Lhs {
			if id, ok := lhs.(*ast.Ident); ok && id.Name == name {
				found = true
			}
		}
		return true
	}
	ast.Inspect(body, walk)
	return found
}
