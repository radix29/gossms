package tui

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A DataGrid's cell context menu and its "Show Value" popup are drawn outside
// the grid's own rect, so DataGrid.Draw paints neither — DrawOverlay is what
// puts them on screen. A host that draws a grid and never overlays it does not
// merely lose the menu: HandleKey/HandleMouse still give OverlayActive() first
// refusal, so a right-click opens a menu nobody can see that swallows every key
// until Escape.
//
// Two panels shipped that way — the Query Store panel, whose Copy / Show Value
// menu had never once been visible, and the Log File Viewer. Both were found by
// reading the code, because nothing catches it: there is no SimulationScreen in
// tcell v3 to assert against a rendered frame, and every unit test still passes.
// This test is the substitute — it reads the source and pins the pairing.
//
// # Why the pairing is per type and not per method
//
// The overlay has to land after every *other* widget in the frame, so a host
// that has one cannot draw it from the same method that draws the grid — it
// exposes its own DrawOverlay for its host to call last. PlanView and
// propsheet.GridRow are both built that way, and an earlier form of this test,
// which required the two calls in one method body, would have reported both as
// bugs. What is actually checkable is that the type which draws a grid reaches
// that same grid from *some* DrawOverlay path of its own.
//
// # Two limits, stated rather than papered over
//
//   - It cannot tell that a type's DrawOverlay is ever called. A host that
//     exposes one and whose own host never calls it is invisible here.
//   - It matches a grid by the text of the expression naming it, so
//     `v.summarySt.grid.Draw` is paired only by `v.summarySt.grid.DrawOverlay`.
//     A grid copied into a local first is not matched at all.

// gridDrawPackages are the packages scanned, relative to the repo root — every
// package that holds a controls.DataGrid. Named rather than discovered so a new
// package holding a grid is a deliberate addition here; the length guards below
// are what catch a path that has gone stale.
var gridDrawPackages = []string{
	"internal/tui",
	"internal/tui/planview",
	"internal/tuikit/propsheet",
}

// TestEveryGridDrawIsPairedWithDrawOverlay fails when a type calls Draw on a
// *controls.DataGrid it holds without reaching the same grid from a DrawOverlay.
func TestEveryGridDrawIsPairedWithDrawOverlay(t *testing.T) {
	fset := token.NewFileSet()
	files := parseGridSources(t, fset)

	// gridFieldNames is every field name declared as *controls.DataGrid
	// anywhere in the scanned packages. A name, not a per-struct set: a grid
	// reached through a chain (v.summarySt.grid) is held by a struct whose own
	// field is not a DataGrid, so keying by the outermost type cannot find it.
	gridFieldNames := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				if !isDataGridPtr(fld.Type) {
					continue
				}
				for _, name := range fld.Names {
					gridFieldNames[name.Name] = true
				}
			}
			return true
		})
	}

	// draws and overlays are keyed by "package.Type" then by the rendered
	// expression naming the grid, so a Draw in one method pairs with a
	// DrawOverlay in another on the same type.
	draws := map[string]map[string]token.Pos{}
	overlays := map[string]map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue
			}
			_, recvType := receiverOf(fn)
			if recvType == "" {
				continue
			}
			key := f.Name.Name + "." + recvType
			for expr, pos := range gridMethodCalls(fset, fn.Body, gridFieldNames, "Draw") {
				if draws[key] == nil {
					draws[key] = map[string]token.Pos{}
				}
				draws[key][expr] = pos
			}
			for expr := range gridMethodCalls(fset, fn.Body, gridFieldNames, "DrawOverlay") {
				if overlays[key] == nil {
					overlays[key] = map[string]bool{}
				}
				overlays[key][expr] = true
			}
		}
	}

	drawn := 0
	for _, key := range sortedKeys(toSet(draws)) {
		for _, expr := range sortedKeys(toSet(draws[key])) {
			drawn++
			if overlays[key][expr] {
				continue
			}
			t.Errorf("%s: %s draws %s but no method on it calls %s.DrawOverlay — "+
				"its context menu and Show Value popup are invisible while still "+
				"swallowing every key until Escape",
				fset.Position(draws[key][expr]), key, expr, expr)
		}
	}

	// Both guards keep the test from passing vacuously: the first if the field
	// type stops being spelled *controls.DataGrid, the second if the call shape
	// changes and no draw is recognised any more.
	if len(gridFieldNames) == 0 {
		t.Fatal("no *controls.DataGrid fields found — this test has stopped checking anything")
	}
	if drawn == 0 {
		t.Fatal("no grid Draw calls found — this test has stopped checking anything")
	}
}

// repoRoot is the module root, two levels up from internal/tui.
const repoRoot = "../.."

// parseGridSources parses every non-test source file of gridDrawPackages.
func parseGridSources(t *testing.T, fset *token.FileSet) []*ast.File {
	t.Helper()
	var files []*ast.File
	for _, pkg := range gridDrawPackages {
		dir := filepath.Join(repoRoot, pkg)
		names, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		found := 0
		for _, name := range names {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			files = append(files, f)
			found++
		}
		if found == 0 {
			t.Fatalf("no sources under %s — gridDrawPackages has gone stale and "+
				"a package holding a grid is no longer being checked", dir)
		}
	}
	return files
}

// isDataGridPtr reports whether expr is written *controls.DataGrid.
func isDataGridPtr(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "DataGrid" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "controls"
}

// receiverOf returns a method's receiver variable name and base type name.
func receiverOf(fn *ast.FuncDecl) (name, typeName string) {
	if len(fn.Recv.List) == 0 {
		return "", ""
	}
	f := fn.Recv.List[0]
	if len(f.Names) > 0 {
		name = f.Names[0].Name
	}
	t := f.Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		typeName = id.Name
	}
	return name, typeName
}

// gridMethodCalls collects, keyed by the rendered expression naming the grid,
// every `<expr>.<method>(...)` in body whose expression ends in a known grid
// field name — so v.summarySt.grid.Draw is found as well as p.grid.Draw.
func gridMethodCalls(fset *token.FileSet, body *ast.BlockStmt, gridNames map[string]bool, method string) map[string]token.Pos {
	found := map[string]token.Pos{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		outer, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || outer.Sel.Name != method {
			return true
		}
		inner, ok := outer.X.(*ast.SelectorExpr)
		if !ok || !gridNames[inner.Sel.Name] {
			return true
		}
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, outer.X); err != nil {
			return true
		}
		found[b.String()] = call.Pos()
		return true
	})
	return found
}

func toSet[V any](m map[string]V) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
